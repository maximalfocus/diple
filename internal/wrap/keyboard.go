package wrap

import (
	"strings"
	"unicode"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/keys"
)

// This file is the keyboard's half of pointing: everything the mouse does to
// reach a block, a code line, or a span, done with keys instead. It builds
// exactly the same selection the mouse builds, so the card that comes out is
// the same card.

// selectionKeyLocked handles one key while a block is selected. It reports
// whether the key was Diple's; the toolbar letters are left to the toolbar.
func (s *Session) selectionKeyLocked(k keys.Key) bool {
	switch {
	case s.Keys.Is(keys.NextBlock, k):
		return s.moveSelectionLocked(1)
	case s.Keys.Is(keys.PrevBlock, k):
		return s.moveSelectionLocked(-1)
	case s.Keys.Is(keys.SelectLine, k):
		return s.selectFirstLineLocked()
	case s.Keys.Is(keys.ExtendLine, k):
		return s.extendLineLocked()
	case s.Keys.Is(keys.Span, k):
		return s.startSpanLocked()
	case s.Keys.Is(keys.ExtendChar, k):
		return s.resizeSpanLocked(1, false)
	case s.Keys.Is(keys.ShrinkChar, k):
		return s.resizeSpanLocked(-1, false)
	case s.Keys.Is(keys.ExtendWord, k):
		return s.resizeSpanLocked(1, true)
	case s.Keys.Is(keys.ShrinkWord, k):
		return s.resizeSpanLocked(-1, true)
	}
	return false
}

// blockList flattens the alignment into the blocks a selection can visit, in
// screen order, skipping the ones that have no rows.
func blockList(al []adapter.TurnAlignment) []blockRef {
	var out []blockRef
	for ti := range al {
		for bi := range al[ti].Blocks {
			b := &al[ti].Blocks[bi]
			if b.First < 0 {
				continue
			}
			out = append(out, blockRef{turn: al[ti].Turn, index: bi, block: b})
		}
	}
	return out
}

type blockRef struct {
	turn, index int
	block       *adapter.AlignedBlock
}

func isLine(b *adapter.AlignedBlock) bool {
	return b.Kind == blocks.CodeLine || b.Kind == blocks.DiffLine
}

// selectTopBlockLocked selects the first whole block in view, which is what a
// modifier-click on the top of the screen would have selected.
func (s *Session) selectTopBlockLocked() {
	rows, al := s.alignmentLocked()
	start := s.windowStart()
	list := blockList(al)
	for _, ref := range list {
		if isLine(ref.block) || ref.block.First < start {
			continue
		}
		s.setSelectionLocked(rows, ref)
		return
	}
	// Nothing below the top of the view: take the last block above it, so
	// the gesture always lands somewhere.
	for i := len(list) - 1; i >= 0; i-- {
		if !isLine(list[i].block) {
			s.setSelectionLocked(rows, list[i])
			return
		}
	}
}

// setSelectionLocked builds the same selection a modifier-click builds.
func (s *Session) setSelectionLocked(rows []string, ref blockRef) {
	b := ref.block
	text := b.Text
	if b.Kind == blocks.CodeBlock && b.Last < len(rows) {
		text = strings.Join(rows[b.First:b.Last+1], "\n")
	}
	s.sel = &selection{turn: ref.turn, block: ref.index, kind: b.Kind, first: b.First, last: b.Last,
		text: text, ordinal: b.Ordinal, absolute: s.dropped() + b.First, parent: -1}
	s.editor, s.highlight, s.focus = nil, nil, focusAgent
	s.ensureVisibleLocked(b.First, b.Last)
}

// moveSelectionLocked walks to the next or previous block. A line selection
// walks by line inside its own block, which is what the gutter does.
func (s *Session) moveSelectionLocked(dir int) bool {
	rows, al := s.alignmentLocked()
	if s.sel == nil {
		return false
	}
	if s.sel.lines != nil {
		return s.moveLineLocked(al, rows, dir)
	}
	list := blockList(al)
	at := -1
	for i, ref := range list {
		if ref.turn == s.sel.turn && ref.index == s.sel.block {
			at = i
			break
		}
	}
	if at < 0 {
		return false
	}
	for i := at + dir; i >= 0 && i < len(list); i += dir {
		if isLine(list[i].block) {
			continue
		}
		s.setSelectionLocked(rows, list[i])
		return true
	}
	return true // at an end: the selection stays where it is
}

// selectFirstLineLocked turns a selected code block into a selection of its
// first line, the keyboard's gutter click.
func (s *Session) selectFirstLineLocked() bool {
	rows, al := s.alignmentLocked()
	if s.sel == nil {
		return false
	}
	for _, t := range al {
		if t.Turn != s.sel.turn {
			continue
		}
		for bi := range t.Blocks {
			b := &t.Blocks[bi]
			if !isLine(b) || b.Parent != s.sel.block || b.First < 0 {
				continue
			}
			s.sel = &selection{turn: t.Turn, block: bi, kind: b.Kind, first: b.First, last: b.Last,
				text: rows[b.First], lines: &card.LineRange{First: bi, Last: bi},
				absolute: s.dropped() + b.First, parent: b.Parent}
			s.editor, s.highlight, s.focus = nil, nil, focusAgent
			s.ensureVisibleLocked(b.First, b.Last)
			return true
		}
	}
	return false
}

