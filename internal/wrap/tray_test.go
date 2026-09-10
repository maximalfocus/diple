package wrap

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/screen"
)

// TestRestingOnACardRaisesItsAnchor: a card is read against what it points at
// without pressing anything.
func TestRestingOnACardRaisesItsAnchor(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	heading := rowOf(t, s, "⏺ Plan")
	dwellOn(t, s, 6, yOf(s, heading))
	pressRaised(t, s, blockLeft(s), yOf(s, heading))
	send(t, s, "good\r")
	lines, _, _, _ := physical(s)
	div := -1
	for i, l := range lines {
		if strings.Contains(l.String(), "› 1 card") {
			div = i
		}
	}
	if div < 0 {
		t.Fatalf("no tray divider in %q", texts(lines))
	}
	// The pointer only rests on the card; nothing is pressed.
	send(t, s, motionAt(5, div+2))
	if s.highlight == nil || s.highlight.first != heading {
		t.Fatalf("resting on a card did not raise its anchor: %+v", s.highlight)
	}
	lines, _, _, _ = physical(s)
	s.mu.Lock()
	r := s.agentToPhysical(heading - s.windowStart())
	s.mu.Unlock()
	if lines[r].Cells[0].Attr.Flags&screen.Reverse == 0 {
		t.Fatalf("anchor row not raised: %+v", lines[r].Cells[0])
	}
	// Pressing the card opens it for editing in place.
	send(t, s, pressAt(5, div+2)+releaseAt(5, div+2))
	if s.editor == nil || s.editor.editing != s.Tray.Cards[0] {
		t.Fatalf("pressing a card did not edit it in place: %+v", s.editor)
	}
	send(t, s, " and more\r")
	if s.Tray.Cards[0].Text != "good and more" {
		t.Fatalf("edited card = %q", s.Tray.Cards[0].Text)
	}
}

// TestTheTimesDeletesACard: a card is deleted outright by the × at its end or
// by d.
func TestTheTimesDeletesACard(t *testing.T) {
	s, _, _ := fixtureSession(t, "inline")
	s.Tray.Add(&card.Card{Kind: card.Free, Text: "one"})
	s.Tray.Add(&card.Card{Kind: card.Free, Text: "two"})
	s.mu.Lock()
	if err := s.syncLocked(); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	div := s.inputRow()
	s.mu.Unlock()
	x := s.cols - 1
	send(t, s, pressAt(x, div+2)+releaseAt(x, div+2))
	if s.Tray.Len() != 1 || s.Tray.Cards[0].Text != "two" {
		t.Fatalf("× did not delete the card: %v", cardTexts(s.Tray))
	}
	// The press on × already moved focus into the tray, so d deletes there.
	send(t, s, "d")
	if s.Tray.Len() != 0 {
		t.Fatalf("d did not delete the card: %v", cardTexts(s.Tray))
	}
}

func TestTrayFocusEditReorderDeleteAndPersistence(t *testing.T) {
	store := &card.Store{Dir: filepath.Join(t.TempDir(), "trays")}
	s, _, agent := fixtureSession(t, "inline")
	s.UseStore(store)
	if err := s.SetSessionID("sess-1"); err != nil {
		t.Fatal(err)
	}
	for i, sub := range []string{"That is the whole plan.", "2. Change the handler", "3. Verify"} {
		dwellOn(t, s, 6, yOf(s, rowOf(t, s, sub)))
		send(t, s, string(rune("nfa"[i])))
		send(t, s, "note "+itoa(i+1)+"\r")
	}
	if s.Tray.Len() != 3 {
		t.Fatalf("tray = %d", s.Tray.Len())
	}
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
	send(t, s, "\x1b")
	if s.focus != focusAgent {
		t.Fatal("Esc did not return focus")
	}
	agent.Reset()
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
	if s2.Tray.Len() != 2 || s2.Tray.Cards[0].Anchor != s.Tray.Cards[0].Anchor ||
		s2.Tray.Cards[1].Tag != "ask" || !s2.Composited() {
		t.Fatalf("restored = %v composited=%v", cardTexts(s2.Tray), s2.Composited())
	}
}

// TestDipleDrawsWithoutTruecolourAndPlainDropsColour is R-011: Diple draws
// only with the default colours, the 16 indexed colours, and the bold, dim,
// reverse, and underline attributes; --plain drops colour entirely and keeps
// the attributes.
func TestDipleDrawsWithoutTruecolourAndPlainDropsColour(t *testing.T) {
	for _, plain := range []bool{false, true} {
		s, _, _ := fixtureSession(t, "inline")
		s.Plain = plain
		for i, sub := range []string{"That is the whole plan.", "2. Change the handler", "3. Verify"} {
			dwellOn(t, s, 6, yOf(s, rowOf(t, s, sub)))
			send(t, s, string(rune("nfa"[i])))
			send(t, s, "x\r")
		}
		dwellOn(t, s, 6, yOf(s, rowOf(t, s, "That is the whole plan.")))
		if s.raised == nil {
			t.Fatal("nothing rose for the strip")
		}
		s.mu.Lock()
		own := s.trayLines()
		strip := s.blankLine()
		s.drawStrip(&strip)
		own = append(own, strip)
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
				if plain && (c.Attr.FG.Kind != screen.ColorDefault || c.Attr.BG.Kind != screen.ColorDefault) {
					t.Fatalf("--plain drew colour: %+v", c.Attr)
				}
				if c.Attr.Flags&^(screen.Reverse|screen.Underline|screen.Bold|screen.Dim) != 0 {
					t.Fatalf("Diple drew an attribute it does not own: %+v", c.Attr)
				}
				if !plain && c.Attr.FG.Kind == screen.ColorIndexed && (c.Attr.FG.Index < 1 || c.Attr.FG.Index > 3) {
					t.Fatalf("tag colour outside 1-3: %+v", c.Attr)
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
	for i, sub := range []string{"That is the whole plan.", "2. Change the handler", "3. Verify"} {
		dwellOn(t, s, 6, yOf(s, rowOf(t, s, sub)))
		send(t, s, string(rune("nfa"[i])))
		send(t, s, "x\r")
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

// TestEmptyTrayStaysPassThrough is R-002: with nothing of Diple's showing, the
// agent's bytes are forwarded unmodified.
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

// TestEnvelopeAsksForMotionReporting: Diple requests motion reporting to know
// what is under the pointer, and gives it back when it stops.
func TestEnvelopeAsksForMotionReporting(t *testing.T) {
	if !strings.Contains(EnvelopeStart, "\x1b[?1003h") {
		t.Fatalf("the envelope does not ask for motion reporting: %q", EnvelopeStart)
	}
	if !strings.Contains(EnvelopeEnd, "\x1b[?1003l") {
		t.Fatalf("the envelope does not give motion reporting back: %q", EnvelopeEnd)
	}
}
