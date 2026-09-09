package card

import (
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/blocks"
)

func TestTrayOperations(t *testing.T) {
	tr := &Tray{}
	a := tr.Add(&Card{Kind: Note, Tag: "fix", Text: "a"})
	b := tr.Add(&Card{Kind: Note, Tag: "question", Text: "b"})
	c := tr.Add(&Card{Kind: Note, Tag: "prefer", Text: "c"})
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
	tr.Add(&Card{Kind: Note, Tag: "fix", Text: "note", Anchor: Anchor{Turn: 1, Block: 2, Kind: blocks.Paragraph, First: 10, Last: 11, Quote: "q", Span: &Span{Row: 10, Col: 2, EndRow: 10, EndCol: 5}}})
	tr.Add(&Card{Kind: Note, Tag: "prefer", Text: "two", Anchor: Anchor{Turn: 1, Block: 3, Kind: blocks.ListItem, Ordinal: 2}})
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
	added := got.Add(&Card{Kind: Note, Tag: "comment", Text: "three"})
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
	if TagByLetter('p') != "prefer" || TagByLetter('x') != "" {
		t.Fatal("tag by letter")
	}
	if Tag("fix").Color() != 1 || Tag("comment").Color() != 6 || Tag("nope").Color() != 0 {
		t.Fatal("tag colours")
	}
}
