package wrap

import (
	"strconv"

	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/screen"
)

// ownsScreen reports whether anything of Diple's is showing, which is when
// the session must composite instead of passing bytes through.
func (s *Session) ownsScreen() bool {
	return s.Tray.Len() > 0 || s.sel != nil || s.editor != nil || s.highlight != nil
}

// trayHeight is the divider plus one row per card, capped at a third of
// the terminal.
func (s *Session) trayHeight() int {
	n := s.Tray.Len()
	if n == 0 {
		return 0
	}
	maxCards := s.rows/3 - 1
	if maxCards < 1 {
		maxCards = 1
	}
	if n > maxCards {
		n = maxCards
	}
	return 1 + n
}

// syncLocked moves between pass-through and composited, applies a changed
// tray height to the model and the PTY, and repaints what differs.
func (s *Session) syncLocked() error {
	if h := s.trayHeight(); h != s.trayH {
		s.trayH = h
		s.Model.Resize(s.cols, s.rows-h)
		if s.back > s.Model.HistoryLen() {
			s.back = s.Model.HistoryLen()
		}
		if s.SetPTYRows != nil {
			s.SetPTYRows(s.rows - h)
		}
		s.painted = nil
	}
	if s.trayScroll > 0 && s.trayScroll > s.Tray.Len()-1 {
		s.trayScroll = 0
	}
	owns := s.ownsScreen()
	switch {
	case owns && !s.composited:
		s.composited = true
		s.painted = nil
		return s.repaintLocked()
	case !owns && s.composited:
		s.composited = false
		s.painted = nil
		if s.back != 0 {
			return s.paintViewportLocked()
		}
		return s.returnToLiveLocked()
	case s.composited:
		return s.repaintLocked()
	}
	return nil
}

// windowStart is the history index of the first row of the agent region.
func (s *Session) windowStart() int {
	if s.Model.AltActive() {
		return 0
	}
	return s.Model.HistoryLen() - s.back
}

// inputRow is the agent-region row where the native input box begins, or
// the region's end when it is not visible or the view is scrolled.
func (s *Session) inputRow() int {
	agentRows := s.rows - s.trayH
	if s.back != 0 || s.adapter == nil {
		return agentRows
	}
	if r := s.adapter.InputRow(s.Model.Text()); r >= 0 && r <= agentRows {
		return r
	}
	return agentRows
}

// physicalToAgent maps a physical row to an agent-region row; tray is true
// when the row lies in the tray (then row is the tray row index).
func (s *Session) physicalToAgent(py int) (row int, tray bool) {
	if s.trayH == 0 {
		return py, false
	}
	in := s.inputRow()
	switch {
	case py < in:
		return py, false
	case py < in+s.trayH:
		return py - in, true
	default:
		return py - s.trayH, false
	}
}

func (s *Session) agentToPhysical(row int) int {
	if s.trayH > 0 && row >= s.inputRow() {
		return row + s.trayH
	}
	return row
}

