package wrap

import (
	"strings"
	"time"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/screen"
)

// This file is the raise: the transient lift Diple draws on the block under
// the pointer, and the strip of choices beneath it. The raise is what makes a
// block pressable, and it claims no modifier key — pressing a raised block is
// the whole gesture.

// The two frames of the raise. Resting the pointer inside a block raises its
// bounding box after the dwell, and the strip wipes in a moment later, so the
// whole animation is two frames inside 150 ms of the dwell.
const (
	raiseDwell = 80 * time.Millisecond
	stripDelay = 70 * time.Millisecond
	// linger is how long the raise outlives the pointer, so a loose path
	// between the block and its strip does not lose it.
	linger = 250 * time.Millisecond
	// multiPress is the window in which a second or third press at the same
	// cell is one gesture rather than two.
	multiPress = 400 * time.Millisecond
)

// raised is the block the pointer rests on, lifted out of the agent's text.
type raised struct {
	turn, block int
	kind        blocks.Kind
	first, last int // history rows the block occupies
	left, right int // bounding box columns, inclusive
	text        string
	ordinal     int
	parent      int  // enclosing code block for a line, -1 otherwise
	line        bool // the unit is a code or diff line, not a whole block
	// at is when the pointer came to rest here; strip is when the strip
	// wiped in. A zero strip time means it has not.
	at    time.Time
	strip bool
	// leftAt is when the pointer left the block and its strip, so the raise
	// can outlive it by a moment. Zero while the pointer is still on it.
	leftAt time.Time
}

// stripChoice is one of the strip's four choices. copy makes no card.
type stripChoice struct {
	label string
	tag   card.Tag // empty for copy
	from  int      // first column, inclusive
	to    int      // last column, inclusive
}

// stripLayout builds the strip's cells at a block's left edge. The strip is
// opaque: every cell from its first to its last is Diple's, its pad cells
// included, so nothing of the agent's text shows through it. Only the four
// choices hold the pointer; the pad and divider cells belong to what the
// strip covers.
func stripLayout(left int) (choices []stripChoice, first, last int) {
	x := left + 1
	add := func(label string, tag card.Tag) {
		choices = append(choices, stripChoice{label: label, tag: tag, from: x, to: x + len(label) - 1})
		x += len(label) + 1
	}
	for _, t := range card.Tags {
		add(string(t), t)
	}
	x++ // the divider's own cell
	add("copy", "")
	return choices, left, x - 1
}

// stripDividerCol is the column the divider sits in: the cell before copy.
func stripDividerCol(choices []stripChoice) int {
	if len(choices) == 0 {
		return -1
	}
	return choices[len(choices)-1].from - 2
}

// stripHit reports the choice whose cells hold the pointer at column px, or
// nil when the pointer is on a pad, the divider, or past the strip's end. A
// pointer that lands anywhere but a choice belongs to what the strip covers.
func stripHit(left, px int) *stripChoice {
	choices, _, _ := stripLayout(left)
	for i := range choices {
		if px >= choices[i].from && px <= choices[i].to {
			return &choices[i]
		}
	}
	return nil
}

// boundingBox is the block's box: from its smallest indent to one column past
// its longest row, so a hanging indent keeps the edge straight and the short
// rows' tails are filled. It is measured in cells, so a wide character counts
// both of its columns, and it starts past the agent's turn marker, which no
// highlight covers.
func boundingBox(lines []screen.Line, first, last, cols int,
	marker func(screen.Line) int) (left, right int) {
	left, right = cols, -1
	for r := first; r <= last && r < len(lines); r++ {
		if r < 0 {
			continue
		}
		l := lines[r]
		start, end := -1, -1
		for x := marker(l); x < len(l.Cells); x++ {
			c := l.Cells[x]
			if c.Width == 0 {
				if end == x-1 {
					end = x // the trailing half of a wide character
				}
				continue
			}
			if !isSpaceCell(c) && c.Rune != '\t' {
				if start < 0 {
					start = x
				}
				end = x
			}
		}
		if start < 0 {
			continue
		}
		if start < left {
			left = start
		}
		if end > right {
			right = end
		}
	}
	if right < 0 {
		return 0, 0
	}
	// One column past the longest row, which is what fills the short tails.
	right++
	if right >= cols {
		right = cols - 1
	}
	if left > right {
		left = right
	}
	return left, right
}

// raiseAtLocked lifts the block under a history row, or drops the raise when
// no block is there. It reports whether anything changed.
func (s *Session) raiseAtLocked(row, col int, now time.Time) bool {
	if !s.Marks || s.hidden || s.prompting {
		// --marks=off removes every mark and the raise with it, leaving the
		// keyboard as the way in.
		return s.dropRaiseLocked()
	}
	rows, al := s.alignmentLocked()
	// In code blocks and diffs the unit is the line, so it is the line under
	// the pointer that rises.
	turn, idx, b := blockAt(al, row, true)
	line := b != nil
	if b == nil {
		turn, idx, b = blockAt(al, row, false)
	}
	if b == nil || b.First < 0 {
		return s.dropRaiseLocked()
	}
	if s.raised != nil && s.raised.turn == turn && s.raised.block == idx && s.raised.line == line {
		// The pointer is still on the block it raised; the raise holds and
		// its clock keeps running.
		s.raised.leftAt = time.Time{}
		return false
	}
	text := b.Text
	if b.Kind == blocks.CodeBlock && b.Last < len(rows) {
		text = strings.Join(rows[b.First:b.Last+1], "\n")
	}
	left, right := boundingBox(s.historyLinesLocked(), b.First, b.Last, s.cols, s.markerCells)
	at := now
	// While one block is raised the next skips the dwell.
	if s.raised != nil {
		at = now.Add(-raiseDwell - stripDelay)
	}
	parent := -1
	if line {
		parent = b.Parent
	}
	s.raised = &raised{turn: turn, block: idx, kind: b.Kind, first: b.First, last: b.Last,
		left: left, right: right, text: text, ordinal: b.Ordinal, parent: parent, line: line, at: at}
	s.raiseTick(now)
	return true
}

// dropRaiseLocked starts the raise's linger, or removes it once that is up.
// It reports whether anything changed.
func (s *Session) dropRaiseLocked() bool {
	if s.raised == nil {
		return false
	}
	if s.raised.leftAt.IsZero() {
		s.raised.leftAt = s.clock()
		return false
	}
	return false
}

// raiseTick advances the raise's two frames and expires a raise the pointer
// has left. It reports whether the screen must be repainted.
func (s *Session) raiseTick(now time.Time) bool {
	r := s.raised
	if r == nil {
		return false
	}
	if !r.leftAt.IsZero() && now.Sub(r.leftAt) >= linger {
		s.raised = nil
		return true
	}
	// --motion=off draws the final frame only.
	if s.NoMotion {
		if !r.strip {
			r.strip = true
			return true
		}
		return false
	}
	if !r.strip && now.Sub(r.at) >= raiseDwell+stripDelay {
		r.strip = true
		return true
	}
	return false
}

// raiseShowing reports whether the dwell has elapsed, which is what makes a
// raised block Diple's. A press landing before it reaches the agent untouched.
func (s *Session) raiseShowing() bool {
	r := s.raised
	if r == nil {
		return false
	}
	if s.NoMotion {
		return true
	}
	return s.clock().Sub(r.at) >= raiseDwell
}

// stripRow is the agent-region row the strip occupies: the row beneath the
// raised block, at the block's own left edge, for every block whatever its
// width. It returns -1 when that row is not on screen.
func (s *Session) stripRow() int {
	if s.raised == nil || !s.raised.strip {
		return -1
	}
	r := s.raised.last + 1 - s.windowStart()
	if r < 0 || r >= s.rows-s.trayH {
		return -1
	}
	return r
}

// raiseSelection turns the raised block into the selection a press makes. The
// reverse video it already wears is the selection, so nothing flickers.
func (s *Session) raiseSelection() *selection {
	r := s.raised
	if r == nil {
		return nil
	}
	sel := &selection{turn: r.turn, block: r.block, kind: r.kind, first: r.first, last: r.last,
		text: r.text, ordinal: r.ordinal, absolute: s.dropped() + r.first, parent: r.parent}
	if r.line {
		sel.lines = &card.LineRange{First: r.block, Last: r.block}
	}
	return sel
}

// extendRaisedLineLocked grows a line selection to the raised line, which is
// what a Shift-press on a second raised line does.
func (s *Session) extendRaisedLineLocked() bool {
	r := s.raised
	if r == nil || !r.line || s.sel == nil || s.sel.lines == nil || s.sel.turn != r.turn {
		return false
	}
	rows, al := s.alignmentLocked()
	lr := s.sel.lines
	if r.block < lr.First {
		lr.First = r.block
	}
	if r.block > lr.Last {
		lr.Last = r.block
	}
	s.sel.first, s.sel.last = lineRows(al, s.sel.turn, lr)
	if s.sel.first >= 0 && s.sel.last < len(rows) {
		s.sel.text = strings.Join(rows[s.sel.first:s.sel.last+1], "\n")
	}
	return true
}

// blockOf finds the aligned block a history row belongs to, preferring the
// enclosing block over its lines, for the tail mark and for copying.
func blockOf(al []adapter.TurnAlignment, row int) *adapter.AlignedBlock {
	if _, _, b := blockAt(al, row, true); b != nil {
		return b
	}
	_, _, b := blockAt(al, row, false)
	return b
}
