package wrap

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/maximalfocus/diple/internal/keys"

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
	// parent is the index of the enclosing code block for a line selection,
	// which is where a diff's file and line numbers come from. -1 otherwise.
	parent int
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
	// overall marks the card being written as the tray's closing remark.
	overall bool
	// fenced records that the text arrived as a paste of several lines.
	fenced bool
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
	// motionNoButton is a motion report with no button down: the motion bit
	// plus the "no button" code the SGR encoding uses.
	motionNoButton = motionBit + 3
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
		ordinal: b.Ordinal, absolute: s.dropped() + b.First, parent: -1}
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
		lines: &card.LineRange{First: idx, Last: idx}, absolute: s.dropped() + b.First, parent: b.Parent}
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
		absolute: s.dropped() + b.First, parent: -1}
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

// stripKeyLocked handles one key while a block is raised or selected. The
// strip's own four letters — n, f, a and c — do what its four choices do,
// without moving the pointer at all, so the strip is a convenience rather
// than the only way to reach a tag.
func (s *Session) stripKeyLocked(chunk []byte) bool {
	if len(chunk) == 1 && chunk[0] == 0x1b {
		// Esc with nothing typed closes and clears the selection.
		s.sel, s.textSel, s.raised = nil, nil, nil
		return true
	}
	r, size := utf8.DecodeRune(chunk)
	if size != len(chunk) {
		return false
	}
	k := keys.Key{Rune: r}
	if s.Keys.Is(keys.Copy, k) {
		s.setKeyErr(s.copySelectionLocked())
		return true
	}
	for _, ta := range keys.TagActions {
		if s.Keys.Is(ta.Action, k) {
			sel := s.sel
			if sel == nil {
				sel = s.raiseSelection()
			}
			if sel == nil {
				return false
			}
			s.raiseTag = card.Tag(ta.Tag)
			s.openEditorLocked(card.Tag(ta.Tag), sel, nil)
			return true
		}
	}
	return false
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

// editorKeysLocked handles one input unit while the editor is open.
//
// Esc never destroys typed text: with text it saves the card and closes,
// empty it closes and clears the selection, and with nothing of Diple's open
// it reaches the agent unchanged, as does a second Esc in every case, so the
// agent's own interrupt is never captured.
func (s *Session) editorKeysLocked(chunk []byte) {
	e := s.editor
	if chunk[0] == 0x1b {
		if len(chunk) == 1 {
			if len(e.text) > 0 || len(e.attached) > 0 {
				s.commitEditorLocked()
				return
			}
			s.editor, s.sel, s.textSel = nil, nil, nil
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

// editorAltLocked answers an Alt key inside the open editor: the tag chips,
// which move the strip's underline with them, and the send, which saves the
// card first and then folds, so a single note costs one keystroke rather than
// two. It reports whether the key was the editor's.
func (s *Session) editorAltLocked(k keys.Key) (bool, error) {
	e := s.editor
	if e == nil {
		return false, nil
	}
	if s.Keys.Is(keys.Send, k) {
		s.commitEditorLocked()
		if s.Tray.Len() == 0 {
			return true, nil
		}
		return true, s.requestSendLocked(true)
	}
	// A chip answers to the Alt form of its own letter, which is the same
	// letter the strip carries.
	for _, ta := range keys.TagActions {
		bound := s.Keys.Key(ta.Action)
		if k.Alt && k.Rune == bound.Rune {
			if e.kind == card.Free {
				return true, nil // a free card carries no tag
			}
			e.tag, s.raiseTag = card.Tag(ta.Tag), card.Tag(ta.Tag)
			return true, nil
		}
	}
	return false, nil
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
		// Emptying a card's text and saving removes it, since a card with
		// nothing in it was never a card.
		if text == "" && len(e.attached) == 0 {
			for i, c := range s.Tray.Cards {
				if c == e.editing {
					s.Tray.Delete(i)
					break
				}
			}
			s.focus = focusTray
			if s.Tray.Len() == 0 {
				s.focus = focusAgent
			}
			s.clampTraySel()
			_ = s.saveLocked()
			return
		}
		e.editing.Text = text
		if e.editing.Kind == card.Anchored {
			e.editing.Tag = e.tag
		}
		e.editing.Attachments = e.attached
		s.focus = focusTray
		_ = s.saveLocked()
		return
	}
	if e.kind == card.Free {
		if text == "" && len(e.attached) == 0 {
			if s.Tray.Len() > 0 {
				s.focus = focusTray
			}
			return
		}
		s.Tray.Add(&card.Card{Kind: card.Free, Text: text, Attachments: e.attached,
			Overall: e.overall, Fenced: e.fenced})
		s.focus = focusTray
		s.clampTraySel()
		_ = s.saveLocked()
		return
	}
	sel := e.sel
	if sel == nil {
		return
	}
	c := &card.Card{Kind: card.Anchored, Tag: e.tag, Text: text, Anchor: card.Anchor{
		Turn: sel.turn, Block: sel.block, Kind: sel.kind, First: sel.first, Last: sel.last,
		Absolute: sel.absolute, Quote: card.Quote(sel.text), Span: sel.span, Lines: sel.lines,
	}}
	// The anchor says what it points at, independently of the tag: a list
	// item's ordinal, or the file and line range a diff named.
	if sel.kind == blocks.ListItem {
		c.Anchor.Ordinal = sel.ordinal
	}
	s.locateAnchorLocked(&c.Anchor, sel)
	s.Tray.Add(c)
	s.focus = focusAgent
	s.sel, s.textSel = nil, nil
	_ = s.saveLocked()
}

// locateAnchorLocked fills in the file and line range a diff anchor names,
// when the diff the lines came from named one. A diff that names neither
// leaves the anchor with its quotation, which is what the agent gets instead.
func (s *Session) locateAnchorLocked(a *card.Anchor, sel *selection) {
	if sel.lines == nil || sel.parent < 0 {
		return
	}
	_, al := s.alignmentLocked()
	for _, t := range al {
		if t.Turn != sel.turn || sel.parent >= len(t.Blocks) {
			continue
		}
		body := t.Blocks[sel.parent].Text
		path, first, ok := blocks.DiffLocation(body, sel.lines.First-sel.parent-1)
		if !ok {
			return
		}
		_, last, ok := blocks.DiffLocation(body, sel.lines.Last-sel.parent-1)
		if !ok {
			last = first
		}
		a.Path, a.LineFirst, a.LineLast = path, first, last
		return
	}
}

// takesAttachments reports whether the card being edited may carry them: a
// free card may, since it is what the user would otherwise have typed.
func (e *editor) takesAttachments() bool {
	if e.editing != nil {
		return e.editing.Kind == card.Free
	}
	return e.kind == card.Free
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
