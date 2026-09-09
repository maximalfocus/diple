package wrap

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/adapter/claude"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/record"
	"github.com/maximalfocus/diple/internal/screen"
)

type staticSource struct{ tr *adapter.Transcript }

func (s staticSource) Transcript() (*adapter.Transcript, error) { return s.tr, nil }

const fixtureDir = "../adapter/claude/testdata/2.1.266"

// fixtureSession replays a recorded Claude Code session through a Session
// with the Claude adapter and the matching transcript.
func fixtureSession(t *testing.T, mode string) (*Session, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	rec, err := record.Open(filepath.Join(fixtureDir, mode+".recording.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(fixtureDir, mode+".transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	a := &claude.Adapter{}
	tr, err := a.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	term := &bytes.Buffer{}
	agent := &bytes.Buffer{}
	s := NewSession(term, agent, rec.Header.Cols, rec.Header.Rows, nil)
	s.UseAdapter(a, "claude")
	s.Source = staticSource{tr}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	for _, ev := range rec.Events {
		switch ev.Kind {
		case record.KindOutput:
			if err := s.HandleOutput(ev.Data); err != nil {
				t.Fatal(err)
			}
		case record.KindResize:
			_ = s.Resize(ev.Cols, ev.Rows)
		}
	}
	term.Reset()
	agent.Reset()
	return s, term, agent
}

// rowOf finds the history row of the assistant turn whose text contains
// sub, searching from the last turn marker so the echoed prompt above it
// is never matched.
func rowOf(t *testing.T, s *Session, sub string) int {
	t.Helper()
	rows := s.HistoryRows()
	start := 0
	for i, r := range rows {
		if strings.HasPrefix(r, "⏺") {
			start = i
		}
	}
	for i := start; i < len(rows); i++ {
		if strings.Contains(rows[i], sub) {
			return i
		}
	}
	t.Fatalf("no row contains %q", sub)
	return -1
}

// yOf returns the 1-based physical mouse row for a history row.
func yOf(s *Session, hist int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentToPhysical(hist-s.windowStart()) + 1
}

func metaClick(t *testing.T, s *Session, x, y int) {
	t.Helper()
	send(t, s, "\x1b[<8;"+itoa(x)+";"+itoa(y)+"M\x1b[<8;"+itoa(x)+";"+itoa(y)+"m")
}

func send(t *testing.T, s *Session, in string) {
	t.Helper()
	if err := s.HandleInput([]byte(in)); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func physical(s *Session) ([]screen.Line, int, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.physicalLocked()
}

func TestAnnotateFixtureProducesExpectedCards(t *testing.T) {
	for _, mode := range []string{"inline", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			s, term, agent := fixtureSession(t, mode)

			// 1. Modifier-click on the paragraph, then fix + note.
			para := rowOf(t, s, "That is the whole plan.")
			metaClick(t, s, 5, yOf(s, para))
			if s.sel == nil || s.sel.kind != blocks.Paragraph || s.sel.first != para {
				t.Fatalf("selection = %+v", s.sel)
			}
			lines, _, _, _ := physical(s)
			if !s.Composited() || term.Len() == 0 || !strings.Contains(strings.Join(texts(lines), "\n"), "fix  question  reject  approve  prefer  comment") {
				t.Fatalf("toolbar not drawn: composited=%v", s.Composited())
			}
			send(t, s, "f")
			if s.editor == nil || s.editor.tag != "fix" {
				t.Fatalf("editor = %+v", s.editor)
			}
			send(t, s, "tighten this")
			if agent.Len() != 0 {
				t.Fatalf("editor keys reached the agent: %q", agent.Bytes())
			}
			send(t, s, "\r")
			if s.Tray.Len() != 1 {
				t.Fatalf("tray = %d cards", s.Tray.Len())
			}
			c := s.Tray.Cards[0]
			if c.Tag != "fix" || c.Text != "tighten this" || c.Anchor.Kind != blocks.Paragraph ||
				c.Anchor.First != para || c.Anchor.Last != para || c.Anchor.Quote != "That is the whole plan." {
				t.Fatalf("card = %+v", c)
			}

			// 2. Modifier-click on the second list item, then prefer.
			item := rowOf(t, s, "2. Change the handler")
			metaClick(t, s, 5, yOf(s, item))
			send(t, s, "p")
			send(t, s, "\r")
			c = s.Tray.Cards[1]
			if c.Tag != "prefer" || c.Anchor.Kind != blocks.ListItem || c.Anchor.Ordinal != 2 || c.Anchor.Quote != "Change the handler" {
				t.Fatalf("prefer card = %+v", c)
			}

			// 3. Gutter click on a code line, shift-click extends, then reject.
			line1 := rowOf(t, s, "func handle(")
			line3 := rowOf(t, s, "  }")
			send(t, s, "\x1b[<0;1;"+itoa(yOf(s, line1))+"M\x1b[<0;1;"+itoa(yOf(s, line1))+"m")
			if s.sel == nil || s.sel.lines == nil || s.sel.first != line1 || s.sel.last != line1 {
				t.Fatalf("line selection = %+v", s.sel)
			}
			send(t, s, "\x1b[<4;1;"+itoa(yOf(s, line3))+"M\x1b[<4;1;"+itoa(yOf(s, line3))+"m")
			if s.sel == nil || s.sel.lines == nil || s.sel.first != line1 || s.sel.last != line3 {
				t.Fatalf("extended line selection = %+v", s.sel)
			}
			send(t, s, "r")
			send(t, s, "wrong status\r")
			c = s.Tray.Cards[2]
			if c.Tag != "reject" || c.Anchor.Kind != blocks.CodeLine || c.Anchor.Lines == nil ||
				c.Anchor.Lines.Last-c.Anchor.Lines.First != 2 || c.Anchor.First != line1 || c.Anchor.Last != line3 ||
				!strings.HasPrefix(c.Anchor.Quote, "func handle(") || !strings.HasSuffix(c.Anchor.Quote, "}") {
				t.Fatalf("line-range card = %+v", c)
			}

			// 4. Modifier-drag a span inside the first list item, then question.
			first := rowOf(t, s, "1. Read the config file")
			y := itoa(yOf(s, first))
			send(t, s, "\x1b[<8;6;"+y+"M")
			send(t, s, "\x1b[<40;20;"+y+"M")
			send(t, s, "\x1b[<8;20;"+y+"m")
			if s.sel == nil || s.sel.span == nil || s.sel.text != "Read the config" {
				t.Fatalf("span selection = %+v", s.sel)
			}
			send(t, s, "q")
			send(t, s, "which ports?\r")
			c = s.Tray.Cards[3]
			if c.Tag != "question" || c.Anchor.Span == nil || c.Anchor.Quote != "Read the config" || c.Anchor.Span.Col != 5 || c.Anchor.Span.EndCol != 19 {
				t.Fatalf("span card = %+v", c)
			}

			// The tray sits directly above the input box, framed by rules.
			lines, _, _, _ = physical(s)
			if len(lines) != s.rows {
				t.Fatalf("physical rows = %d, want %d", len(lines), s.rows)
			}
			div := -1
			for i, l := range lines {
				if strings.Contains(l.String(), "› 4 cards") {
					div = i
				}
			}
			if div < 0 {
				t.Fatalf("no tray divider in %q", texts(lines))
			}
			for i := 1; i <= 4; i++ {
				if !strings.Contains(lines[div+i].String(), "["+string(s.Tray.Cards[i-1].Tag)+"]") {
					t.Fatalf("card row %d = %q", i, lines[div+i].String())
				}
			}
			if s.AgentRows() != s.rows-5 {
				t.Fatalf("agent rows = %d, want %d", s.AgentRows(), s.rows-5)
			}
			if mode == "inline" {
				// Inline, shrinking the agent's rows moves its top rows into
				// scrollback, so the recorded input box stays visible and the
				// tray must sit directly above it.
				if !strings.HasPrefix(lines[div+5].String(), "─") || !strings.HasPrefix(lines[div+6].String(), "❯") {
					t.Fatalf("input box not directly below the tray:\n%s", strings.Join(texts(lines[div:]), "\n"))
				}
			} else {
				// On the alternate screen a recording cannot redraw for the
				// smaller size the way the live agent does, so the box is
				// gone and the tray sits at the bottom of the agent region.
				if div+5 != s.rows {
					t.Fatalf("tray not at the bottom: divider %d rows %d", div, s.rows)
				}
			}
		})
	}
}

func texts(lines []screen.Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.String()
	}
	return out
}

