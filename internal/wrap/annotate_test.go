package wrap

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/adapter/claude"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/clip"
	"github.com/maximalfocus/diple/internal/record"
	"github.com/maximalfocus/diple/internal/screen"
)

type staticSource struct{ tr *adapter.Transcript }

func (s staticSource) Transcript() (*adapter.Transcript, error) { return s.tr, nil }

// fixtureDir stays on 2.1.266: the journeys' pointer rows were read off that
// fixture's geometry. At 2.1.268 the input box sits one row higher, so the
// first card pushes the heading into scrollback and resting on the card
// scrolls to it, which moves the tray to the bottom (see issue #27).
const fixtureDir = "../adapter/claude/testdata/2.1.266"

// advance moves the session's own clock forward, which is how a test drives
// the raise's two frames without waiting for them.
func advance(s *Session, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := s.now().Add(d)
	s.now = func() time.Time { return at }
}

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
	// The host answers for the clipboard, so a copy lands in the terminal
	// stream as OSC 52 and the test can read exactly what was copied.
	s.Clip = &clip.Writer{OSC52Answered: true}
	at := time.Unix(1700000000, 0)
	s.SetClock(func() time.Time { return at })
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

// rowOf finds the history row of the assistant turn whose text contains sub,
// searching from the last turn marker so the echoed prompt above it is never
// matched.
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

func send(t *testing.T, s *Session, in string) {
	t.Helper()
	if err := s.HandleInput([]byte(in)); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// motionAt is a pointer report with no button down.
func motionAt(x, y int) string { return "\x1b[<35;" + itoa(x) + ";" + itoa(y) + "M" }

// pressAt and releaseAt are the two halves of a press. A press means what it
// means when it ends, so a test that means a press sends both.
func pressAt(x, y int) string   { return "\x1b[<0;" + itoa(x) + ";" + itoa(y) + "M" }
func releaseAt(x, y int) string { return "\x1b[<0;" + itoa(x) + ";" + itoa(y) + "m" }
func dragTo(x, y int) string    { return "\x1b[<32;" + itoa(x) + ";" + itoa(y) + "M" }

// shiftPressAt is the one place the model lets a modifier through: a
// Shift-press on a second raised line extends to a range.
func shiftPressAt(x, y int) string {
	return "\x1b[<4;" + itoa(x) + ";" + itoa(y) + "M\x1b[<4;" + itoa(x) + ";" + itoa(y) + "m"
}

// dwellOn rests the pointer on a cell until the raise has both its frames.
func dwellOn(t *testing.T, s *Session, x, y int) {
	t.Helper()
	send(t, s, motionAt(x, y))
	advance(s, raiseDwell+stripDelay+time.Millisecond)
	if err := s.Tick(); err != nil {
		t.Fatal(err)
	}
}

// raisePress is the whole gesture: rest the pointer on a block until it rises,
// then press it. It claims no modifier key.
func raisePress(t *testing.T, s *Session, x, y int) {
	t.Helper()
	dwellOn(t, s, x, y)
	send(t, s, pressAt(x, y)+releaseAt(x, y))
}

// pressRaised presses a raised block, or one of its strip's choices.
func pressRaised(t *testing.T, s *Session, x, y int) {
	t.Helper()
	send(t, s, pressAt(x, y)+releaseAt(x, y))
}

// physical composes what the terminal should show.
func physical(s *Session) ([]screen.Line, int, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.physicalLocked()
}

// clipboard is the text the last OSC 52 put on the clipboard.
func clipboard(t *testing.T, term *bytes.Buffer) string {
	t.Helper()
	out := term.String()
	i := strings.LastIndex(out, "\x1b]52;c;")
	if i < 0 {
		return ""
	}
	rest := out[i+len("\x1b]52;c;"):]
	j := strings.IndexByte(rest, '\a')
	if j < 0 {
		t.Fatalf("unterminated OSC 52 in %q", rest)
	}
	data, err := base64.StdEncoding.DecodeString(rest[:j])
	if err != nil {
		t.Fatalf("OSC 52 payload: %v", err)
	}
	return string(data)
}

// blockLeft is the column just inside a raised block, for a pointer that
// means the block rather than the margin beside it.
func blockLeft(s *Session) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.raised == nil {
		return 1
	}
	return s.raised.left + 1
}

func texts(lines []screen.Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.String()
	}
	return out
}

func cardTexts(t *card.Tray) []string {
	var out []string
	for _, c := range t.Cards {
		out = append(out, c.Text)
	}
	return out
}

