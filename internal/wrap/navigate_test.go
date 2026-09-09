package wrap

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/adapter/claude"
	"github.com/maximalfocus/diple/internal/blocks"
)

// turnWords name one paragraph per assistant turn, so a search has exactly
// one match and a quotation is unambiguous.
var turnWords = []string{"alpha", "bravo", "charlie", "delta", "echo"}

func turnTranscript(n int) *adapter.Transcript {
	tr := &adapter.Transcript{Agent: "claude", Version: "2.1.266", SessionID: "s"}
	for i := 1; i <= n; i++ {
		tr.Turns = append(tr.Turns, adapter.Turn{Ordinal: i, ID: "t" + strconv.Itoa(i), Blocks: []blocks.Block{
			{Kind: blocks.Paragraph, Text: "turn " + turnWords[i-1] + " body", Parent: -1},
		}})
	}
	return tr
}

// turnsSession prints n assistant turns in the Claude Code style into a
// terminal short enough that the earlier ones end up in scrollback.
func turnsSession(t *testing.T, n int) (*Session, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	s, term, agent := newTestSession(60, 6)
	s.UseAdapter(&claude.Adapter{}, "claude")
	s.Source = staticSource{turnTranscript(n)}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		out := "⏺ turn " + turnWords[i-1] + " body\r\n\r\n  ran a tool\r\n"
		if err := s.HandleOutput([]byte(out)); err != nil {
			t.Fatal(err)
		}
	}
	term.Reset()
	agent.Reset()
	return s, term, agent
}

func turnFirstRow(t *testing.T, s *Session, turn int) int {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, al := s.alignmentLocked()
	first, _, ok, aligned := turnRows(al, turn)
	if !ok || !aligned {
		t.Fatalf("turn %d is not aligned", turn)
	}
	return first
}

func viewTop(s *Session) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.windowStart()
}

func TestTurnNavigationLandsOnTurnFirstRows(t *testing.T) {
	s, _, agent := turnsSession(t, 5)
	// A scrolled viewport is one of the three states where the navigation
	// keys are Diple's; before that they belong to the agent.
	if err := s.Scroll(1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		send(t, s, "[")
	}
	if got, want := viewTop(s), turnFirstRow(t, s, 1); got != want {
		t.Fatalf("after [ to the first turn, view top = %d, want turn 1 first row %d", got, want)
	}
	// `[` at the first turn leaves the view where it is.
	send(t, s, "[")
	if got, want := viewTop(s), turnFirstRow(t, s, 1); got != want {
		t.Fatalf("[ at the first turn moved the view to %d, want %d", got, want)
	}
	send(t, s, "]")
	if got, want := viewTop(s), turnFirstRow(t, s, 2); got != want {
		t.Fatalf("] from turn 1: view top = %d, want turn 2 first row %d", got, want)
	}
	send(t, s, "]")
	if got, want := viewTop(s), turnFirstRow(t, s, 3); got != want {
		t.Fatalf("] from turn 2: view top = %d, want turn 3 first row %d", got, want)
	}
	send(t, s, "[")
	if got, want := viewTop(s), turnFirstRow(t, s, 2); got != want {
		t.Fatalf("[ from turn 3: view top = %d, want turn 2 first row %d", got, want)
	}
	if agent.Len() != 0 {
		t.Fatalf("navigation keys reached the agent: %q", agent.Bytes())
	}
	// `]` past the last turn leaves the view where it is.
	for i := 0; i < 6; i++ {
		send(t, s, "]")
	}
	top := viewTop(s)
	send(t, s, "]")
	if viewTop(s) != top {
		t.Fatalf("] at the last turn moved the view from %d to %d", top, viewTop(s))
	}
}

