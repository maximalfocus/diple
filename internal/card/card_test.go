package card

import (
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/blocks"
)

func TestTrayOperations(t *testing.T) {
	tr := &Tray{}
	a := tr.Add(&Card{Kind: Anchored, Tag: "fix", Text: "a"})
	b := tr.Add(&Card{Kind: Anchored, Tag: "ask", Text: "b"})
	c := tr.Add(&Card{Kind: Anchored, Tag: "note", Text: "c"})
	if a.ID == "" || a.ID == b.ID || tr.Len() != 3 {
		t.Fatalf("ids %q %q len %d", a.ID, b.ID, tr.Len())
	}
	if !tr.Move(2, 0) || tr.Cards[0] != c || tr.Cards[1] != a {
		t.Fatalf("move: %v", tr.Cards)
	}
	if tr.Move(0, 5) || tr.Move(1, 1) {
		t.Fatal("invalid moves must fail")
	}
	if !tr.Delete(1) || tr.Len() != 2 || tr.Cards[1] != b {
		t.Fatalf("delete: %v", tr.Cards)
	}
	if tr.Delete(9) {
		t.Fatal("invalid delete must fail")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := &Store{Dir: t.TempDir() + "/trays"}
	tr := &Tray{}
	tr.Add(&Card{Kind: Anchored, Tag: "fix", Text: "note", Anchor: Anchor{Turn: 1, Block: 2, Kind: blocks.Paragraph, First: 10, Last: 11, Quote: "q", Span: &Span{Row: 10, Col: 2, EndRow: 10, EndCol: 5}}})
	tr.Add(&Card{Kind: Anchored, Tag: "note", Text: "two", Anchor: Anchor{Turn: 1, Block: 3, Kind: blocks.ListItem, Ordinal: 2}})
	if err := s.Save("claude", "sess/1", tr); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("claude", "sess/1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != 2 || got.Cards[0].Text != "note" || got.Cards[0].Anchor.Span == nil || got.Cards[0].Anchor.Span.EndCol != 5 || got.Cards[1].Anchor.Ordinal != 2 {
		t.Fatalf("loaded %+v", got.Cards)
	}
	added := got.Add(&Card{Kind: Anchored, Tag: "ask", Text: "three"})
	if added.ID == got.Cards[0].ID {
		t.Fatal("id collision after load")
	}
	// Empty tray removes the file; a missing file loads empty.
	if err := s.Save("claude", "sess/1", &Tray{}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Load("claude", "sess/1")
	if err != nil || got.Len() != 0 {
		t.Fatalf("after empty save: %+v %v", got, err)
	}
	if err := s.Save("claude", "", tr); err != nil {
		t.Fatal("saving without a session id must be a no-op")
	}
}

func TestQuoteAndTags(t *testing.T) {
	long := strings.Repeat("word ", 40)
	q := Quote(long)
	if r := []rune(q); len(r) != MaxQuote || !strings.HasSuffix(q, "…") {
		t.Fatalf("quote len %d %q", len(r), q)
	}
	if Quote("  a   b  ") != "a b" {
		t.Fatal("quote must collapse whitespace")
	}
	// The strip's own three letters, in strip order, mapped to indexed
	// colours 1–3.
	if TagByLetter('n') != "note" || TagByLetter('f') != "fix" || TagByLetter('a') != "ask" || TagByLetter('x') != "" {
		t.Fatal("tag by letter")
	}
	if Tag("note").Color() != 1 || Tag("fix").Color() != 2 || Tag("ask").Color() != 3 || Tag("nope").Color() != 0 {
		t.Fatal("tag colours")
	}
	if len(Tags) != 3 || DefaultTag != "note" {
		t.Fatalf("three tags, note by default: %v", Tags)
	}
}

func TestOverallCardIsSingleAndStaysLast(t *testing.T) {
	tr := &Tray{}
	tr.Add(&Card{Kind: Anchored, Tag: "fix", Text: "a"})
	o := tr.Add(&Card{Kind: Free, Text: "keep it small", Overall: true})
	q := tr.Add(&Card{Kind: Free, Text: "which ports?"})
	if tr.Len() != 3 || tr.Cards[1] != q || tr.Cards[2] != o {
		t.Fatalf("a later card must go before the overall: %v", texts(tr))
	}
	// A second overall updates the one the tray has.
	again := tr.Add(&Card{Kind: Free, Text: "and rebase", Overall: true})
	if again != o || tr.Len() != 3 || tr.Overall().Text != "and rebase" {
		t.Fatalf("second overall: len=%d overall=%+v", tr.Len(), tr.Overall())
	}
	// Nothing moves onto or past the last place.
	if tr.Move(2, 0) || tr.Move(0, 2) {
		t.Fatalf("the overall card holds the last place: %v", texts(tr))
	}
	if !tr.Move(1, 0) || tr.Cards[0] != q {
		t.Fatalf("ordinary moves still work: %v", texts(tr))
	}
}

func texts(t *Tray) []string {
	var out []string
	for _, c := range t.Cards {
		out = append(out, string(c.Kind)+":"+c.Text)
	}
	return out
}

func TestStashSetsATrayAsideAndUnstashGivesItBack(t *testing.T) {
	s := &Store{Dir: t.TempDir() + "/trays"}
	tr := &Tray{}
	tr.Add(&Card{Kind: Anchored, Tag: "fix", Text: "note", Anchor: Anchor{Turn: 2, Quote: "q"}})
	tr.Add(&Card{Kind: Free, Text: "rebase", Attachments: []Attachment{
		{Kind: PathAttachment, Spec: "src/auth.ts"},
		{Kind: CommandAttachment, Spec: "git diff --stat", Output: "one line", Status: 0},
	}})
	tr.Add(&Card{Kind: Free, Text: "keep it small", Overall: true})
	if err := s.Save("claude", "sess-1", tr); err != nil {
		t.Fatal(err)
	}
	n, err := s.Stash("claude")
	if err != nil || n != 3 {
		t.Fatalf("stash: n=%d err=%v", n, err)
	}
	empty, err := s.Load("claude", "sess-1")
	if err != nil || empty.Len() != 0 {
		t.Fatalf("the stashed session tray is empty: len=%d err=%v", empty.Len(), err)
	}
	if n, err := s.Unstash("claude"); err != nil || n != 3 {
		t.Fatalf("unstash: n=%d err=%v", n, err)
	}
	// The next session of that agent takes the tray over, whatever its id.
	got, err := s.Load("claude", "sess-2")
	if err != nil || got.Len() != 3 {
		t.Fatalf("restored: len=%d err=%v", got.Len(), err)
	}
	if got.Cards[0].Text != "note" || got.Cards[0].Anchor.Quote != "q" ||
		got.Cards[1].Kind != Free || len(got.Cards[1].Attachments) != 2 ||
		got.Cards[1].Attachments[1].Output != "one line" || got.Overall() != got.Cards[2] {
		t.Fatalf("restored tray differs: %+v", got.Cards)
	}
	// Once the session has saved it, the queue is spent.
	if err := s.Save("claude", "sess-2", got); err != nil {
		t.Fatal(err)
	}
	third, err := s.Load("claude", "sess-3")
	if err != nil || third.Len() != 0 {
		t.Fatalf("queue not cleared: len=%d err=%v", third.Len(), err)
	}
	if n, err := s.Stash("claude"); err != nil || n != 3 {
		t.Fatalf("restash: n=%d err=%v", n, err)
	}
}
