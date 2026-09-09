package wrap

import (
	"github.com/maximalfocus/diple/internal/card"
)

func (s *Session) clampTraySel() {
	if s.traySel >= s.Tray.Len() {
		s.traySel = s.Tray.Len() - 1
	}
	if s.traySel < 0 {
		s.traySel = 0
	}
}

// trayKeysLocked handles a key while the tray has focus.
func (s *Session) trayKeysLocked(chunk []byte) {
	if len(chunk) != 1 {
		return
	}
	switch chunk[0] {
	case '+':
		s.chooser = true
	case 0x1b, '\t':
		s.focus = focusAgent
	case 'j':
		s.traySel++
	case 'k':
		s.traySel--
	case 'J':
		if s.Tray.Move(s.traySel, s.traySel+1) {
			s.traySel++
			_ = s.saveLocked()
		}
	case 'K':
		if s.Tray.Move(s.traySel, s.traySel-1) {
			s.traySel--
			_ = s.saveLocked()
		}
	case 'd', 0x7f, 0x08:
		if s.Tray.Delete(s.traySel) {
			_ = s.saveLocked()
		}
		if s.Tray.Len() == 0 {
			s.focus = focusAgent
		}
	case 'e':
		if s.traySel < s.Tray.Len() {
			c := s.Tray.Cards[s.traySel]
			s.openEditorLocked(c.Tag, nil, c)
		}
	case 's', '\r', '\n':
		s.setKeyErr(s.requestSendLocked(true))
	case 'p':
		s.setKeyErr(s.requestSendLocked(false))
	}
	s.clampTraySel()
}

// openFreeEditorLocked opens the one-line editor for a new free card, or
// for the tray's existing overall card, since a tray carries only one.
func (s *Session) openFreeEditorLocked(k card.Kind) {
	if k == card.Overall {
		if existing := s.Tray.Overall(); existing != nil {
			s.openEditorLocked("", nil, existing)
			return
		}
	}
	s.editor = &editor{kind: k, first: -1, last: -1}
	s.sel = nil
}

// setKeyErr keeps the first error raised while handling a consumed key, for
// HandleInput to return.
func (s *Session) setKeyErr(err error) {
	if err != nil && s.keyErr == nil {
		s.keyErr = err
	}
}

// showAnchorLocked scrolls so a card's anchor is visible and highlights it
// until the next input.
func (s *Session) showAnchorLocked(c *card.Card) {
	first, last, ok := s.resolveAnchor(&c.Anchor)
	if !ok {
		return
	}
	s.highlight = &rowRange{first: first, last: last}
	s.ensureVisibleLocked(first, last)
}

// ensureVisibleLocked scrolls Diple's scrollback the least it can so rows
// first..last are inside the agent region. The alternate screen belongs to
// the agent, so nothing is scrolled there.
func (s *Session) ensureVisibleLocked(first, last int) {
	if s.Model.AltActive() {
		return
	}
	agentRows := s.rows - s.trayH
	start := s.windowStart()
	if first < start {
		s.back += start - first
	} else if last >= start+agentRows {
		s.back -= last - (start + agentRows - 1)
		if s.back < 0 {
			s.back = 0
		}
	}
	if h := s.Model.HistoryLen(); s.back > h {
		s.back = h
	}
}

// mouseLocked routes one mouse report: Diple's gestures are consumed, the
// wheel scrolls, and the rest reaches the agent only if it asked.
func (s *Session) mouseLocked(ev mouseEvent, forward *[]byte) error {
	py := ev.y - 1
	px := ev.x - 1
	tracking := s.Model.MouseTracking()

	if ev.isWheel() {
		s.nav = nil
		if tracking && s.Model.AltActive() {
			*forward = append(*forward, encodeSGRMouse(ev)...)
			return nil
		}
		if ev.release {
			return nil
		}
		if ev.wheelUp() {
			return s.scrollLocked(WheelLines)
		}
		return s.scrollLocked(-WheelLines)
	}

	row, inTray := s.physicalToAgent(py)

	// Editor open: clicks are swallowed.
	if s.editor != nil {
		return nil
	}

	// Tray clicks: select, focus, and show the anchor; drags reorder.
	if inTray {
		idx := row - 1 + s.trayScroll
		if ev.button0() == 0 && !ev.release && !ev.motion() && idx >= 0 && idx < s.Tray.Len() {
			s.focus = focusTray
			s.traySel = idx
			s.sel = nil
			s.drag = &dragState{card: idx, row: -1}
			s.showAnchorLocked(s.Tray.Cards[idx])
		} else if ev.release && s.drag != nil && s.drag.card >= 0 && idx >= 0 && idx < s.Tray.Len() && idx != s.drag.card {
			if s.Tray.Move(s.drag.card, idx) {
				s.traySel = idx
				_ = s.saveLocked()
			}
			s.drag = nil
		} else if ev.release {
			s.drag = nil
		}
		return nil
	}
	if s.drag != nil && s.drag.card >= 0 {
		if ev.release {
			s.drag = nil
		}
		return nil
	}

	hist := s.windowStart() + row
	toolbarRow := -1
	if s.sel != nil {
		toolbarRow = s.overlayRowLocked(s.rows - s.trayH)
	}

	switch {
	case s.sel != nil && row == toolbarRow && ev.button0() == 0 && !ev.release && !ev.motion():
		// A click on the toolbar chooses the action under the pointer.
		if tag := s.toolbarHit(px); tag != "" {
			s.openEditorLocked(tag, s.sel, nil)
		} else {
			s.sel = nil
		}
		return nil
	case ev.meta() && ev.button0() == 0 && !ev.release && !ev.motion():
		// Modifier press: begin a possible drag, select on release.
		s.drag = &dragState{row: hist, col: px, card: -1}
		s.highlight = nil
		return nil
	case ev.meta() && ev.motion() && s.drag != nil:
		s.drag.moved = true
		if s.drag.moved {
			s.selectSpanLocked(s.drag.row, s.drag.col, hist, px)
		}
		return nil
	case ev.release && s.drag != nil:
		d := s.drag
		s.drag = nil
		if d.moved {
			s.selectSpanLocked(d.row, d.col, hist, px)
		} else {
			s.focus = focusAgent
			s.selectBlockLocked(hist)
		}
		return nil
	case ev.button0() == 0 && !ev.release && !ev.motion() && px < 2:
		// A plain click in the gutter of a code or diff line selects it.
		if s.selectLineLocked(hist, ev.shift()) {
			return nil
		}
	}

	if s.sel != nil && ev.button0() == 0 && !ev.release && !ev.motion() {
		s.sel = nil
	}
	if s.focus == focusTray && !ev.release {
		s.focus = focusAgent
	}
	if tracking {
		mapped := ev
		mapped.y = row + 1
		*forward = append(*forward, encodeSGRMouse(mapped)...)
	}
	return nil
}

// toolbarHit maps a column on the toolbar row to its tag.
func (s *Session) toolbarHit(px int) card.Tag {
	x := 1 + 2
	for _, t := range card.Tags {
		w := len(string(t))
		if px >= x && px < x+w {
			return t
		}
		x += w + 2
	}
	return ""
}
