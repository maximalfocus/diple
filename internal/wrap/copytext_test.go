package wrap

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/adapter/claude"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/clip"
	"github.com/maximalfocus/diple/internal/screen"
)

// copySession is a session with one assistant turn: md is its transcript, and
// rows are what Claude Code drew for it, below the user's prompt.
func copySession(t *testing.T, md string, rows ...string) (*Session, *bytes.Buffer) {
	t.Helper()
	term, agent := &bytes.Buffer{}, &bytes.Buffer{}
	s := NewSession(term, agent, 80, 24, nil)
	s.UseAdapter(&claude.Adapter{}, "claude")
	turn := adapter.Turn{Ordinal: 1, Blocks: blocks.Parse(md)}
	tr := &adapter.Transcript{Agent: "claude", Turns: []adapter.Turn{turn}}
	s.Source = staticSource{tr}
	s.Clip = &clip.Writer{OSC52Answered: true}
	s.CopyOnSelect = true
	at := time.Unix(1700000000, 0)
	s.SetClock(func() time.Time { return at })
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	out := "❯ show me\r\n\r\n" + strings.Join(rows, "\r\n") + "\r\n\r\n"
	if err := s.HandleOutput([]byte(out)); err != nil {
		t.Fatal(err)
	}
	term.Reset()
	return s, term
}

func output(t *testing.T, s *Session, text string) {
	t.Helper()
	if err := s.HandleOutput([]byte(text)); err != nil {
		t.Fatal(err)
	}
}

// dragCopy drags from column from of history row first to column to of row
// last, both columns 0-based, and returns what the drag copied.
func dragCopy(t *testing.T, s *Session, term *bytes.Buffer, first, from, last, to int) string {
	t.Helper()
	term.Reset()
	send(t, s, pressAt(from+1, yOf(s, first)))
	send(t, s, dragTo(to+1, yOf(s, last)))
	send(t, s, releaseAt(to+1, yOf(s, last)))
	return clipboard(t, term)
}

// pressesCopy presses n times at column col of a history row and returns what
// the last press copied.
func pressesCopy(t *testing.T, s *Session, term *bytes.Buffer, row, col, n int) string {
	t.Helper()
	term.Reset()
	y := yOf(s, row)
	for i := 0; i < n; i++ {
		send(t, s, pressAt(col+1, y)+releaseAt(col+1, y))
	}
	return clipboard(t, term)
}

// highlighted is the text of the cells drawn in reverse video on a history row.
func highlighted(s *Session, row int) string {
	lines, _, _, _ := physical(s)
	s.mu.Lock()
	pr := s.agentToPhysical(row - s.windowStart())
	s.mu.Unlock()
	var b strings.Builder
	for _, c := range lines[pr].Cells {
		if c.Width != 0 && c.Attr.Flags&screen.Reverse != 0 {
			b.WriteRune(c.Rune)
			b.WriteString(c.Extra)
		}
	}
	return strings.TrimSpace(b.String())
}

// TestACopyCarriesTheVisibleText: a list item with emphasis and inline code,
// and one with a link, copy what the screen shows — the marker included, and
// none of the Markdown the screen hides.
//
// Covers S-017 T-01.
func TestACopyCarriesTheVisibleText(t *testing.T) {
	md := "Here is the list:\n\n1. Use **bold** and `code` here\n" +
		"2. See [the docs](https://example.com/a) first"
	s, term := copySession(t, md,
		"⏺ Here is the list:",
		"",
		"  1. Use bold and code here",
		"  2. See the docs first")
	for _, c := range []struct{ sub, want string }{
		{"1. Use bold", "1. Use bold and code here"},
		{"2. See the docs", "2. See the docs first"},
	} {
		r := rowOf(t, s, c.sub)
		if got := dragCopy(t, s, term, r, 0, r, 79); got != c.want {
			t.Fatalf("a drag over %q copied %q, want %q", c.sub, got, c.want)
		}
		if got := pressesCopy(t, s, term, r, 8, 3); got != c.want {
			t.Fatalf("a triple-press on %q copied %q, want %q", c.sub, got, c.want)
		}
	}
}

