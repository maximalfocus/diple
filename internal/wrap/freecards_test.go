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

// TestFreeCardsAreWrittenFromAnEmptyTray: a free card is untagged prose, which
// is what the user would otherwise have typed into the box, and Alt+N opens
// the same editor on Diple's own overlay row.
func TestFreeCardsAreWrittenFromAnEmptyTray(t *testing.T) {
	s, st, dir, _ := freeCardSession(t)
	if err := os.WriteFile(filepath.Join(dir, "here.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	send(t, s, "\x1bn")
	if s.editor == nil || s.editor.kind != card.Free {
		t.Fatalf("Alt+N did not open the free editor: %+v", s.editor)
	}
	send(t, s, "which ports?\r")
	c := s.Tray.Cards[0]
	if s.Tray.Len() != 1 || c.Kind != card.Free || c.Text != "which ports?" || c.Tag != "" {
		t.Fatalf("free card: %+v", s.Tray.Cards)
	}
	// A free card takes attachments from its own editor, and the editor stays
	// open for the next line.
	send(t, s, "\x1bn")
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
		t.Fatalf("free card not committed: editor=%v len=%d", s.editor, s.Tray.Len())
	}
	ins := s.Tray.Cards[1]
	if ins.Kind != card.Free || ins.Text != "rebase first" || len(ins.Attachments) != 2 {
		t.Fatalf("free card with attachments: %+v", ins)
	}
	// The overall card is the tray's one closing remark and stays last.
	send(t, s, "\x1bo")
	send(t, s, "keep it small\r")
	if s.Tray.Len() != 3 || s.Tray.Overall() != s.Tray.Cards[2] {
		t.Fatalf("overall card: %+v", s.Tray.Cards)
	}
	// A second overall edits the first.
	send(t, s, "\x1bo")
	if s.editor == nil || s.editor.editing != s.Tray.Overall() {
		t.Fatalf("a second overall must edit the first: %+v", s.editor)
	}
	send(t, s, "\x1b")
	// The tray persisted; a restart restores it exactly.
	stored, err := st.Load("claude", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Len() != 3 || stored.Cards[1].Attachments[1].Output != ins.Attachments[1].Output ||
		!stored.Cards[2].Overall {
		t.Fatalf("stored tray: %+v", stored.Cards)
	}
	want := fold.Compile(s.Tray.Cards, 1)
	if !strings.Contains(want, "2. rebase first") || !strings.Contains(want, "Overall: keep it small") ||
		strings.HasPrefix(want, "Review") {
		t.Fatalf("compiled:\n%s", want)
	}
}

func TestTrayKeysReachTheAgentWhenTheTrayIsNotFocused(t *testing.T) {
	s, _, _, agent := freeCardSession(t)
	// '+' is the tray's key, not Diple's, while the agent has focus.
	send(t, s, "+")
	if agent.String() != "+" {
		t.Fatalf("agent got %q, want %q", agent.String(), "+")
	}
}

func TestTrayPlusWritesAFreeCard(t *testing.T) {
	s, _, _, _ := freeCardSession(t)
	s.Tray.Add(&card.Card{Kind: card.Anchored, Tag: "fix", Text: "a"})
	send(t, s, "\t") // focus the tray
	if s.focus != focusTray {
		t.Fatal("tab did not focus the tray")
	}
	send(t, s, "+")
	if s.editor == nil || s.editor.kind != card.Free {
		t.Fatalf("editor = %+v", s.editor)
	}
}

func TestEveryCardEditsReordersDeletesAndSurvivesARestart(t *testing.T) {
	s, st, _, _ := freeCardSession(t)
	for _, in := range []string{"\x1bnfirst question\r", "\x1bnship it\r", "\x1bokeep it small\r"} {
		send(t, s, in)
	}
	if s.Tray.Len() != 3 || s.focus != focusTray {
		t.Fatalf("tray = %d cards, focus = %v", s.Tray.Len(), s.focus)
	}
	// Edit the first card in place.
	s.traySel = 0
	send(t, s, "e")
	if s.editor == nil || s.editor.editing == nil {
		t.Fatalf("editor = %+v", s.editor)
	}
	send(t, s, "\x7f\x7f\x7f\x7f\x7f\x7f\x7f\x7fports?\r")
	if got := s.Tray.Cards[0].Text; got != "first ports?" {
		t.Fatalf("edited text = %q", got)
	}
	// Reorder the two ordinary cards; the overall stays last.
	send(t, s, "J")
	if s.Tray.Cards[0].Text != "ship it" || !s.Tray.Cards[2].Overall {
		t.Fatalf("after J: %v", cardTexts(s.Tray))
	}
	// Delete the first.
	s.traySel = 0
	send(t, s, "d")
	if s.Tray.Len() != 2 || s.Tray.Cards[0].Text != "first ports?" {
		t.Fatalf("after delete: %v", cardTexts(s.Tray))
	}
	// A restart of the agent and the terminal reloads the same tray.
	stored, err := st.Load("claude", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Len() != 2 || stored.Cards[0].Text != "first ports?" || !stored.Cards[1].Overall {
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

// TestEmptyingACardRemovesIt: a card with nothing in it was never a card.
func TestEmptyingACardRemovesIt(t *testing.T) {
	s, _, _, _ := freeCardSession(t)
	send(t, s, "\x1bnkeep me\r")
	send(t, s, "\x1bnremove me\r")
	if s.Tray.Len() != 2 {
		t.Fatalf("tray = %d", s.Tray.Len())
	}
	s.traySel = 1
	send(t, s, "e")
	for i := 0; i < len("remove me"); i++ {
		send(t, s, "\x7f")
	}
	send(t, s, "\r")
	if s.Tray.Len() != 1 || s.Tray.Cards[0].Text != "keep me" {
		t.Fatalf("emptying a card must remove it: %v", cardTexts(s.Tray))
	}
}

func TestUnstashedTrayIsPickedUpByTheNextSession(t *testing.T) {
	s, st, _, _ := freeCardSession(t)
	send(t, s, "\x1bnwhich ports?\r")
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