func TestEscRestoresOverlayAndSelection(t *testing.T) {
	s, term, agent := fixtureSession(t, "inline")
	para := rowOf(t, s, "That is the whole plan.")
	before, _, _, _ := physical(s)
	metaClick(t, s, 5, yOf(s, para))
	send(t, s, "c")
	if s.editor == nil {
		t.Fatal("editor did not open")
	}
	term.Reset()
	send(t, s, "\x1b") // Esc discards
	if s.editor != nil || s.sel != nil || s.Composited() {
		t.Fatalf("editor=%v sel=%v composited=%v", s.editor, s.sel, s.Composited())
	}
	// Leaving composited repaints the live screen from the model: rows equal
	// what they were before anything was drawn, attribute for attribute.
	replay := screen.New(s.cols, s.rows)
	_, _ = replay.Write(term.Bytes())
	for i, want := range before {
		got := replay.Row(i)
		got.Wrapped = want.Wrapped
		if !got.Equal(want) {
			t.Fatalf("row %d after Esc = %q, want %q", i, got.String(), want.String())
		}
	}
	if agent.Len() != 0 {
		t.Fatalf("keys reached the agent: %q", agent.Bytes())
	}
	// Esc with only a selection also clears it; other keys forward.
	metaClick(t, s, 5, yOf(s, para))
	send(t, s, "x")
	if s.sel != nil || agent.String() != "x" {
		t.Fatalf("sel=%v agent=%q", s.sel, agent.String())
	}
}