// TestADragCopiesItsOwnHighlightAndNeverTheTurnMarker: a drag that starts
// before a visible list marker copies it and one that starts after does not,
// and the agent's turn marker is neither highlighted nor copied.
//
// Covers S-017 T-02.
func TestADragCopiesItsOwnHighlightAndNeverTheTurnMarker(t *testing.T) {
	s, term := copySession(t, "Here is the list:\n\n1. Read the config",
		"⏺ Here is the list:",
		"",
		"  1. Read the config")
	item := rowOf(t, s, "1. Read")
	if got := dragCopy(t, s, term, item, 0, item, 79); got != "1. Read the config" {
		t.Fatalf("a drag from before the list marker copied %q", got)
	}
	if got := dragCopy(t, s, term, item, 5, item, 79); got != "Read the config" {
		t.Fatalf("a drag from after the list marker copied %q", got)
	}
	head := rowOf(t, s, "Here is the list")
	if got := dragCopy(t, s, term, head, 0, head, 79); got != "Here is the list:" {
		t.Fatalf("a drag over the turn marker's row copied %q", got)
	}
	lines, _, _, _ := physical(s)
	s.mu.Lock()
	pr := s.agentToPhysical(head - s.windowStart())
	s.mu.Unlock()
	if lines[pr].Cells[0].Attr.Flags&screen.Reverse != 0 {
		t.Fatal("the turn marker is highlighted")
	}
	if lines[pr].Cells[2].Attr.Flags&screen.Reverse == 0 {
		t.Fatal("the text beside the turn marker is not highlighted")
	}
	both := "Here is the list:\n\n1. Read the config"
	if got := dragCopy(t, s, term, head, 0, item, 79); got != both {
		t.Fatalf("a drag across both rows copied %q", got)
	}
}

