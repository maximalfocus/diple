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
	"unicode/utf8"

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

// Blocks aligns bs against rows[start:end] the way Turn does, except that a
// block that cannot be matched gives up only its own rows: matching resumes at
// the first later row where a following block begins, so the blocks on either
// side keep theirs. It reports which blocks matched; an unmatched block keeps
// the span {-1, -1}. Matching never crosses the user's prompt.
func Blocks(bs []blocks.Block, rows []string, start, end int, rules Rules) ([]Span, []bool) {
	if end > len(rows) {
		end = len(rows)
	}
	spans := make([]Span, len(bs))
	matched := make([]bool, len(bs))
	for i := range spans {
		spans[i] = Span{-1, -1}
	}
	norm := make([]string, len(rows))
	for i, r := range rows {
		norm[i] = Normalize(r)
	}
	pos := start
	// search is set once a block went unmatched, or a tool call's results
	// follow it, so the next block is looked for rather than expected in place.
	search := false
	for idx, b := range bs {
		if b.Kind == blocks.CodeBlock {
			continue // spans of a code block come from its lines
		}
		var sp Span
		ok := false
		if search {
			for r := pos; r < end && !ok && !atPrompt(rows[r], rules); r++ {
				sp, ok = blockAt(b, norm, rows, r, end, rules, true)
			}
		} else {
			sp, ok = blockAt(b, norm, rows, pos, end, rules, false)
		}
		if !ok {
			search = true
			continue
		}
		spans[idx], matched[idx] = sp, true
		pos = sp.Last + 1
		search = b.Kind == blocks.ToolCall
	}
	// A code block spans whichever of its lines matched.
	for idx, b := range bs {
		if b.Kind != blocks.CodeBlock {
			continue
		}
		for j := idx + 1; j < len(bs) && bs[j].Parent == idx; j++ {
			if !matched[j] {
				continue
			}
			if !matched[idx] {
				spans[idx].First = spans[j].First
			}
			spans[idx].Last = spans[j].Last
			matched[idx] = true
		}
	}
	return spans, matched
}

// blockAt matches one block beginning at row r or, unless exact, at the first
// row from r that is not noise. The block's text must begin on that row, which
// is what keeps a searched-for block from matching a blank row or the middle
// of another block.
func blockAt(b blocks.Block, norm, rows []string, r, end int, rules Rules,
	exact bool) (Span, bool) {
	t := target(b)
	if t == "" && b.Kind != blocks.ToolCall {
		// A blank line inside a code block occupies exactly one row, and
		// cannot be told from any other blank row once its place is lost.
		if exact || r >= end || norm[r] != "" {
			return Span{}, false
		}
		return Span{r, r}, true
	}
	if !exact {
		r = skipNoise(norm, rows, r, rules)
	}
	if r >= end {
		return Span{}, false
	}
	if b.Kind == blocks.ToolCall {
		name := Normalize(toolName(b.Text))
		if name == "" || !strings.HasPrefix(norm[r], name) {
			return Span{}, false
		}
		return Span{r, r}, true
	}
	if norm[r] == "" || !strings.HasPrefix(t, norm[r]) {
		return Span{}, false
	}
	acc := ""
	for i := r; i < end && !atPrompt(rows[i], rules); i++ {
		acc += norm[i]
		if acc == t {
			return Span{r, i}, true
		}
		if !strings.HasPrefix(t, acc) {
			break
		}
	}
	return Span{}, false
}

func atPrompt(row string, rules Rules) bool {
	return rules.PromptMarker != "" && strings.HasPrefix(row, rules.PromptMarker)
}

// Paragraphs splits rows[from:to] into the rendered paragraphs a turn falls
// back to when its blocks cannot be aligned. A paragraph is a run of rows that
// carry letters or digits, and it also ends before a row that begins with a
// list marker, a fence or a turn marker, or that is indented less than the
// paragraph's text. So each item of a tight list, nested ones included, is a
// paragraph of its own, and so are the lead-in above a list and the code drawn
// under an item.
func Paragraphs(rows []string, from, to int, rules Rules) []Span {
	var out []Span
	start, text := -1, 0
	for i := from; i < to && i < len(rows); i++ {
		if Normalize(rows[i]) == "" {
			if start >= 0 {
				out = append(out, Span{start, i - 1})
				start = -1
			}
			continue
		}
		opens, at := opening(rows[i], rules)
		if start >= 0 && (opens || indent(rows[i]) < text) {
			out = append(out, Span{start, i - 1})
			start = -1
		}
		if start < 0 {
			start, text = i, at
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

// opening reports whether a row begins a paragraph of its own — a turn
// marker, a fence, or a list marker — and the column its text starts at, which
// the rows continuing that paragraph are indented to at least.
func opening(row string, rules Rules) (bool, int) {
	if rules.TurnMarker != "" && strings.HasPrefix(row, rules.TurnMarker) {
		rest := row[len(rules.TurnMarker):]
		return true, utf8.RuneCountInString(rules.TurnMarker) + indent(rest)
	}
	lead := indent(row)
	s := row[lead:]
	for _, f := range []string{"```", "~~~", rules.Fence} {
		if f != "" && strings.HasPrefix(s, f) {
			return true, lead
		}
	}
	if n, cols := listMarker(s); n > 0 {
		return true, lead + cols + indent(s[n:])
	}
	return false, lead
}

// listMarker returns the length in bytes and in columns of the list marker s
// begins with — a bullet, or a number and its dot or parenthesis — when a
// space follows it, and zeros otherwise.
func listMarker(s string) (int, int) {
	r, size := utf8.DecodeRuneInString(s)
	if size > 0 && strings.ContainsRune("-*+•◦▪‣", r) {
		if strings.HasPrefix(s[size:], " ") {
			return size, 1
		}
		return 0, 0
	}
	n := 0
	for n < len(s) && n < 9 && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n > 0 && n+1 < len(s) && (s[n] == '.' || s[n] == ')') && s[n+1] == ' ' {
		return n + 1, n + 1
	}
	return 0, 0
}

// indent is the number of spaces a row begins with.
func indent(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }
