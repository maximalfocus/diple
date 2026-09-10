package wrap

import (
	"strings"
	"unicode"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/clip"
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
// double-press takes.
func (s *Session) selectWordLocked(row, col int) bool {
	rows, _ := s.alignmentLocked()
	if row < 0 || row >= len(rows) {
		return false
	}
	line := []rune(rows[row])
	if col >= len(line) || col < 0 || unicode.IsSpace(line[col]) {
		return false
	}
	from, to := col, col
	for from > 0 && !unicode.IsSpace(line[from-1]) {
		from--
	}
	for to+1 < len(line) && !unicode.IsSpace(line[to+1]) {
		to++
	}
	s.setTextSelectionLocked(row, from, row, to)
	return true
}

// selectLogicalLineLocked selects the whole logical line under a point, which
// is what a triple-press takes: the transcript's line where one covers the
// row, and the screen row where none does.
func (s *Session) selectLogicalLineLocked(row int) bool {
	rows, al := s.alignmentLocked()
	if row < 0 || row >= len(rows) {
		return false
	}
	first, last := row, row
	if b := blockOf(al, row); b != nil && b.First >= 0 {
		first, last = b.First, b.Last
	}
	lastCol := 0
	if last < len(rows) {
		lastCol = len([]rune(rows[last]))
		if lastCol > 0 {
			lastCol--
		}
	}
	s.setTextSelectionLocked(first, 0, last, lastCol)
	return true
}

// selectionTextLocked is what the current selection puts on the clipboard.
//
// What is copied is the transcript's text rather than the screen's, so a
// command that wrapped over three rows returns as one line and no decoration,
// gutter, or wrap artefact travels with it. Where a selection has no
// transcript behind it — tool output, a native prompt, anything Diple could
// not align — the screen's own rows are copied instead, joined as they read,
// so nothing on the screen is ever unselectable.
func (s *Session) selectionTextLocked() string {
	rows, al := s.alignmentLocked()
	if s.textSel != nil {
		first, firstCol, last, lastCol := s.textSel.rows(s.dropped())
		return selectedText(rows, al, first, firstCol, last, lastCol)
	}
	if s.sel != nil {
		return s.sel.text
	}
	if r := s.raised; r != nil {
		return r.text
	}
	return ""
}

// selectedText walks the selected rows in order. A block whose rows the
// selection covers whole gives its transcript text, unwrapped; a partly
// covered block, and every row no block covers, gives the screen's own
// columns as they read.
func selectedText(rows []string, al []adapter.TurnAlignment, first, firstCol, last, lastCol int) string {
	if first < 0 {
		first, firstCol = 0, 0
	}
	if last >= len(rows) {
		last = len(rows) - 1
		if last >= 0 {
			lastCol = len([]rune(rows[last]))
		}
	}
	if first > last || last < 0 {
		return ""
	}
	var out []string
	for r := first; r <= last; r++ {
		if b := blockOf(al, r); coversWhole(rows, b, r, first, firstCol, last, lastCol) {
			// The selection covers this block whole: its transcript text is
			// what the user meant, however the renderer wrapped it.
			if text := strings.TrimRight(b.Text, "\n"); text != "" {
				out = append(out, text)
			}
			r = b.Last
			continue
		}
		line := []rune(rows[r])
		from, to := 0, len(line)-1
		if r == first {
			from = firstCol
		}
		if r == last {
			to = lastCol
		}
		if to >= len(line) {
			to = len(line) - 1
		}
		if from < 0 {
			from = 0
		}
		if from > to {
			out = append(out, "")
			continue
		}
		out = append(out, strings.TrimRight(string(line[from:to+1]), " "))
	}
	return strings.Join(out, "\n")
}

// coversWhole reports whether the selection takes in every row of the block
// starting at row r, with no column of its first or last row left out. Only
// then is the transcript's own text what the user selected; a partly covered
// block gives the screen's columns instead.
func coversWhole(rows []string, b *adapter.AlignedBlock, r, first, firstCol, last, lastCol int) bool {
	if b == nil || b.First < 0 || b.First != r || b.Last > last || b.Last < b.First {
		return false
	}
	if b.First == first && firstCol > 0 {
		return false
	}
	if b.Last == last && b.Last < len(rows) {
		if end := len([]rune(rows[b.Last])) - 1; lastCol < end {
			return false
		}
	}
	return true
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
