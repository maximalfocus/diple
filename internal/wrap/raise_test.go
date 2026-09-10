package wrap

import (
	"strings"
	"testing"
	"time"

	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/screen"
)

// pointerAway moves the pointer to a row no block covers and lets the raise's
// linger run out, which is when the cells it borrowed are given back.
func pointerAway(t *testing.T, s *Session) {
	t.Helper()
	send(t, s, motionAt(1, blankRow(t, s)))
	advance(s, linger+time.Millisecond)
	if err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	if s.raised != nil {
		t.Fatalf("the pointer left, but a block is still raised: %+v", s.raised)
	}
}

// blankRow is a 1-based physical row that no aligned block occupies, which is
// where a pointer goes when it means to be nowhere.
func blankRow(t *testing.T, s *Session) int {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, al := s.alignmentLocked()
	start := s.windowStart()
	for r := s.rows - s.trayH - 1; r >= 0; r-- {
		if blockOf(al, start+r) == nil {
			return s.agentToPhysical(r) + 1
		}
	}
	t.Fatal("every row on screen carries a block")
	return 0
}

// TestRaiseIsReverseOverItsWholeBoundingBox: the box runs from the block's
// smallest indent to one column past its longest row, so a hanging indent
// keeps the edge straight and the short rows' tails are filled.
func TestRaiseIsReverseOverItsWholeBoundingBox(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	item := rowOf(t, s, "1. Read the config file")
	dwellOn(t, s, 6, yOf(s, item))
	r := s.raised
	if r == nil {
		t.Fatal("nothing rose")
	}
	if r.last < r.first {
		t.Fatalf("raise rows %d..%d", r.first, r.last)
	}
	lines, _, _, _ := physical(s)
	s.mu.Lock()
	start := s.windowStart()
	s.mu.Unlock()
	for h := r.first; h <= r.last; h++ {
		row := s.agentToPhysical(h - start)
		for x := r.left; x <= r.right; x++ {
			if lines[row].Cells[x].Attr.Flags&screen.Reverse == 0 {
				t.Fatalf("row %d col %d is not reverse: the box must fill the short rows' tails", h, x)
			}
		}
		// One column past the box belongs to the agent again.
		if r.right+1 < len(lines[row].Cells) && lines[row].Cells[r.right+1].Attr.Flags&screen.Reverse != 0 {
			t.Fatalf("row %d col %d is reverse past the box's edge", h, r.right+1)
		}
	}
}

// TestStripIsOpaqueAndBorrowedCellsComeBack is the other half of R-005: the
// strip writes every cell it covers, its own pad cells included, and every
// cell the raise and the strip borrow is restored attribute for attribute the
// moment the pointer leaves.
func TestStripIsOpaqueAndBorrowedCellsComeBack(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	before, _, _, _ := physical(s)
	item := rowOf(t, s, "2. Change the handler")
	dwellOn(t, s, 6, yOf(s, item))
	if s.raised == nil || !s.raised.strip {
		t.Fatalf("the strip did not wipe in: %+v", s.raised)
	}
	sr := stripRowFor(t, s) - 1 // 0-based physical row
	lines, _, _, _ := physical(s)
	choices, first, last := stripLayout(s.raised.left)
	covered := before[sr]
	changedSomething := false
	for x := first; x <= last && x < len(lines[sr].Cells); x++ {
		if lines[sr].Cells[x] != covered.Cells[x] {
			changedSomething = true
		}
	}
	if !changedSomething {
		t.Fatal("the strip covered nothing: it must be opaque over what lies under it")
	}
	// Every cell between the strip's first and last is Diple's own: either a
	// choice's letter, the divider, or one of its pad blanks.
	own := map[int]rune{}
	for _, c := range choices {
		for i, r := range c.label {
			own[c.from+i] = r
		}
	}
	own[stripDividerCol(choices)] = '│'
	for x := first; x <= last && x < len(lines[sr].Cells); x++ {
		want, isLabel := own[x]
		got := lines[sr].Cells[x].Rune
		if isLabel && got != want {
			t.Fatalf("strip col %d = %q, want %q", x, got, want)
		}
		if !isLabel && got != ' ' {
			t.Fatalf("strip pad col %d showed %q through: the strip must be opaque", x, got)
		}
	}
	// The tag in force is underlined, so the strip and the editor's chip never
	// say different things.
	for _, c := range choices {
		under := lines[sr].Cells[c.from].Attr.Flags&screen.Underline != 0
		if (c.tag == card.DefaultTag) != under {
			t.Fatalf("choice %q underlined=%v, in force=%q", c.label, under, card.DefaultTag)
		}
	}
	// The pointer leaves: every borrowed cell comes back attribute for
	// attribute.
	pointerAway(t, s)
	if s.raised != nil {
		t.Fatal("the raise outlived its linger")
	}
	after, _, _, _ := physical(s)
	for i := range before {
		if !after[i].Equal(before[i]) {
			t.Fatalf("row %d was not given back:\n got %q\nwant %q", i, after[i].String(), before[i].String())
		}
	}
}

