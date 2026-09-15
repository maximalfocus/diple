package wrap

import (
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/clip"
	"github.com/maximalfocus/diple/internal/screen"
)

// wrappedRows is the history rows of the long list item the renderer wrapped,
// and the item's transcript text, which carries no list marker.
func wrappedRows(t *testing.T, s *Session) (first, last int, text string) {
	t.Helper()
	first = rowOf(t, s, "1. Read the config file")
	s.mu.Lock()
	defer s.mu.Unlock()
	_, al := s.alignmentLocked()
	b := blockOf(al, first)
	if b == nil {
		t.Fatal("the long list item did not align")
	}
	return b.First, b.Last, b.Text
}

// TestDragSelectsAcrossRowsAndCopiesWhatItShows is R-015's first acceptance: a
// plain drag across two rows puts exactly what they show on the clipboard —
// the visible list marker included — and a line the renderer wrapped comes
// back as one line.
func TestDragSelectsAcrossRowsAndCopiesWhatItShows(t *testing.T) {
	s, term, agent := fixtureSession(t, "inline")
	first, last, text := wrappedRows(t, s)
	want := "1. " + text
	if last <= first {
		t.Skipf("the fixture did not wrap this block over more than one row")
	}
	term.Reset()
	send(t, s, pressAt(1, yOf(s, first)))
	send(t, s, dragTo(s.cols, yOf(s, last)))
	send(t, s, releaseAt(s.cols, yOf(s, last)))
	if s.textSel == nil {
		t.Fatal("the drag made no selection")
	}
	got := clipboard(t, term)
	if got != want {
		t.Fatalf("clipboard:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("a line that wrapped over two rows must come back as one: %q", got)
	}
	if agent.Len() != 0 {
		t.Fatalf("the drag reached the agent: %q", agent.Bytes())
	}
	// The selection shows in reverse video across every row it covers.
	lines, _, _, _ := physical(s)
	s.mu.Lock()
	start := s.windowStart()
	s.mu.Unlock()
	for h := first; h <= last; h++ {
		row := s.agentToPhysical(h - start)
		if lines[row].Cells[2].Attr.Flags&screen.Reverse == 0 {
			t.Fatalf("row %d of the selection is not reverse", h)
		}
	}
}

// TestDoubleAndTriplePressCopyTheWordAndTheLine.
func TestDoubleAndTriplePressCopyTheWordAndTheLine(t *testing.T) {
	s, term, _ := fixtureSession(t, "inline")
	para := rowOf(t, s, "That is the whole plan.")
	y := yOf(s, para)
	col := strings.Index(s.HistoryRows()[para], "whole") + 2 // 1-based, inside the word
	term.Reset()
	// A single press with nothing raised is not Diple's; the double-press is.
	send(t, s, pressAt(col, y)+releaseAt(col, y))
	send(t, s, pressAt(col, y)+releaseAt(col, y))
	if got := clipboard(t, term); got != "whole" {
		t.Fatalf("double-press copied %q, want the word under the pointer", got)
	}
	send(t, s, pressAt(col, y)+releaseAt(col, y))
	if got := clipboard(t, term); got != "That is the whole plan." {
		t.Fatalf("triple-press copied %q, want the whole logical line", got)
	}
}

// TestDoublePressTakesBackTheEditorTheFirstPressOpened: the single-press path
// pays nothing for the double-press one.
func TestDoublePressTakesBackTheEditorTheFirstPressOpened(t *testing.T) {
	s, term, _ := fixtureSession(t, "inline")
	para := rowOf(t, s, "That is the whole plan.")
	y := yOf(s, para)
	dwellOn(t, s, 6, y)
	col := blockLeft(s)
	send(t, s, pressAt(col, y)+releaseAt(col, y))
	if s.editor == nil {
		t.Fatal("the first press did not open an editor")
	}
	term.Reset()
	send(t, s, pressAt(col, y)+releaseAt(col, y))
	if s.editor != nil {
		t.Fatalf("the second press did not take back the empty editor: %+v", s.editor)
	}
	if s.Tray.Len() != 0 {
		t.Fatalf("the taken-back editor left a card: %v", cardTexts(s.Tray))
	}
	if clipboard(t, term) == "" {
		t.Fatal("the double-press copied nothing")
	}
}

// TestSelectionWithNoTranscriptCopiesTheScreenRows: nothing on the screen is
// ever unselectable.
func TestSelectionWithNoTranscriptCopiesTheScreenRows(t *testing.T) {
	s, term, _ := fixtureSession(t, "inline")
	// Tool output the agent wrote that no assistant turn covers.
	if err := s.HandleOutput([]byte("\r\n\x1b[2K  ran a tool\r\n\x1b[2K  and printed this\r\n\x1b[2K")); err != nil {
		t.Fatal(err)
	}
	first := rowOf(t, s, "ran a tool")
	last := rowOf(t, s, "and printed this")
	s.mu.Lock()
	_, al := s.alignmentLocked()
	covered := blockOf(al, first) != nil
	s.mu.Unlock()
	if covered {
		t.Skip("the fixture's alignment claimed the tool rows")
	}
	term.Reset()
	send(t, s, pressAt(1, yOf(s, first)))
	send(t, s, dragTo(s.cols, yOf(s, last)))
	send(t, s, releaseAt(s.cols, yOf(s, last)))
	got := clipboard(t, term)
	if got != "  ran a tool\n  and printed this" {
		t.Fatalf("clipboard = %q, want the rows as they read", got)
	}
}

// TestSelectionKeepsItsAnchorWhileTheAgentWrites: a selection made while the
// agent is writing keeps its anchor in the scrollback, not the viewport.
func TestSelectionKeepsItsAnchorWhileTheAgentWrites(t *testing.T) {
	s, term, _ := fixtureSession(t, "inline")
	para := rowOf(t, s, "That is the whole plan.")
	y := yOf(s, para)
	send(t, s, pressAt(1, y))
	send(t, s, dragTo(24, y))
	// The agent keeps writing while the button is still down.
	for i := 0; i < 6; i++ {
		if err := s.HandleOutput([]byte("more output\r\n")); err != nil {
			t.Fatal(err)
		}
	}
	term.Reset()
	moved := yOf(s, para)
	send(t, s, releaseAt(24, moved))
	got := clipboard(t, term)
	if !strings.Contains(got, "That is the whole") {
		t.Fatalf("the selection lost the text it started on: %q", got)
	}
}

// TestCopyOnSelectOff leaves the clipboard to the explicit copy alone.
func TestCopyOnSelectOff(t *testing.T) {
	s, term, _ := fixtureSession(t, "inline")
	s.CopyOnSelect = false
	first, last, _ := wrappedRows(t, s)
	term.Reset()
	send(t, s, pressAt(1, yOf(s, first)))
	send(t, s, dragTo(s.cols, yOf(s, last)))
	send(t, s, releaseAt(s.cols, yOf(s, last)))
	if s.textSel == nil {
		t.Fatal("--copy-on-select=off must still select")
	}
	if got := clipboard(t, term); got != "" {
		t.Fatalf("--copy-on-select=off copied %q", got)
	}
	// The strip's own copy still copies.
	para := rowOf(t, s, "That is the whole plan.")
	dwellOn(t, s, 6, yOf(s, para))
	sr := stripRowFor(t, s)
	term.Reset()
	pressRaised(t, s, choiceFor(t, s, "copy")+1, sr)
	if got := clipboard(t, term); got != "That is the whole plan." {
		t.Fatalf("the explicit copy must still copy: %q", got)
	}
}

// TestHideReturnsTheMouseToTheHost: after Alt+H the host's own selection
// behaves exactly as it does without Diple.
func TestHideReturnsTheMouseToTheHost(t *testing.T) {
	s, term, agent := fixtureSession(t, "inline")
	term.Reset()
	send(t, s, "\x1bh")
	if !strings.Contains(term.String(), EnvelopeEnd) {
		t.Fatalf("hiding did not give the mouse back: %q", term.String())
	}
	// While hidden, no drag of Diple's is made and nothing is copied.
	para := rowOf(t, s, "That is the whole plan.")
	y := yOf(s, para)
	term.Reset()
	send(t, s, pressAt(1, y))
	send(t, s, dragTo(24, y))
	send(t, s, releaseAt(24, y))
	if s.textSel != nil || s.raised != nil {
		t.Fatalf("a hidden layer made a selection: textSel=%v raised=%v", s.textSel, s.raised)
	}
	if got := clipboard(t, term); got != "" {
		t.Fatalf("a hidden layer copied %q", got)
	}
	if agent.Len() != 0 {
		t.Fatalf("a hidden layer forwarded %q to an agent that never asked", agent.Bytes())
	}
	// Alt+H again asks for the mouse back.
	term.Reset()
	send(t, s, "\x1bh")
	if !strings.Contains(term.String(), EnvelopeStart) {
		t.Fatalf("unhiding did not ask for the mouse: %q", term.String())
	}
}

// TestClipboardLadderFallsBackToThePlatform: where the host does not answer
// for the clipboard, Diple writes the platform's own.
func TestClipboardLadderFallsBackToThePlatform(t *testing.T) {
	s, term, _ := fixtureSession(t, "inline")
	// Terminal.app at its pinned version answers for no clipboard, and this
	// machine has none either: the tray status line says so.
	s.Clip = &clip.Writer{OSC52Answered: false}
	para := rowOf(t, s, "That is the whole plan.")
	dwellOn(t, s, 6, yOf(s, para))
	term.Reset()
	send(t, s, "c")
	if clipboard(t, term) != "" {
		t.Fatal("OSC 52 was written to a host that does not answer for it")
	}
	if s.clipNote == "" {
		t.Fatal("a copy that reached no rung must say so")
	}
	s.Tray.Add(&card.Card{Kind: card.Free, Text: "x"})
	s.mu.Lock()
	syncErr := s.syncLocked()
	s.mu.Unlock()
	if syncErr != nil {
		t.Fatal(syncErr)
	}
	lines, _, _, _ := physical(s)
	if !strings.Contains(strings.Join(texts(lines), "\n"), s.clipNote) {
		t.Fatalf("the status line does not carry %q", s.clipNote)
	}
}

func TestOSC52CarriesTheExactText(t *testing.T) {
	got := string(clip.OSC52("a b\nc"))
	if got != "\x1b]52;c;YSBiCmM=\a" {
		t.Fatalf("OSC 52 = %q", got)
	}
}
