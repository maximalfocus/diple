package wrap

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/fold"
)

// freeCardSession is a session with a store and a working directory, as a
// wrapped run has, but with no agent output of its own.
func freeCardSession(t *testing.T) (*Session, *card.Store, string, *bytes.Buffer) {
	t.Helper()
	s, _, agent := newTestSession(80, 24)
	dir := t.TempDir()
	st := &card.Store{Dir: filepath.Join(dir, "trays")}
	s.UseStore(st)
	s.UseDir(dir)
	s.agentName = "claude"
	s.sessionID = "sess-1"
	return s, st, dir, agent
}

func TestFreeCardsAreWrittenFromAnEmptyTray(t *testing.T) {
	s, st, dir, _ := freeCardSession(t)
	if err := os.WriteFile(filepath.Join(dir, "here.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Alt+N, then the kind's letter, then the text.
	send(t, s, "\x1bn")
	if !s.chooser {
		t.Fatal("Alt+N did not open the chooser")
	}
	lines, _, _, _ := physical(s)
	if !strings.Contains(strings.Join(texts(lines), "\n"), "question  instruction  overall") {
		t.Fatalf("chooser not drawn:\n%s", strings.Join(texts(lines), "\n"))
	}
	send(t, s, "q")
	send(t, s, "which ports?\r")
	if s.Tray.Len() != 1 || s.Tray.Cards[0].Kind != card.Question || s.Tray.Cards[0].Text != "which ports?" {
		t.Fatalf("question card: %+v", s.Tray.Cards)
	}
	// An instruction card takes attachments from its own editor, and the
	// editor stays open for the next line.
	send(t, s, "\x1bn")
	send(t, s, "i")
	send(t, s, "@here.txt\r")
	if s.editor == nil || len(s.editor.attached) != 1 || s.editor.attached[0].Spec != "here.txt" {
		t.Fatalf("path attachment: editor=%+v", s.editor)
	}
	send(t, s, "!echo captured\r")
	if s.editor == nil || len(s.editor.attached) != 2 || !strings.Contains(s.editor.attached[1].Output, "captured") {
		t.Fatalf("command attachment: %+v", s.editor.attached)
	}
	send(t, s, "rebase first\r")
	if s.editor != nil || s.Tray.Len() != 2 {
		t.Fatalf("instruction not committed: editor=%v len=%d", s.editor, s.Tray.Len())
	}
	ins := s.Tray.Cards[1]
	if ins.Kind != card.Instruction || ins.Text != "rebase first" || len(ins.Attachments) != 2 {
		t.Fatalf("instruction card: %+v", ins)
	}
	// The overall card is created the same way and stays last.
	send(t, s, "\x1bn")
	send(t, s, "o")
	send(t, s, "keep it small\r")
	if s.Tray.Len() != 3 || s.Tray.Overall() != s.Tray.Cards[2] {
		t.Fatalf("overall card: %+v", s.Tray.Cards)
	}
	// The tray persisted; a restart restores it exactly.
	stored, err := st.Load("claude", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Len() != 3 || stored.Cards[1].Attachments[1].Output != ins.Attachments[1].Output ||
		stored.Cards[2].Kind != card.Overall {
		t.Fatalf("stored tray: %+v", stored.Cards)
	}
	want := fold.Compile(s.Tray.Cards, 1)
	if !strings.Contains(want, "2. [instruction] rebase first") || !strings.Contains(want, "Overall: keep it small") ||
		!strings.HasPrefix(want, "Review (2 items).") {
		t.Fatalf("compiled:\n%s", want)
	}
}

func TestChooserKeysReachTheAgentWhenTheTrayIsNotFocused(t *testing.T) {
	s, _, _, agent := freeCardSession(t)
	// '+' is the tray's key, not Diple's, while the agent has focus.
	send(t, s, "+")
	if agent.String() != "+" {
		t.Fatalf("agent got %q, want %q", agent.String(), "+")
	}
	// A stray letter after the chooser closes it and is not swallowed twice.
	send(t, s, "\x1bn")
	send(t, s, "z")
	if s.chooser || s.editor != nil {
		t.Fatalf("chooser=%v editor=%v after an unknown letter", s.chooser, s.editor)
	}
}

func TestTrayPlusOpensTheChooser(t *testing.T) {
	s, _, _, _ := freeCardSession(t)
	s.Tray.Add(&card.Card{Kind: card.Note, Tag: "fix", Text: "a"})
	send(t, s, "\t") // focus the tray
	if s.focus != focusTray {
		t.Fatal("tab did not focus the tray")
	}
	send(t, s, "+")
	if !s.chooser {
		t.Fatal("+ did not open the chooser from the tray")
	}
	send(t, s, "i")
	if s.editor == nil || s.editor.kind != card.Instruction {
		t.Fatalf("editor = %+v", s.editor)
	}
}

func TestEveryKindEditsReordersDeletesAndSurvivesARestart(t *testing.T) {
	s, st, _, _ := freeCardSession(t)
	for _, in := range []string{"\x1bnqfirst question\r", "\x1bniship it\r", "\x1bnokeep it small\r"} {
		send(t, s, in)
	}
	if s.Tray.Len() != 3 || s.focus != focusTray {
		t.Fatalf("tray = %d cards, focus = %v", s.Tray.Len(), s.focus)
	}
	// Edit the question in place.
	s.traySel = 0
	send(t, s, "e")
	if s.editor == nil || s.editor.kind != card.Question {
		t.Fatalf("editor = %+v", s.editor)
	}
	send(t, s, "\x7f\x7f\x7f\x7f\x7f\x7f\x7f\x7fports?\r")
	if got := s.Tray.Cards[0].Text; got != "first ports?" {
		t.Fatalf("edited text = %q", got)
	}
	// Reorder the two ordinary cards; the overall stays last.
	send(t, s, "J")
	if s.Tray.Cards[0].Kind != card.Instruction || s.Tray.Cards[2].Kind != card.Overall {
		t.Fatalf("after J: %+v", s.Tray.Cards)
	}
	// Delete the instruction.
	s.traySel = 0
	send(t, s, "d")
	if s.Tray.Len() != 2 || s.Tray.Cards[0].Kind != card.Question {
		t.Fatalf("after delete: %+v", s.Tray.Cards)
	}
	// A restart of the agent and the terminal reloads the same tray.
	stored, err := st.Load("claude", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Len() != 2 || stored.Cards[0].Text != "first ports?" || stored.Cards[1].Kind != card.Overall {
		t.Fatalf("reloaded: %+v", stored.Cards)
	}
	fresh, _, _ := newTestSession(80, 24)
	fresh.UseStore(st)
	fresh.agentName = "claude"
	if err := fresh.SetSessionID("sess-1"); err != nil {
		t.Fatal(err)
	}
	if fresh.Tray.Len() != 2 || fresh.Tray.Cards[0].Text != "first ports?" || fresh.Tray.Overall() == nil {
		t.Fatalf("restored session tray: %+v", fresh.Tray.Cards)
	}
}

func TestUnstashedTrayIsPickedUpByTheNextSession(t *testing.T) {
	s, st, _, _ := freeCardSession(t)
	send(t, s, "\x1bnqwhich ports?\r")
	if s.Tray.Len() != 1 {
		t.Fatalf("tray = %d", s.Tray.Len())
	}
	if n, err := st.Stash("claude"); err != nil || n != 1 {
		t.Fatalf("stash: n=%d err=%v", n, err)
	}
	if n, err := st.Unstash("claude"); err != nil || n != 1 {
		t.Fatalf("unstash: n=%d err=%v", n, err)
	}
	next, _, _ := newTestSession(80, 24)
	next.UseStore(st)
	next.agentName = "claude"
	if err := next.SetSessionID("sess-9"); err != nil {
		t.Fatal(err)
	}
	if next.Tray.Len() != 1 || next.Tray.Cards[0].Text != "which ports?" {
		t.Fatalf("unstashed into the next session: %+v", next.Tray.Cards)
	}
}
