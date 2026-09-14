package align

import (
	"testing"

	"github.com/maximalfocus/diple/internal/blocks"
)

var rules = Rules{TurnMarker: "⏺", PromptMarker: "❯", ResultMarker: "⎿"}

func TestTurnAlignsWrappedAndDecoratedRows(t *testing.T) {
	bs := blocks.Parse("## Plan\n1. Read the config file and note the two ports\n2. Change\n   - keep\n```go\nfunc f() {\n\treturn\n}\n```\nDone.")
	rows := []string{
		"❯ Plan", // echoed prompt: must not be matched
		"",
		"⏺ Plan",
		"",
		"  1. Read the config file and note",
		"     the two ports",
		"  2. Change",
		"     - keep",
		"  func f() {",
		"      return",
		"  }",
		"  Done.",
		"",
		"❯ ",
	}
	spans, ok := Turn(bs, rows, 2, rules)
	if !ok {
		t.Fatalf("not aligned: %+v", spans)
	}
	want := []Span{{2, 2}, {4, 5}, {6, 6}, {7, 7}, {8, 10}, {8, 8}, {9, 9}, {10, 10}, {11, 11}}
	for i := range want {
		if spans[i] != want[i] {
			t.Fatalf("block %d (%s %q) span %+v want %+v", i, bs[i].Kind, bs[i].Text, spans[i], want[i])
		}
	}
}

func TestTurnFailsOnMismatch(t *testing.T) {
	bs := blocks.Parse("Hello world\n\nSecond")
	rows := []string{"⏺ Hello world", "", "  Something else"}
	if _, ok := Turn(bs, rows, 0, rules); ok {
		t.Fatal("mismatched text should not align")
	}
}

func TestTurnStopsAtPromptMarker(t *testing.T) {
	bs := blocks.Parse("Hello")
	rows := []string{"⏺ Hel", "❯ lo"}
	if _, ok := Turn(bs, rows, 0, rules); ok {
		t.Fatal("must not continue a block across the user's prompt")
	}
}

func TestToolCallResync(t *testing.T) {
	bs := []blocks.Block{
		{Kind: blocks.ToolCall, Text: "Bash(ls -la)", Parent: -1},
		{Kind: blocks.Paragraph, Text: "Two files.", Parent: -1},
	}
	rows := []string{"⏺ Bash(ls -la)", "  ⎿  a.go", "     b.go", "", "⏺ Two files."}
	spans, ok := Turn(bs, rows, 0, rules)
	if !ok || spans[0] != (Span{0, 0}) || spans[1] != (Span{4, 4}) {
		t.Fatalf("ok=%v spans=%+v", ok, spans)
	}
}

func TestBlankCodeLine(t *testing.T) {
	bs := blocks.Parse("```\na\n\nb\n```")
	rows := []string{"  a", "", "  b"}
	spans, ok := Turn(bs, rows, 0, rules)
	if !ok || spans[0] != (Span{0, 2}) || spans[2] != (Span{1, 1}) {
		t.Fatalf("ok=%v spans=%+v", ok, spans)
	}
}

func TestParagraphs(t *testing.T) {
	rows := []string{"", "a", "b", "", "", "c", ""}
	got := Paragraphs(rows, 0, len(rows), rules)
	if len(got) != 2 || got[0] != (Span{1, 2}) || got[1] != (Span{5, 5}) {
		t.Fatalf("paragraphs = %+v", got)
	}
}

func sameSpans(got, want []Span) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestParagraphsGiveEachTightListItemItsOwnBlock is the fallback over a tight
// list with no blank rows between its items, the way Claude Code draws one: a
// wrapped item keeps its continuation, and every item, nested ones included,
// is a paragraph of its own.
//
// Covers S-016 T-01.
func TestParagraphsGiveEachTightListItemItsOwnBlock(t *testing.T) {
	rows := []string{
		"  1. Read the config file and note the two ports that the service listens on for",
		"     HTTP and metrics",
		"  2. Change the handler",
		"     - keep the old route",
		"     - add a fallback that logs and returns 404",
		"       * a third level",
		"  3. Verify",
		"  • a bullet drawn as a dot",
		"  - a bullet drawn as a dash",
	}
	want := []Span{{0, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}, {6, 6}, {7, 7}, {8, 8}}
	if got := Paragraphs(rows, 0, len(rows), rules); !sameSpans(got, want) {
		t.Fatalf("paragraphs = %+v, want %+v", got, want)
	}
}

