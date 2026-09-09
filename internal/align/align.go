// Package align matches a turn's blocks against rendered screen rows. It
// compares only letters and digits, so wrapping, indentation, bullets,
// numbering, box drawing, and the CLI's decoration characters do not matter,
// and it walks rows in order so the same text echoed elsewhere on screen
// (the user's own prompt, a stale row) is never picked by accident.
package align

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/maximalfocus/diple/internal/blocks"
)

// Rules are the decoration facts of one CLI.
type Rules struct {
	// TurnMarker begins a row where an assistant turn starts.
	TurnMarker string
	// PromptMarker begins a row where the user's own prompt is echoed; it
	// ends the region a turn may occupy.
	PromptMarker string
	// ResultMarker begins a tool-result row, which the transcript's blocks
	// do not describe.
	ResultMarker string
	// MaxResync bounds how many rows may be skipped after a tool call
	// before the next block must appear.
	MaxResync int
	// Fence is the marker a renderer draws around a code block when it
	// prints the fence itself, as pi does. Those rows belong to no block,
	// so matching steps over them.
	Fence string
}

// Span is a block's first and last row index.
type Span struct {
	First, Last int
}

// Normalize keeps only letters and digits.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// target is the normalized text a block must match on screen.
func target(b blocks.Block) string {
	t := Normalize(b.Text)
	if b.Kind == blocks.ListItem && b.Ordered {
		t = strconv.Itoa(b.Ordinal) + t
	}
	return t
}

// Turn aligns bs against rows starting at row start, which should be a turn
// marker row. It returns one span per block, in block order, and false when
// the blocks cannot be matched in order.
func Turn(bs []blocks.Block, rows []string, start int, rules Rules) ([]Span, bool) {
	spans := make([]Span, len(bs))
	for i := range spans {
		spans[i] = Span{-1, -1}
	}
	norm := make([]string, len(rows))
	for i, r := range rows {
		norm[i] = Normalize(r)
	}
	pos := start
	afterTool := false
	for idx, b := range bs {
		if b.Kind == blocks.CodeBlock {
			continue // spans of a code block come from its lines
		}
		t := target(b)
		if b.Kind == blocks.ToolCall {
			// A tool call renders as one row that begins with the tool name
			// and is followed by result rows the transcript does not carry.
			pos = skipNoise(norm, rows, pos, rules)
			if pos >= len(rows) || !strings.HasPrefix(norm[pos], Normalize(toolName(b.Text))) {
				return spans, false
			}
			spans[idx] = Span{pos, pos}
			pos++
			afterTool = true
			continue
		}
		if t == "" {
			// A blank line inside a code block occupies exactly one row.
			if pos >= len(rows) || norm[pos] != "" {
				return spans, false
			}
			spans[idx] = Span{pos, pos}
			pos++
			continue
		}
		first := pos
		if afterTool {
			// Tool results sit between the tool call and the next block.
			first = resync(norm, rows, pos, t, rules)
			if first < 0 {
				return spans, false
			}
			afterTool = false
		} else {
			first = skipNoise(norm, rows, first, rules)
		}
		acc := ""
		last := -1
		for i := first; i < len(rows); i++ {
			if rules.PromptMarker != "" && strings.HasPrefix(rows[i], rules.PromptMarker) {
				break
			}
			acc += norm[i]
			if acc == t {
				last = i
				break
			}
			if !strings.HasPrefix(t, acc) {
				break
			}
		}
		if last < 0 {
			return spans, false
		}
		spans[idx] = Span{first, last}
		pos = last + 1
	}
	// Code blocks span their lines.
	for idx, b := range bs {
		if b.Kind != blocks.CodeBlock {
			continue
		}
		first, last := -1, -1
		for j := idx + 1; j < len(bs) && bs[j].Parent == idx; j++ {
			if spans[j].First < 0 {
				continue
			}
			if first < 0 {
				first = spans[j].First
			}
			last = spans[j].Last
		}
		if first < 0 {
			// An empty code block renders nothing; anchor it to the
			// following row so it still has a place.
			first, last = pos, pos
		}
		spans[idx] = Span{first, last}
	}
	return spans, true
}

func skipBlank(norm []string, pos int) int {
	for pos < len(norm) && norm[pos] == "" {
		pos++
	}
	return pos
}

// skipNoise steps over the rows that belong to no block: blank ones, and the
// fence rows a renderer draws around a code block.
func skipNoise(norm []string, rows []string, pos int, rules Rules) int {
	for pos < len(rows) {
		if norm[pos] == "" {
			pos++
			continue
		}
		if rules.Fence != "" && strings.HasPrefix(strings.TrimSpace(rows[pos]), rules.Fence) {
			pos++
			continue
		}
		return pos
	}
	return pos
}

func resync(norm []string, rows []string, pos int, t string, rules Rules) int {
	limit := rules.MaxResync
	if limit <= 0 {
		limit = 200
	}
	for i := pos; i < len(rows) && i-pos <= limit; i++ {
		if rules.PromptMarker != "" && strings.HasPrefix(rows[i], rules.PromptMarker) {
			return -1
		}
		if norm[i] != "" && strings.HasPrefix(t, norm[i]) {
			return i
		}
	}
	return -1
}

func toolName(text string) string {
	if i := strings.IndexByte(text, '('); i > 0 {
		return text[:i]
	}
	return text
}

// Paragraphs splits rows[from:to] into one span per run of non-blank rows,
// the fallback when a turn cannot be aligned.
func Paragraphs(rows []string, from, to int) []Span {
	var out []Span
	start := -1
	for i := from; i < to && i < len(rows); i++ {
		blank := Normalize(rows[i]) == ""
		switch {
		case !blank && start < 0:
			start = i
		case blank && start >= 0:
			out = append(out, Span{start, i - 1})
			start = -1
		}
	}
	if start >= 0 {
		end := to - 1
		if end >= len(rows) {
			end = len(rows) - 1
		}
		out = append(out, Span{start, end})
	}
	return out
}
