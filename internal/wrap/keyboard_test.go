package wrap

import (
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/keys"
)

// selectBlockByText walks the keyboard selection to the block whose text
// starts with prefix, the way a user would with the block keys.
func selectBlockByText(t *testing.T, s *Session, prefix string) {
	t.Helper()
	send(t, s, "\x1bk") // select-block: the first block in view
	at := func() bool {
		return s.sel != nil && strings.HasPrefix(strings.TrimSpace(s.sel.text), prefix)
	}
	for _, key := range []string{"j", "k"} {
		for i := 0; i < 60; i++ {
			if at() {
				return
			}
			send(t, s, key)
		}
		if at() {
			return
		}
		send(t, s, "\x1bk")
	}
	got := ""
	if s.sel != nil {
		got = s.sel.text
	}
	t.Fatalf("no block starting %q reached by keyboard; stopped at %q", prefix, got)
}

// TestKeyboardReplayOfTheMouseJourney is R-013's acceptance: the R-005
// journey done with keys only, producing the same cards.
func TestKeyboardReplayOfTheMouseJourney(t *testing.T) {
	for _, mode := range []string{"inline", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			s, _, agent := fixtureSession(t, mode)

			// 1. The paragraph, tagged fix.
			selectBlockByText(t, s, "That is the whole plan.")
			send(t, s, "f")
			send(t, s, "tighten this\r")
			c := s.Tray.Cards[0]
			if c.Tag != "fix" || c.Text != "tighten this" || c.Anchor.Kind != blocks.Paragraph ||
				c.Anchor.Quote != "That is the whole plan." {
				t.Fatalf("paragraph card = %+v", c)
			}

			// 2. The second list item, tagged prefer, keeping its ordinal.
			selectBlockByText(t, s, "Change the handler")
			send(t, s, "p")
			send(t, s, "\r")
			c = s.Tray.Cards[1]
			if c.Tag != "prefer" || c.Anchor.Kind != blocks.ListItem || c.Anchor.Ordinal != 2 ||
				c.Anchor.Quote != "Change the handler" {
				t.Fatalf("prefer card = %+v", c)
			}

			// 3. A code-line range: select the code block, take its first
			// line, extend twice, tag reject.
			selectBlockByText(t, s, "func handle(")
			send(t, s, "L")
			if s.sel == nil || s.sel.lines == nil {
				t.Fatalf("L did not select a code line: %+v", s.sel)
			}
			first := s.sel.first
			send(t, s, "V")
			send(t, s, "V")
			send(t, s, "r")
			send(t, s, "wrong status\r")
			c = s.Tray.Cards[2]
			if c.Tag != "reject" || c.Anchor.Kind != blocks.CodeLine || c.Anchor.Lines == nil ||
				c.Anchor.Lines.Last-c.Anchor.Lines.First != 2 || c.Anchor.First != first ||
				!strings.HasPrefix(c.Anchor.Quote, "func handle(") || !strings.HasSuffix(c.Anchor.Quote, "}") {
				t.Fatalf("line-range card = %+v", c)
			}

			// 4. A span inside the first list item, by word, tagged question.
			selectBlockByText(t, s, "Read the config file")
			send(t, s, "v")
			send(t, s, "w")
			send(t, s, "w")
			if s.sel == nil || s.sel.span == nil || s.sel.text != "Read the config" {
				t.Fatalf("span selection = %+v text=%q", s.sel.span, s.sel.text)
			}
			// The same span the drag produced, column for column.
			if s.sel.span.Col != 5 || s.sel.span.EndCol != 19 {
				t.Fatalf("span columns = %d..%d, want 5..19", s.sel.span.Col, s.sel.span.EndCol)
			}
			send(t, s, "q")
			send(t, s, "which ports?\r")
			c = s.Tray.Cards[3]
			if c.Tag != "question" || c.Anchor.Span == nil || c.Anchor.Quote != "Read the config" {
				t.Fatalf("span card = %+v", c)
			}
			if agent.Len() != 0 {
				t.Fatalf("keyboard gestures reached the agent: %q", agent.Bytes())
			}
			if s.Tray.Len() != 4 {
				t.Fatalf("tray = %d cards", s.Tray.Len())
			}
		})
	}
}

