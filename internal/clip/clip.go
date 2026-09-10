// Package clip writes the system clipboard, and only writes it. Diple never
// reads the clipboard: a paste is text the user's own terminal sent because
// the user asked it to, and nothing Diple does needs to look at what is
// already there.
//
// It writes through the host terminal's OSC 52 where the host answers for it,
// and through the platform's own clipboard command where the host does not,
// since Diple is a local process on the user's own machine. When neither is
// available it says so, and the session puts that on the tray status line.
package clip

import (
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// OSC52 is the sequence that hands text to a terminal that answers for the
// clipboard. `c` is the selection every host that implements OSC 52 accepts.
func OSC52(text string) []byte {
	b := make([]byte, 0, 16+base64.StdEncoding.EncodedLen(len(text)))
	b = append(b, "\x1b]52;c;"...)
	b = append(b, base64.StdEncoding.EncodeToString([]byte(text))...)
	return append(b, '\a')
}

// Rung is how a write reached the clipboard, for the status line to report.
type Rung string

// The rungs of the ladder, in the order they are tried.
const (
	ViaOSC52    Rung = "osc52"
	ViaPlatform Rung = "platform"
	Unavailable Rung = "none"
)

// Writer decides which rung a session is on and writes there. Term is the
// sequence sink for OSC 52 — the host terminal — and Platform is the command
// that owns the platform clipboard, empty when this machine has none.
type Writer struct {
	// OSC52Answered reports whether the host answers for the clipboard.
	OSC52Answered bool
	// Platform is the argv of the platform clipboard command, or nil.
	Platform []string
}

// NewWriter reads the environment for the host's identity and this machine's
// clipboard command.
func NewWriter(env func(string) string) *Writer {
	platform := platformCommand(env)
	return &Writer{OSC52Answered: hostAnswersOSC52(env, len(platform) > 0), Platform: platform}
}

// hostAnswersOSC52 reports whether the host answers for the clipboard itself.
//
// Two hosts do not. Terminal.app has never implemented OSC 52, and it is named
// rather than guessed at. A multiplexer does not answer either: it forwards
// the sequence to whatever is outside it, which may be a terminal that ignores
// it, and the copy is then lost with nothing to report — which is exactly what
// a user who copies by dragging must never suffer. So where a multiplexer is
// in the way and this machine has a clipboard of its own, Diple writes that
// directly, since it is a local process on the user's own machine. With no
// platform clipboard to write — a remote or headless session — forwarding the
// sequence outward is the only way left, and it is taken.
func hostAnswersOSC52(env func(string) string, hasPlatform bool) bool {
	if env("TERM_PROGRAM") == "Apple_Terminal" {
		return false
	}
	if hasPlatform && inMultiplexer(env) {
		return false
	}
	return true
}

// inMultiplexer reports whether a multiplexer stands between Diple and the
// terminal that owns the clipboard.
func inMultiplexer(env func(string) string) bool {
	if env("TMUX") != "" || env("STY") != "" || env("TERM_PROGRAM") == "tmux" {
		return true
	}
	term := env("TERM")
	return strings.HasPrefix(term, "tmux") || strings.HasPrefix(term, "screen")
}

// platformCommand is the command that owns this machine's clipboard: pbcopy
// on macOS, and on Linux whichever of the Wayland and X11 tools is installed.
func platformCommand(env func(string) string) []string {
	if runtime.GOOS == "darwin" {
		if p, err := exec.LookPath("pbcopy"); err == nil {
			return []string{p}
		}
		return nil
	}
	candidates := [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	if env("WAYLAND_DISPLAY") == "" {
		candidates = candidates[1:]
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c[0]); err == nil {
			return append([]string{p}, c[1:]...)
		}
	}
	return nil
}

// Write puts text on the clipboard and reports which rung carried it. term
// receives the OSC 52 sequence when the host answers for the clipboard; a nil
// term skips that rung. Text that reaches no rung reports Unavailable, which
// is what the tray status line says.
func (w *Writer) Write(term func([]byte) error, text string) (Rung, error) {
	if w == nil || text == "" {
		return Unavailable, nil
	}
	if w.OSC52Answered && term != nil {
		if err := term(OSC52(text)); err != nil {
			return Unavailable, err
		}
		return ViaOSC52, nil
	}
	if len(w.Platform) > 0 {
		cmd := exec.Command(w.Platform[0], w.Platform[1:]...)
		cmd.Stdin = strings.NewReader(text)
		cmd.Stdout, cmd.Stderr = nil, nil
		if err := cmd.Run(); err != nil {
			return Unavailable, err
		}
		return ViaPlatform, nil
	}
	return Unavailable, nil
}

// Env is the ordinary environment lookup, for a session that has no reason to
// substitute one.
func Env(key string) string { return os.Getenv(key) }
