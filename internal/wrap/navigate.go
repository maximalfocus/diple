package wrap

import (
	"strings"
	"unicode/utf8"

	"github.com/maximalfocus/diple/internal/adapter"
)

// maxNavSteps bounds how many wheel notches one turn jump forwards to a
// fullscreen agent, so a viewport that keeps repainting without moving can
// never make Diple spin.
const maxNavSteps = 64

// searchField is the transient one-line field `/` opens. It is Diple's own
// field on Diple's own overlay row, never a second box for the agent.
type searchField struct {
	query   []rune
	match   int  // index into the current query's matches, -1 before the first
	noMatch bool // the last search found nothing
}

// navRequest is a turn jump waiting on a fullscreen agent. Diple cannot move
// the agent's viewport itself, so it forwards wheel notches and re-aligns
// whatever becomes visible until the turn shows or the viewport stops moving.
type navRequest struct {
	turn  int
	up    bool
	steps int
	seen  string // the visible text the last notch was sent for
	// block is the search match to highlight once the turn shows, or -1
	// when the jump is plain navigation.
	block int
}

// navOwnedLocked reports whether Diple already owns navigation, which is the
// only time `[`, `]` and `/` are its gestures: the viewport is scrolled off
// live, a block selection is open, or the tray has focus. Otherwise all three
// reach the agent unchanged, so a slash command still starts with `/`.
func (s *Session) navOwnedLocked() bool {
	return s.back != 0 || s.sel != nil || s.focus == focusTray
}

// turnRows returns the rows a turn occupies now and whether its blocks were
// really matched against them. An unmatched turn still has fallback rows, but
// only a matched one proves the turn is on screen.
func turnRows(al []adapter.TurnAlignment, turn int) (first, last int, ok, aligned bool) {
	for _, t := range al {
		if t.Turn != turn {
			continue
		}
		for _, b := range t.Blocks {
			if b.First < 0 {
				continue
			}
			if !ok || b.First < first {
				first = b.First
			}
			if !ok || b.Last > last {
				last = b.Last
			}
			ok = true
		}
		return first, last, ok, t.Aligned && ok
	}
	return 0, 0, false, false
}

// blockRows returns one block's rows, falling back to its turn's rows when
// the block itself has none.
func blockRows(al []adapter.TurnAlignment, turn, block int) (first, last int, ok bool) {
	for _, t := range al {
		if t.Turn != turn {
			continue
		}
		if block >= 0 && block < len(t.Blocks) && t.Blocks[block].First >= 0 {
			return t.Blocks[block].First, t.Blocks[block].Last, true
		}
		break
	}
	f, l, ok, _ := turnRows(al, turn)
	return f, l, ok
}

// turnOrdinalsLocked lists the assistant turns in order. The transcript knows
// them all; the alignment knows only what the mode makes visible.
func (s *Session) turnOrdinalsLocked(al []adapter.TurnAlignment) []int {
	var out []int
	if tr := s.transcript(); tr != nil && len(tr.Turns) > 0 {
		for _, t := range tr.Turns {
			out = append(out, t.Ordinal)
		}
		return out
	}
	for _, t := range al {
		out = append(out, t.Turn)
	}
	return out
}

// currentTurnLocked is the turn a jump moves from: the one at the top of the
// view, else the first turn below it, else the last turn. It is read from the
// view rather than remembered, so a jump, a scroll, and a click all agree.
func (s *Session) currentTurnLocked(al []adapter.TurnAlignment) int {
	start := s.windowStart()
	below := 0
	for _, t := range al {
		first, last, ok, _ := turnRows(al, t.Turn)
		if !ok {
			continue
		}
		if first <= start && start <= last {
			return t.Turn
		}
		if first > start && below == 0 {
			below = t.Turn
		}
	}
	if below != 0 {
		return below
	}
	if ords := s.turnOrdinalsLocked(al); len(ords) > 0 {
		return ords[len(ords)-1]
	}
	return 0
}

// jumpTurnLocked moves the history view to the next (dir +1) or previous
// (dir -1) assistant turn, stopping at the ends rather than wrapping.
func (s *Session) jumpTurnLocked(dir int) error {
	_, al := s.alignmentLocked()
	ords := s.turnOrdinalsLocked(al)
	if len(ords) == 0 {
		return nil
	}
	cur := s.currentTurnLocked(al)
	idx := -1
	for i, o := range ords {
		if o == cur {
			idx = i
			break
		}
	}
	next := 0
	switch {
	case idx < 0 && dir > 0:
		next = 0
	case idx < 0:
		next = len(ords) - 1
	default:
		next = idx + dir
	}
	if next < 0 || next >= len(ords) {
		return nil
	}
	return s.gotoTurnLocked(ords[next], dir < 0, -1)
}

// gotoTurnLocked puts a turn's first row at the top of the agent region.
// Inline that is a scroll of Diple's own scrollback; in fullscreen the agent
// owns the viewport, so Diple asks it to scroll and lands when the turn shows.
func (s *Session) gotoTurnLocked(turn int, up bool, block int) error {
	_, al := s.alignmentLocked()
	first, _, ok, aligned := turnRows(al, turn)
	if s.Model.AltActive() {
		if aligned {
			s.nav = nil
			return nil
		}
		return s.startNavLocked(turn, up, block)
	}
	if !ok {
		return nil
	}
	return s.scrollLocked(s.Model.HistoryLen() - first - s.back)
}