// TestRaiseAndStripProduceExpectedCards exercises each tag on a paragraph, a
// list item, a code line range, and a dragged span, and checks the cards that
// come out.
func TestRaiseAndStripProduceExpectedCards(t *testing.T) {
	for _, mode := range []string{"inline", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			s, term, agent := fixtureSession(t, mode)

			// 1. Rest on the paragraph and press it: the editor opens tagged
			// note, with nothing between pointing and typing.
			para := rowOf(t, s, "That is the whole plan.")
			dwellOn(t, s, 6, yOf(s, para))
			if s.raised == nil || s.raised.kind != blocks.Paragraph || s.raised.first != para {
				t.Fatalf("raise = %+v", s.raised)
			}
			if !s.Composited() || term.Len() == 0 {
				t.Fatalf("the raise did not draw: composited=%v", s.Composited())
			}
			pressRaised(t, s, blockLeft(s), yOf(s, para))
			if s.editor == nil || s.editor.tag != card.DefaultTag {
				t.Fatalf("editor = %+v", s.editor)
			}
			// Motion reaches an agent that asked for its own; the editor's
			// own keys never do.
			agent.Reset()
			send(t, s, "tighten this")
			if agent.Len() != 0 {
				t.Fatalf("editor keys reached the agent: %q", agent.Bytes())
			}
			send(t, s, "\r")
			c := s.Tray.Cards[0]
			if c.Kind != card.Anchored || c.Tag != "note" || c.Text != "tighten this" ||
				c.Anchor.Kind != blocks.Paragraph || c.Anchor.First != para ||
				c.Anchor.Quote != "That is the whole plan." {
				t.Fatalf("card = %+v", c)
			}

			// 2. A list item, tagged fix from the strip. The ordinal is
			// recorded whatever the tag is.
			item := rowOf(t, s, "2. Change the handler")
			dwellOn(t, s, 6, yOf(s, item))
			sr := stripRowFor(t, s)
			hit := choiceFor(t, s, "fix")
			pressRaised(t, s, hit+1, sr)
			if s.editor == nil || s.editor.tag != "fix" {
				t.Fatalf("editor after a strip press = %+v", s.editor)
			}
			send(t, s, "pick this\r")
			c = s.Tray.Cards[1]
			if c.Tag != "fix" || c.Anchor.Kind != blocks.ListItem || c.Anchor.Ordinal != 2 ||
				c.Anchor.Quote != "Change the handler" {
				t.Fatalf("list card = %+v", c)
			}

			// 3. A code line, extended to a range by a Shift-press on a
			// second raised line, with the chip moved to ask.
			line1 := rowOf(t, s, "func handle(")
			line3 := rowOf(t, s, "  }")
			dwellOn(t, s, 6, yOf(s, line1))
			if s.raised == nil || !s.raised.line {
				t.Fatalf("a code line must rise, not its block: %+v", s.raised)
			}
			pressRaised(t, s, blockLeft(s), yOf(s, line1))
			if s.editor == nil || s.editor.sel == nil || s.editor.sel.lines == nil {
				t.Fatalf("pressing a raised line did not open a line editor: %+v", s.editor)
			}
			dwellOn(t, s, 6, yOf(s, line3))
			send(t, s, shiftPressAt(blockLeft(s), yOf(s, line3)))
			if s.editor == nil || s.editor.sel.first != line1 || s.editor.sel.last != line3 {
				t.Fatalf("Shift-press did not extend the range: %+v", s.editor.sel)
			}
			send(t, s, "\x1ba") // the chip moves to ask
			if s.editor.tag != "ask" {
				t.Fatalf("chip = %q", s.editor.tag)
			}
			send(t, s, "wrong status\r")
			c = s.Tray.Cards[2]
			if c.Tag != "ask" || c.Anchor.Kind != blocks.CodeLine || c.Anchor.Lines == nil ||
				c.Anchor.Lines.Last-c.Anchor.Lines.First != 2 || c.Anchor.First != line1 || c.Anchor.Last != line3 {
				t.Fatalf("line-range card = %+v", c)
			}

			// 4. A dragged span inside one list item is also a span, so the
			// strip follows it and the drag that copied it can tag it too.
			first := rowOf(t, s, "1. Read the config file")
			y := yOf(s, first)
			send(t, s, pressAt(6, y))
			send(t, s, dragTo(20, y))
			send(t, s, releaseAt(20, y))
			if s.sel == nil || s.sel.span == nil {
				t.Fatalf("span selection = %+v", s.sel)
			}
			send(t, s, "f")
			send(t, s, "which ports?\r")
			c = s.Tray.Cards[3]
			if c.Tag != "fix" || c.Anchor.Span == nil || c.Anchor.Quote == "" {
				t.Fatalf("span card = %+v", c)
			}

			// The tray sits directly above the input box.
			lines, _, _, _ := physical(s)
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
		})
	}
}

// stripRowFor is the 1-based physical row the strip occupies.
func stripRowFor(t *testing.T, s *Session) int {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.stripRow()
	if r < 0 {
		t.Fatal("the strip is not on screen")
	}
	return s.agentToPhysical(r) + 1
}

// choiceFor is the first column of a strip choice, 0-based.
func choiceFor(t *testing.T, s *Session, label string) int {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	choices, _, _ := stripLayout(s.raised.left)
	for _, c := range choices {
		if c.label == label {
			return c.from
		}
	}
	t.Fatalf("no strip choice %q", label)
	return 0
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// selectBlockAt opens a block selection directly, for tests whose subject is
// what a selection makes possible rather than the gesture that makes one.
func selectBlockAt(t *testing.T, s *Session, hist int) {
	t.Helper()
	s.mu.Lock()
	ok := s.selectBlockLocked(hist)
	s.mu.Unlock()
	if !ok {
		t.Fatalf("no block at history row %d", hist)
	}
	if err := s.Tick(); err != nil {
		t.Fatal(err)
	}
}
