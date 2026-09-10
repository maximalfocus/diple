package wrap

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/adapter/pi"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/record"
)

const piFixtureDir = "../adapter/pi/testdata/3"

// piSession replays the recorded pi session through a Session with the pi
// adapter and the session file pi wrote for it.
func piSession(t *testing.T) (*Session, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	rec, err := record.Open(filepath.Join(piFixtureDir, "inline.recording.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(piFixtureDir, "inline.transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	a := &pi.Adapter{}
	tr, err := a.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	term := &bytes.Buffer{}
	agent := &bytes.Buffer{}
	s := NewSession(term, agent, rec.Header.Cols, rec.Header.Rows, nil)
	s.UseAdapter(a, "pi")
	s.Source = staticSource{tr}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	for _, ev := range rec.Events {
		switch ev.Kind {
		case record.KindOutput:
			if err := s.HandleOutput(ev.Data); err != nil {
				t.Fatal(err)
			}
		case record.KindResize:
			_ = s.Resize(ev.Cols, ev.Rows)
		}
	}
	term.Reset()
	agent.Reset()
	return s, term, agent
}

// piRow finds the history row of the reply that contains sub. pi echoes the
// prompt above its reply, so the last occurrence is the reply's.
func piRow(t *testing.T, s *Session, sub string) int {
	t.Helper()
	rows := s.HistoryRows()
	for i := len(rows) - 1; i >= 0; i-- {
		if strings.Contains(rows[i], sub) {
			return i
		}
	}
	t.Fatalf("no row contains %q in:\n%s", sub, strings.Join(rows, "\n"))
	return -1
}

// piY finds a row of the reply and returns the physical mouse row for it,
// scrolling Diple's viewport when the growing tray has pushed it out of
// sight — which is what a user does with the wheel.
func piY(t *testing.T, s *Session, sub string) int {
	t.Helper()
	hist := piRow(t, s, sub)
	if start := viewTop(s); hist < start {
		if err := s.Scroll(start - hist); err != nil {
			t.Fatal(err)
		}
		hist = piRow(t, s, sub)
	}
	return yOf(s, hist)
}

// TestPiFixtureReplaysTheAnnotateJourney is R-005 under pi.
func TestPiFixtureReplaysTheAnnotateJourney(t *testing.T) {
	s, _, agent := piSession(t)

	dwellOn(t, s, 5, piY(t, s, "That is the whole plan."))
	if s.raised == nil || s.raised.kind != blocks.Paragraph {
		t.Fatalf("raise = %+v", s.raised)
	}
	send(t, s, "f")
	send(t, s, "tighten this\r")
	c := s.Tray.Cards[0]
	if c.Tag != "fix" || c.Anchor.Quote != "That is the whole plan." {
		t.Fatalf("paragraph card = %+v", c)
	}

	dwellOn(t, s, 5, piY(t, s, "2. Change the handler"))
	send(t, s, "n")
	send(t, s, "\r")
	c = s.Tray.Cards[1]
	if c.Tag != "note" || c.Anchor.Kind != blocks.ListItem || c.Anchor.Ordinal != 2 ||
		c.Anchor.Quote != "Change the handler" {
		t.Fatalf("list card = %+v", c)
	}

	y1 := piY(t, s, "func handle(")
	y3 := piY(t, s, "  }")
	dwellOn(t, s, 5, y1)
	pressRaised(t, s, blockLeft(s), y1)
	dwellOn(t, s, 5, y3)
	send(t, s, shiftPressAt(blockLeft(s), y3))
	send(t, s, "\x1ba")
	send(t, s, "wrong status\r")
	c = s.Tray.Cards[2]
	if c.Tag != "ask" || c.Anchor.Kind != blocks.CodeLine || c.Anchor.Lines == nil ||
		c.Anchor.Lines.Last-c.Anchor.Lines.First != 2 || !strings.HasPrefix(c.Anchor.Quote, "func handle(") {
		t.Fatalf("line-range card = %+v", c)
	}

	// pi indents a list item one column less than the other agents, so the
	// drag starts one column earlier.
	y := itoa(piY(t, s, "1. Read the config file"))
	send(t, s, pressAt(5, atoi(y)))
	send(t, s, dragTo(19, atoi(y)))
	send(t, s, releaseAt(19, atoi(y)))
	if s.sel == nil || s.sel.span == nil || s.sel.text != "Read the config" {
		t.Fatalf("span selection = %+v text=%q", s.sel, s.sel.text)
	}
	send(t, s, "a")
	send(t, s, "which ports?\r")
	if s.Tray.Len() != 4 || agent.Len() != 0 {
		t.Fatalf("tray = %d cards, agent got %q", s.Tray.Len(), agent.Bytes())
	}
}

// TestPiFixtureReplaysTheKeyboardJourney is R-013 under pi.
func TestPiFixtureReplaysTheKeyboardJourney(t *testing.T) {
	s, _, agent := piSession(t)
	selectBlockByText(t, s, "That is the whole plan.")
	send(t, s, "f")
	send(t, s, "tighten this\r")
	selectBlockByText(t, s, "Change the handler")
	send(t, s, "n")
	send(t, s, "\r")
	selectBlockByText(t, s, "Read the config file")
	send(t, s, "v")
	send(t, s, "w")
	send(t, s, "w")
	if s.sel == nil || s.sel.text != "Read the config" {
		t.Fatalf("span by keyboard = %q", s.sel.text)
	}
	send(t, s, "a")
	send(t, s, "which ports?\r")
	if s.Tray.Len() != 3 || agent.Len() != 0 {
		t.Fatalf("tray = %d cards, agent got %q", s.Tray.Len(), agent.Bytes())
	}
	if s.Tray.Cards[1].Anchor.Ordinal != 2 {
		t.Fatalf("list card lost its ordinal: %+v", s.Tray.Cards[1])
	}
}

// TestPiFixtureReplaysTheFold is R-009 under pi.
func TestPiFixtureReplaysTheFold(t *testing.T) {
	s, _, agent := piSession(t)
	dwellOn(t, s, 5, piY(t, s, "That is the whole plan."))
	send(t, s, "f")
	send(t, s, "tighten this\r")
	dwellOn(t, s, 5, piY(t, s, "2. Change the handler"))
	send(t, s, "n")
	send(t, s, "\r")
	agent.Reset()
	send(t, s, "\x1b\r")
	want := pasteStart + `1. [fix] > "That is the whole plan."
   tighten this
2. [note] Option 2 of the list starting "Change the handler".` + pasteEnd + "\r"
	if agent.String() != want {
		t.Fatalf("fold:\n got %q\nwant %q", agent.String(), want)
	}
	if s.Tray.Len() != 0 {
		t.Fatalf("tray not emptied: %d", s.Tray.Len())
	}
}
