//go:build unix

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/maximalfocus/diple/internal/record"
	"github.com/maximalfocus/diple/internal/screen"
	"github.com/maximalfocus/diple/internal/wrap"
)

var (
	dipleBin string
	fakeDir  string
)

const fakeAgent = `#!/bin/sh
case "$1" in
  -p|--print) printf 'print:%s\n' "$2"; exit 5 ;;
  --version) printf 'fake 1.0\n'; exit 0 ;;
  hang) trap 'exit 9' TERM; echo trapped; sleep 30 & wait; exit 0 ;;
esac
printf 'hello \033[1mworld\033[0m\n'
read line
printf 'got:%s\n' "$line"
exit 3
`

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "diple-test")
	if err != nil {
		panic(err)
	}
	dipleBin = filepath.Join(dir, "diple")
	build := exec.Command("go", "build", "-o", dipleBin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	fakeDir = filepath.Join(dir, "bin")
	if err := os.Mkdir(fakeDir, 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(fakeDir, "fake"), []byte(fakeAgent), 0o755); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func env() []string {
	return append(os.Environ(), "PATH="+fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func TestBypassWithoutTerminalIsByteIdentical(t *testing.T) {
	direct := exec.Command(filepath.Join(fakeDir, "fake"), "-p", "x")
	direct.Env = env()
	directOut, directErr := direct.CombinedOutput()

	wrapped := exec.Command(dipleBin, "fake", "-p", "x")
	wrapped.Env = env()
	wrappedOut, wrappedErr := wrapped.CombinedOutput()

	if !bytes.Equal(directOut, wrappedOut) {
		t.Fatalf("output differs: direct %q wrapped %q", directOut, wrappedOut)
	}
	if exitCode(directErr) != 5 || exitCode(wrappedErr) != 5 {
		t.Fatalf("exit codes: direct %d wrapped %d", exitCode(directErr), exitCode(wrappedErr))
	}
}

func TestUsageAndNotFound(t *testing.T) {
	cmd := exec.Command(dipleBin)
	if err := cmd.Run(); exitCode(err) != 64 {
		t.Fatalf("no-arg exit = %d", exitCode(err))
	}
	cmd = exec.Command(dipleBin, "no-such-agent-xyz")
	cmd.Env = env()
	if err := cmd.Run(); exitCode(err) != 127 {
		t.Fatalf("not-found exit = %d", exitCode(err))
	}
}

// ptyRun starts diple under a pseudo-terminal and returns the master, the
// command, the slave state before start, and a live output capture.
type capture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (c *capture) waitFor(t *testing.T, sub string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(c.String(), sub) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in %q", sub, c.String())
}

func ptyRun(t *testing.T, args ...string) (*exec.Cmd, *os.File, *os.File, *term.State, *capture) {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 10, Cols: 40}); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(int(tty.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(dipleBin, args...)
	cmd.Env = append(env(), "TERM=xterm-256color")
	// No controlling terminal on purpose: on macOS a slave whose session
	// leader has exited is revoked, and the test reads its state afterwards.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cap := &capture{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				cap.mu.Lock()
				cap.buf.Write(buf[:n])
				cap.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return cmd, ptmx, tty, before, cap
}

func sameState(a, b *term.State) bool {
	// term.State is opaque; compare through its textual form.
	return strings.Compare(stateString(a), stateString(b)) == 0
}

func stateString(s *term.State) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(sprintState(s), "\n", " "), "  ", " ")))
	return b.String()
}

