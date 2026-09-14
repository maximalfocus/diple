package adapter

import (
	"strings"

	"github.com/maximalfocus/diple/internal/align"
	"github.com/maximalfocus/diple/internal/blocks"
)

// Assign maps a transcript's turns onto rendered rows. The rows may be the
// whole history or only the window a viewport shows, so turns are matched to
// the turn-marker regions the rows actually carry rather than assumed to
// start at the top: each turn takes, in order, the first region after the
// previous turn's where its blocks fit. A turn that fits nowhere but sits
// between two matched turns still gets that region as one paragraph per
// rendered paragraph, which is what a stale or corrupt transcript leaves; a
// turn the rows do not show gets no rows at all.
//
// Every adapter aligns this way — the differences between agents live in the
// decoration facts each one passes in.
func Assign(t *Transcript, rows []string, rules align.Rules) []TurnAlignment {
	regions := candidateRegions(rows, rules)
	claimed := make([]int, len(t.Turns)) // region index per turn, -1 when none
	spansOf := make([][]align.Span, len(t.Turns))
	cursor := 0
	for ti, turn := range t.Turns {
		claimed[ti] = -1
		for r := cursor; r < len(regions); r++ {
			s, ok := align.Turn(turn.Blocks, rows, regions[r].first, rules)
			if !ok {
				continue
			}
			claimed[ti], spansOf[ti] = r, s
			// The next turn starts after the rows this one occupies, not
			// merely after its opening region: an agent that draws every
			// paragraph as a candidate would otherwise let the next turn
			// match inside this one.
			cursor = nextRegion(regions, r, lastRow(s))
			break
		}
	}
	parts := make([]*partialMatch, len(t.Turns))
	for ti := range t.Turns {
		if claimed[ti] >= 0 {
			continue
		}
		lo, hi := 0, len(regions)
		for pi := ti - 1; pi >= 0; pi-- {
			if claimed[pi] >= 0 {
				lo = claimed[pi] + 1
				break
			}
		}
		for ni := ti + 1; ni < len(t.Turns); ni++ {
			if claimed[ni] >= 0 {
				hi = claimed[ni]
				break
			}
		}
		// A turn not all of whose blocks match may still match some. The
		// first unclaimed region in its place where any block does is its
		// own, and only the blocks that failed give up their rows. An agent
		// that marks nothing offers every paragraph as a region, so there
		// the turn's first block must be among those that match.
		end := len(rows)
		if hi < len(regions) {
			end = regions[hi].first
		}
		bs := t.Turns[ti].Blocks
		for r := lo; r < hi && claimed[ti] < 0; r++ {
			if claimedRegion(claimed, r) {
				continue
			}
			last := min(regions[r].last, end)
			s, m := align.Blocks(bs, rows, regions[r].first, last, rules)
			if anyMatched(bs, m) && (rules.TurnMarker != "" || firstMatched(bs, m)) {
				claimed[ti], parts[ti] = r, &partialMatch{spans: s, matched: m, last: last}
			}
		}
		for r := lo; r < hi && claimed[ti] < 0; r++ {
			if !claimedRegion(claimed, r) {
				claimed[ti] = r
			}
		}
	}
	out := make([]TurnAlignment, 0, len(t.Turns))
	for ti, turn := range t.Turns {
		if turn.Echo {
			continue // the user's own text holds its place and nothing more
		}
		ta := TurnAlignment{Turn: turn.Ordinal, Aligned: spansOf[ti] != nil || parts[ti] != nil}
		switch {
		case spansOf[ti] != nil:
			for i, b := range turn.Blocks {
				ta.Blocks = append(ta.Blocks, AlignedBlock{Block: b,
					Span: Span{First: spansOf[ti][i].First, Last: spansOf[ti][i].Last}})
			}
		case parts[ti] != nil:
			p := parts[ti]
			r := regions[claimed[ti]]
			ta.Blocks = partial(turn.Blocks, p.spans, p.matched, rows, r.first, p.last, rules)
		case claimed[ti] >= 0:
			r := regions[claimed[ti]]
			ta.Blocks = Paragraphs(rows, r.first, r.last, rules)
		}
		out = append(out, ta)
	}
	return out
}

// Paragraphed treats every turn-marker region as a turn of paragraphs, which
// is what an adapter falls back to with no transcript at all. An agent that
// does not mark its turns has no regions to divide, so its rows become one
// turn of paragraphs.
func Paragraphed(rows []string, rules align.Rules) []TurnAlignment {
	if rules.TurnMarker == "" {
		blocks := Paragraphs(rows, 0, len(rows), rules)
		if len(blocks) == 0 {
			return nil
		}
		return []TurnAlignment{{Turn: 1, Aligned: false, Blocks: blocks}}
	}
	var out []TurnAlignment
	for _, r := range markerRegions(rows, rules) {
		out = append(out, TurnAlignment{Turn: len(out) + 1, Aligned: false,
			Blocks: Paragraphs(rows, r.first, r.last, rules)})
	}
	return out
}

