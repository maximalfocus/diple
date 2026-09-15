package wrap

import (
	"github.com/maximalfocus/diple/internal/clip"
	"github.com/maximalfocus/diple/internal/screen"
)

// This file is the selection Diple gives back. Diple owns the mouse, so the
// host stops offering its own drag-selection; a user who copies by dragging
// would lose that on the day Diple is installed. So Diple makes the selection
// itself, across rows and past any block's edge, and copies it when the
// button comes up.

// textSelection is a contiguous range of screen text, which may cross blocks
// and rows and need not lie in an aligned turn at all. Its rows are absolute,
// counted from the start of the session, so a selection made while the agent
// is writing keeps its anchor in the scrollback rather than the viewport.
type textSelection struct {
	absFirst, firstCol int
	absLast, lastCol   int
}

// rows converts the selection back to the history rows it covers now.
func (t *textSelection) rows(dropped int) (first, firstCol, last, lastCol int) {
	first, last = t.absFirst-dropped, t.absLast-dropped
	firstCol, lastCol = t.firstCol, t.lastCol
	if last < first || (last == first && lastCol < firstCol) {
		first, last = last, first
		firstCol, lastCol = lastCol, firstCol
	}
	return first, firstCol, last, lastCol
}

// setTextSelectionLocked records a drag from one point to another, in history
// rows and screen columns.
func (s *Session) setTextSelectionLocked(fromRow, fromCol, toRow, toCol int) {
	d := s.dropped()
	s.textSel = &textSelection{absFirst: d + fromRow, firstCol: fromCol, absLast: d + toRow, lastCol: toCol}
	s.sel, s.editor = nil, nil
}

// selectWordLocked selects the word under a point, which is what a
// double-press takes. A word is a run of cells that are not blank, so a wide
// character is one piece of it, both its columns.
func (s *Session) selectWordLocked(row, col int) bool {
	lines := s.historyLinesLocked()
	if row < 0 || row >= len(lines) {
		return false
	}
	cells := lines[row].Cells
	if col < 0 || col >= len(cells) {
		return false
	}
	for col > 0 && cells[col].Width == 0 {
		col--
	}
	start := s.markerCells(lines[row])
	if col < start || isSpaceCell(cells[col]) {
		return false
	}
	inWord := func(x int) bool {
		return x >= start && x < len(cells) && (cells[x].Width == 0 || !isSpaceCell(cells[x]))
	}
	from, to := col, col
	for inWord(from - 1) {
		from--
	}
	for inWord(to + 1) {
		to++
	}
	s.setTextSelectionLocked(row, from, row, to)
	return true
}

// selectLogicalLineLocked selects the whole logical line under a point, which
// is what a triple-press takes: the rows of the block that covers the row, and
// the screen row where none does.
func (s *Session) selectLogicalLineLocked(row int) bool {
	rows, al := s.alignmentLocked()
	lines := s.historyLinesLocked()
	if row < 0 || row >= len(rows) || row >= len(lines) {
		return false
	}
	first, last := row, row
	if b := blockOf(al, row); b != nil && b.First >= 0 {
		first, last = b.First, b.Last
	}
	if last >= len(lines) {
		last = len(lines) - 1
	}
	s.setTextSelectionLocked(first, 0, last, lastTextCell(lines[last]))
	return true
}

// lastTextCell is the column of a row's last cell that is not blank, or 0.
func lastTextCell(l screen.Line) int {
	for x := len(l.Cells) - 1; x >= 0; x-- {
		if c := l.Cells[x]; c.Width == 0 || !isSpaceCell(c) {
			return x
		}
	}
	return 0
}

// selectionTextLocked is what the current selection puts on the clipboard:
// what it shows. Each kind of selection copies the cells its highlight covers
// — a drag from its press to its release, a span its own range, a selected
// block or line range its rows, a raised block its box — read by copiedText.
func (s *Session) selectionTextLocked() string {
	lines := s.historyLinesLocked()
	_, al := s.alignmentLocked()
	switch {
	case s.textSel != nil:
		first, firstCol, last, lastCol := s.textSel.rows(s.dropped())
		return s.copiedText(lines, al, first, firstCol, last, lastCol)
	case s.sel != nil && s.sel.span != nil:
		sp := s.sel.span
		return s.copiedText(lines, al, sp.Row, sp.Col, sp.EndRow, sp.EndCol)
	case s.sel != nil:
		return s.copiedText(lines, al, s.sel.first, 0, s.sel.last, 1<<30)
	case s.raised != nil:
		r := s.raised
		return s.copiedText(lines, al, r.first, r.left, r.last, r.right)
	}
	return ""
}

// copySelectionLocked puts the current selection on the clipboard through the
// ladder, and reports on the tray status line when no rung is available.
func (s *Session) copySelectionLocked() error {
	text := s.selectionTextLocked()
	if text == "" {
		return nil
	}
	return s.copyTextLocked(text)
}

func (s *Session) copyTextLocked(text string) error {
	// The copy is counted before the ladder, because what a host check must
	// prove is that the gesture arrived and Diple copied the right text; which
	// rung carried it is the ladder's own business and is verified live.
	s.copies++
	s.lastCopy = text
	if s.Clip == nil {
		s.clipNote = "no clipboard"
		return nil
	}
	// OSC 52 goes to the host terminal, which is the sink Diple already owns.
	rung, err := s.Clip.Write(func(p []byte) error { return writeAll(s.term, p) }, text)
	if err != nil {
		s.clipNote = "clipboard failed"
		return nil
	}
	if rung == clip.Unavailable {
		s.clipNote = "no clipboard"
		return nil
	}
	s.clipNote = ""
	return nil
}
