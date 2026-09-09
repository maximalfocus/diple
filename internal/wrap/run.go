//go:build unix

package wrap

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/fold"
	"github.com/maximalfocus/diple/internal/record"
)

// Options describe one wrapped agent session.
type Options struct {
	Path   string   // resolved agent executable
	Name   string   // argv[0] the agent sees
	Args   []string // remaining arguments
	Record string   // fixture path, or "" for none
	// Adapter, when set, follows the session transcript.
	Adapter adapter.Adapter
	// Plain restricts Diple's drawing to reverse and underline.
	Plain bool
	// NoMarks disables the gutter mark on anchored blocks.
	NoMarks bool
	// NoArchive disables the sent-fold archive.
	NoArchive bool
	Stdin     *os.File // the host terminal
	Stdout    *os.File
	Stderr    *os.File
}

// drainTimeout bounds how long Run waits for the agent's last bytes after it
// exits. A grandchild that keeps the terminal open must not hold Diple.
const drainTimeout = 2 * time.Second

// Run wraps the agent in a pseudo-terminal until it exits and returns its
// exit status. The host terminal is restored on every path out, including a
// panic in Diple's own logic.
func Run(opts Options) (exitCode int, err error) {
	fd := int(opts.Stdin.Fd())
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return 1, fmt.Errorf("terminal size: %w", err)
	}

	var rec Recorder
	var recCloser io.Closer
	if opts.Record != "" {
		w, err := record.Create(opts.Record, record.Header{
			Agent: opts.Name, Args: opts.Args, Cols: cols, Rows: rows, Term: os.Getenv("TERM"),
		})
		if err != nil {
			return 1, fmt.Errorf("record: %w", err)
		}
		rec, recCloser = w, w
	}

	cmd := exec.Command(opts.Path, opts.Args...)
	cmd.Args[0] = opts.Name
	cmd.Env = os.Environ()
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		if recCloser != nil {
			_ = recCloser.Close()
		}
		return 1, fmt.Errorf("start %s: %w", opts.Name, err)
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return 1, fmt.Errorf("raw mode: %w", err)
	}
	var restoreOnce sync.Once
	restore := func() { restoreOnce.Do(func() { _ = term.Restore(fd, oldState) }) }
	defer restore()

	session := NewSession(opts.Stdout, ptmx, cols, rows, rec)
	session.Plain = opts.Plain
	session.Marks = !opts.NoMarks
	session.SetPTYRows = func(r int) {
		if c, _, err := term.GetSize(fd); err == nil {
			_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(r), Cols: uint16(c)})
		}
	}
	if opts.Adapter != nil {
		session.UseAdapter(opts.Adapter, opts.Name)
		if st, err := card.DefaultStore(); err == nil {
			session.UseStore(st)
		}
		if !opts.NoArchive {
			if cwd, err := os.Getwd(); err == nil {
				if ar, err := fold.NewArchive(cwd, ".diple-archive.md"); err == nil {
					session.UseArchive(ar)
				}
			}
		}
		cwd, err := os.Getwd()
		if err == nil {
			onFound := func(id string) {
				if rec != nil {
					rec.Transcript(id)
				}
				_ = session.SetSessionID(id)
			}
			session.Tracker = NewTracker(opts.Adapter, cwd, onFound)
			session.Tracker.Start()
			defer session.Tracker.Stop()
		}
	}
	sessionStopped := false
	defer func() {
		if !sessionStopped {
			_ = session.Stop()
		}
	}()
	if err := session.Start(); err != nil {
		return 1, err
	}

	// Agent output → terminal.
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		buf := make([]byte, 64*1024)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				if werr := session.HandleOutput(buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// Terminal input → agent. The goroutine may outlive the session, blocked
	// on a read of the terminal; the process exits regardless.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := opts.Stdin.Read(buf)
			if n > 0 {
				if werr := session.HandleInput(buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// Signals: resize follows the host terminal; termination is forwarded.
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(sigs)
	go func() {
		for sig := range sigs {
			switch sig {
			case syscall.SIGWINCH:
				if c, r, err := term.GetSize(fd); err == nil {
					_ = session.Resize(c, r)
					_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(session.AgentRows()), Cols: uint16(c)})
				}
			default:
				if s, ok := sig.(syscall.Signal); ok && cmd.Process != nil {
					_ = cmd.Process.Signal(s)
				}
			}
		}
	}()

	waitErr := cmd.Wait()

	select {
	case <-outDone:
	case <-time.After(drainTimeout):
	}
	_ = ptmx.Close()
	sessionStopped = true
	stopErr := session.Stop()
	restore()
	if recCloser != nil {
		if cerr := recCloser.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("record: %w", cerr)
		}
	}
	if stopErr != nil && err == nil {
		err = stopErr
	}
	return exitStatus(waitErr, cmd), err
}

func exitStatus(waitErr error, cmd *exec.Cmd) int {
	if waitErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return exitErr.ExitCode()
	}
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	return 1
}
