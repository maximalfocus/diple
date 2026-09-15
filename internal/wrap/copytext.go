package wrap

import (
	"strings"
	"unicode/utf8"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/align"
	"github.com/maximalfocus/diple/internal/screen"
)

// This file is what a copy carries: what the selection shows. Its highlighted
// cells are read by the column model that drew them, so a wide or combined
// character comes out once, in place, and Markdown the screen hides — emphasis,
// a link's target — never travels, while a visible list marker does. Rows join
// where a logical line wrapped, and the agent's turn marker, which no highlight
// covers, is never copied.

// historyLinesLocked returns the history rows as cells, row for row with
// HistoryRows: scrollback plus the screen inline, the visible screen when the
// agent is on the alternate screen.
func (s *Session) historyLinesLocked() []screen.Line {
	var lines []screen.Line
	if !s.Model.AltActive() {
		lines = append(lines, s.Model.History()...)
	}
	return append(lines, s.Model.Rows()...)
}

// markerCells is how many cells the agent's turn marker takes at the start of
// a row, with the spaces after it, or 0 when the row begins with none.
func (s *Session) markerCells(l screen.Line) int {
	if s.adapter == nil {
		return 0
	}
	m := s.adapter.Marker(l.String())
	if m == "" {
		return 0
	}
	want, x := utf8.RuneCountInString(m), 0
	for x < len(l.Cells) && want > 0 {
		if l.Cells[x].Width != 0 {
			want--
		}
		x++
	}
	for x < len(l.Cells) && (l.Cells[x].Width == 0 || isSpaceCell(l.Cells[x])) {
		x++
	}
	return x
}

func isSpaceCell(c screen.Cell) bool { return c.Width == 1 && c.Rune == ' ' && c.Extra == "" }

// indentCells is the number of blank cells a row begins with.
func indentCells(l screen.Line) int {
	x := 0
	for x < len(l.Cells) && isSpaceCell(l.Cells[x]) {
		x++
	}
	return x
}

// cellText reads cells from through to of a row as the screen shows them: a
// wide character once, whichever half the range begins or ends on, and
// combining marks with the character they are drawn onto.
func cellText(l screen.Line, from, to int) string {
	if from < 0 {
		from = 0
	}
	if to >= len(l.Cells) {
		to = len(l.Cells) - 1
	}
	for from > 0 && from <= to && l.Cells[from].Width == 0 {
		from--
	}
	var b strings.Builder
	for x := from; x <= to; x++ {
		c := l.Cells[x]
		if c.Width == 0 {
			continue
		}
		b.WriteRune(c.Rune)
		b.WriteString(c.Extra)
	}
	return b.String()
}

// copyBlock is what a copy needs to know about the aligned block a row lies
// in: where the renderer's indent ends, and after which of its rows the
// transcript breaks the line rather than the renderer wrapping it.
type copyBlock struct {
	first, last int
	left        int          // the block's smallest indent, in cells
	breaks      map[int]bool // rows the transcript's line ends on
}

// blockForCopy finds the block of an aligned turn that covers row r. A row in
// no block, or in a turn whose transcript did not match, is copied as the
// screen shows it.
func blockForCopy(lines []screen.Line, al []adapter.TurnAlignment, r int) *copyBlock {
	for _, t := range al {
		if !t.Aligned {
			continue
		}
		for i := range t.Blocks {
			b := &t.Blocks[i]
			if b.First < 0 || r < b.First || r > b.Last || isLine(b) {
				continue
			}
			cb := &copyBlock{first: b.First, last: b.Last, left: -1, breaks: map[int]bool{}}
			for x := b.First; x <= b.Last && x < len(lines); x++ {
				if l := lines[x]; strings.TrimSpace(l.String()) != "" {
					if in := indentCells(l); cb.left < 0 || in < cb.left {
						cb.left = in
					}
				}
			}
			if cb.left < 0 {
				cb.left = 0
			}
			// The transcript's own line breaks: walk the rows against its
			// lines by letters and digits, and mark the row each line ends on.
			want := strings.Split(strings.TrimRight(b.Text, "\n"), "\n")
			k, acc := 0, ""
			for x := b.First; x <= b.Last && x < len(lines) && k < len(want); x++ {
				acc += align.Normalize(lines[x].String())
				if target := align.Normalize(want[k]); len(acc) >= len(target) {
					cb.breaks[x] = true
					k, acc = k+1, ""
				}
			}
			return cb
		}
	}
	return nil
}

// fitsAbove reports whether the first word of a row would have fitted on the
// row above it. A renderer wraps greedily, moving a word down only when it
// does not fit, so a row whose next word would have fitted ended on a line
// break of the text — a hard break the parsed transcript may no longer carry —
// and not on a wrap.
func fitsAbove(above, row screen.Line) bool {
	end := lastTextCell(above) + 1
	if end <= 1 && len(above.Cells) > 0 && isSpaceCell(above.Cells[0]) {
		return false
	}
	start := indentCells(row)
	word := 0
	for x := start; x < len(row.Cells) && !isSpaceCell(row.Cells[x]); x++ {
		word++
	}
	return word > 0 && end+1+word <= len(above.Cells)
}

// copiedText is what a highlighted range puts on the clipboard: from column
// from on history row first to column to on row last, as the screen shows it.
func (s *Session) copiedText(lines []screen.Line, al []adapter.TurnAlignment,
	first, from, last, to int) string {
	if first < 0 {
		first, from = 0, 0
	}
	if last >= len(lines) {
		last, to = len(lines)-1, 1<<30
	}
	if first > last || last < 0 {
		return ""
	}
	var b strings.Builder
	var prev *copyBlock
	for r := first; r <= last; r++ {
		l := lines[r]
		lo, hi := 0, len(l.Cells)-1
		if r == first {
			lo = from
		}
		if r == last && to < hi {
			hi = to
		}
		if m := s.markerCells(l); lo < m {
			lo = m
		}
		cb := blockForCopy(lines, al, r)
		joined := r > first && prev != nil && cb != nil && prev.first == cb.first &&
			!prev.breaks[r-1] && !fitsAbove(lines[r-1], l)
		switch {
		case r == first:
		case lines[r-1].Wrapped:
			// A terminal soft wrap: the rows are one line, spaces and all.
		case joined:
			// The agent wrapped the line: its continuation indent is the
			// renderer's, and the wrap stood for one space.
			if in := indentCells(l); lo < in {
				lo = in
			}
			b.WriteByte(' ')
		default:
			b.WriteByte('\n')
		}
		// The renderer's indent left of an aligned block is not the text.
		if cb != nil && !joined && lo < cb.left {
			lo = cb.left
		}
		text := ""
		if lo <= hi {
			text = cellText(l, lo, hi)
		}
		if !l.Wrapped || r == last {
			text = strings.TrimRight(text, " ")
		}
		b.WriteString(text)
		prev = cb
	}
	return b.String()
}
