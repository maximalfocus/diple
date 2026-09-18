package clip

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOSC52CarriesTheTextBase64Encoded(t *testing.T) {
	got := string(OSC52("hello"))
	if got != "\x1b]52;c;aGVsbG8=\a" {
		t.Fatalf("OSC 52 = %q", got)
	}
	// A newline travels intact, since what is copied may be several rows.
	if got := string(OSC52("a\nb")); got != "\x1b]52;c;YQpi\a" {
		t.Fatalf("OSC 52 with a newline = %q", got)
	}
}

// envOf answers a few variables and nothing else.
func envOf(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

// TestTerminalAppDoesNotAnswerForTheClipboard names the host rather than
// guessing at it, which is what R-015's ladder asks for.
func TestTerminalAppDoesNotAnswerForTheClipboard(t *testing.T) {
	if hostAnswersOSC52(envOf(map[string]string{"TERM_PROGRAM": "Apple_Terminal"}), true) {
		t.Fatal("Terminal.app does not answer for the clipboard")
	}
	for _, program := range []string{"WezTerm", "ghostty", "iTerm.app", ""} {
		if !hostAnswersOSC52(envOf(map[string]string{"TERM_PROGRAM": program}), true) {
			t.Fatalf("%q was refused OSC 52", program)
		}
	}
}

// TestAMultiplexerDoesNotAnswerEither: it forwards the sequence to whatever is
// outside it, which may ignore it, and the copy would be lost with nothing to
// report. Where this machine has a clipboard of its own, Diple writes that.
// Terminal.app's exclusion outlives R-014's claim on it: passing a host
// through is not claiming it.
//
// Covers S-019 T-03.
func TestAMultiplexerDoesNotAnswerEither(t *testing.T) {
	for _, in := range []map[string]string{
		{"TMUX": "/tmp/tmux-501/default,1,0"},
		{"TERM_PROGRAM": "tmux"},
		{"TERM": "tmux-256color"},
		{"TERM": "screen.xterm-256color"},
		{"STY": "1234.pts-0.host"},
	} {
		if hostAnswersOSC52(envOf(in), true) {
			t.Fatalf("%v was treated as answering for the clipboard", in)
		}
		// With no platform clipboard to write — a remote or headless session
		// — forwarding the sequence outward is the only way left.
		if !hostAnswersOSC52(envOf(in), false) {
			t.Fatalf("%v had no other rung and was still refused OSC 52", in)
		}
	}
	// Terminal.app never gets the sequence, platform clipboard or not.
	if hostAnswersOSC52(envOf(map[string]string{"TERM_PROGRAM": "Apple_Terminal"}), false) {
		t.Fatal("Terminal.app must never be sent OSC 52")
	}
}

// TestTheLadderTakesTheHostRungFirst.
func TestTheLadderTakesTheHostRungFirst(t *testing.T) {
	var wrote []byte
	w := &Writer{OSC52Answered: true, Platform: []string{"/bin/false"}}
	rung, err := w.Write(func(p []byte) error { wrote = append(wrote, p...); return nil }, "copy me")
	if err != nil || rung != ViaOSC52 {
		t.Fatalf("rung = %q err = %v", rung, err)
	}
	if string(wrote) != string(OSC52("copy me")) {
		t.Fatalf("wrote %q", wrote)
	}
}

// TestTheLadderFallsBackToThePlatform: where the host does not answer, Diple
// writes the platform clipboard directly, since it is a local process on the
// user's own machine.
func TestTheLadderFallsBackToThePlatform(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "pasteboard")
	script := filepath.Join(dir, "fake-pbcopy")
	body := "#!/bin/sh\ncat > " + out + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	w := &Writer{OSC52Answered: false, Platform: []string{script}}
	rung, err := w.Write(func([]byte) error { t.Fatal("the host rung must not be taken"); return nil }, "copy me")
	if err != nil || rung != ViaPlatform {
		t.Fatalf("rung = %q err = %v", rung, err)
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != "copy me" {
		t.Fatalf("platform clipboard = %q err = %v", got, err)
	}
}

// TestNeitherRungSaysSo: only when neither is available does Diple say so.
func TestNeitherRungSaysSo(t *testing.T) {
	w := &Writer{}
	rung, err := w.Write(nil, "copy me")
	if err != nil || rung != Unavailable {
		t.Fatalf("rung = %q err = %v", rung, err)
	}
	// Empty text is not a copy at all.
	if rung, _ := (&Writer{OSC52Answered: true}).Write(func([]byte) error {
		t.Fatal("an empty copy must write nothing")
		return nil
	}, ""); rung != Unavailable {
		t.Fatalf("empty copy rung = %q", rung)
	}
}

// TestAFailingPlatformCommandIsReportedNotSwallowed.
func TestAFailingPlatformCommandIsReportedNotSwallowed(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "failing")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := &Writer{Platform: []string{script}}
	rung, err := w.Write(nil, "copy me")
	if err == nil {
		t.Fatalf("a failing clipboard command must be reported: rung = %q", rung)
	}
	if rung != Unavailable {
		t.Fatalf("a failed write must not claim a rung: %q", rung)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("err = %v", err)
	}
}

// TestNewWriterFindsThisMachinesClipboard.
func TestNewWriterFindsThisMachinesClipboard(t *testing.T) {
	w := NewWriter(envOf(nil))
	if runtime.GOOS == "darwin" {
		if len(w.Platform) == 0 || !strings.HasSuffix(w.Platform[0], "pbcopy") {
			t.Fatalf("macOS platform command = %v", w.Platform)
		}
	}
	if !w.OSC52Answered {
		t.Fatal("a host that is not Terminal.app is asked through the sequence itself")
	}
}