// TestWideAndCombinedCharactersCopyOnceInPlace: CJK, a combining accent and a
// ZWJ emoji, before and inside a selection, each copy once and in place,
// whichever half of a wide character the selection begins on.
//
// Covers S-017 T-03.
func TestWideAndCombinedCharactersCopyOnceInPlace(t *testing.T) {
	s, term := copySession(t, "Done.", "⏺ Done.")
	// Columns: 漢 2-3, 字 4-5, café 7-10 with the accent drawn onto column
	// 10, the ZWJ emoji 12-15, end 17-19.
	output(t, s, "  漢字 café 👩‍💻 end\r\n")
	r := rowOf(t, s, "end")
	for _, c := range []struct {
		from, to int
		want     string
	}{
		{2, 5, "漢字"},
		{3, 5, "漢字"},
		{7, 10, "café"},
		{12, 15, "👩‍💻"},
		{7, 19, "café 👩‍💻 end"},
		{17, 19, "end"},
	} {
		if got := dragCopy(t, s, term, r, c.from, r, c.to); got != c.want {
			t.Fatalf("columns %d-%d copied %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

// TestRowsJoinWhereALogicalLineWrapped: an agent wrap inside an aligned block
// joins with one space and drops the renderer's continuation indent, a hard
// break the transcript made stays a line break, a terminal soft wrap joins
// with its literal spaces, and unaligned output copies the screen's rows.
//
// Covers S-017 T-04.
func TestRowsJoinWhereALogicalLineWrapped(t *testing.T) {
	long := "Read the config file and note the two ports that the service listens on for " +
		"HTTP and metrics"
	s, term := copySession(t, "Plan\n\n1. "+long+"\n\nFirst line  \nsecond line",
		"⏺ Plan",
		"",
		"  1. Read the config file and note the two ports that the service listens on for",
		"     HTTP and metrics",
		"",
		"  First line",
		"  second line")
	item := rowOf(t, s, "1. Read")
	if got := dragCopy(t, s, term, item, 0, item+1, 79); got != "1. "+long {
		t.Fatalf("an agent wrap copied %q", got)
	}
	first := rowOf(t, s, "First line")
	if got := dragCopy(t, s, term, first, 0, first+1, 79); got != "First line\nsecond line" {
		t.Fatalf("a hard break copied %q", got)
	}
	cmd := "echo " + strings.Repeat("a  b ", 20)
	output(t, s, cmd+"\r\n")
	w := rowOf(t, s, "echo ")
	if got, want := dragCopy(t, s, term, w, 0, w+1, 79), strings.TrimRight(cmd, " "); got != want {
		t.Fatalf("a soft wrap copied\n %q\nwant\n %q", got, want)
	}
	// A soft wrap inside an aligned block: the continuation keeps every cell,
	// and the renderer's indent before the first row still drops.
	code := strings.TrimRight("echo "+strings.Repeat("a  b ", 20), " ")
	c, cterm := copySession(t, "Run:\n\n```sh\n"+code+"\n```", "⏺ Run:", "", "  "+code)
	cr := rowOf(t, c, "echo ")
	if got := dragCopy(t, c, cterm, cr, 0, cr+1, 79); got != code {
		t.Fatalf("a soft wrap in an aligned block copied\n %q\nwant\n %q", got, code)
	}
	output(t, s, "  ran a tool\r\n  and printed this\r\n")
	tool := rowOf(t, s, "ran a tool")
	if got := dragCopy(t, s, term, tool, 0, tool+1, 79); got != "  ran a tool\n  and printed this" {
		t.Fatalf("unaligned output copied %q", got)
	}
}

// TestEveryCopyActionCopiesWhatItHighlights: a double-press, a triple-press,
// the strip's copy and c each copy what they highlight.
//
// Covers S-017 T-05.
func TestEveryCopyActionCopiesWhatItHighlights(t *testing.T) {
	md := "Here is the list:\n\n1. Use **bold** and `code` here\n2. Second item"
	rows := []string{"⏺ Here is the list:", "", "  1. Use bold and code here", "  2. Second item"}
	const want = "1. Use bold and code here"

	s, term := copySession(t, md, rows...)
	item := rowOf(t, s, "1. Use bold")
	col := strings.Index(s.HistoryRows()[item], "bold") + 1
	if got := pressesCopy(t, s, term, item, col, 2); got != "bold" {
		t.Fatalf("a double-press copied %q", got)
	}
	if got := highlighted(s, item); got != "bold" {
		t.Fatalf("a double-press highlighted %q", got)
	}
	if got := pressesCopy(t, s, term, item, col+8, 3); got != want {
		t.Fatalf("a triple-press copied %q", got)
	}
	if got := highlighted(s, item); got != want {
		t.Fatalf("a triple-press highlighted %q", got)
	}

	s, term = copySession(t, md, rows...)
	dwellOn(t, s, 6, yOf(s, item))
	if got := highlighted(s, item); got != want {
		t.Fatalf("the raise highlighted %q", got)
	}
	sr := stripRowFor(t, s)
	term.Reset()
	pressRaised(t, s, choiceFor(t, s, "copy")+1, sr)
	if got := clipboard(t, term); got != want {
		t.Fatalf("the strip's copy copied %q", got)
	}

	s, term = copySession(t, md, rows...)
	dwellOn(t, s, 6, yOf(s, item))
	term.Reset()
	send(t, s, "c")
	if got := clipboard(t, term); got != want {
		t.Fatalf("c copied %q", got)
	}
}

// TestASelectionKeepsItsTextAsOutputAndTheTrayMove: output that arrives while
// the button is down, and a tray that grows before it comes up, leave the
// selection's text the text first selected.
//
// Covers S-017 T-06.
func TestASelectionKeepsItsTextAsOutputAndTheTrayMove(t *testing.T) {
	s, term := copySession(t, "Here is the list:\n\n1. Read the config",
		"⏺ Here is the list:",
		"",
		"  1. Read the config")
	item := rowOf(t, s, "1. Read")
	send(t, s, pressAt(1, yOf(s, item)))
	send(t, s, dragTo(80, yOf(s, item)))
	for i := 0; i < 5; i++ {
		output(t, s, "more output\r\n")
	}
	s.Tray.Add(&card.Card{Kind: card.Free, Text: "a card that grows the tray"})
	s.mu.Lock()
	err := s.syncLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	term.Reset()
	send(t, s, releaseAt(80, yOf(s, item)))
	if got := clipboard(t, term); got != "1. Read the config" {
		t.Fatalf("the selection copied %q after output and a taller tray", got)
	}
}
