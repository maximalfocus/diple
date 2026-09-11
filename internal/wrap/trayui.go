package wrap

import (
	"time"

	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/keys"
	"github.com/maximalfocus/diple/internal/screen"
)

func (s *Session) clampTraySel() {
	if s.traySel >= s.Tray.Len() {
		s.traySel = s.Tray.Len() - 1
	}
	if s.traySel < 0 {
		s.traySel = 0
	}
}

// trayKeysLocked handles a key while the tray has focus. The tray's own
// Enter and `p` come from R-009 and are not table entries; everything else
// is a binding.
func (s *Session) trayKeysLocked(chunk []byte) {
	k, ok := keyOf(chunk)
	if !ok {
		return
	}
	switch {
	case s.Keys.Is(keys.TrayNew, k):
		s.openFreeEditorLocked(false)
	case s.Keys.Is(keys.OverallCard, k):
		s.openFreeEditorLocked(true)
	case s.Keys.Is(keys.Cancel, k), s.Keys.Is(keys.TrayFocus, k):
		s.focus = focusAgent
	case s.Keys.Is(keys.TrayNext, k):
		s.traySel++
	case s.Keys.Is(keys.TrayPrev, k):
		s.traySel--
	case s.Keys.Is(keys.TrayMoveDn, k):
		if s.Tray.Move(s.traySel, s.traySel+1) {
			s.traySel++
			_ = s.saveLocked()
		}
	case s.Keys.Is(keys.TrayMoveUp, k):
		if s.Tray.Move(s.traySel, s.traySel-1) {
			s.traySel--
			_ = s.saveLocked()
		}
	case s.Keys.Is(keys.TrayDelete, k), k.Rune == 0x7f, k.Rune == 0x08:
		if s.Tray.Delete(s.traySel) {
			_ = s.saveLocked()
		}
		if s.Tray.Len() == 0 {
			s.focus = focusAgent
		}
	case s.Keys.Is(keys.TrayEdit, k):
		if s.traySel < s.Tray.Len() {
			c := s.Tray.Cards[s.traySel]
			s.openEditorLocked(c.Tag, nil, c)
		}
	case k.Rune == 's', k.Rune == '\r', k.Rune == '\n':
		s.setKeyErr(s.requestSendLocked(true))
	case k.Rune == 'p':
		s.setKeyErr(s.requestSendLocked(false))
	}
	s.clampTraySel()
}

// openFreeEditorLocked opens the one-line editor for a new free card on
// Diple's own overlay row, or for the tray's existing overall card, since a
// second closing remark edits the first.
func (s *Session) openFreeEditorLocked(overall bool) {
	if overall {
		if existing := s.Tray.Overall(); existing != nil {
			s.openEditorLocked("", nil, existing)
			return
		}
	}
	s.editor = &editor{kind: card.Free, overall: overall, first: -1, last: -1}
	s.sel, s.raised, s.textSel = nil, nil, nil
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
	s.revealed = s.back != 0
}