func TestNativePromptTakesTheKeyboardBackAndGivesTheEditorBack(t *testing.T) {
	s, _, agent := fixtureSession(t, "inline")
	// A note is being written when the agent asks its own question.
	selectBlockByText(t, s, "That is the whole plan.")
	send(t, s, "f")
	send(t, s, "half a not")
	if s.editor == nil {
		t.Fatal("editor did not open")
	}
	showPrompt(t, s, true)
	if s.editor != nil || s.suspended == nil || !s.prompting || s.focus != focusAgent {
		t.Fatalf("editor=%v suspended=%v prompting=%v focus=%v", s.editor, s.suspended, s.prompting, s.focus)
	}
	// The next keypress answers the prompt instead of typing into the note.
	agent.Reset()
	send(t, s, "1")
	if agent.String() != "1" {
		t.Fatalf("agent got %q, want the answer to its prompt", agent.String())
	}
	// A slash command reaches the agent unchanged too.
	agent.Reset()
	send(t, s, "/help\r")
	if agent.String() != "/help\r" {
		t.Fatalf("agent got %q", agent.String())
	}
	// When the prompt clears the note is back, exactly as it was.
	showPrompt(t, s, false)
	if s.editor == nil || s.suspended != nil || s.prompting {
		t.Fatalf("editor=%v suspended=%v prompting=%v", s.editor, s.suspended, s.prompting)
	}
	if string(s.editor.text) != "half a not" || s.editor.tag != "fix" {
		t.Fatalf("restored editor = %+v", s.editor)
	}
	send(t, s, "e\r")
	if s.Tray.Len() != 1 || s.Tray.Cards[0].Text != "half a note" {
		t.Fatalf("card = %+v", s.Tray.Cards)
	}
}

// showPrompt makes the agent draw, or stop drawing, one of its own dialogs
// on the row above its input box.
func showPrompt(t *testing.T, s *Session, on bool) {
	t.Helper()
	row := "                              "
	if on {
		row = "Esc to cancel · Tab to amend"
	}
	if err := s.HandleOutput([]byte("\x1b7\x1b[1;1H" + row + "\x1b8")); err != nil {
		t.Fatal(err)
	}
}

func TestBindingsFileRebindsAndSurvivesBadLines(t *testing.T) {
	table := keys.Defaults()
	complaints := keys.Read(strings.NewReader(`
# comment
next-turn = n
send = alt+s
not-an-action = x
prev-turn = shift+meta+q
next-turn = m
`), table)
	if len(complaints) != 3 {
		t.Fatalf("complaints = %v", complaints)
	}
	if !table.Is(keys.NextTurn, keys.Key{Rune: 'n'}) || !table.Is(keys.Send, keys.Key{Rune: 's', Alt: true}) {
		t.Fatalf("table = %v", table)
	}
	if !table.Is(keys.PrevTurn, keys.Key{Rune: '['}) {
		t.Fatal("a bad key must leave the default in place")
	}
	// The session answers to the rebound key and no longer to the old one.
	s, _, agent := turnsSession(t, 5)
	s.Keys = table
	if err := s.Scroll(1); err != nil {
		t.Fatal(err)
	}
	agent.Reset()
	send(t, s, "]")
	if agent.String() != "]" {
		t.Fatalf("the old key must reach the agent, got %q", agent.String())
	}
	if err := s.Scroll(1); err != nil {
		t.Fatal(err)
	}
	before := viewTop(s)
	agent.Reset()
	send(t, s, "n")
	if viewTop(s) == before || agent.Len() != 0 {
		t.Fatalf("the rebound key did not move the view: view %d→%d agent=%q", before, viewTop(s), agent.String())
	}
}

func TestHideHotkeyGetsOutOfTheWayAndComesBack(t *testing.T) {
	s, term, agent := fixtureSession(t, "inline")
	selectBlockByText(t, s, "That is the whole plan.")
	send(t, s, "f")
	send(t, s, "tighten this\r")
	if s.Tray.Len() != 1 || !s.Composited() {
		t.Fatalf("tray=%d composited=%v", s.Tray.Len(), s.Composited())
	}
	rowsWithTray := s.AgentRows()

	// Alt+H hides the layer: the wrapped process gets its rows back and
	// Diple stops owning the screen.
	send(t, s, "\x1bh")
	if s.Composited() || s.AgentRows() != s.rows {
		t.Fatalf("hidden: composited=%v agentRows=%d rows=%d", s.Composited(), s.AgentRows(), s.rows)
	}
	if rowsWithTray >= s.rows {
		t.Fatalf("the tray never took a row: %d vs %d", rowsWithTray, s.rows)
	}
	// While hidden the agent's bytes go straight through, and Diple's own
	// gestures are inert.
	term.Reset()
	chunk := []byte("\x1b[1mhello\x1b[0m\r\n")
	if err := s.HandleOutput(chunk); err != nil {
		t.Fatal(err)
	}
	if got := term.Bytes(); string(got) != string(chunk) {
		t.Fatalf("hidden output = %q, want %q", got, chunk)
	}
	agent.Reset()
	send(t, s, "\x1bn") // the free-card chooser must not open
	send(t, s, "j")
	if s.chooser || s.sel != nil || s.Composited() {
		t.Fatalf("hidden layer answered a gesture: chooser=%v sel=%v", s.chooser, s.sel)
	}
	// Alt+H again brings the layer back with the tray intact.
	send(t, s, "\x1bh")
	if !s.Composited() || s.Tray.Len() != 1 || s.Tray.Cards[0].Text != "tighten this" {
		t.Fatalf("restored: composited=%v tray=%+v", s.Composited(), s.Tray.Cards)
	}
	if s.AgentRows() != rowsWithTray {
		t.Fatalf("agent rows = %d, want %d", s.AgentRows(), rowsWithTray)
	}
}
