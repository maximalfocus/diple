package fold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/card"
)

// TestCompileMatchesFormat pins the whole shape of §7: the numbering, the
// four anchor forms, an untagged free card, a fenced paste, and the overall
// card outside the numbering. Nothing announces the message.
func TestCompileMatchesFormat(t *testing.T) {
	cards := []*card.Card{
		{Kind: card.Anchored, Tag: "fix", Text: "The user's note.", Anchor: card.Anchor{Turn: 3, Kind: blocks.Paragraph, Quote: "quoted anchor text"}},
		{Kind: card.Anchored, Tag: "ask", Text: "The user's question.", Anchor: card.Anchor{Turn: 3, Kind: blocks.Paragraph, Quote: "quoted anchor text…"}},
		{Kind: card.Anchored, Tag: "note", Text: "The user's note.", Anchor: card.Anchor{Turn: 3, Kind: blocks.ListItem, Ordinal: 2, Quote: "…"}},
		{Kind: card.Anchored, Tag: "fix", Text: "The user's note.", Anchor: card.Anchor{Turn: 3, Kind: blocks.DiffLine, Path: "src/auth.ts", LineFirst: 42, LineLast: 47}},
		{Kind: card.Free, Text: "The user's free instruction or question.", Attachments: []card.Attachment{
			{Kind: card.PathAttachment, Spec: "src/auth.ts"},
		}},
		{Kind: card.Free, Text: "pasted text,\nmore than one line of it", Fenced: true},
		{Kind: card.Free, Text: "the overall card, if any.", Overall: true},
	}
	got := Compile(cards, 3)
	want := "1. [fix] > \"quoted anchor text\"\n" +
		"   The user's note.\n" +
		"2. [ask] > \"quoted anchor text…\"\n" +
		"   The user's question.\n" +
		"3. [note] Option 2 of the list starting \"…\".\n" +
		"   The user's note.\n" +
		"4. [fix] src/auth.ts:42-47\n" +
		"   The user's note.\n" +
		"5. The user's free instruction or question.\n" +
		"   attached: @src/auth.ts\n" +
		"6. ```\n" +
		"   pasted text,\n" +
		"   more than one line of it\n" +
		"   ```\n\n" +
		"Overall: the overall card, if any."
	if got != want {
		t.Fatalf("compiled:\n%s\n\nwant:\n%s", got, want)
	}
}

// TestCompileNothingAnnouncesTheMessage is the negative half of the format:
// no header of Diple's own ever precedes the first card.
func TestCompileNothingAnnouncesTheMessage(t *testing.T) {
	got := Compile([]*card.Card{
		{Kind: card.Free, Text: "one"},
		{Kind: card.Free, Text: "two"},
	}, 1)
	if strings.HasPrefix(got, "Review") || !strings.HasPrefix(got, "1. one") {
		t.Fatalf("compiled:\n%q", got)
	}
}

// TestCompileOneCardHasNoNumber: a numbered list of one item is a list only
// in form, so a single card compiles as itself.
func TestCompileOneCardHasNoNumber(t *testing.T) {
	got := Compile([]*card.Card{
		{Kind: card.Anchored, Tag: "fix", Text: "tighten this", Anchor: card.Anchor{Turn: 3, Quote: "That is the whole plan."}},
	}, 3)
	want := "[fix] > \"That is the whole plan.\"\ntighten this"
	if got != want {
		t.Fatalf("compiled:\n%q\nwant:\n%q", got, want)
	}
	free := Compile([]*card.Card{{Kind: card.Free, Text: "just this"}}, 1)
	if free != "just this" {
		t.Fatalf("one free card compiled to %q", free)
	}
}

// TestCompileOnlyAnOverallCard keeps the closing remark readable when it is
// the only thing in the tray.
func TestCompileOnlyAnOverallCard(t *testing.T) {
	got := Compile([]*card.Card{{Kind: card.Free, Text: "Keep the diff small.", Overall: true}}, 1)
	if got != "Overall: Keep the diff small." {
		t.Fatalf("compiled %q", got)
	}
}

