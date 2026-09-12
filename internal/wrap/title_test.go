package wrap

import (
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/card"
)

func TestTitlesAreFoundAcrossChunks(t *testing.T) {
	var tt titles
	var got []string
	for _, chunk := range []string{
		"plain \x1b]2;work",
		"ing\x07 more \x1b]0;a\x1b",
		"\\ \x1b]8;;http://x\x07link\x1b]8;;\x07 \x1b]52;c;aGk=\x07 \x1b]1;icon\x07 \x1b]10;?\x07",
		"\x1b]2;abandoned\x1b[0m \x1b",
		"]2;split at the escape\x07",
	} {
		for _, seq := range tt.scan([]byte(chunk)) {
			got = append(got, string(seq))
		}
	}
	want := []string{
		"\x1b]2;working\x07", "\x1b]0;a\x1b\\", "\x1b]1;icon\x07", "\x1b]2;split at the escape\x07",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("titles = %q\nwant     %q", got, want)
	}
}

func TestAnUnfinishedTitleIsBounded(t *testing.T) {
	var tt titles
	tt.scan([]byte("\x1b]2;" + strings.Repeat("x", maxTitle*2)))
	if len(tt.buf) > maxTitle {
		t.Fatalf("an unfinished title grew to %d bytes", len(tt.buf))
	}
	if got := tt.scan([]byte("\x07")); len(got) != 0 {
		t.Fatalf("an overlong title was forwarded: %q", got)
	}
}

// TestTheHostKeepsTheAgentsTitleWithCardsInTheTray: R-017. With cards in the
// tray Diple composites, and a host that reads the pane's title — herdr reads
// codex's state from it — still sees the agent's.
func TestTheHostKeepsTheAgentsTitleWithCardsInTheTray(t *testing.T) {
	s, term, _ := newTestSession(80, 24)
	s.Tray.Add(&card.Card{Kind: card.Free, Text: "one"})
	s.mu.Lock()
	if err := s.syncLocked(); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()
	if !s.Composited() {
		t.Fatal("a card in the tray did not composite")
	}
	term.Reset()
	title := "\x1b]0;⠋ codex working\x07"
	if err := s.HandleOutput([]byte("reply\r\n" + title[:6])); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleOutput([]byte(title[6:])); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(term.String(), title); n != 1 {
		t.Fatalf("the host saw the agent's title %d times: %q", n, term.String())
	}
}

func TestAScrolledViewKeepsTheAgentsTitle(t *testing.T) {
	s, term, _ := newTestSession(80, 5)
	for i := 0; i < 20; i++ {
		if err := s.HandleOutput([]byte("line\r\n")); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	_ = s.scrollLocked(3)
	s.mu.Unlock()
	if !s.Scrolled() || s.Composited() {
		t.Fatalf("scrolled=%v composited=%v", s.Scrolled(), s.Composited())
	}
	term.Reset()
	title := "\x1b]2;codex idle\x07"
	if err := s.HandleOutput([]byte("more\r\n" + title)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(term.String(), title) {
		t.Fatalf("a scrolled view dropped the agent's title: %q", term.String())
	}
}

func TestAPassThroughTitleIsForwardedOnce(t *testing.T) {
	s, term, _ := newTestSession(80, 24)
	title := "\x1b]2;codex\x07"
	if err := s.HandleOutput([]byte("x" + title + "y")); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(term.String(), title); n != 1 {
		t.Fatalf("title forwarded %d times: %q", n, term.String())
	}
}