// physicalLocked composes the rows the terminal should show and where the
// cursor goes.
func (s *Session) physicalLocked() (lines []screen.Line, cx, cy int, cursorVisible bool) {
	agentRows := s.rows - s.trayH
	var base []screen.Line
	if s.back != 0 {
		base = s.Model.Viewport(s.back)
	} else {
		base = s.Model.Rows()
	}
	start := s.windowStart()
	toScreen := func(hist int) int { return hist - start }

	// Gutter marks on anchored blocks.
	if s.Marks {
		for _, c := range s.Tray.Cards {
			first, _, ok := s.resolveAnchor(&c.Anchor)
			if !ok {
				continue
			}
			if r := toScreen(first); r >= 0 && r < len(base) && len(base[r].Cells) > 0 && base[r].Cells[0].Rune == ' ' {
				base[r].Cells[0] = screen.Cell{Rune: '›', Width: 1, Attr: s.dimAttr()}
			}
		}
	}
	// Highlight of a card's anchor.
	if s.highlight != nil {
		for h := s.highlight.first; h <= s.highlight.last; h++ {
			if r := toScreen(h); r >= 0 && r < len(base) {
				reverseRow(&base[r])
			}
		}
	}
	// Selection: block rows in reverse, a span underlined.
	if s.sel != nil {
		if s.sel.span != nil {
			sp := s.sel.span
			for h := sp.Row; h <= sp.EndRow; h++ {
				r := toScreen(h)
				if r < 0 || r >= len(base) {
					continue
				}
				from, to := 0, len(base[r].Cells)-1
				if h == sp.Row {
					from = sp.Col
				}
				if h == sp.EndRow {
					to = sp.EndCol
				}
				for x := from; x <= to && x < len(base[r].Cells); x++ {
					base[r].Cells[x].Attr.Flags |= screen.Underline | screen.Reverse
				}
			}
		} else {
			for h := s.sel.first; h <= s.sel.last; h++ {
				if r := toScreen(h); r >= 0 && r < len(base) {
					reverseRow(&base[r])
				}
			}
		}
	}
	// Toolbar or editor overlay row.
	overlayRow := -1
	if s.sel != nil || s.editor != nil {
		overlayRow = s.overlayRowLocked(agentRows)
		if overlayRow >= 0 && overlayRow < len(base) {
			if s.editor != nil {
				base[overlayRow] = s.editorLine()
			} else {
				base[overlayRow] = s.toolbarLine()
			}
		}
	}

	in := s.inputRow()
	lines = make([]screen.Line, 0, s.rows)
	lines = append(lines, base[:in]...)
	lines = append(lines, s.trayLines()...)
	lines = append(lines, base[in:]...)
	for len(lines) < s.rows {
		lines = append(lines, screen.Line{Cells: blankCells(s.cols)})
	}
	lines = lines[:s.rows]

	if s.editor != nil && overlayRow >= 0 {
		cx = s.editorCursorCol()
		cy = s.agentToPhysical(overlayRow)
		return lines, cx, cy, true
	}
	x, y := s.Model.Cursor()
	return lines, x, s.agentToPhysical(y), s.back == 0 && s.Model.CursorVisible() && s.focus == focusAgent
}

func reverseRow(l *screen.Line) {
	for x := range l.Cells {
		l.Cells[x].Attr.Flags ^= screen.Reverse
	}
}

func blankCells(n int) []screen.Cell {
	cells := make([]screen.Cell, n)
	for i := range cells {
		cells[i] = screen.Blank(screen.Attr{})
	}
	return cells
}

// repaintLocked emits only the physical rows that differ from the last
// paint, then places the cursor.
func (s *Session) repaintLocked() error {
	lines, cx, cy, visible := s.physicalLocked()
	buf := make([]byte, 0, 4096)
	buf = append(buf, "\x1b[?25l"...)
	changed := 0
	for i, l := range lines {
		if s.painted != nil && i < len(s.painted) && l.Equal(s.painted[i]) {
			continue
		}
		buf = appendRowAt(buf, i, l)
		changed++
	}
	s.painted = lines
	buf = appendCursor(buf, cx, cy)
	if visible {
		buf = append(buf, "\x1b[?25h"...)
	}
	return writeAll(s.term, buf)
}

// --- Diple's own drawing ---------------------------------------------------

func (s *Session) dimAttr() screen.Attr {
	if s.Plain {
		return screen.Attr{}
	}
	return screen.Attr{Flags: screen.Dim}
}

func (s *Session) boldAttr() screen.Attr {
	if s.Plain {
		return screen.Attr{Flags: screen.Underline}
	}
	return screen.Attr{Flags: screen.Bold}
}

func (s *Session) tagAttr(t card.Tag) screen.Attr {
	if s.Plain {
		return screen.Attr{}
	}
	if c := t.Color(); c != 0 {
		return screen.Attr{FG: screen.Color{Kind: screen.ColorIndexed, Index: c}}
	}
	return screen.Attr{}
}

// putText writes text into a line from column x with attr, returning the
// next column. Wide runes take two cells.
func putText(l *screen.Line, x int, text string, attr screen.Attr) int {
	for _, r := range text {
		if x >= len(l.Cells) {
			break
		}
		w := 1
		if runeWidth(r) == 2 {
			w = 2
		}
		if w == 2 && x+1 >= len(l.Cells) {
			break
		}
		l.Cells[x] = screen.Cell{Rune: r, Width: uint8(w), Attr: attr}
		if w == 2 {
			l.Cells[x+1] = screen.Cell{Rune: 0, Width: 0, Attr: attr}
		}
		x += w
	}
	return x
}