func TestWrappedSessionForwardsAndRestores(t *testing.T) {
	cmd, ptmx, tty, before, cap := ptyRun(t, "fake")
	defer ptmx.Close()
	cap.waitFor(t, wrap.EnvelopeStart)
	cap.waitFor(t, "hello \x1b[1mworld\x1b[0m\r\n")

	// While the agent runs, the slave is raw: echo is off.
	if mid, err := term.GetState(int(tty.Fd())); err == nil && sameState(mid, before) {
		t.Fatal("terminal was not put into raw mode")
	}

	if _, err := ptmx.Write([]byte("abc\r")); err != nil {
		t.Fatal(err)
	}
	cap.waitFor(t, "got:abc")
	err := cmd.Wait()
	if exitCode(err) != 3 {
		t.Fatalf("exit = %d, want the agent's 3", exitCode(err))
	}
	cap.waitFor(t, wrap.EnvelopeEnd)
	out := cap.String()
	if !strings.HasPrefix(out, wrap.EnvelopeStart) || !strings.HasSuffix(out, wrap.EnvelopeEnd) {
		t.Fatalf("envelope missing: %q", out)
	}
	after, err := term.GetState(int(tty.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !sameState(before, after) {
		t.Fatalf("terminal not restored:\n before %s\n after  %s", stateString(before), stateString(after))
	}
	_ = tty.Close()
}

func TestBypassFlagUnderTerminal(t *testing.T) {
	cmd, ptmx, tty, before, cap := ptyRun(t, "fake", "--version")
	defer ptmx.Close()
	cap.waitFor(t, "fake 1.0")
	if err := cmd.Wait(); exitCode(err) != 0 {
		t.Fatalf("exit = %d", exitCode(err))
	}
	time.Sleep(50 * time.Millisecond)
	if out := cap.String(); strings.Contains(out, "\x1b[?1000h") {
		t.Fatalf("bypass must not allocate the wrapper: %q", out)
	}
	after, _ := term.GetState(int(tty.Fd()))
	if !sameState(before, after) {
		t.Fatal("terminal state changed by a bypassed invocation")
	}
	_ = tty.Close()
}

func TestSignalIsForwardedAndTerminalRestored(t *testing.T) {
	cmd, ptmx, tty, before, cap := ptyRun(t, "fake", "hang")
	defer ptmx.Close()
	cap.waitFor(t, wrap.EnvelopeStart)
	// The agent says when its trap is in place, so the signal cannot race it.
	cap.waitFor(t, "trapped")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if exitCode(err) != 9 {
			t.Fatalf("exit = %d, want the agent's trap status 9", exitCode(err))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("diple did not exit after SIGTERM")
	}
	cap.waitFor(t, wrap.EnvelopeEnd)
	after, _ := term.GetState(int(tty.Fd()))
	if !sameState(before, after) {
		t.Fatal("terminal not restored after signal")
	}
	_ = tty.Close()
}

func TestRecordWritesReplayableFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	cmd, ptmx, tty, _, cap := ptyRun(t, "--record", path, "fake")
	defer ptmx.Close()
	cap.waitFor(t, "hello")
	_, _ = ptmx.Write([]byte("xyz\r"))
	cap.waitFor(t, "got:xyz")
	_ = cmd.Wait()
	cap.waitFor(t, wrap.EnvelopeEnd)
	_ = tty.Close()

	rec, err := record.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Header.Agent != "fake" || rec.Header.Cols != 40 || rec.Header.Rows != 10 {
		t.Fatalf("header = %+v", rec.Header)
	}
	out := rec.Output()
	if !bytes.Contains(out, []byte("hello \x1b[1mworld\x1b[0m\r\n")) || !bytes.Contains(out, []byte("got:xyz")) {
		t.Fatalf("recorded output = %q", out)
	}
	var in []byte
	for _, ev := range rec.Events {
		if ev.Kind == record.KindInput {
			in = append(in, ev.Data...)
		}
	}
	if string(in) != "xyz\r" {
		t.Fatalf("recorded input = %q", in)
	}
	// The recorded output, with the envelope excluded, is what the terminal saw.
	seen := cap.String()
	body := strings.TrimSuffix(strings.TrimPrefix(seen, wrap.EnvelopeStart), wrap.EnvelopeEnd)
	if body != string(out) {
		t.Fatalf("terminal body differs from recorded output:\n term %q\n rec  %q", body, out)
	}
	s := screen.New(rec.Header.Cols, rec.Header.Rows)
	if err := rec.Replay(s); err != nil {
		t.Fatal(err)
	}
	if got := s.Text()[0]; got != "hello world" {
		t.Fatalf("replayed row 0 = %q", got)
	}
}
