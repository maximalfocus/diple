// Package adapter defines the per-CLI knowledge Diple needs: where the
// transcript lives, how it parses into turns and blocks, which rendering
// mode the CLI is in, and how a turn's blocks line up with screen rows.
package adapter

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/screen"
)

// Mode is the rendering mode an agent CLI is using.
type Mode string

// The two rendering modes. Inline agents print into the terminal, so
// Diple's scrollback is the history. Fullscreen agents run on the alternate
// screen with their own viewport, which is then the history source.
const (
	ModeInline     Mode = "inline"
	ModeFullscreen Mode = "fullscreen"
)

// Turn is one assistant message as recorded in the transcript.
type Turn struct {
	Ordinal int // 1-based among assistant turns; 0 for an echo
	ID      string
	Blocks  []blocks.Block
	// Echo marks text the agent draws in the same shape as a reply but that
	// the user wrote — pi echoes a prompt that way. It takes its place in
	// the order so a reply is not mistaken for it, and it is never offered
	// as something to annotate.
	Echo bool
}

// Transcript is a parsed session transcript.
type Transcript struct {
	Agent     string
	Version   string
	SessionID string
	Turns     []Turn
	// Unverified marks a transcript from a version the adapter's fixtures do
	// not pin. It is parsed and aligned all the same, since a CLI updates
	// itself past its pinned versions; the block-by-block fallback guards what
	// no longer matches, and only a fixture refuses it.
	Unverified bool
}

// Span is a block's first and last row, as absolute indexes into the rows
// the caller aligned against.
type Span struct {
	First, Last int
}

// AlignedBlock is a block with the rows it occupies.
type AlignedBlock struct {
	blocks.Block
	Span
}

// TurnAlignment is one turn's blocks mapped to rows. When Aligned is false
// the transcript could not be matched and Blocks holds one paragraph per
// rendered paragraph instead, so annotation still has targets.
type TurnAlignment struct {
	Turn    int
	Aligned bool
	Blocks  []AlignedBlock
}

// ErrNoTranscript means the session has not written its transcript yet.
var ErrNoTranscript = errors.New("adapter: transcript not found yet")

// VersionError reports a fixture from a CLI version the adapter was not
// verified against. A fixture is the record of what a pinned version draws,
// so one from another version fails loudly rather than being guessed around.
type VersionError struct {
	Agent    string
	Version  string
	Verified []string
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("adapter: %s transcript is from version %s; verified versions: %s",
		e.Agent, e.Version, strings.Join(e.Verified, ", "))
}

// RequireVerified refuses a transcript from a version a's fixtures do not pin.
// Fixtures call it; a live session never does.
func RequireVerified(a Adapter, t *Transcript) error {
	if t.Unverified {
		return &VersionError{Agent: a.Name(), Version: t.Version, Verified: a.VerifiedVersions()}
	}
	return nil
}

// Adapter is the per-CLI unit. Every method must be safe to call from any
// goroutine.
type Adapter interface {
	// Name is the CLI's executable name.
	Name() string
	// VerifiedVersions lists the CLI versions the adapter's fixtures pin.
	VerifiedVersions() []string
	// Bypass reports whether args are a non-interactive invocation.
	Bypass(args []string) bool
	// Discover returns the transcript path of the session started in cwd
	// at or after since, or ErrNoTranscript while it does not exist yet.
	Discover(cwd string, since time.Time) (string, error)
	// Parse reads a transcript into turns and blocks.
	Parse(r io.Reader) (*Transcript, error)
	// Mode reports the rendering mode from the screen model.
	Mode(s *screen.Screen) Mode
	// Align maps each turn's blocks onto rows, the history the mode
	// provides as plain row text.
	Align(t *Transcript, rows []string) []TurnAlignment
	// Fallback splits rows into paragraph blocks with no transcript at all.
	Fallback(rows []string) []TurnAlignment
	// Marker returns the turn marker a row begins with, or "" when it begins
	// with none or the agent marks no turns. The highlight never covers it and
	// a copy never carries it.
	Marker(row string) string
	// InputRow returns the index of the row where the native input box
	// begins within the visible screen rows, or -1 when it is not shown.
	InputRow(screenRows []string) int
	// Busy reports whether the agent is working, from the screen model.
	Busy(s *screen.Screen) bool
	// Prompt reports whether the agent is showing a native prompt — a
	// permission question, a choice, a text question — that owns the
	// keyboard until the user answers it.
	Prompt(s *screen.Screen) bool
	// QueuesWhenBusy reports whether the agent accepts input while busy and
	// runs it when it becomes idle. When false, Diple holds a fold until the
	// agent is idle.
	QueuesWhenBusy() bool
}

var (
	regMu    sync.RWMutex
	registry = map[string]Adapter{}
)

// Register makes an adapter available under its name.
func Register(a Adapter) {
	regMu.Lock()
	defer regMu.Unlock()
	registry[a.Name()] = a
}

// For returns the adapter registered for an agent name.
func For(name string) (Adapter, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	a, ok := registry[name]
	return a, ok
}

// Names lists the registered adapters.
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