func TestNoteOnEarlierTurnCompilesWithItsTurnReference(t *testing.T) {
	s, _, agent := turnsSession(t, 5)
	if err := s.Scroll(1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		send(t, s, "[")
	}
	send(t, s, "]") // turn 2 at the top of the view
	row := turnFirstRow(t, s, 2)
	if viewTop(s) != row {
		t.Fatalf("view top = %d, want turn 2 first row %d", viewTop(s), row)
	}
	metaClick(t, s, 5, yOf(s, row))
	if s.sel == nil || s.sel.turn != 2 {
		t.Fatalf("selection = %+v, want turn 2", s.sel)
	}
	send(t, s, "f")
	send(t, s, "tighten this\r")
	if s.Tray.Len() != 1 || s.Tray.Cards[0].Anchor.Turn != 2 {
		t.Fatalf("tray = %+v", s.Tray.Cards)
	}
	agent.Reset()
	send(t, s, "\x1b\r")
	want := "Review (1 item).\n\n1. [fix] In your reply 3 turns ago: > \"turn bravo body\"\n   tighten this"
	if got := agent.String(); got != pasteStart+want+pasteEnd+"\r" {
		t.Fatalf("fold:\n got %q\nwant %q", got, pasteStart+want+pasteEnd+"\r")
	}
}

func TestSearchMovesToMatchAndEscapeLeavesTheView(t *testing.T) {
	s, term, agent := turnsSession(t, 5)
	if err := s.Scroll(1); err != nil {
		t.Fatal(err)
	}
	send(t, s, "/")
	if s.search == nil {
		t.Fatal("/ did not open the search field")
	}
	send(t, s, "delta")
	lines, _, _, _ := physical(s)
	if !strings.Contains(strings.Join(texts(lines), "\n"), "/delta") {
		t.Fatalf("search field not drawn:\n%s", strings.Join(texts(lines), "\n"))
	}
	send(t, s, "\r")
	want := turnFirstRow(t, s, 4)
	if s.highlight == nil || s.highlight.first != want {
		t.Fatalf("highlight = %+v, want first row %d", s.highlight, want)
	}
	top := viewTop(s)
	if want < top || want >= top+s.rows-s.trayH {
		t.Fatalf("match row %d is not visible from view top %d", want, top)
	}
	// A query with no match reports it and moves nothing.
	send(t, s, "\x7f\x7f\x7f\x7f\x7fzeta")
	before := viewTop(s)
	send(t, s, "\r")
	if s.search == nil || !s.search.noMatch {
		t.Fatalf("search = %+v, want no match", s.search)
	}
	if viewTop(s) != before {
		t.Fatalf("a failed search moved the view from %d to %d", before, viewTop(s))
	}
	// Esc closes the field and leaves the view where it is.
	send(t, s, "\x1b")
	if s.search != nil {
		t.Fatal("esc did not close the search field")
	}
	if viewTop(s) != before {
		t.Fatalf("esc moved the view from %d to %d", before, viewTop(s))
	}
	if agent.Len() != 0 {
		t.Fatalf("search keys reached the agent: %q", agent.Bytes())
	}
	_ = term
}

func TestNavigationKeysReachTheAgentWhenDipleOwnsNothing(t *testing.T) {
	s, _, agent := turnsSession(t, 5)
	for _, in := range []string{"[", "]", "/help"} {
		agent.Reset()
		send(t, s, in)
		if agent.String() != in {
			t.Fatalf("agent got %q, want %q", agent.String(), in)
		}
		if s.search != nil {
			t.Fatalf("%q opened the search field with focus on the native box", in)
		}
	}
}

// altSession puts a session on the alternate screen with the agent tracking
// the mouse, showing exactly the rows given.
func altSession(t *testing.T, turns int, rows []string) (*Session, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	s, term, agent := newTestSession(60, 8)
	s.UseAdapter(&claude.Adapter{}, "claude")
	s.Source = staticSource{turnTranscript(turns)}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleOutput([]byte("\x1b[?1049h\x1b[?1002h\x1b[?1006h")); err != nil {
		t.Fatal(err)
	}
	paintAlt(t, s, rows)
	term.Reset()
	agent.Reset()
	return s, term, agent
}

func paintAlt(t *testing.T, s *Session, rows []string) {
	t.Helper()
	if err := s.HandleOutput([]byte("\x1b[H\x1b[2J" + strings.Join(rows, "\r\n"))); err != nil {
		t.Fatal(err)
	}
}