func TestTrayFocusEditReorderDeleteAndPersistence(t *testing.T) {
	store := &card.Store{Dir: filepath.Join(t.TempDir(), "trays")}
	s, term, agent := fixtureSession(t, "inline")
	s.UseStore(store)
	if err := s.SetSessionID("sess-1"); err != nil {
		t.Fatal(err)
	}
	for i, sub := range []string{"That is the whole plan.", "2. Change the handler", "3. Verify"} {
		metaClick(t, s, 5, yOf(s, rowOf(t, s, sub)))
		send(t, s, string(rune("fqa"[i])))
		send(t, s, "note "+itoa(i+1)+"\r")
	}
	if s.Tray.Len() != 3 {
		t.Fatalf("tray = %d", s.Tray.Len())
	}

	// Tab focuses the tray; j/k move; J/K reorder; e edits; d deletes.
	send(t, s, "\t")
	if s.focus != focusTray || agent.Len() != 0 {
		t.Fatalf("focus=%v agent=%q", s.focus, agent.Bytes())
	}
	send(t, s, "j")
	send(t, s, "J")
	if s.Tray.Cards[2].Text != "note 2" || s.traySel != 2 {
		t.Fatalf("reorder: %v sel %d", cardTexts(s.Tray), s.traySel)
	}
	send(t, s, "K")
	if s.Tray.Cards[1].Text != "note 2" {
		t.Fatalf("reorder back: %v", cardTexts(s.Tray))
	}
	send(t, s, "e")
	if s.editor == nil || s.editor.editing == nil || string(s.editor.text) != "note 2" {
		t.Fatalf("edit: %+v", s.editor)
	}
	send(t, s, " more\r")
	if s.Tray.Cards[1].Text != "note 2 more" || s.focus != focusTray {
		t.Fatalf("after edit: %v focus %v", cardTexts(s.Tray), s.focus)
	}
	send(t, s, "d")
	if s.Tray.Len() != 2 || s.Tray.Cards[1].Text != "note 3" {
		t.Fatalf("after delete: %v", cardTexts(s.Tray))
	}
	term.Reset()
	send(t, s, "\x1b")
	if s.focus != focusAgent {
		t.Fatal("Esc did not return focus")
	}
	send(t, s, "typed")
	if agent.String() != "typed" {
		t.Fatalf("agent got %q", agent.String())
	}

	// Persistence: a new session for the same id restores the tray exactly.
	stored, err := store.Load("claude", "sess-1")
	if err != nil || stored.Len() != 2 || stored.Cards[0].Text != "note 1" || stored.Cards[1].Text != "note 3" {
		t.Fatalf("stored = %v err %v", cardTexts(stored), err)
	}
	s2, _, _ := fixtureSession(t, "inline")
	s2.UseStore(store)
	if err := s2.SetSessionID("sess-1"); err != nil {
		t.Fatal(err)
	}
	if s2.Tray.Len() != 2 || s2.Tray.Cards[0].Anchor != s.Tray.Cards[0].Anchor || s2.Tray.Cards[1].Tag != "approve" || !s2.Composited() {
		t.Fatalf("restored = %v composited=%v", cardTexts(s2.Tray), s2.Composited())
	}
}

func cardTexts(t *card.Tray) []string {
	var out []string
	for _, c := range t.Cards {
		out = append(out, c.Text)
	}
	return out
}