// TestParagraphsSplitWhereTheScreenStartsSomethingNew: a lead-in, a list item,
// code drawn under an item, a fence, a turn marker, and a row indented less
// than the paragraph's text each begin a fallback block of their own.
//
// Covers S-016 T-02.
func TestParagraphsSplitWhereTheScreenStartsSomethingNew(t *testing.T) {
	rows := []string{
		"⏺ Here is the plan:",                // 0 a turn marker, and the lead-in
		"  1. Read the config file and note", // 1 a list item
		"     the two ports",                 // 2 its continuation
		"  2. Change the handler",            // 3 a list item
		"  func handle() {",                  // 4 code under it, shallower than its text
		"      return",                       // 5
		"  ```go",                            // 6 a fence
		"  x := 1",                           // 7
		"⏺ Next turn",                        // 8 a turn marker
		"",                                   // 9
		"    deeper start",                   // 10
		"  shallower",                        // 11 indented less than row 10's text
	}
	want := []Span{{0, 0}, {1, 2}, {3, 3}, {4, 5}, {6, 7}, {8, 8}, {10, 10}, {11, 11}}
	if got := Paragraphs(rows, 0, len(rows), rules); !sameSpans(got, want) {
		t.Fatalf("paragraphs = %+v, want %+v", got, want)
	}
	// A wrapped plain paragraph stays one.
	plain := []string{"  A long sentence that the terminal", "  wrapped onto a second row."}
	if got := Paragraphs(plain, 0, len(plain), rules); !sameSpans(got, []Span{{0, 1}}) {
		t.Fatalf("wrapped paragraph = %+v", got)
	}
}

// TestBlocksLoseOnlyTheUnmatchedBlock: a block the screen draws differently
// from the transcript gives up its own rows, and the blocks before and after
// it keep theirs — wherever it falls in the turn.
//
// Covers S-016 T-04.
func TestBlocksLoseOnlyTheUnmatchedBlock(t *testing.T) {
	// A link renders as its text alone, so its paragraph cannot match.
	bs := blocks.Parse("Intro line.\n\nSee [the docs](https://example.com/a) for more.\n\nLast line.")
	rows := []string{"⏺ Intro line.", "", "  See the docs for more.", "", "  Last line.", "", "❯ "}
	spans, matched := Blocks(bs, rows, 0, len(rows), rules)
	if !matched[0] || matched[1] || !matched[2] ||
		spans[0] != (Span{0, 0}) || spans[2] != (Span{4, 4}) {
		t.Fatalf("matched=%v spans=%+v", matched, spans)
	}
	if spans[1] != (Span{-1, -1}) {
		t.Fatalf("an unmatched block has rows: %+v", spans[1])
	}
	// The first block, and then the last, fail in turn.
	for _, lost := range []int{0, 2} {
		bs := blocks.Parse("Intro line.\n\nMiddle line.\n\nLast line.")
		bs[lost].Text = "Something the screen never showed"
		rows := []string{"⏺ Intro line.", "", "  Middle line.", "", "  Last line."}
		_, matched := Blocks(bs, rows, 0, len(rows), rules)
		for i := range bs {
			if matched[i] == (i == lost) {
				t.Fatalf("lost %d: matched=%v", lost, matched)
			}
		}
	}
	// A code block keeps the lines that matched.
	bs = blocks.Parse("```go\nfunc f() {\n\treturn nil\n}\n```\n\nDone.")
	bs[2].Text = "\treturn errors.New(\"boom\")"
	rows = []string{"⏺ func f() {", "      return nil", "  }", "  Done."}
	spans, matched = Blocks(bs, rows, 0, len(rows), rules)
	done := len(bs) - 1
	if !matched[0] || !matched[1] || matched[2] || !matched[done] || spans[done] != (Span{3, 3}) {
		t.Fatalf("matched=%v spans=%+v", matched, spans)
	}
}

func TestAnotherTurnMarkerBeginsATurn(t *testing.T) {
	r := Rules{TurnMarker: "⏺", OtherTurnMarkers: []string{"●"}, PromptMarker: "❯"}
	for row, want := range map[string]string{"⏺ Plan": "⏺", "● Plan": "●", "  Plan": "", "❯ x": ""} {
		if got := r.Marker(row); got != want {
			t.Fatalf("Marker(%q) = %q, want %q", row, got, want)
		}
	}
	if (Rules{}).Marker("⏺ Plan") != "" {
		t.Fatal("a CLI that marks no turns has no marker")
	}
	rows := []string{"● 1. Read the config", "  2. Change the handler", "     - keep the old route"}
	want := []Span{{0, 0}, {1, 1}, {2, 2}}
	if got := Paragraphs(rows, 0, len(rows), r); !sameSpans(got, want) {
		t.Fatalf("paragraphs = %+v, want %+v", got, want)
	}
}

func TestBlocksStopAtThePrompt(t *testing.T) {
	bs := blocks.Parse("First.\n\nNever drawn.\n\nAfter the prompt.")
	rows := []string{"⏺ First.", "", "❯ After the prompt."}
	_, matched := Blocks(bs, rows, 0, len(rows), rules)
	if !matched[0] || matched[1] || matched[2] {
		t.Fatalf("matched=%v, must not match past the user's prompt", matched)
	}
}

func TestBlocksFindTheBlockAfterAToolCallsResults(t *testing.T) {
	bs := []blocks.Block{
		{Kind: blocks.ToolCall, Text: "Bash(ls -la)", Parent: -1},
		{Kind: blocks.Paragraph, Text: "Two files.", Parent: -1},
	}
	rows := []string{"⏺ Bash(ls -la)", "  ⎿  a.go", "     b.go", "", "⏺ Two files."}
	spans, matched := Blocks(bs, rows, 0, len(rows), rules)
	if !matched[0] || !matched[1] || spans[1] != (Span{4, 4}) {
		t.Fatalf("matched=%v spans=%+v", matched, spans)
	}
}