func TestCompileEarlierTurnAndSingular(t *testing.T) {
	cards := []*card.Card{
		{Kind: card.Anchored, Tag: "ask", Anchor: card.Anchor{Turn: 1, Quote: "the API shape"}},
	}
	got := Compile(cards, 4)
	want := `[ask] In your reply 3 turns ago: > "the API shape"`
	if got != want {
		t.Fatalf("compiled:\n%q\nwant:\n%q", got, want)
	}
}

func TestCompileSingularTurnAgo(t *testing.T) {
	cards := []*card.Card{{Kind: card.Anchored, Tag: "fix", Anchor: card.Anchor{Turn: 2, Quote: "x"}}}
	got := Compile(cards, 3)
	if !strings.Contains(got, "In your reply 1 turn ago: ") {
		t.Fatalf("compiled: %q", got)
	}
}

// TestCompileOrdinalIsIndependentOfTheTag: the tag says what to do with the
// card and the anchor says what it points at, and the two are chosen
// independently.
func TestCompileOrdinalIsIndependentOfTheTag(t *testing.T) {
	for _, tag := range card.Tags {
		got := Compile([]*card.Card{
			{Kind: card.Anchored, Tag: tag, Anchor: card.Anchor{Turn: 1, Kind: blocks.ListItem, Ordinal: 2, Quote: "Change the handler"}},
		}, 1)
		if !strings.Contains(got, `Option 2 of the list starting "Change the handler".`) {
			t.Fatalf("tag %q compiled to %q", tag, got)
		}
	}
}

// TestCompileSingleLinePathAnchor names one line rather than a range.
func TestCompileSingleLinePathAnchor(t *testing.T) {
	got := Compile([]*card.Card{
		{Kind: card.Anchored, Tag: "fix", Anchor: card.Anchor{Turn: 1, Kind: blocks.DiffLine, Path: "src/auth.ts", LineFirst: 42, LineLast: 42}},
	}, 1)
	if got != "[fix] src/auth.ts:42" {
		t.Fatalf("compiled %q", got)
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

func TestCompileFreeCardAttachmentsAndOverall(t *testing.T) {
	cards := []*card.Card{
		{Kind: card.Anchored, Tag: "fix", Text: "tighten this", Anchor: card.Anchor{Turn: 2, Kind: blocks.Paragraph, Quote: "That is the whole plan."}},
		{Kind: card.Free, Text: "Rebase onto main first.", Attachments: []card.Attachment{
			{Kind: card.PathAttachment, Spec: "src/auth.ts"},
			{Kind: card.CommandAttachment, Spec: "git diff --stat", Output: " auth.ts | 3 +-\n 1 file changed"},
		}},
		{Kind: card.Free, Text: "Keep the diff small.", Overall: true},
	}
	got := Compile(cards, 2)
	want := "1. [fix] > \"That is the whole plan.\"\n" +
		"   tighten this\n" +
		"2. Rebase onto main first.\n" +
		"   attached: @src/auth.ts\n" +
		"   attached: git diff --stat\n" +
		"   ```\n" +
		"    auth.ts | 3 +-\n" +
		"    1 file changed\n" +
		"   ```\n\n" +
		"Overall: Keep the diff small."
	if got != want {
		t.Fatalf("compiled:\n%q\nwant:\n%q", got, want)
	}
}

func TestCompileFailedCaptureAndTruncation(t *testing.T) {
	cards := []*card.Card{
		{Kind: card.Free, Text: "look at this", Attachments: []card.Attachment{
			{Kind: card.CommandAttachment, Spec: "false", Output: "boom", Status: 1, Truncated: true},
		}},
	}
	got := Compile(cards, 1)
	if !strings.Contains(got, "attached: false (exit 1)") || !strings.Contains(got, "… output truncated") {
		t.Fatalf("compiled:\n%s", got)
	}
}