func runeWidth(r rune) int {
	// Diple's own labels are ASCII plus a few symbols; treat CJK-range
	// runes in quotations as wide, as the screen model does.
	switch {
	case r >= 0x1100 && (r <= 0x115f || r >= 0x2e80 && r <= 0xa4cf || r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff || r >= 0xff00 && r <= 0xff60 || r >= 0x1f300 && r <= 0x1faff || r >= 0x20000 && r <= 0x3fffd):
		return 2
	}
	return 1
}

func (s *Session) blankLine() screen.Line { return screen.Line{Cells: blankCells(s.cols)} }

// trayLines renders the divider and the visible cards.
func (s *Session) trayLines() []screen.Line {
	if s.trayH == 0 {
		return nil
	}
	lines := make([]screen.Line, 0, s.trayH)
	div := s.blankLine()
	for x := range div.Cells {
		div.Cells[x] = screen.Cell{Rune: '─', Width: 1, Attr: s.dimAttr()}
	}
	label := " › " + strconv.Itoa(s.Tray.Len()) + " card"
	if s.Tray.Len() != 1 {
		label += "s"
	}
	label += " "
	putText(&div, 1, label, s.dimAttr())
	lines = append(lines, div)
	visible := s.trayH - 1
	if s.traySel >= s.trayScroll+visible {
		s.trayScroll = s.traySel - visible + 1
	}
	if s.traySel < s.trayScroll {
		s.trayScroll = s.traySel
	}
	for i := s.trayScroll; i < s.Tray.Len() && len(lines) < s.trayH; i++ {
		lines = append(lines, s.cardLine(i))
	}
	for len(lines) < s.trayH {
		lines = append(lines, s.blankLine())
	}
	return lines
}

func (s *Session) cardLine(i int) screen.Line {
	c := s.Tray.Cards[i]
	l := s.blankLine()
	x := putText(&l, 1, strconv.Itoa(i+1)+" ", s.dimAttr())
	x = putText(&l, x, "["+string(c.Tag)+"] ", s.tagAttr(c.Tag))
	if c.Anchor.Quote != "" {
		x = putText(&l, x, "“"+c.Anchor.Quote+"” ", s.dimAttr())
	}
	putText(&l, x, c.Text, screen.Attr{})
	if s.focus == focusTray && i == s.traySel {
		reverseRow(&l)
	}
	return l
}

// toolbarLine renders the six actions with their initial letters marked.
func (s *Session) toolbarLine() screen.Line {
	l := s.blankLine()
	x := putText(&l, 1, "› ", s.dimAttr())
	for _, t := range card.Tags {
		name := string(t)
		x = putText(&l, x, name[:1], mergeAttr(s.tagAttr(t), s.boldAttr()))
		x = putText(&l, x, name[1:]+"  ", s.tagAttr(t))
	}
	putText(&l, x, "esc", s.dimAttr())
	return l
}

func mergeAttr(a, b screen.Attr) screen.Attr {
	a.Flags |= b.Flags
	return a
}

// editorLine renders the inline editor: the tag, then the note text.
func (s *Session) editorLine() screen.Line {
	l := s.blankLine()
	x := putText(&l, 1, "› ", s.dimAttr())
	x = putText(&l, x, string(s.editor.tag)+": ", s.tagAttr(s.editor.tag))
	putText(&l, x, string(s.editor.text), screen.Attr{})
	return l
}

func (s *Session) editorCursorCol() int {
	x := 1 + 2 + len(string(s.editor.tag)) + 2
	for _, r := range s.editor.text {
		x += runeWidth(r)
	}
	if x >= s.cols {
		x = s.cols - 1
	}
	return x
}

// overlayRowLocked chooses the agent-region row for the toolbar or editor:
// directly under the selection, else directly above it, else the region's
// last row.
func (s *Session) overlayRowLocked(agentRows int) int {
	last, first := -1, -1
	switch {
	case s.editor != nil:
		first, last = s.editor.first, s.editor.last
	case s.sel != nil:
		first, last = s.sel.first, s.sel.last
	}
	start := s.windowStart()
	if r := last - start + 1; last >= 0 && r >= 0 && r < agentRows {
		return r
	}
	if r := first - start - 1; first >= 0 && r >= 0 && r < agentRows {
		return r
	}
	return agentRows - 1
}