// startNavLocked begins a fullscreen jump by forwarding the first notch. An
// agent that did not ask for mouse reports cannot be scrolled, and Diple fails
// open rather than pretending otherwise.
func (s *Session) startNavLocked(turn int, up bool, block int) error {
	if !s.Model.MouseTracking() {
		s.nav = nil
		return nil
	}
	s.nav = &navRequest{turn: turn, up: up, block: block}
	return s.stepNavLocked()
}

// stepNavLocked forwards one wheel notch to the agent and records the viewport
// it was sent for, so the next repaint can tell a scroll from a redraw.
func (s *Session) stepNavLocked() error {
	n := s.nav
	if n == nil {
		return nil
	}
	if n.steps >= maxNavSteps {
		s.nav = nil
		return nil
	}
	n.steps++
	n.seen = strings.Join(s.Model.Text(), "\n")
	button := 65
	if n.up {
		button = 64
	}
	return writeAll(s.agent, encodeSGRMouse(mouseEvent{button: button, x: 1, y: 1}))
}

// driveNavLocked continues a fullscreen jump after the agent repaints: it
// re-aligns what is now visible, lands when the target turn is there, and
// waits rather than pushing when the viewport did not move.
func (s *Session) driveNavLocked() error {
	n := s.nav
	if n == nil {
		return nil
	}
	if !s.Model.AltActive() {
		s.nav = nil
		return nil
	}
	_, al := s.alignmentLocked()
	if _, _, _, aligned := turnRows(al, n.turn); aligned {
		// A jump a search started highlights its match once it shows.
		if n.block >= 0 {
			if first, last, ok := blockRows(al, n.turn, n.block); ok {
				s.highlight = &rowRange{first: first, last: last}
			}
		}
		s.nav = nil
		return nil
	}
	if strings.Join(s.Model.Text(), "\n") == n.seen {
		return nil
	}
	return s.stepNavLocked()
}

// openSearchLocked opens the transcript search field.
func (s *Session) openSearchLocked() {
	s.search = &searchField{match: -1}
	s.sel = nil
}

// searchKeysLocked handles one input unit while the search field is open: Esc
// closes it and leaves the view where it is, Enter moves to the next match,
// Backspace deletes, and printable runes extend the query.
func (s *Session) searchKeysLocked(chunk []byte) error {
	f := s.search
	if chunk[0] == 0x1b {
		if len(chunk) == 1 {
			s.search = nil
		}
		return nil
	}
	for len(chunk) > 0 {
		r, size := utf8.DecodeRune(chunk)
		chunk = chunk[size:]
		switch {
		case r == '\r' || r == '\n':
			return s.runSearchLocked()
		case r == 0x7f || r == 0x08:
			if len(f.query) > 0 {
				f.query = f.query[:len(f.query)-1]
			}
			f.match, f.noMatch = -1, false
		case r >= 0x20 && r != utf8.RuneError:
			f.query = append(f.query, r)
			f.match, f.noMatch = -1, false
		}
	}
	return nil
}

// searchMatch is one block of one turn whose text contains the query.
type searchMatch struct{ turn, block int }

// matchesLocked lists the matches in transcript order.
func (s *Session) matchesLocked(q string) []searchMatch {
	var out []searchMatch
	if tr := s.transcript(); tr != nil && len(tr.Turns) > 0 {
		for _, t := range tr.Turns {
			for i, b := range t.Blocks {
				if strings.Contains(strings.ToLower(b.Text), q) {
					out = append(out, searchMatch{t.Ordinal, i})
				}
			}
		}
		return out
	}
	_, al := s.alignmentLocked()
	for _, t := range al {
		for i, b := range t.Blocks {
			if strings.Contains(strings.ToLower(b.Text), q) {
				out = append(out, searchMatch{t.Turn, i})
			}
		}
	}
	return out
}

// runSearchLocked moves the view to the next match of the query, wrapping at
// the end, and leaves nothing moved when there is none.
func (s *Session) runSearchLocked() error {
	f := s.search
	q := strings.ToLower(strings.TrimSpace(string(f.query)))
	if q == "" {
		return nil
	}
	ms := s.matchesLocked(q)
	if len(ms) == 0 {
		f.match, f.noMatch = -1, true
		return nil
	}
	f.noMatch = false
	f.match = (f.match + 1) % len(ms)
	return s.revealLocked(ms[f.match].turn, ms[f.match].block)
}

// revealLocked brings a turn's block into view and highlights it, the same
// way clicking a card shows its anchor.
func (s *Session) revealLocked(turn, block int) error {
	_, al := s.alignmentLocked()
	if s.Model.AltActive() {
		if _, _, _, aligned := turnRows(al, turn); !aligned {
			return s.gotoTurnLocked(turn, turn <= s.currentTurnLocked(al), block)
		}
	}
	first, last, ok := blockRows(al, turn, block)
	if !ok {
		return nil
	}
	s.highlight = &rowRange{first: first, last: last}
	s.ensureVisibleLocked(first, last)
	return nil
}