func TestClickOnCardHighlightsAnchor(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	heading := rowOf(t, s, "⏺ Plan")
	metaClick(t, s, 3, yOf(s, heading))
	send(t, s, "a")
	send(t, s, "good\r")
	lines, _, _, _ := physical(s)
	div := -1
	for i, l := range lines {
		if strings.Contains(l.String(), "› 1 card") {
			div = i
		}
	}
	send(t, s, "\x1b[<0;5;"+itoa(div+2)+"M\x1b[<0;5;"+itoa(div+2)+"m")
	if s.focus != focusTray || s.highlight == nil || s.highlight.first != heading {
		t.Fatalf("focus=%v highlight=%+v", s.focus, s.highlight)
	}
	lines, _, _, _ = physical(s)
	s.mu.Lock()
	r := s.agentToPhysical(heading - s.windowStart())
	s.mu.Unlock()
	if lines[r].Cells[0].Attr.Flags&screen.Reverse == 0 {
		t.Fatalf("anchor row not highlighted: %+v", lines[r].Cells[0])
	}
	send(t, s, "\x1b")
	if s.highlight != nil {
		t.Fatal("highlight must clear on the next key")
	}
}

func TestDipleDrawsWithoutTruecolourAndPlainUsesOnlyReverseUnderline(t *testing.T) {
	for _, plain := range []bool{false, true} {
		s, _, _ := fixtureSession(t, "inline")
		s.Plain = plain
		for _, sub := range []string{"That is the whole plan.", "2. Change the handler", "3. Verify"} {
			metaClick(t, s, 5, yOf(s, rowOf(t, s, sub)))
			send(t, s, "f")
			send(t, s, "n\r")
		}
		metaClick(t, s, 5, yOf(s, rowOf(t, s, "⏺ Plan")))
		s.mu.Lock()
		own := append(s.trayLines(), s.toolbarLine())
		s.editor = &editor{tag: "fix", text: []rune("x")}
		own = append(own, s.editorLine())
		s.editor = nil
		s.mu.Unlock()
		var buf []byte
		for _, l := range own {
			buf = l.AppendEmit(buf)
			for _, c := range l.Cells {
				if c.Attr.FG.Kind == screen.ColorRGB || c.Attr.BG.Kind == screen.ColorRGB {
					t.Fatalf("truecolour in Diple's drawing: %+v", c)
				}
				if plain && (c.Attr.Flags&^(screen.Reverse|screen.Underline) != 0 || c.Attr.FG.Kind != screen.ColorDefault || c.Attr.BG.Kind != screen.ColorDefault) {
					t.Fatalf("--plain drew %+v", c.Attr)
				}
				if !plain && c.Attr.FG.Kind == screen.ColorIndexed && (c.Attr.FG.Index < 1 || c.Attr.FG.Index > 6) {
					t.Fatalf("tag colour outside 1-6: %+v", c.Attr)
				}
			}
		}
		if bytes.Contains(buf, []byte("38;2")) || bytes.Contains(buf, []byte("48;2")) {
			t.Fatalf("24-bit SGR in Diple's drawing: %q", buf)
		}
	}
}

func TestResizeKeepsTrayAboveInputBox(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	for _, sub := range []string{"That is the whole plan.", "2. Change the handler", "3. Verify"} {
		metaClick(t, s, 5, yOf(s, rowOf(t, s, sub)))
		send(t, s, "c")
		send(t, s, "n\r")
	}
	if err := s.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	lines, _, _, _ := physical(s)
	if len(lines) != 30 || len(lines[0].Cells) != 100 || s.AgentRows() != 26 {
		t.Fatalf("rows %d cols %d agent rows %d", len(lines), len(lines[0].Cells), s.AgentRows())
	}
	div := -1
	for i, l := range lines {
		if strings.Contains(l.String(), "› 3 cards") {
			div = i
		}
	}
	if div < 0 || !strings.HasPrefix(lines[div+4].String(), "─") || !strings.HasPrefix(lines[div+5].String(), "❯") {
		t.Fatalf("layout after resize:\n%s", strings.Join(texts(lines), "\n"))
	}
}

func TestEmptyTrayStaysPassThrough(t *testing.T) {
	s, term, _ := fixtureSession(t, "inline")
	if s.Composited() {
		t.Fatal("composited with nothing to show")
	}
	_ = s.HandleOutput([]byte("more output\r\n"))
	if got := term.String(); got != "more output\r\n" {
		t.Fatalf("pass-through altered the stream: %q", got)
	}
}
