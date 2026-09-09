package wrap

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/attach"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/card"
)

// selection is the block, span, or line range the user pointed at.
type selection struct {
	turn, block int
	kind        blocks.Kind
	first, last int // history rows
	text        string
	ordinal     int
	span        *card.Span
	lines       *card.LineRange
	absolute    int
}

// editor is the one-line inline editor under a selection or a card. A note
// carries a tag; a free card carries its kind instead.
type editor struct {
	tag         card.Tag
	kind        card.Kind
	text        []rune
	first, last int // history rows the editor sits under
	editing     *card.Card
	sel         *selection
	// attached is what the editor has attached so far, for a card that
	// takes attachments.
	attached []card.Attachment
	// note is a transient line under the field, such as a failed capture.
	note string
}

type dragState struct {
	row, col int // history row and column of the press
	moved    bool
	card     int // tray card index being dragged, -1 otherwise
}

// SGR mouse modifier bits.
const (
	modShift  = 4
	modMeta   = 8
	modCtrl   = 16
	motionBit = 32
)

func (m mouseEvent) button0() int { return m.button &^ (modShift | modMeta | modCtrl | motionBit) }
func (m mouseEvent) meta() bool   { return m.button&modMeta != 0 }
func (m mouseEvent) shift() bool  { return m.button&modShift != 0 }
func (m mouseEvent) motion() bool { return m.button&motionBit != 0 }

// alignmentLocked computes the current alignment over the history rows.
func (s *Session) alignmentLocked() ([]string, []adapter.TurnAlignment) {
	rows := s.HistoryRows()
	if s.adapter == nil {
		return rows, nil
	}
	if tr := s.transcript(); tr != nil {
		return rows, s.adapter.Align(tr, rows)
	}
	return rows, s.adapter.Fallback(rows)
}

// blockAt finds the block covering a history row. Lines wants a code or
// diff line rather than its enclosing block.
func blockAt(al []adapter.TurnAlignment, row int, lines bool) (turn int, index int, b *adapter.AlignedBlock) {
	for ti := range al {
		for bi := range al[ti].Blocks {
			ab := &al[ti].Blocks[bi]
			if row < ab.First || row > ab.Last {
				continue
			}
			isLine := ab.Kind == blocks.CodeLine || ab.Kind == blocks.DiffLine
			if lines && isLine {
				return al[ti].Turn, bi, ab
			}
			if !lines && !isLine {
				return al[ti].Turn, bi, ab
			}
		}
	}
	return 0, -1, nil
}

func (s *Session) dropped() int {
	return int(s.Model.ScrolledOff()) - s.Model.HistoryLen()
}

// selectBlockLocked selects the block at a history row by modifier-click.
func (s *Session) selectBlockLocked(row int) bool {
	rows, al := s.alignmentLocked()
	turn, idx, b := blockAt(al, row, false)
	if b == nil {
		return false
	}
	text := b.Text
	if b.Kind == blocks.CodeBlock {
		text = strings.Join(rows[b.First:b.Last+1], "\n")
	}
	s.sel = &selection{turn: turn, block: idx, kind: b.Kind, first: b.First, last: b.Last, text: text,
		ordinal: b.Ordinal, absolute: s.dropped() + b.First}
	s.editor, s.highlight, s.focus = nil, nil, focusAgent
	return true
}

// selectLineLocked selects a code or diff line by a gutter click; extend
// grows the current line selection within the same block.
func (s *Session) selectLineLocked(row int, extend bool) bool {
	rows, al := s.alignmentLocked()
	turn, idx, b := blockAt(al, row, true)
	if b == nil {
		return false
	}
	if extend && s.sel != nil && s.sel.lines != nil && s.sel.turn == turn {
		lr := s.sel.lines
		if idx < lr.First {
			lr.First = idx
		}
		if idx > lr.Last {
			lr.Last = idx
		}
		s.sel.first, s.sel.last = lineRows(al, turn, lr)
		s.sel.text = strings.Join(rows[s.sel.first:s.sel.last+1], "\n")
		return true
	}
	s.sel = &selection{turn: turn, block: idx, kind: b.Kind, first: b.First, last: b.Last, text: rows[b.First],
		lines: &card.LineRange{First: idx, Last: idx}, absolute: s.dropped() + b.First}
	s.editor, s.highlight, s.focus = nil, nil, focusAgent
	return true
}