// candidateRegions is where a turn may begin. An agent that marks its turns
// gives one region per marker; an agent that draws them as plain rows — pi
// does — offers every paragraph start instead, and the turns still take them
// in order.
func candidateRegions(rows []string, rules align.Rules) []region {
	if rules.TurnMarker != "" {
		return markerRegions(rows, rules)
	}
	var out []region
	blank := true
	for i, r := range rows {
		if strings.TrimSpace(r) == "" {
			blank = true
			continue
		}
		if blank {
			out = append(out, region{first: i, last: len(rows)})
		}
		blank = false
	}
	return out
}

// Paragraphs splits a region into one block per rendered paragraph, with the
// turn marker trimmed off the first row.
func Paragraphs(rows []string, from, to int, rules align.Rules) []AlignedBlock {
	var out []AlignedBlock
	for _, sp := range align.Paragraphs(rows, from, to, rules) {
		first := strings.TrimSpace(rows[sp.First])
		text := strings.TrimSpace(strings.TrimPrefix(first, rules.Marker(first)))
		for i := sp.First + 1; i <= sp.Last; i++ {
			text += " " + strings.TrimSpace(rows[i])
		}
		out = append(out, AlignedBlock{
			Block: blocks.Block{Kind: blocks.Paragraph, Text: text, Parent: -1},
			Span:  Span{First: sp.First, Last: sp.Last},
		})
	}
	return out
}

// partialMatch is a turn only some of whose blocks matched, and the row its
// region ends before.
type partialMatch struct {
	spans   []align.Span
	matched []bool
	last    int
}

// partial lays out a turn only some of whose blocks matched: each matched
// block keeps its rows, and the rows of every run of unmatched blocks, between
// the matched blocks on either side of it or between one and the edge of the
// region, become paragraphs, in row order.
func partial(bs []blocks.Block, spans []align.Span, matched []bool, rows []string,
	from, to int, rules align.Rules) []AlignedBlock {
	var out []AlignedBlock
	at := make([]int, len(bs)) // where each block landed in out, -1 when it did not
	next, lost := from, false
	for i, b := range bs {
		at[i] = -1
		if !matched[i] {
			lost = lost || b.Kind != blocks.CodeBlock
			continue
		}
		if lost {
			out = append(out, Paragraphs(rows, next, spans[i].First, rules)...)
			lost = false
		}
		kept := b
		if b.Parent >= 0 {
			kept.Parent = at[b.Parent]
		}
		at[i] = len(out)
		span := Span{First: spans[i].First, Last: spans[i].Last}
		out = append(out, AlignedBlock{Block: kept, Span: span})
		if b.Kind != blocks.CodeBlock {
			next = spans[i].Last + 1 // a code block's lines follow it and move on
		}
	}
	if lost {
		out = append(out, Paragraphs(rows, next, to, rules)...)
	}
	return out
}

// anyMatched reports whether any block other than a code block's container
// matched.
func anyMatched(bs []blocks.Block, matched []bool) bool {
	for i, b := range bs {
		if matched[i] && b.Kind != blocks.CodeBlock {
			return true
		}
	}
	return false
}

// firstMatched reports whether the turn's first block matched.
func firstMatched(bs []blocks.Block, matched []bool) bool {
	for i, b := range bs {
		if b.Kind != blocks.CodeBlock {
			return matched[i]
		}
	}
	return false
}

// lastRow is the final row a match occupies.
func lastRow(spans []align.Span) int {
	last := -1
	for _, sp := range spans {
		if sp.Last > last {
			last = sp.Last
		}
	}
	return last
}

// nextRegion is the first region that begins after row end, or the one after
// r when the match occupies no rows at all.
func nextRegion(regions []region, r, end int) int {
	for i := r + 1; i < len(regions); i++ {
		if regions[i].first > end {
			return i
		}
	}
	return len(regions)
}

// region is one turn-marker row and the rows up to the next marker or prompt.
type region struct{ first, last int }

func markerRegions(rows []string, rules align.Rules) []region {
	var out []region
	for i := 0; i < len(rows); i++ {
		if rules.Marker(rows[i]) == "" {
			continue
		}
		to := regionEnd(rows, i+1, rules)
		out = append(out, region{first: i, last: to})
		i = to - 1
	}
	return out
}

func regionEnd(rows []string, from int, rules align.Rules) int {
	for i := from; i < len(rows); i++ {
		if rules.PromptMarker != "" && strings.HasPrefix(rows[i], rules.PromptMarker) {
			return i
		}
		if rules.Marker(rows[i]) != "" {
			return i
		}
	}
	return len(rows)
}

func claimedRegion(claimed []int, r int) bool {
	for _, c := range claimed {
		if c == r {
			return true
		}
	}
	return false
}