// TestPointerTravelsFromBlockToStripWithoutDroppingTheRaise: the block and its
// strip are one target.
func TestPointerTravelsFromBlockToStripWithoutDroppingTheRaise(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	item := rowOf(t, s, "2. Change the handler")
	dwellOn(t, s, 6, yOf(s, item))
	sr := stripRowFor(t, s)
	fixCol := choiceFor(t, s, "fix")
	// Move onto the strip's fix cells: the raise holds.
	send(t, s, motionAt(fixCol+1, sr))
	if s.raised == nil || s.raised.first != item {
		t.Fatalf("the raise dropped on the way to the strip: %+v", s.raised)
	}
	// Pressing that tag opens the editor named for it.
	pressRaised(t, s, fixCol+1, sr)
	if s.editor == nil || s.editor.tag != "fix" {
		t.Fatalf("editor = %+v, want the tag that was pressed", s.editor)
	}
}

// TestPointerOffTheStripsTagsRaisesWhatTheStripCovers: a strip never stands
// between the user and the block beneath it.
func TestPointerOffTheStripsTagsRaisesWhatTheStripCovers(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	item := rowOf(t, s, "2. Change the handler")
	dwellOn(t, s, 6, yOf(s, item))
	sr := stripRowFor(t, s)
	_, _, last := stripLayout(s.raised.left)
	covered := item + 1
	// A pointer past the strip's last choice belongs to what it covers.
	send(t, s, motionAt(last+3, sr))
	advance(s, raiseDwell+stripDelay+time.Millisecond)
	if err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	if s.raised == nil || s.raised.first != covered {
		t.Fatalf("raise = %+v, want the block at row %d that the strip covered", s.raised, covered)
	}
}

// TestPressBeforeTheDwellReachesTheAgent: the dwell is what makes a raised
// block Diple's.
func TestPressBeforeTheDwellReachesTheAgent(t *testing.T) {
	s, _, agent := fixtureSession(t, "inline")
	if err := s.HandleOutput([]byte("\x1b[?1000h")); err != nil {
		t.Fatal(err)
	}
	agent.Reset()
	para := rowOf(t, s, "That is the whole plan.")
	y := yOf(s, para)
	// The pointer arrives, but the press lands before the dwell has run.
	send(t, s, motionAt(6, y))
	send(t, s, pressAt(6, y)+releaseAt(6, y))
	if s.editor != nil || s.sel != nil {
		t.Fatalf("a press before the dwell was taken: editor=%v sel=%v", s.editor, s.sel)
	}
	if got := agent.String(); !strings.Contains(got, pressAt(6, y)) || !strings.Contains(got, releaseAt(6, y)) {
		t.Fatalf("agent got %q, want the press and its release unchanged", got)
	}
}

// TestWheelAndPressOutsideARaiseReachTheAgent covers the rest of what the
// dwell rule protects.
func TestPressOutsideARaisedBlockReachesTheAgent(t *testing.T) {
	s, _, agent := fixtureSession(t, "inline")
	if err := s.HandleOutput([]byte("\x1b[?1000h")); err != nil {
		t.Fatal(err)
	}
	para := rowOf(t, s, "That is the whole plan.")
	dwellOn(t, s, 6, yOf(s, para))
	agent.Reset()
	// A press on a row with nothing raised on it.
	send(t, s, pressAt(2, 1)+releaseAt(2, 1))
	if s.editor != nil {
		t.Fatalf("a press outside the raise opened an editor: %+v", s.editor)
	}
	if agent.Len() == 0 {
		t.Fatal("a press outside a raised block must reach the agent")
	}
}

// TestNoModifierIsClaimed: the host's own selection modifier never reaches
// Diple, so selecting text in the terminal works exactly as it did.
func TestNoModifierIsClaimed(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	para := rowOf(t, s, "That is the whole plan.")
	y := yOf(s, para)
	// A modifier-press with nothing raised is not a Diple gesture.
	send(t, s, "\x1b[<8;6;"+itoa(y)+"M\x1b[<8;6;"+itoa(y)+"m")
	if s.sel != nil || s.editor != nil {
		t.Fatalf("a modifier-press was taken as a gesture: sel=%v editor=%v", s.sel, s.editor)
	}
	// And the raise itself needs no modifier at all.
	dwellOn(t, s, 6, y)
	pressRaised(t, s, blockLeft(s), y)
	if s.editor == nil {
		t.Fatal("the plain press did not open the editor")
	}
}

// TestCopyLeavesTheTrayUntouched: copy is the drag-free way to reach the
// clipboard and makes no card.
func TestCopyLeavesTheTrayUntouched(t *testing.T) {
	s, term, _ := fixtureSession(t, "inline")
	para := rowOf(t, s, "That is the whole plan.")
	dwellOn(t, s, 6, yOf(s, para))
	sr := stripRowFor(t, s)
	term.Reset()
	pressRaised(t, s, choiceFor(t, s, "copy")+1, sr)
	if s.Tray.Len() != 0 || s.editor != nil {
		t.Fatalf("copy made a card: tray=%d editor=%v", s.Tray.Len(), s.editor)
	}
	if got := clipboard(t, term); got != "That is the whole plan." {
		t.Fatalf("clipboard = %q", got)
	}
	// The keyboard's own c does the same.
	dwellOn(t, s, 6, yOf(s, para))
	term.Reset()
	send(t, s, "c")
	if s.Tray.Len() != 0 {
		t.Fatalf("c made a card: %d", s.Tray.Len())
	}
	if got := clipboard(t, term); got != "That is the whole plan." {
		t.Fatalf("clipboard after c = %q", got)
	}
}

// TestEscKeepsWhatWasTyped: Esc never destroys typed text.
func TestEscKeepsWhatWasTyped(t *testing.T) {
	s, _, agent := fixtureSession(t, "inline")
	para := rowOf(t, s, "That is the whole plan.")
	dwellOn(t, s, 6, yOf(s, para))
	pressRaised(t, s, blockLeft(s), yOf(s, para))
	send(t, s, "half written")
	send(t, s, "\x1b")
	if s.Tray.Len() != 1 || s.Tray.Cards[0].Text != "half written" {
		t.Fatalf("Esc with text must save the card: %v", cardTexts(s.Tray))
	}
	if s.editor != nil {
		t.Fatal("Esc did not close the editor")
	}
	// An empty editor closes and clears the selection, and keeps nothing.
	dwellOn(t, s, 6, yOf(s, para))
	pressRaised(t, s, blockLeft(s), yOf(s, para))
	send(t, s, "\x1b")
	if s.Tray.Len() != 1 || s.editor != nil || s.sel != nil {
		t.Fatalf("empty Esc: tray=%d editor=%v sel=%v", s.Tray.Len(), s.editor, s.sel)
	}
	// With nothing of Diple's open, Esc reaches the agent unchanged, and so
	// does a second one.
	pointerAway(t, s)
	agent.Reset()
	send(t, s, "\x1b")
	send(t, s, "\x1b")
	if agent.String() != "\x1b\x1b" {
		t.Fatalf("agent got %q, want both escapes", agent.String())
	}
}

// TestTailMarkSitsAfterTheBlocksLastCharacter is the only thing Diple leaves
// in the agent's text once the pointer is elsewhere.
func TestTailMarkSitsAfterTheBlocksLastCharacter(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	para := rowOf(t, s, "That is the whole plan.")
	dwellOn(t, s, 6, yOf(s, para))
	pressRaised(t, s, blockLeft(s), yOf(s, para))
	send(t, s, "one\r")
	pointerAway(t, s)
	lines, _, _, _ := physical(s)
	s.mu.Lock()
	row := s.agentToPhysical(para - s.windowStart())
	s.mu.Unlock()
	text := lines[row].String()
	i := strings.Index(text, "plan.")
	if i < 0 {
		t.Fatalf("row = %q", text)
	}
	mark := []rune(strings.TrimRight(text, " "))
	if mark[len(mark)-1] != '›' {
		t.Fatalf("no tail mark at the block's tail: %q", text)
	}
	if c := lines[row].Cells[len(mark)-1]; c.Attr.FG.Kind != screen.ColorIndexed || c.Attr.FG.Index != card.Tag("note").Color() {
		t.Fatalf("tail mark colour = %+v, want the card's tag colour", c.Attr.FG)
	}
	// A second card on the same block shows the count.
	dwellOn(t, s, 6, yOf(s, para))
	pressRaised(t, s, blockLeft(s), yOf(s, para))
	send(t, s, "two\r")
	pointerAway(t, s)
	lines, _, _, _ = physical(s)
	s.mu.Lock()
	row = s.agentToPhysical(para - s.windowStart())
	s.mu.Unlock()
	if got := strings.TrimRight(lines[row].String(), " "); !strings.HasSuffix(got, "›2") {
		t.Fatalf("row = %q, want a count after the mark", got)
	}
}

// TestMarksOffRemovesTheRaiseWithTheMarks leaves the keyboard as the way in.
func TestMarksOffRemovesTheRaiseWithTheMarks(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	s.Marks = false
	para := rowOf(t, s, "That is the whole plan.")
	dwellOn(t, s, 6, yOf(s, para))
	if s.raised != nil {
		t.Fatalf("--marks=off still raised a block: %+v", s.raised)
	}
	// The keyboard still reaches a block.
	selectBlockAt(t, s, para)
	send(t, s, "f")
	send(t, s, "still works\r")
	if s.Tray.Len() != 1 || s.Tray.Cards[0].Tag != "fix" {
		t.Fatalf("keyboard path: %+v", s.Tray.Cards)
	}
}

// TestMotionOffDrawsTheFinalFrameOnly.
func TestMotionOffDrawsTheFinalFrameOnly(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	s.NoMotion = true
	para := rowOf(t, s, "That is the whole plan.")
	send(t, s, motionAt(6, yOf(s, para)))
	if s.raised == nil || !s.raised.strip {
		t.Fatalf("--motion=off must draw the final frame at once: %+v", s.raised)
	}
}
