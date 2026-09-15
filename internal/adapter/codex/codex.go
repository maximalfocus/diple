// Package codex is the adapter for the Codex CLI.
package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/agent"
	"github.com/maximalfocus/diple/internal/align"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/screen"
)

// Verified lists the Codex CLI versions the fixtures under testdata pin.
var Verified = []string{"0.153.4"}

// Decoration facts of the Codex renderer, read off a recorded session: an
// assistant turn opens with a bullet, the input box opens with a chevron,
// and a tool result is indented under its call.
const (
	TurnMarker   = "•"
	PromptMarker = "›"
	ResultMarker = "└"
)

var rules = align.Rules{TurnMarker: TurnMarker, PromptMarker: PromptMarker, ResultMarker: ResultMarker}

// Adapter implements adapter.Adapter for the Codex CLI.
type Adapter struct {
	// Home is the user's home directory; empty means the current user's.
	Home string
}

func init() { adapter.Register(&Adapter{}) }

// Name returns "codex".
func (a *Adapter) Name() string { return "codex" }

// VerifiedVersions returns the pinned versions.
func (a *Adapter) VerifiedVersions() []string { return append([]string(nil), Verified...) }

// Bypass reports the non-interactive forms. Codex adds `codex exec`, which
// runs a prompt without a TUI, to the forms every agent shares.
func (a *Adapter) Bypass(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg == "exec"
	}
	return agent.Bypass(args)
}

func (a *Adapter) home() (string, error) {
	if a.Home != "" {
		return a.Home, nil
	}
	return os.UserHomeDir()
}

// SessionsDir is where the Codex CLI keeps its rollout files, which it files
// by date under its own state directory.
func SessionsDir(home string) string { return filepath.Join(home, ".codex", "sessions") }

// Discover returns the rollout file of the session started in cwd at or
// after since; a rollout's opening entry carries both the working directory
// and the start, which is what binds it to a session.
//
// Codex 0.153.4 keeps a running session's thread in its own state database
// and writes the rollout file later, so a live session usually has no
// transcript to find. That is not an error: Diple keeps asking, and until a
// transcript appears the adapter's paragraph fallback keeps annotation
// working at paragraph granularity, exactly as it does for a transcript that
// cannot be read.
func (a *Adapter) Discover(cwd string, since time.Time) (string, error) {
	home, err := a.home()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	root := SessionsDir(home)
	cutoff := since.Add(-2 * time.Second)
	type cand struct {
		path string
		mod  time.Time
	}
	var cands []cand
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable branch is not a reason to stop looking
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		cands = append(cands, cand{p, info.ModTime()})
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return "", adapter.ErrNoTranscript
		}
		return "", err
	}
	if len(cands) == 0 {
		return "", adapter.ErrNoTranscript
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	for _, c := range cands {
		dir, started, ok := sessionStart(c.path)
		if !ok || started.Before(cutoff) {
			continue
		}
		if sameDir(dir, cwd) {
			return c.path, nil
		}
	}
	return "", adapter.ErrNoTranscript
}

// sessionStart reads the working directory and start time a rollout file
// records in its opening entry.
func sessionStart(path string) (string, time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", time.Time{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for n := 0; sc.Scan() && n < 20; n++ {
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type != "session_meta" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil {
			ts, err = time.Parse(time.RFC3339Nano, e.Payload.Timestamp)
			if err != nil {
				return "", time.Time{}, false
			}
		}
		return e.Payload.Cwd, ts, true
	}
	return "", time.Time{}, false
}

func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}

// entry is one line of a rollout file: the session's own opening entry, or
// one item of the conversation.
type entry struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		// session_meta
		SessionID  string `json:"session_id"`
		Cwd        string `json:"cwd"`
		CLIVersion string `json:"cli_version"`
		Timestamp  string `json:"timestamp"`
		// response_item
		ItemType string `json:"type"`
		ID       string `json:"id"`
		Role     string `json:"role"`
		Content  []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		// function_call
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"payload"`
}

// Parse reads a Codex rollout file. Each assistant message is one turn; its
// output text is Markdown, and a function call becomes a tool-call block so
// the rows it draws are still addressable.
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
			return nil, fmt.Errorf("codex: transcript line %d: %w", line, err)
		}
		if e.Type == "session_meta" {
			if t.Version == "" && e.Payload.CLIVersion != "" {
				t.Version = e.Payload.CLIVersion
				t.Unverified = !verified(t.Version)
			}
			if t.SessionID == "" {
				t.SessionID = e.Payload.SessionID
			}
			continue
		}
		if e.Type != "response_item" {
			continue
		}
		switch {
		case e.Payload.ItemType == "message" && e.Payload.Role == "assistant":
			var text strings.Builder
			for _, c := range e.Payload.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
			bs := blocks.Parse(text.String())
			if len(bs) == 0 {
				continue
			}
			t.Turns = append(t.Turns, adapter.Turn{Ordinal: len(t.Turns) + 1, ID: e.Payload.ID, Blocks: bs})
		case e.Payload.ItemType == "function_call":
			if n := len(t.Turns); n > 0 {
				t.Turns[n-1].Blocks = append(t.Turns[n-1].Blocks,
					blocks.Block{Kind: blocks.ToolCall, Text: e.Payload.Name, Parent: -1})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("codex: transcript: %w", err)
	}
	return t, nil
}

// Marker returns the turn marker a row begins with.
func (a *Adapter) Marker(row string) string { return rules.Marker(row) }

func verified(v string) bool {
	for _, ok := range Verified {
		if ok == v {
			return true
		}
	}
	return false
}

// Mode reports inline: the Codex CLI prints into the terminal and does not
// take the alternate screen, so Diple's scrollback is the history.
func (a *Adapter) Mode(s *screen.Screen) adapter.Mode {
	if s.AltActive() && s.MouseTracking() {
		return adapter.ModeFullscreen
	}
	return adapter.ModeInline
}

// Align maps each turn onto rows the way the Claude adapter does: turns take
// the turn-marker regions the rows actually carry, and a turn that matches
// nothing but sits between two matched turns still gets its region as
// paragraphs.
func (a *Adapter) Align(t *adapter.Transcript, rows []string) []adapter.TurnAlignment {
	return adapter.Assign(t, rows, rules)
}

// Fallback treats every turn-marker region as a turn of paragraphs.
func (a *Adapter) Fallback(rows []string) []adapter.TurnAlignment {
	return adapter.Paragraphed(rows, rules)
}

// InputRow finds the Codex input box: the last row that opens with the
// prompt marker.
func (a *Adapter) InputRow(screenRows []string) int {
	for i := len(screenRows) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(screenRows[i]), PromptMarker) {
			return i
		}
	}
	return -1
}

// Busy reports whether Codex is working: it draws a working line with an
// interrupt hint while a turn is in flight.
func (a *Adapter) Busy(s *screen.Screen) bool {
	for _, row := range s.Text() {
		low := strings.ToLower(row)
		if strings.Contains(low, "esc to interrupt") || strings.Contains(low, "working") && strings.Contains(low, "esc") {
			return true
		}
	}
	return false
}

// Prompt reports whether Codex is showing a question of its own: its
// directory-trust question and its approval questions offer numbered choices
// and tell the user which key confirms.
func (a *Adapter) Prompt(s *screen.Screen) bool {
	for _, row := range s.Text() {
		low := strings.ToLower(strings.TrimSpace(row))
		if strings.Contains(low, "press enter to continue") || strings.Contains(low, "esc to cancel") {
			return true
		}
	}
	return false
}

// QueuesWhenBusy is true: Codex accepts input while a turn runs and sends it
// when the turn ends, so Diple delivers a fold immediately.
func (a *Adapter) QueuesWhenBusy() bool { return true }