func turnRow(i int) string { return "⏺ turn " + turnWords[i-1] + " body" }

func TestFullscreenTurnJumpForwardsWheelUntilTheTurnIsVisible(t *testing.T) {
	s, _, agent := altSession(t, 3, []string{turnRow(1), "", "  ran a tool", "", "❯ "})
	if !s.Model.AltActive() {
		t.Fatal("not on the alternate screen")
	}
	// A selection is one of the states where the navigation keys are Diple's.
	metaClick(t, s, 5, yOf(s, 0))
	if s.sel == nil || s.sel.turn != 1 {
		t.Fatalf("selection = %+v, want turn 1", s.sel)
	}
	agent.Reset()
	send(t, s, "]")
	wheelDown := "\x1b[<65;1;1M"
	if agent.String() != wheelDown {
		t.Fatalf("agent got %q, want one wheel notch %q", agent.String(), wheelDown)
	}
	if s.nav == nil || s.nav.turn != 2 {
		t.Fatalf("nav = %+v, want a pending jump to turn 2", s.nav)
	}
	// A repaint that scrolled but has not reached the turn forwards another.
	agent.Reset()
	paintAlt(t, s, []string{"  ran a tool", "", "  more output", "", "❯ "})
	if agent.String() != wheelDown {
		t.Fatalf("after a scroll that missed the turn, agent got %q, want %q", agent.String(), wheelDown)
	}
	// The turn becomes visible: the jump lands and nothing more is forwarded.
	agent.Reset()
	paintAlt(t, s, []string{turnRow(2), "", "  ran a tool", "", "❯ "})
	if s.nav != nil {
		t.Fatalf("nav = %+v, want the jump landed", s.nav)
	}
	if agent.Len() != 0 {
		t.Fatalf("agent got %q after the turn became visible", agent.Bytes())
	}
}

func TestFullscreenJumpFailsOpenWithoutMouseTracking(t *testing.T) {
	s, _, agent := newTestSession(60, 8)
	s.UseAdapter(&claude.Adapter{}, "claude")
	s.Source = staticSource{turnTranscript(3)}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleOutput([]byte("\x1b[?1049h")); err != nil {
		t.Fatal(err)
	}
	paintAlt(t, s, []string{turnRow(1), "", "  ran a tool", "", "❯ "})
	agent.Reset()
	metaClick(t, s, 5, yOf(s, 0))
	agent.Reset()
	send(t, s, "]")
	if s.nav != nil {
		t.Fatalf("nav = %+v, want none without mouse tracking", s.nav)
	}
	if agent.Len() != 0 {
		t.Fatalf("agent got %q, want nothing forwarded", agent.Bytes())
	}
}

func TestFullscreenSearchHighlightsTheMatchWhenItComesIntoView(t *testing.T) {
	s, _, agent := altSession(t, 3, []string{turnRow(3), "", "  ran a tool", "", "❯ "})
	metaClick(t, s, 5, yOf(s, 0)) // a selection makes `/` Diple's
	if s.sel == nil {
		t.Fatal("no selection")
	}
	agent.Reset()
	send(t, s, "/")
	send(t, s, "alpha\r")
	if s.nav == nil || s.nav.turn != 1 || s.nav.block != 0 {
		t.Fatalf("nav = %+v, want a jump to turn 1 carrying its match", s.nav)
	}
	if want := "\x1b[<64;1;1M"; agent.String() != want {
		t.Fatalf("agent got %q, want one wheel notch up %q", agent.String(), want)
	}
	if s.highlight != nil {
		t.Fatalf("highlight = %+v before the turn is visible", s.highlight)
	}
	paintAlt(t, s, []string{turnRow(1), "", "  ran a tool", "", "❯ "})
	if s.nav != nil {
		t.Fatalf("nav = %+v, want the jump landed", s.nav)
	}
	if s.highlight == nil || s.highlight.first != 0 {
		t.Fatalf("highlight = %+v, want the match's rows", s.highlight)
	}
}
