package wrap

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/fold"
	"github.com/maximalfocus/diple/internal/screen"
)

// busyAdapter is a non-queuing adapter whose busy state the test controls.
type busyAdapter struct{ busy bool }

func (b *busyAdapter) Name() string               { return "busy" }
func (b *busyAdapter) VerifiedVersions() []string { return []string{"1"} }
func (b *busyAdapter) Bypass([]string) bool       { return false }
func (b *busyAdapter) Discover(string, time.Time) (string, error) {
	return "", adapter.ErrNoTranscript
}
func (b *busyAdapter) Parse(io.Reader) (*adapter.Transcript, error) {
	return &adapter.Transcript{}, nil
}
func (b *busyAdapter) Mode(*screen.Screen) adapter.Mode                            { return adapter.ModeInline }
func (b *busyAdapter) Align(*adapter.Transcript, []string) []adapter.TurnAlignment { return nil }
func (b *busyAdapter) Fallback([]string) []adapter.TurnAlignment                   { return nil }
func (b *busyAdapter) InputRow([]string) int                                       { return -1 }
func (b *busyAdapter) Busy(*screen.Screen) bool                                    { return b.busy }
func (b *busyAdapter) QueuesWhenBusy() bool                                        { return false }

func threeCardTray() *card.Tray {
	tr := &card.Tray{}
	tr.Add(&card.Card{Kind: card.Note, Tag: "fix", Text: "tighten this", Anchor: card.Anchor{Turn: 1, Kind: blocks.Paragraph, Quote: "That is the whole plan."}})
	tr.Add(&card.Card{Kind: card.Note, Tag: "prefer", Text: "pick this", Anchor: card.Anchor{Turn: 1, Kind: blocks.ListItem, Ordinal: 2, Quote: "Change the handler"}})
	tr.Add(&card.Card{Kind: card.Note, Tag: "reject", Text: "wrong status", Anchor: card.Anchor{Turn: 1, Kind: blocks.CodeLine, Quote: "func handle() {}"}})
	return tr
}

const wantFold = `Review (3 items).

1. [fix] > "That is the whole plan."
   tighten this
2. [prefer] Option 2 of the list starting "Change the handler".
   pick this
3. [reject] > "func handle() {}"
   wrong status`

func TestSendGestureDeliversExactFoldAndEmpties(t *testing.T) {
	s, _, agent := newTestSession(80, 24)
	s.Tray = threeCardTray()
	// Alt+Enter sends.
	if err := s.HandleInput([]byte("\x1b\r")); err != nil {
		t.Fatal(err)
	}
	want := pasteStart + wantFold + pasteEnd + "\r"
	if agent.String() != want {
		t.Fatalf("agent got:\n%q\nwant:\n%q", agent.String(), want)
	}
	if s.Tray.Len() != 0 || s.Composited() {
		t.Fatalf("tray not emptied: len=%d composited=%v", s.Tray.Len(), s.Composited())
	}
}

func TestPasteOnlyLeavesEditableAndKeepsTray(t *testing.T) {
	s, _, agent := newTestSession(80, 24)
	s.Tray = threeCardTray()
	if err := s.HandleInput([]byte("\x1bp")); err != nil {
		t.Fatal(err)
	}
	got := agent.String()
	if !strings.HasPrefix(got, pasteStart) || !strings.HasSuffix(got, pasteEnd) {
		t.Fatalf("paste not bracketed: %q", got)
	}
	if strings.HasSuffix(got, "\r") || strings.Contains(got, pasteEnd+"\r") {
		t.Fatalf("paste-only must not submit: %q", got)
	}
	if s.Tray.Len() != 3 {
		t.Fatalf("paste-only changed the tray: %d", s.Tray.Len())
	}
}

func TestSendHeldWhileBusyThenDeliveredOnIdle(t *testing.T) {
	term := &bytes.Buffer{}
	agent := &bytes.Buffer{}
	ba := &busyAdapter{busy: true}
	s := NewSession(term, agent, 80, 24, nil)
	s.UseAdapter(ba, "busy")
	s.Tray = threeCardTray()

	if err := s.HandleInput([]byte("\x1b\r")); err != nil {
		t.Fatal(err)
	}
	if agent.Len() != 0 {
		t.Fatalf("busy send delivered early: %q", agent.Bytes())
	}
	if !s.pendingSubmit {
		t.Fatal("send was not held")
	}
	lines, _, _, _ := physical(s)
	if !strings.Contains(strings.Join(texts(lines), "\n"), "will send when idle") {
		t.Fatal("status line does not show the pending send")
	}
	// More output while still busy does not deliver.
	_ = s.HandleOutput([]byte("still working\r\n"))
	if agent.Len() != 0 {
		t.Fatalf("delivered while still busy: %q", agent.Bytes())
	}
	// The agent goes idle: the fold is delivered exactly once.
	ba.busy = false
	_ = s.HandleOutput([]byte("done\r\n"))
	want := pasteStart + wantFold + pasteEnd + "\r"
	if agent.String() != want {
		t.Fatalf("on idle agent got:\n%q\nwant:\n%q", agent.String(), want)
	}
	if s.Tray.Len() != 0 || s.pendingSubmit {
		t.Fatalf("after delivery: tray=%d pending=%v", s.Tray.Len(), s.pendingSubmit)
	}
	_ = s.HandleOutput([]byte("more\r\n"))
	if agent.String() != want {
		t.Fatal("delivered more than once")
	}
}

func TestSendArchivesAndNoArchiveDoesNot(t *testing.T) {
	dir := t.TempDir()
	ar, err := fold.NewArchive(dir, ".diple-archive.md")
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := newTestSession(80, 24)
	s.Tray = threeCardTray()
	s.UseArchive(ar)
	if err := s.HandleInput([]byte("\x1b\r")); err != nil {
		t.Fatal(err)
	}
	data, err := readFile(filepath.Join(dir, ".diple-archive.md"))
	if err != nil || !strings.Contains(data, wantFold) {
		t.Fatalf("archive = %q err %v", data, err)
	}

	// Paste-only does not archive.
	dir2 := t.TempDir()
	ar2, _ := fold.NewArchive(dir2, ".diple-archive.md")
	s2, _, _ := newTestSession(80, 24)
	s2.Tray = threeCardTray()
	s2.UseArchive(ar2)
	_ = s2.HandleInput([]byte("\x1bp"))
	if _, err := readFile(filepath.Join(dir2, ".diple-archive.md")); err == nil {
		t.Fatal("paste-only wrote an archive")
	}
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

func TestSendGestureForwardedWhenTrayEmpty(t *testing.T) {
	s, _, agent := newTestSession(80, 24)
	if err := s.HandleInput([]byte("\x1b\r")); err != nil {
		t.Fatal(err)
	}
	if agent.String() != "\x1b\r" {
		t.Fatalf("empty-tray Alt+Enter must reach the agent: %q", agent.String())
	}
}