// ensureVisibleLocked scrolls Diple's scrollback the least it can so rows
// first..last are inside the agent region. The alternate screen belongs to
// the agent, so nothing is scrolled there.
func (s *Session) ensureVisibleLocked(first, last int) {
	s.revealed = false
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

// pressState is the button that is down. Diple holds it rather than acting on
// it, because a press means what it means when it ends: a press that moves is
// a selection, wherever it started, and only a press that comes up where it
// went down is a press on what lies under it.
type pressState struct {
	row, col int // history row and screen column of the press
	py       int // physical row, for a press on the tray or the status line
	moved    bool
	onRaise  bool         // the press landed on a raised block
	choice   *stripChoice // the strip choice it landed on, if any
	shift    bool
	// count is 1 for a single press, 2 for the second of a double-press, 3
	// for the third.
	count int
}

// mouseLocked routes one mouse report: Diple's gestures are consumed, the
// wheel scrolls, and the rest reaches the agent only if it asked.
func (s *Session) mouseLocked(ev mouseEvent, forward *[]byte) error {
	py := ev.y - 1
	px := ev.x - 1
	tracking := s.Model.MouseTracking()

	// A hidden layer owns no gesture: reports reach the agent if it asked for
	// them and are dropped otherwise.
	if s.hidden {
		if tracking {
			*forward = append(*forward, encodeSGRMouse(ev)...)
		}
		return nil
	}
	if ev.isWheel() {
		s.nav = nil
		s.raised, s.press = nil, nil
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
	// The host's own bypass selection — Shift, or Option on macOS — never
	// reaches Diple at all, so selecting text in the terminal works exactly as
	// it did. A Shift-press on a raised line is the one exception the model
	// makes, and it extends a range rather than starting one.
	row, inTray := s.physicalToAgent(py)
	hist := s.windowStart() + row
	now := s.clock()

	switch {
	case ev.motion() && ev.button0() == 0 && s.press != nil:
		return s.dragLocked(hist, px)
	case ev.motion():
		return s.pointerLocked(py, row, px, inTray, now, forward, tracking)
	case ev.release:
		return s.releaseLocked(hist, px, py, forward, tracking)
	case ev.button0() == 0:
		return s.pressLocked(hist, px, py, row, inTray, ev.shift(), now, forward, tracking)
	}
	if tracking {
		mapped := ev
		mapped.y = row + 1
		*forward = append(*forward, encodeSGRMouse(mapped)...)
	}
	return nil
}

// pointerLocked follows the pointer with no button down: it raises the block
// under it, keeps the raise while the pointer travels to the strip, and shows
// a card's anchor while the pointer rests on the card.
func (s *Session) pointerLocked(py, row, px int, inTray bool, now time.Time, forward *[]byte, tracking bool) error {
	// An agent that asked for its own motion reporting gets it unchanged.
	if tracking && s.Model.Mode(screen.ModeMouseAny) {
		ev := mouseEvent{button: motionNoButton, x: px + 1, y: row + 1}
		*forward = append(*forward, encodeSGRMouse(ev)...)
	}
	if s.prompting {
		return nil
	}
	// While an editor is open on a range of code lines, the lines of that
	// same block still rise, so a Shift-press can extend the range without
	// putting the note away first.
	if s.editor != nil {
		if s.editor.sel != nil && s.editor.sel.lines != nil && !inTray {
			s.raiseAtLocked(s.windowStart()+row, px, now)
		}
		return nil
	}
	if inTray {
		// Resting the pointer on a card raises its anchor where it is,
		// scrolling to it when it has left the screen, so a card is read
		// against what it points at without pressing anything.
		idx := row - 1 + s.trayScroll
		if idx >= 0 && idx < s.Tray.Len() {
			s.raised = nil
			s.showAnchorLocked(s.Tray.Cards[idx])
		}
		return nil
	}
	s.highlight = nil
	// Only the strip's choice cells hold the pointer: a pointer that lands
	// anywhere else on that row belongs to what the strip covers, which takes
	// the strip away and raises the block there, so a strip never stands
	// between the user and the block beneath it.
	if sr := s.stripRow(); sr >= 0 && row == sr {
		if stripHit(s.raised.left, px) != nil {
			s.raised.leftAt = time.Time{}
			return nil
		}
	}
	s.raiseAtLocked(s.windowStart()+row, px, now)
	return nil
}

// pressLocked takes the button down and holds it. Nothing is forwarded yet,
// because what the press means is not known until it comes up.
func (s *Session) pressLocked(hist, px, py, row int, inTray bool, shift bool, now time.Time, forward *[]byte, tracking bool) error {
	// A press in the tray is the tray's own, and answers at once.
	if inTray {
		return s.trayPressLocked(row, px, py)
	}
	if s.editor != nil {
		// A Shift-press on a second raised line extends to a range, and the
		// note being written follows it.
		if shift && s.editor.sel != nil && s.editor.sel.lines != nil && s.raiseShowing() {
			s.sel = s.editor.sel
			if s.extendRaisedLineLocked() {
				s.editor.first, s.editor.last = s.sel.first, s.sel.last
				s.editor.sel, s.sel = s.sel, nil
				return s.syncLocked()
			}
			s.sel = nil
		}
		// An editor with nothing typed in it yet does not stand in the way of
		// a second press or a drag: that editor is still empty, so the
		// single-press path pays nothing for the double-press one.
		if len(s.editor.text) > 0 || s.editor.editing != nil || len(s.editor.attached) > 0 {
			return nil
		}
	}
	p := &pressState{row: hist, col: px, py: py, shift: shift, count: 1}
	// A second or third press at the same cell inside the window is one
	// gesture: the word, then the whole logical line.
	if s.lastPress != nil && now.Sub(s.lastPressAt) < multiPress && s.lastPress.row == hist &&
		abs(s.lastPress.col-px) <= 1 {
		p.count = s.lastPress.count + 1
		if p.count > 3 {
			p.count = 3
		}
	}
	// The dwell is what makes a raised block Diple's: a press landing before
	// it, like every press outside a raised block, reaches the agent untouched.
	if s.raiseShowing() {
		if sr := s.stripRow(); sr >= 0 && py == s.agentToPhysical(sr) {
			p.choice = stripHit(s.raised.left, px)
			p.onRaise = p.choice != nil
		}
		if p.choice == nil && hist >= s.raised.first && hist <= s.raised.last &&
			px >= s.raised.left && px <= s.raised.right {
			p.onRaise = true
		}
	}
	s.press = p
	s.textSel = nil
	// The strip is taken off the screen the instant a drag begins, so it
	// costs no cell its ability to start one; that happens on the first
	// motion, not here.
	return s.syncLocked()
}

// dragLocked grows a selection from the press. A press that moves is a
// selection, wherever it started.
func (s *Session) dragLocked(hist, px int) error {
	p := s.press
	if !p.moved {
		p.moved = true
		// A drag takes the strip off the screen at once.
		s.raised = nil
		s.sel, s.editor = nil, nil
	}
	s.setTextSelectionLocked(p.row, p.col, hist, px)
	return s.syncLocked()
}

// releaseLocked is where a press finds out what it was.
func (s *Session) releaseLocked(hist, px, py int, forward *[]byte, tracking bool) error {
	p := s.press
	s.press = nil
	if s.drag != nil && s.drag.card >= 0 {
		return s.trayReleaseLocked(py)
	}
	if p == nil {
		return nil
	}
	s.lastPress, s.lastPressAt = p, s.clock()
	if p.moved {
		// A drag selects, and the selection is copied when the button comes
		// up: this is the copy the host used to make.
		s.setTextSelectionLocked(p.row, p.col, hist, px)
		if s.CopyOnSelect {
			if err := s.copySelectionLocked(); err != nil {
				return err
			}
		}
		// A selection that falls inside one block is also a span, so the
		// strip follows it and the drag that copied it can tag it too.
		s.spanFromSelectionLocked()
		return s.syncLocked()
	}
	switch p.count {
	case 2:
		// The editor a single press had opened is taken back by the second
		// press of a double-press, since that editor is still empty.
		s.takeBackEmptyEditorLocked()
		if s.selectWordLocked(p.row, p.col) && s.CopyOnSelect {
			if err := s.copySelectionLocked(); err != nil {
				return err
			}
		}
		return s.syncLocked()
	case 3:
		s.takeBackEmptyEditorLocked()
		if s.selectLogicalLineLocked(p.row) && s.CopyOnSelect {
			if err := s.copySelectionLocked(); err != nil {
				return err
			}
		}
		return s.syncLocked()
	}
	if p.onRaise {
		return s.pressRaisedLocked(p)
	}
	// A press on nothing of Diple's reaches the agent as the press and the
	// release it was.
	s.sel, s.textSel = nil, nil
	if s.focus == focusTray {
		s.focus = focusAgent
	}
	if tracking {
		row := hist - s.windowStart()
		*forward = append(*forward, encodeSGRMouse(mouseEvent{button: 0, x: px + 1, y: row + 1})...)
		*forward = append(*forward, encodeSGRMouse(mouseEvent{button: 0, x: px + 1, y: row + 1, release: true})...)
	}
	return s.syncLocked()
}

// pressRaisedLocked answers a press on a raised block or on one of its
// strip's choices.
func (s *Session) pressRaisedLocked(p *pressState) error {
	if s.raised == nil {
		return nil
	}
	// copy makes no card and does not touch the tray: it is the drag-free way
	// to reach the clipboard.
	if p.choice != nil && p.choice.tag == "" {
		return s.copyRaisedLocked()
	}
	// A Shift-press on a second raised line extends to a range.
	if p.choice == nil && p.shift && s.extendRaisedLineLocked() {
		return s.syncLocked()
	}
	tag := card.DefaultTag
	if p.choice != nil {
		tag = p.choice.tag
	}
	sel := s.raiseSelection()
	if p.choice == nil && s.sel != nil && s.sel.lines != nil && s.raised.line &&
		s.sel.turn == s.raised.turn {
		// Keep a range the user has already extended.
		sel = s.sel
	}
	s.raiseTag = tag
	s.openEditorLocked(tag, sel, nil)
	return s.syncLocked()
}

// copyRaisedLocked copies the raised block, the raised line, or a range of
// lines the user extended, without touching the tray.
func (s *Session) copyRaisedLocked() error {
	text := ""
	if s.sel != nil && s.sel.lines != nil && s.raised != nil && s.sel.turn == s.raised.turn {
		text = s.sel.text
	} else if s.raised != nil {
		text = s.raised.text
	}
	if text == "" {
		return nil
	}
	if err := s.copyTextLocked(text); err != nil {
		return err
	}
	return s.syncLocked()
}

// spanFromSelectionLocked turns a selection that falls inside one block into
// a span, so the strip follows it and it can be tagged.
func (s *Session) spanFromSelectionLocked() {
	if s.textSel == nil {
		return
	}
	first, firstCol, last, lastCol := s.textSel.rows(s.dropped())
	_, al := s.alignmentLocked()
	b := blockOf(al, first)
	if b == nil || last > b.Last || first < b.First {
		return
	}
	s.selectSpanLocked(first, firstCol, last, lastCol)
}

// takeBackEmptyEditorLocked closes an editor a single press had just opened
// and that has nothing in it, so the single-press path pays nothing for the
// double-press one.
func (s *Session) takeBackEmptyEditorLocked() {
	if s.editor != nil && len(s.editor.text) == 0 && s.editor.editing == nil {
		s.editor = nil
	}
	s.sel = nil
}

// trayPressLocked answers a press inside the tray: the status line's send,
// and a card, which is selected, shown, and opened for a drag to reorder.
func (s *Session) trayPressLocked(row, px, py int) error {
	if row == 0 {
		// The tray's status line carries the send at its right end and
		// answers a press on it.
		if from, to := s.sendHit(); px >= from && px <= to {
			return s.requestSendLocked(true)
		}
		return nil
	}
	idx := row - 1 + s.trayScroll
	if idx < 0 || idx >= s.Tray.Len() {
		return nil
	}
	s.focus = focusTray
	s.traySel = idx
	s.sel, s.raised = nil, nil
	// The × at a card's end deletes it outright.
	if px >= s.cols-2 {
		if s.Tray.Delete(idx) {
			_ = s.saveLocked()
		}
		if s.Tray.Len() == 0 {
			s.focus = focusAgent
		}
		s.clampTraySel()
		return s.syncLocked()
	}
	s.drag = &dragState{card: idx, row: -1}
	s.showAnchorLocked(s.Tray.Cards[idx])
	return s.syncLocked()
}

// trayReleaseLocked finishes a card drag: a release on another row reorders,
// and a release where it started opens the card for editing in place.
func (s *Session) trayReleaseLocked(py int) error {
	d := s.drag
	s.drag = nil
	if d == nil || d.card < 0 {
		return nil
	}
	row, inTray := s.physicalToAgent(py)
	if !inTray {
		return s.syncLocked()
	}
	idx := row - 1 + s.trayScroll
	if idx >= 0 && idx < s.Tray.Len() && idx != d.card {
		if s.Tray.Move(d.card, idx) {
			s.traySel = idx
			_ = s.saveLocked()
		}
		return s.syncLocked()
	}
	if d.card < s.Tray.Len() {
		c := s.Tray.Cards[d.card]
		s.openEditorLocked(c.Tag, nil, c)
	}
	return s.syncLocked()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
