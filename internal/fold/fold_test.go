package fold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/card"
)

func TestCompileMatchesFormat(t *testing.T) {
	cards := []*card.Card{
		{Kind: card.Note, Tag: "fix", Text: "tighten this", Anchor: card.Anchor{Turn: 3, Kind: blocks.Paragraph, Quote: "That is the whole plan."}},
		{Kind: card.Note, Tag: "prefer", Text: "pick this", Anchor: card.Anchor{Turn: 3, Kind: blocks.ListItem, Ordinal: 2, Quote: "Change the handler"}},
		{Kind: card.Note, Tag: "reject", Text: "wrong status", Anchor: card.Anchor{Turn: 3, Kind: blocks.CodeLine, Quote: "func handle() { w.WriteHeader(404) }"}},
	}
	got := Compile(cards, 3)
	want := `Review (3 items).

1. [fix] > "That is the whole plan."
   tighten this
2. [prefer] Option 2 of the list starting "Change the handler".
   pick this
3. [reject] > "func handle() { w.WriteHeader(404) }"
   wrong status`
	if got != want {
		t.Fatalf("compiled:\n%q\nwant:\n%q", got, want)
	}
}

func TestCompileEarlierTurnAndSingular(t *testing.T) {
	cards := []*card.Card{
		{Kind: card.Note, Tag: "question", Anchor: card.Anchor{Turn: 1, Quote: "the API shape"}},
	}
	got := Compile(cards, 4)
	want := `Review (1 item).

1. [question] In your reply 3 turns ago: > "the API shape"`
	if got != want {
		t.Fatalf("compiled:\n%q\nwant:\n%q", got, want)
	}
}

func TestCompileSingularTurnAgo(t *testing.T) {
	cards := []*card.Card{{Kind: card.Note, Tag: "fix", Anchor: card.Anchor{Turn: 2, Quote: "x"}}}
	got := Compile(cards, 3)
	if !strings.Contains(got, "In your reply 1 turn ago: ") {
		t.Fatalf("compiled: %q", got)
	}
}

func TestArchiveAppendsAndRefusesEscape(t *testing.T) {
	dir := t.TempDir()
	a, err := NewArchive(dir, ".diple-archive.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Append("first fold"); err != nil {
		t.Fatal(err)
	}
	if err := a.Append("second fold"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".diple-archive.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "first fold") || !strings.Contains(s, "second fold") || strings.HasPrefix(s, "\n") {
		t.Fatalf("archive = %q", s)
	}
	if strings.Count(s, "---") != 1 {
		t.Fatalf("expected one separator between two entries: %q", s)
	}
	if _, err := NewArchive(dir, "../escape.md"); err == nil {
		t.Fatal("archive path escaping the project must be refused")
	}
	if _, err := NewArchive(dir, "sub/ok.md"); err != nil {
		t.Fatalf("a subdirectory path is fine: %v", err)
	}
}

func TestCompileFreeCardsAttachmentsAndOverall(t *testing.T) {
	cards := []*card.Card{
		{Kind: card.Note, Tag: "fix", Text: "tighten this", Anchor: card.Anchor{Turn: 2, Kind: blocks.Paragraph, Quote: "That is the whole plan."}},
		{Kind: card.Question, Text: "Which ports does it listen on?"},
		{Kind: card.Instruction, Text: "Rebase onto main first.", Attachments: []card.Attachment{
			{Kind: card.PathAttachment, Spec: "src/auth.ts"},
			{Kind: card.CommandAttachment, Spec: "git diff --stat", Output: " auth.ts | 3 +-\n 1 file changed"},
		}},
		{Kind: card.Overall, Text: "Keep the diff small."},
	}
	got := Compile(cards, 2)
	want := "Review (3 items).\n" + `
1. [fix] > "That is the whole plan."
   tighten this
2. [question] Which ports does it listen on?
3. [instruction] Rebase onto main first.
   attached: @src/auth.ts
   attached: git diff --stat
   ` + "```" + `
    auth.ts | 3 +-
    1 file changed
   ` + "```" + `

Overall: Keep the diff small.`
	if got != want {
		t.Fatalf("compiled:\n%q\nwant:\n%q", got, want)
	}
}

func TestCompileFailedCaptureAndTruncation(t *testing.T) {
	cards := []*card.Card{
		{Kind: card.Instruction, Text: "look at this", Attachments: []card.Attachment{
			{Kind: card.CommandAttachment, Spec: "false", Output: "boom", Status: 1, Truncated: true},
		}},
	}
	got := Compile(cards, 1)
	if !strings.Contains(got, "attached: false (exit 1)") || !strings.Contains(got, "… output truncated") {
		t.Fatalf("compiled:\n%s", got)
	}
}
