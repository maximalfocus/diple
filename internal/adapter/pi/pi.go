// Package pi is the adapter for the pi CLI.
package pi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/agent"
	"github.com/maximalfocus/diple/internal/align"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/screen"
)

// Verified lists the pi session formats the fixtures under testdata pin. pi
// records no CLI version in a session file, so its own format version is
// what an adapter can honestly pin.
var Verified = []string{"3"}

// Decoration facts of pi's renderer. pi draws an assistant turn as plain
// rows with nothing to key on, so it has no turn marker and alignment offers
// every paragraph start as a candidate instead. Its input area is fenced by
// rules rather than opened by a marker.
var rules = align.Rules{Fence: "```"}

// Marker is always "": pi marks no turns.
func (a *Adapter) Marker(row string) string { return rules.Marker(row) }

// Adapter implements adapter.Adapter for pi.
type Adapter struct {
	// Home is the user's home directory; empty means the current user's.
	Home string
}

func init() { adapter.Register(&Adapter{}) }

// Name returns "pi".
func (a *Adapter) Name() string { return "pi" }

// VerifiedVersions returns the pinned session formats.
func (a *Adapter) VerifiedVersions() []string { return append([]string(nil), Verified...) }

// Bypass reports the non-interactive forms: pi's own subcommands and print
// mode, along with the forms every agent shares.
func (a *Adapter) Bypass(args []string) bool {
	for _, arg := range args {
		if arg == "-p" || arg == "--print" {
			return true
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch arg {
		case "install", "remove", "uninstall", "update", "list", "config", "auth":
			return true
		}
		break
	}
	return agent.Bypass(args)
}

func (a *Adapter) home() (string, error) {
	if a.Home != "" {
		return a.Home, nil
	}
	return os.UserHomeDir()
}

// SessionDir is where pi keeps the sessions of one working directory: the
// path with every character outside letters and digits turned into a dash,
// wrapped in a dash at each end.
func SessionDir(home, cwd string) string {
	var b strings.Builder
	b.WriteByte('-')
	for _, r := range cwd {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	b.WriteByte('-')
	return filepath.Join(home, ".pi", "agent", "sessions", b.String())
}

// Discover returns the session file of the session started in cwd at or
// after since. pi writes the file when the session opens and appends to it
// as the conversation goes, so a live session has a transcript to align
// against from the first turn.
func (a *Adapter) Discover(cwd string, since time.Time) (string, error) {
	home, err := a.home()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	dir := SessionDir(home, cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", adapter.ErrNoTranscript
		}
		return "", err
	}
	type cand struct {
		path string
		mod  time.Time
	}
	var cands []cand
	cutoff := since.Add(-2 * time.Second)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			continue
		}
		cands = append(cands, cand{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	if len(cands) == 0 {
		return "", adapter.ErrNoTranscript
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	for _, c := range cands {
		if started, ok := sessionStart(c.path); ok && !started.Before(cutoff) {
			return c.path, nil
		}
	}
	return "", adapter.ErrNoTranscript
}

// sessionStart reads the start a session file records in its opening entry.
func sessionStart(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for n := 0; sc.Scan() && n < 20; n++ {
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type != "session" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil {
			return time.Time{}, false
		}
		return ts, true
	}
	return time.Time{}, false
}

// entry is one line of a pi session file: the opening session entry, or one
// message of the conversation.
type entry struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	Message   struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
}

// Parse reads a pi session file. Each assistant message is one turn; only
// its text parts become blocks, because its thinking parts are the model's
// own working and no card should quote them.
func (a *Adapter) Parse(r io.Reader) (*adapter.Transcript, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	t := &adapter.Transcript{Agent: a.Name()}
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("pi: transcript line %d: %w", line, err)
		}
		if e.Type == "session" {
			t.Version = strconv.Itoa(e.Version)
			t.Unverified = !verified(t.Version)
			t.SessionID = e.ID
			continue
		}
		if e.Type != "message" || (e.Message.Role != "assistant" && e.Message.Role != "user") {
			continue
		}
		var text strings.Builder
		for _, c := range e.Message.Content {
			if c.Type == "text" {
				text.WriteString(c.Text)
			}
		}
		bs := blocks.Parse(text.String())
		if len(bs) == 0 {
			continue
		}
		// pi echoes the user's prompt in the same shape as a reply, so the
		// prompt joins the alignment as an echo: it holds its place in the
		// order, which is what keeps a reply from matching it instead.
		if e.Message.Role == "user" {
			t.Turns = append(t.Turns, adapter.Turn{ID: e.ID, Blocks: bs, Echo: true})
			continue
		}
		t.Turns = append(t.Turns, adapter.Turn{Ordinal: ordinal(t.Turns) + 1, ID: e.ID, Blocks: bs})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("pi: transcript: %w", err)
	}
	return t, nil
}

