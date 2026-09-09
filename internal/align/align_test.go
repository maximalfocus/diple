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
	got := Paragraphs(rows, 0, len(rows))
	if len(got) != 2 || got[0] != (Span{1, 2}) || got[1] != (Span{5, 5}) {
		t.Fatalf("paragraphs = %+v", got)
	}
}