// moveLineLocked moves a line selection to the next or previous line of the
// same code block.
func (s *Session) moveLineLocked(al []adapter.TurnAlignment, rows []string, dir int) bool {
	for _, t := range al {
		if t.Turn != s.sel.turn {
			continue
		}
		next := s.sel.block + dir
		if next < 0 || next >= len(t.Blocks) || !isLine(&t.Blocks[next]) || t.Blocks[next].First < 0 {
			return true
		}
		b := &t.Blocks[next]
		s.sel.block, s.sel.kind = next, b.Kind
		s.sel.first, s.sel.last = b.First, b.Last
		s.sel.lines = &card.LineRange{First: next, Last: next}
		s.sel.text = rows[b.First]
		s.sel.absolute = s.dropped() + b.First
		s.ensureVisibleLocked(b.First, b.Last)
		return true
	}
	return false
}

// extendLineLocked grows a line selection by one line, the keyboard's
// shift-click on the gutter.
func (s *Session) extendLineLocked() bool {
	rows, al := s.alignmentLocked()
	if s.sel == nil || s.sel.lines == nil {
		return false
	}
	for _, t := range al {
		if t.Turn != s.sel.turn {
			continue
		}
		next := s.sel.lines.Last + 1
		if next >= len(t.Blocks) || !isLine(&t.Blocks[next]) || t.Blocks[next].First < 0 {
			return true
		}
		s.sel.lines.Last = next
		s.sel.first, s.sel.last = lineRows(al, s.sel.turn, s.sel.lines)
		s.sel.text = strings.Join(rows[s.sel.first:s.sel.last+1], "\n")
		s.ensureVisibleLocked(s.sel.first, s.sel.last)
		return true
	}
	return false
}

// startSpanLocked opens a span on the first word of the block's text, where
// a drag would naturally begin, the way a double-click takes a word.
func (s *Session) startSpanLocked() bool {
	rows, _ := s.alignmentLocked()
	if s.sel == nil || s.sel.first < 0 || s.sel.first >= len(rows) {
		return false
	}
	line := []rune(padRunes(rows[s.sel.first], s.cols))
	col := textStart(rows[s.sel.first], s.sel.text)
	s.sel.span = &card.Span{Row: s.sel.first, Col: col, EndRow: s.sel.first, EndCol: wordEndAt(line, col)}
	s.updateSpanTextLocked(rows)
	return true
}

// resizeSpanLocked moves the span's end by a character or a word. Shrinking
// past the start closes the span again.
func (s *Session) resizeSpanLocked(dir int, word bool) bool {
	rows, _ := s.alignmentLocked()
	if s.sel == nil || s.sel.span == nil {
		return false
	}
	sp := s.sel.span
	if sp.EndRow < 0 || sp.EndRow >= len(rows) {
		return false
	}
	line := []rune(padRunes(rows[sp.EndRow], s.cols))
	end := sp.EndCol
	switch {
	case word && dir > 0:
		end = wordEnd(line, end)
	case word:
		end = wordStart(line, end)
	default:
		end += dir
	}
	if end < sp.Col {
		end = sp.Col
	}
	if end >= len(line) {
		end = len(line) - 1
	}
	sp.EndCol = end
	s.updateSpanTextLocked(rows)
	return true
}

// updateSpanTextLocked keeps the selection's quotation in step with its span.
func (s *Session) updateSpanTextLocked(rows []string) {
	sp := s.sel.span
	line := []rune(padRunes(rows[sp.EndRow], s.cols))
	to := sp.EndCol
	if to >= len(line) {
		to = len(line) - 1
	}
	if sp.Col > to {
		s.sel.text = ""
		return
	}
	s.sel.text = strings.TrimSpace(string(line[sp.Col : to+1]))
}

// textStart is the column where a block's own text begins on its first row,
// past any indent, bullet, or numbering the renderer drew.
func textStart(row, text string) int {
	first := strings.Fields(strings.TrimSpace(text))
	if len(first) == 0 {
		return 0
	}
	if i := strings.Index(row, first[0]); i >= 0 {
		return len([]rune(row[:i]))
	}
	r := []rune(row)
	for i, c := range r {
		if !unicode.IsSpace(c) {
			return i
		}
	}
	return 0
}

// wordEndAt is the last column of the word that starts at from.
func wordEndAt(line []rune, from int) int {
	i := from
	for i < len(line) && !unicode.IsSpace(line[i]) {
		i++
	}
	if i > from {
		return i - 1
	}
	return from
}

// wordEnd is the last column of the next word at or after from.
func wordEnd(line []rune, from int) int {
	i := from
	for i < len(line) && !unicode.IsSpace(line[i]) {
		i++
	}
	for i < len(line) && unicode.IsSpace(line[i]) {
		i++
	}
	for i < len(line) && !unicode.IsSpace(line[i]) {
		i++
	}
	if i > from {
		return i - 1
	}
	return from
}

// wordStart is the last column of the previous word before from.
func wordStart(line []rune, from int) int {
	i := from
	for i > 0 && !unicode.IsSpace(line[i]) {
		i--
	}
	for i > 0 && unicode.IsSpace(line[i]) {
		i--
	}
	return i
}
