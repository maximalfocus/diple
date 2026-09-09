package blocks

import (
	"strings"
	"testing"
)

const sample = `## Plan
1. Read the config file and note the two ports
2. Change the handler
   - keep the old route
   - add a fallback that logs and returns 404
3. Verify
` + "```go\nfunc handle() {\n\treturn\n}\n```\n```diff\n-\treturn nil\n+\treturn err\n```\n" + `That is the whole plan.

Second paragraph
continues here.

| a | b |
|---|---|
| 1 | 2 |
`

func kinds(bs []Block) string {
	var k []string
	for _, b := range bs {
		k = append(k, string(b.Kind))
	}
	return strings.Join(k, " ")
}

func TestParseSample(t *testing.T) {
	bs := Parse(sample)
	want := "heading list-item list-item list-item list-item list-item code-block code-line code-line code-line code-block diff-line diff-line paragraph paragraph table-row table-row"
	if got := kinds(bs); got != want {
		t.Fatalf("kinds:\n got  %s\n want %s", got, want)
	}
	if bs[0].Text != "Plan" {
		t.Fatalf("heading text %q", bs[0].Text)
	}
	if bs[1].Ordinal != 1 || bs[2].Ordinal != 2 || bs[5].Ordinal != 3 {
		t.Fatalf("ordinals %d %d %d", bs[1].Ordinal, bs[2].Ordinal, bs[5].Ordinal)
	}
	if !bs[1].Ordered || bs[3].Ordered {
		t.Fatalf("ordered flags %v %v", bs[1].Ordered, bs[3].Ordered)
	}
	if bs[3].Depth != 1 || bs[3].Ordinal != 1 || bs[4].Ordinal != 2 || bs[3].Text != "keep the old route" {
		t.Fatalf("nested item %+v %+v", bs[3], bs[4])
	}
	if bs[6].Lang != "go" || bs[7].Parent != 6 || bs[8].Text != "\treturn" {
		t.Fatalf("code block %+v %+v %+v", bs[6], bs[7], bs[8])
	}
	if bs[10].Lang != "diff" || bs[11].Kind != DiffLine || bs[11].Parent != 10 || bs[12].Text != "+\treturn err" {
		t.Fatalf("diff block %+v %+v", bs[10], bs[12])
	}
	if bs[13].Text != "That is the whole plan." || bs[14].Text != "Second paragraph continues here." {
		t.Fatalf("paragraphs %q %q", bs[13].Text, bs[14].Text)
	}
	if bs[15].Text != "| a | b |" || bs[16].Text != "| 1 | 2 |" {
		t.Fatalf("table rows %q %q", bs[15].Text, bs[16].Text)
	}
}

func TestListContinuationAndReset(t *testing.T) {
	bs := Parse("- one\n  more of one\n- two\n\ntext\n\n1. a\n1. b\n")
	if len(bs) != 5 {
		t.Fatalf("blocks: %s", kinds(bs))
	}
	if bs[0].Text != "one more of one" || bs[1].Ordinal != 2 {
		t.Fatalf("items %+v %+v", bs[0], bs[1])
	}
	if bs[3].Ordinal != 1 || bs[4].Ordinal != 2 {
		t.Fatalf("ordinals restart after a paragraph: %d %d", bs[3].Ordinal, bs[4].Ordinal)
	}
}

func TestUnterminatedFenceAndRule(t *testing.T) {
	bs := Parse("---\ntext\n```\ncode\n")
	if len(bs) != 3 || bs[0].Kind != Paragraph || bs[1].Kind != CodeBlock || bs[2].Text != "code" {
		t.Fatalf("blocks: %+v", bs)
	}
}

func TestEmpty(t *testing.T) {
	if bs := Parse(""); len(bs) != 0 {
		t.Fatalf("blocks: %+v", bs)
	}
}