func lineRows(al []adapter.TurnAlignment, turn int, lr *card.LineRange) (first, last int) {
	for _, t := range al {
		if t.Turn != turn {
			continue
		}
		return t.Blocks[lr.First].First, t.Blocks[lr.Last].Last
	}
	return -1, -1
}

// selectSpanLocked selects the text between two points inside one block.
func (s *Session) selectSpanLocked(row, col, endRow, endCol int) bool {
	rows, al := s.alignmentLocked()
	turn, idx, b := blockAt(al, row, false)
	if b == nil {
		return false
	}
	if endRow < row || (endRow == row && endCol < col) {
		row, col, endRow, endCol = endRow, endCol, row, col
	}
	if endRow > b.Last {
		endRow, endCol = b.Last, s.cols-1
	}
	if row < b.First {
		row, col = b.First, 0
	}
	var text []string
	for r := row; r <= endRow; r++ {
		line := []rune(padRunes(rows[r], s.cols))
		from, to := 0, len(line)-1
		if r == row {
			from = col
		}
		if r == endRow {
			to = endCol
		}
		if from > to || from >= len(line) {
			continue
		}
		if to >= len(line) {
			to = len(line) - 1
		}
		text = append(text, strings.TrimRight(string(line[from:to+1]), " "))
	}
	quote := strings.TrimSpace(strings.Join(text, " "))
	if quote == "" {
		return false
	}
	s.sel = &selection{turn: turn, block: idx, kind: b.Kind, first: b.First, last: b.Last, text: quote,
		ordinal: b.Ordinal, span: &card.Span{Row: row, Col: col, EndRow: endRow, EndCol: endCol},
		absolute: s.dropped() + b.First}
	s.editor, s.highlight, s.focus = nil, nil, focusAgent
	return true
}

func padRunes(s string, n int) string {
	r := []rune(s)
	for len(r) < n {
		r = append(r, ' ')
	}
	return string(r)
}

// toolbarKeyLocked handles a key while a selection shows its toolbar.
func (s *Session) toolbarKeyLocked(chunk []byte) bool {
	if len(chunk) == 1 && chunk[0] == 0x1b {
		s.sel = nil
		return true
	}
	r, size := utf8.DecodeRune(chunk)
	if size != len(chunk) {
		return false
	}
	tag := card.TagByLetter(r)
	if tag == "" {
		return false
	}
	s.openEditorLocked(tag, s.sel, nil)
	return true
}

func (s *Session) openEditorLocked(tag card.Tag, sel *selection, editing *card.Card) {
	e := &editor{tag: tag, sel: sel, editing: editing}
	if sel != nil {
		e.first, e.last = sel.first, sel.last
	}
	if editing != nil {
		e.kind = editing.Kind
		e.attached = append([]card.Attachment(nil), editing.Attachments...)
		e.text = []rune(editing.Text)
		if first, last, ok := s.resolveAnchor(&editing.Anchor); ok {
			e.first, e.last = first, last
		} else {
			e.first, e.last = -1, -1
		}
	}
	s.editor = e
	s.sel = nil
}

