package wrap

import (
	"strconv"
	"strings"

	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/screen"
)

// ownsScreen reports whether anything of Diple's is showing, which is when
// the session must composite instead of passing bytes through.
func (s *Session) ownsScreen() bool {
	if s.hidden {
		return false
	}
	return s.Tray.Len() > 0 || s.sel != nil || s.editor != nil || s.search != nil ||
		s.highlight != nil || s.raised != nil || s.textSel != nil
}

// trayHeight is the divider plus one row per card, capped at a third of
// the terminal.
func (s *Session) trayHeight() int {
	n := s.Tray.Len()
	if s.hidden {
		return 0
	}
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
// the region's end when it is not visible or the view is scrolled. A view
// scrolled only to reveal a card's anchor keeps the tray above the live input
// box, so the card the pointer rests on stays under it.
func (s *Session) inputRow() int {
	agentRows := s.rows - s.trayH
	if (s.back != 0 && !s.revealed) || s.adapter == nil {
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

	// The tail mark on a block that carries a card: Diple's own › one column
	// after the block's last character, in the card's tag colour, with the
	// count when a block carries more than one. It is the only thing Diple
	// leaves in the agent's text once the pointer is elsewhere.
	if s.Marks {
		s.drawTailMarks(base, toScreen)
	}
	// Highlight of a card's anchor.
	if s.highlight != nil {
		for h := s.highlight.first; h <= s.highlight.last; h++ {
			if r := toScreen(h); r >= 0 && r < len(base) {
				reverseRow(&base[r])
			}
		}
	}
	// The raise: the block's bounding box in reverse video, which fills the
	// short rows' tails and makes the block read as one slab. Selection is
	// reverse video too, which is why pressing a raised block changes nothing.
	if s.raised != nil {
		for h := s.raised.first; h <= s.raised.last; h++ {
			r := toScreen(h)
			if r < 0 || r >= len(base) {
				continue
			}
			reverseSpan(&base[r], s.raised.left, s.raised.right)
		}
	}
	// A drag selection, which may cross blocks and rows.
	if s.textSel != nil {
		first, firstCol, last, lastCol := s.textSel.rows(s.dropped())
		for h := first; h <= last; h++ {
			r := toScreen(h)
			if r < 0 || r >= len(base) {
				continue
			}
			from, to := 0, len(base[r].Cells)-1
			if h == first {
				from = firstCol
			}
			if h == last {
				to = lastCol
			}
			reverseSpan(&base[r], from, to)
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
	// The strip wipes in on the row beneath the block, at the block's own left
	// edge, for every block whatever its width, because a user who never has
	// to look for it can reach it without looking.
	if sr := s.stripRow(); sr >= 0 && sr < len(base) {
		s.drawStrip(&base[sr])
	}
	// The editor, search field, or the strip's own row when nothing is raised.
	overlayRow := -1
	if s.editor != nil || s.search != nil || s.sel != nil {
		overlayRow = s.overlayRowLocked(agentRows)
		if overlayRow >= 0 && overlayRow < len(base) {
			switch {
			case s.editor != nil:
				base[overlayRow] = s.editorLine()
			case s.search != nil:
				base[overlayRow] = s.searchLine()
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
	if s.search != nil && overlayRow >= 0 {
		cx = s.searchCursorCol()
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

// reverseSpan reverses the cells between two columns, inclusive.
func reverseSpan(l *screen.Line, from, to int) {
	if from < 0 {
		from = 0
	}
	if to >= len(l.Cells) {
		to = len(l.Cells) - 1
	}
	for x := from; x <= to; x++ {
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

// --plain drops colour entirely, leaving the bold, dim, reverse, and
// underline attributes, which is why only tagAttr changes under it.
func (s *Session) dimAttr() screen.Attr {
	return screen.Attr{Flags: screen.Dim}
}

func (s *Session) boldAttr() screen.Attr {
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
	if s.pendingSubmit {
		label = " › will send when idle "
	}
	if s.clipNote != "" {
		label = " › " + s.clipNote + " "
	}
	putText(&div, 1, label, s.dimAttr())
	// The tray's status line carries the send at its right end, since a
	// product that is pointed at needs a way to send that is pointed at too.
	from, _ := s.sendHit()
	putText(&div, from-1, " ", s.dimAttr())
	putText(&div, from, sendLabel, s.boldAttr())
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
	// An anchored card wears its tag; a free card carries none, since a card
	// with nothing to point at is simply the text the user would have typed.
	if c.Kind == card.Anchored {
		x = putText(&l, x, "["+string(c.Tag)+"] ", s.tagAttr(c.Tag))
	} else if c.Overall {
		x = putText(&l, x, "[overall] ", s.dimAttr())
	}
	if c.Anchor.Quote != "" {
		x = putText(&l, x, "“"+card.Quote(c.Anchor.Quote)+"” ", s.dimAttr())
	}
	x = putText(&l, x, firstLine(c.Text), screen.Attr{})
	if n := len(c.Attachments); n > 0 {
		putText(&l, x+1, "("+strconv.Itoa(n)+" attached)", s.dimAttr())
	}
	// The × at a card's end deletes it outright.
	putText(&l, s.cols-2, "×", s.dimAttr())
	if s.focus == focusTray && i == s.traySel {
		reverseRow(&l)
	}
	return l
}

// firstLine is what a card shows on its one row when its text holds more.
func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i] + " …"
	}
	return text
}

// sendHit is the column range the status line's send answers on.
func (s *Session) sendHit() (from, to int) {
	to = s.cols - 2
	return to - len(sendLabel) + 1, to
}

const sendLabel = "send"

// drawStrip writes the raised block's row of choices. The strip is opaque: it
// writes every cell it covers, its own pad cells included, so nothing of the
// agent's text shows through it or crowds it. It underlines the tag in force
// rather than a fixed default, so the strip and the editor's chip never say
// different things.
func (s *Session) drawStrip(l *screen.Line) {
	r := s.raised
	if r == nil {
		return
	}
	choices, first, last := stripLayout(r.left)
	// Every cell of the strip is Diple's, pads included.
	for x := first; x <= last && x < len(l.Cells); x++ {
		if x < 0 {
			continue
		}
		l.Cells[x] = screen.Blank(screen.Attr{})
	}
	if d := stripDividerCol(choices); d >= 0 && d < len(l.Cells) {
		l.Cells[d] = screen.Cell{Rune: '│', Width: 1, Attr: s.dimAttr()}
	}
	inForce := s.raiseTag
	if inForce == "" {
		inForce = card.DefaultTag
	}
	for _, c := range choices {
		attr := s.tagAttr(c.tag)
		if c.tag == "" {
			attr = s.dimAttr()
		}
		if c.tag != "" && c.tag == inForce {
			attr = mergeAttr(attr, screen.Attr{Flags: screen.Underline})
		}
		putText(l, c.from, c.label, attr)
	}
}

// drawTailMarks puts Diple's own › one column after the last character of
// every block that carries a card, in the card's tag colour, and the count
// when a block carries more than one.
func (s *Session) drawTailMarks(base []screen.Line, toScreen func(int) int) {
	type mark struct {
		tag   card.Tag
		count int
	}
	marks := map[int]*mark{}
	for _, c := range s.Tray.Cards {
		if c.Kind != card.Anchored {
			continue
		}
		_, last, ok := s.resolveAnchor(&c.Anchor)
		if !ok {
			continue
		}
		if m := marks[last]; m != nil {
			m.count++
			continue
		}
		marks[last] = &mark{tag: c.Tag, count: 1}
	}
	for hist, m := range marks {
		r := toScreen(hist)
		if r < 0 || r >= len(base) {
			continue
		}
		x := lastTextCol(base[r]) + 1
		if x < 0 || x >= len(base[r].Cells) {
			continue
		}
		attr := s.tagAttr(m.tag)
		base[r].Cells[x] = screen.Cell{Rune: '›', Width: 1, Attr: attr}
		if m.count > 1 {
			putText(&base[r], x+1, strconv.Itoa(m.count), attr)
		}
	}
}

// lastTextCol is the column of a row's last non-blank cell, or -1.
func lastTextCol(l screen.Line) int {
	for x := len(l.Cells) - 1; x >= 0; x-- {
		if l.Cells[x].Rune != 0 && l.Cells[x].Rune != ' ' {
			return x
		}
	}
	return -1
}

func mergeAttr(a, b screen.Attr) screen.Attr {
	a.Flags |= b.Flags
	return a
}

// editorLine renders the inline editor: what is being written — a note's
// tag or a free card's kind — then the text, and any transient note such as
// a capture that just happened.
func (s *Session) editorLine() screen.Line {
	l := s.blankLine()
	x := putText(&l, 1, "› ", s.dimAttr())
	x = putText(&l, x, s.editorLabel()+": ", s.tagAttr(s.editor.tag))
	x = putText(&l, x, string(s.editor.text), screen.Attr{})
	if n := s.editor.note; n != "" {
		putText(&l, x+2, n, s.dimAttr())
	}
	return l
}

// editorLabel is the tag of a note or the kind of a free card.
func (s *Session) editorLabel() string {
	if e := s.editor; e.tag != "" {
		return string(e.tag)
	} else if e.kind != "" {
		return string(e.kind)
	}
	return "note"
}

// searchLine renders the transcript search field: the query, and a note when
// the last search found nothing.
func (s *Session) searchLine() screen.Line {
	l := s.blankLine()
	x := putText(&l, 1, "› ", s.dimAttr())
	x = putText(&l, x, "/"+string(s.search.query), screen.Attr{})
	if s.search.noMatch {
		putText(&l, x+2, "no match", s.dimAttr())
	}
	return l
}

func (s *Session) searchCursorCol() int {
	x := 1 + 2 + 1
	for _, r := range s.search.query {
		x += runeWidth(r)
	}
	if x >= s.cols {
		x = s.cols - 1
	}
	return x
}

func (s *Session) editorCursorCol() int {
	x := 1 + 2 + len(s.editorLabel()) + 2
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
	// The search field and the kind chooser are not tied to a block, so they
	// sit at the foot of the agent region.
	if s.search != nil || s.chooser {
		return agentRows - 1
	}
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
