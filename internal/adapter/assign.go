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
	regions := markerRegions(rows, rules)
	claimed := make([]int, len(t.Turns)) // region index per turn, -1 when none
	spansOf := make([][]align.Span, len(t.Turns))
	cursor := 0
	for ti, turn := range t.Turns {
		claimed[ti] = -1
		for r := cursor; r < len(regions); r++ {
			if s, ok := align.Turn(turn.Blocks, rows, regions[r].first, rules); ok {
				claimed[ti], spansOf[ti] = r, s
				cursor = r + 1
				break
			}
		}
	}
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
		for r := lo; r < hi; r++ {
			if !claimedRegion(claimed, r) {
				claimed[ti] = r
				break
			}
		}
	}
	out := make([]TurnAlignment, 0, len(t.Turns))
	for ti, turn := range t.Turns {
		ta := TurnAlignment{Turn: turn.Ordinal, Aligned: spansOf[ti] != nil}
		switch {
		case ta.Aligned:
			for i, b := range turn.Blocks {
				ta.Blocks = append(ta.Blocks, AlignedBlock{Block: b,
					Span: Span{First: spansOf[ti][i].First, Last: spansOf[ti][i].Last}})
			}
		case claimed[ti] >= 0:
			r := regions[claimed[ti]]
			ta.Blocks = Paragraphs(rows, r.first, r.last, rules)
		}
		out = append(out, ta)
	}
	return out
}

// Paragraphed treats every turn-marker region as a turn of paragraphs, which
// is what an adapter falls back to with no transcript at all.
func Paragraphed(rows []string, rules align.Rules) []TurnAlignment {
	var out []TurnAlignment
	for _, r := range markerRegions(rows, rules) {
		out = append(out, TurnAlignment{Turn: len(out) + 1, Aligned: false,
			Blocks: Paragraphs(rows, r.first, r.last, rules)})
	}
	return out
}

// Paragraphs splits a region into one block per rendered paragraph, with the
// turn marker trimmed off the first row.
func Paragraphs(rows []string, from, to int, rules align.Rules) []AlignedBlock {
	var out []AlignedBlock
	for _, sp := range align.Paragraphs(rows, from, to) {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rows[sp.First]), rules.TurnMarker))
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

// region is one turn-marker row and the rows up to the next marker or prompt.
type region struct{ first, last int }

func markerRegions(rows []string, rules align.Rules) []region {
	var out []region
	for i := 0; i < len(rows); i++ {
		if !strings.HasPrefix(rows[i], rules.TurnMarker) {
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
		if strings.HasPrefix(rows[i], rules.TurnMarker) {
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