// editorKeysLocked handles one input unit while the editor is open: Esc
// discards, Enter saves, Backspace deletes, printable runes are appended,
// and escape sequences (arrows and friends) are ignored.
func (s *Session) editorKeysLocked(chunk []byte) {
	e := s.editor
	if chunk[0] == 0x1b {
		if len(chunk) == 1 {
			s.editor = nil
			if e.editing != nil {
				s.focus = focusTray
			}
		}
		return
	}
	for len(chunk) > 0 {
		r, size := utf8.DecodeRune(chunk)
		chunk = chunk[size:]
		switch {
		case r == '\r' || r == '\n':
			s.commitEditorLocked()
			return
		case r == 0x7f || r == 0x08:
			if len(e.text) > 0 {
				e.text = e.text[:len(e.text)-1]
			}
		case r >= 0x20 && r != utf8.RuneError:
			e.text = append(e.text, r)
		}
	}
}

func (s *Session) commitEditorLocked() {
	e := s.editor
	text := strings.TrimSpace(string(e.text))
	// On a card that takes attachments, a line that names one attaches it
	// and the field stays open for the next line.
	if e.takesAttachments() {
		if a, ok := attach.Parse(text); ok {
			a = attach.Capture(a, s.cwd)
			e.attached = append(e.attached, a)
			e.text, e.note = nil, attachNote(a)
			return
		}
	}
	s.editor = nil
	if e.editing != nil {
		e.editing.Text = text
		if e.editing.Kind == card.Note {
			e.editing.Tag = e.tag
		}
		e.editing.Attachments = e.attached
		s.focus = focusTray
		_ = s.saveLocked()
		return
	}
	if e.kind != "" && e.kind != card.Note {
		if text == "" && len(e.attached) == 0 {
			s.focus = focusTray
			return
		}
		s.Tray.Add(&card.Card{Kind: e.kind, Text: text, Attachments: e.attached})
		s.focus = focusTray
		s.clampTraySel()
		_ = s.saveLocked()
		return
	}
	sel := e.sel
	if sel == nil {
		return
	}
	c := &card.Card{Kind: card.Note, Tag: e.tag, Text: text, Anchor: card.Anchor{
		Turn: sel.turn, Block: sel.block, Kind: sel.kind, First: sel.first, Last: sel.last,
		Absolute: sel.absolute, Quote: card.Quote(sel.text), Span: sel.span, Lines: sel.lines,
	}}
	if e.tag == "prefer" && sel.kind == blocks.ListItem {
		c.Anchor.Ordinal = sel.ordinal
	}
	s.Tray.Add(c)
	s.focus = focusAgent
	_ = s.saveLocked()
}

// takesAttachments reports whether the card being edited may carry them.
func (e *editor) takesAttachments() bool {
	if e.editing != nil {
		return e.editing.Kind == card.Instruction
	}
	return e.kind == card.Instruction
}

// attachNote is the one-line confirmation shown under the field.
func attachNote(a card.Attachment) string {
	if a.Kind == card.PathAttachment {
		return "attached @" + a.Spec
	}
	if a.Status != 0 {
		return "attached " + a.Spec + " (exit " + strconv.Itoa(a.Status) + ")"
	}
	return "attached " + a.Spec
}

// resolveAnchor finds the rows an anchor occupies now: through the current
// alignment when the turn and block still exist, else through the absolute
// row recorded when the note was made.
func (s *Session) resolveAnchor(a *card.Anchor) (first, last int, ok bool) {
	_, al := s.alignmentLocked()
	for _, t := range al {
		if t.Turn != a.Turn || a.Block < 0 || a.Block >= len(t.Blocks) {
			continue
		}
		b := t.Blocks[a.Block]
		if b.Kind != a.Kind {
			break
		}
		if a.Lines != nil && a.Lines.Last < len(t.Blocks) {
			return t.Blocks[a.Lines.First].First, t.Blocks[a.Lines.Last].Last, true
		}
		return b.First, b.Last, true
	}
	if s.Model.AltActive() {
		return 0, 0, false
	}
	first = a.Absolute - s.dropped()
	last = first + (a.Last - a.First)
	total := s.Model.HistoryLen() + s.rows - s.trayH
	if first < 0 || last >= total {
		return 0, 0, false
	}
	return first, last, true
}
