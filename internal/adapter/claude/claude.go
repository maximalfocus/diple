// Package claude is the adapter for Claude Code.
package claude

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

// Verified lists the Claude Code versions the fixtures under testdata pin.
var Verified = []string{"2.1.266", "2.1.268"}

// Decoration facts of Claude Code's renderer.
const (
	TurnMarker   = "⏺"
	PromptMarker = "❯"
	ResultMarker = "⎿"
)

var rules = align.Rules{TurnMarker: TurnMarker, PromptMarker: PromptMarker, ResultMarker: ResultMarker}

// Adapter implements adapter.Adapter for Claude Code.
type Adapter struct {
	// Home is the user's home directory; empty means the current user's.
	Home string
}

func init() { adapter.Register(&Adapter{}) }

// Name returns "claude".
func (a *Adapter) Name() string { return "claude" }

// VerifiedVersions returns the pinned versions.
func (a *Adapter) VerifiedVersions() []string { return append([]string(nil), Verified...) }

// Bypass reports the non-interactive forms.
func (a *Adapter) Bypass(args []string) bool { return agent.Bypass(args) }

func (a *Adapter) home() (string, error) {
	if a.Home != "" {
		return a.Home, nil
	}
	return os.UserHomeDir()
}

// ProjectDir returns the directory Claude Code keeps transcripts in for a
// working directory: every character outside letters and digits becomes a
// dash, under ~/.claude/projects.
func ProjectDir(home, cwd string) string {
	var b strings.Builder
	for _, r := range cwd {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return filepath.Join(home, ".claude", "projects", b.String())
}

// Discover returns the transcript of the session started in cwd at or after
// since. Claude Code creates the file after the first reply, so callers
// retry while ErrNoTranscript is returned. A file's modification time is not
// enough to bind it: Claude Code keeps writing to an earlier session's
// transcript after that session exits, so the candidate's own first entry
// timestamp must fall at or after the start.
func (a *Adapter) Discover(cwd string, since time.Time) (string, error) {
	home, err := a.home()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	entries, err := os.ReadDir(ProjectDir(home, cwd))
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
		cands = append(cands, cand{filepath.Join(ProjectDir(home, cwd), e.Name()), info.ModTime()})
	}
	if len(cands) == 0 {
		return "", adapter.ErrNoTranscript
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	for _, c := range cands {
		if started, ok := firstTimestamp(c.path); ok && !started.Before(cutoff) {
			return c.path, nil
		}
	}
	return "", adapter.ErrNoTranscript
}

// firstTimestamp reads the earliest timestamp in a transcript's leading
// entries, which Claude Code stamps on every user and assistant line.
func firstTimestamp(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for n := 0; sc.Scan() && n < 200; n++ {
		var e struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Timestamp == "" {
			continue
		}
		if ts, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

type entry struct {
	Type      string `json:"type"`
	Version   string `json:"version"`
	SessionID string `json:"sessionId"`
	Message   struct {
		ID      string          `json:"id"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type part struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Parse reads a Claude Code JSON-lines transcript. Assistant entries that
// share a message id form one turn; text parts become Markdown blocks and
// tool-use parts become tool-call blocks.
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
			return nil, fmt.Errorf("claude: transcript line %d: %w", line, err)
		}
		if e.Version != "" && t.Version == "" {
			t.Version = e.Version
			t.Unverified = true
			for _, v := range Verified {
				if v == e.Version {
					t.Unverified = false
				}
			}
		}
		if e.SessionID != "" && t.SessionID == "" {
			t.SessionID = e.SessionID
		}
		if e.Type != "assistant" {
			continue
		}
		var parts []part
		if err := json.Unmarshal(e.Message.Content, &parts); err != nil {
			// A string content is a single text part.
			var s string
			if json.Unmarshal(e.Message.Content, &s) != nil {
				continue
			}
			parts = []part{{Type: "text", Text: s}}
		}
		var bs []blocks.Block
		for _, p := range parts {
			switch p.Type {
			case "text":
				bs = append(bs, blocks.Parse(p.Text)...)
			case "tool_use":
				bs = append(bs, blocks.Block{Kind: blocks.ToolCall, Text: toolSummary(p), Parent: -1})
			}
		}
		if len(bs) == 0 {
			continue
		}
		if n := len(t.Turns); n > 0 && e.Message.ID != "" && t.Turns[n-1].ID == e.Message.ID {
			base := len(t.Turns[n-1].Blocks)
			for i := range bs {
				if bs[i].Parent >= 0 {
					bs[i].Parent += base
				}
			}
			t.Turns[n-1].Blocks = append(t.Turns[n-1].Blocks, bs...)
			continue
		}
		t.Turns = append(t.Turns, adapter.Turn{Ordinal: len(t.Turns) + 1, ID: e.Message.ID, Blocks: bs})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("claude: transcript: %w", err)
	}
	return t, nil
}

// toolSummary renders a tool-use part the way Claude Code prints it:
// the tool name and its primary argument in parentheses.
func toolSummary(p part) string {
	var input map[string]json.RawMessage
	_ = json.Unmarshal(p.Input, &input)
	for _, key := range []string{"command", "file_path", "path", "pattern", "url", "description", "prompt"} {
		if raw, ok := input[key]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				if i := strings.IndexByte(s, '\n'); i >= 0 {
					s = s[:i]
				}
				return p.Name + "(" + s + ")"
			}
		}
	}
	return p.Name
}

// Mode reports fullscreen when Claude Code has switched to the alternate
// screen and asked for mouse tracking, which its fullscreen TUI does.
func (a *Adapter) Mode(s *screen.Screen) adapter.Mode {
	if s.AltActive() && s.MouseTracking() {
		return adapter.ModeFullscreen
	}
	return adapter.ModeInline
}

// Align maps each turn onto the rows Claude Code drew, using the shared
// region matching with Claude's own decoration facts.
func (a *Adapter) Align(t *adapter.Transcript, rows []string) []adapter.TurnAlignment {
	return adapter.Assign(t, rows, rules)
}

// Fallback treats every turn-marker region as a turn of paragraphs.
func (a *Adapter) Fallback(rows []string) []adapter.TurnAlignment {
	return adapter.Paragraphed(rows, rules)
}

// InputRow finds Claude Code's input box on the visible screen: the last
// row that begins with the prompt marker, together with the rule row drawn
// directly above it when there is one.
func (a *Adapter) InputRow(screenRows []string) int {
	for i := len(screenRows) - 1; i >= 0; i-- {
		if !strings.HasPrefix(screenRows[i], PromptMarker) {
			continue
		}
		if i > 0 && isRule(screenRows[i-1]) {
			return i - 1
		}
		return i
	}
	return -1
}

// isRule reports a horizontal rule row: only box-drawing dashes.
func isRule(row string) bool {
	row = strings.TrimSpace(row)
	if row == "" {
		return false
	}
	for _, r := range row {
		if r != '─' && r != '━' && r != '-' {
			return false
		}
	}
	return true
}

// Busy reports whether Claude Code is working: its footer shows an interrupt
// hint while a turn is in flight.
func (a *Adapter) Busy(s *screen.Screen) bool {
	for _, row := range s.Text() {
		if strings.Contains(row, "esc to interrupt") || strings.Contains(row, "to interrupt)") {
			return true
		}
	}
	return false
}

// Prompt reports whether Claude Code is showing one of its own dialogs: a
// permission question, the model or effort chooser, or the workspace-trust
// question. Every one of them ends in the same footer offer to cancel, which
// the working footer ("esc to interrupt") never carries.
func (a *Adapter) Prompt(s *screen.Screen) bool {
	for _, row := range s.Text() {
		if strings.Contains(strings.ToLower(row), "esc to cancel") {
			return true
		}
	}
	return false
}

// QueuesWhenBusy is true: Claude Code accepts typed or pasted input while a
// turn runs and processes it when idle, so Diple sends immediately.
func (a *Adapter) QueuesWhenBusy() bool { return true }