// ordinal is how many real turns a transcript has so far, echoes aside.
func ordinal(turns []adapter.Turn) int {
	n := 0
	for _, t := range turns {
		if !t.Echo {
			n++
		}
	}
	return n
}

func verified(v string) bool {
	for _, ok := range Verified {
		if ok == v {
			return true
		}
	}
	return false
}

// Mode reports inline: pi prints into the terminal and keeps no alternate
// screen of its own, so Diple's scrollback is the history.
func (a *Adapter) Mode(s *screen.Screen) adapter.Mode {
	if s.AltActive() && s.MouseTracking() {
		return adapter.ModeFullscreen
	}
	return adapter.ModeInline
}

// Align maps each turn onto the rows pi drew. pi marks nothing, so the
// shared matching offers every paragraph start as a candidate and the turns
// take them in order — which is what keeps an echoed prompt above a reply
// from being mistaken for it.
func (a *Adapter) Align(t *adapter.Transcript, rows []string) []adapter.TurnAlignment {
	return adapter.Assign(t, rows, rules)
}

// Fallback treats the rendered paragraphs as one turn, since pi draws no
// boundary an adapter could divide them on.
func (a *Adapter) Fallback(rows []string) []adapter.TurnAlignment {
	return adapter.Paragraphed(rows, rules)
}

// InputRow finds pi's input area: it is fenced by rules, and the box begins
// at the last rule with the status lines below it.
func (a *Adapter) InputRow(screenRows []string) int {
	for i := len(screenRows) - 1; i >= 0; i-- {
		if !isRule(screenRows[i]) {
			continue
		}
		// The fence above the input line, not the one below it.
		for j := i - 1; j >= 0; j-- {
			if isRule(screenRows[j]) {
				return j
			}
			if strings.TrimSpace(screenRows[j]) != "" {
				break
			}
		}
		return i
	}
	return -1
}

func isRule(row string) bool {
	row = strings.TrimSpace(row)
	if len([]rune(row)) < 8 {
		return false
	}
	for _, r := range row {
		if r != '─' && r != '━' && r != '-' {
			return false
		}
	}
	return true
}

// Busy reports whether pi is working: it draws a working line inside its
// fence while a turn is in flight.
func (a *Adapter) Busy(s *screen.Screen) bool {
	for _, row := range s.Text() {
		if strings.Contains(row, "Working") && strings.Contains(row, "─") {
			return true
		}
		if strings.Contains(strings.ToLower(row), "esc to interrupt") {
			return true
		}
	}
	return false
}

// Prompt reports whether pi is asking a question of its own: it offers the
// choice inline and says which key answers it.
func (a *Adapter) Prompt(s *screen.Screen) bool {
	for _, row := range s.Text() {
		low := strings.ToLower(strings.TrimSpace(row))
		if strings.Contains(low, "esc to cancel") || strings.Contains(low, "press enter to continue") ||
			strings.Contains(low, "y/n") {
			return true
		}
	}
	return false
}

// QueuesWhenBusy is true: pi accepts input while a turn runs and sends it
// when the turn ends.
func (a *Adapter) QueuesWhenBusy() bool { return true }
