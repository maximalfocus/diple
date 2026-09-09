package wrap

import (
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/fold"
)

// Bracketed-paste wrappers. Diple always brackets a fold so pasted newlines
// never submit early in the agent's input box.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// latestTurnLocked is the newest assistant turn ordinal, or 1 when the
// transcript is not available.
func (s *Session) latestTurnLocked() int {
	if tr := s.transcript(); tr != nil && len(tr.Turns) > 0 {
		return tr.Turns[len(tr.Turns)-1].Ordinal
	}
	return 1
}

// requestSendLocked handles a send (submit) or a paste-only gesture. A submit
// while the agent is busy and does not queue is held until the agent is idle;
// everything else is delivered at once.
func (s *Session) requestSendLocked(submit bool) error {
	if s.Tray.Len() == 0 {
		return nil
	}
	if submit && s.adapter != nil && !s.adapter.QueuesWhenBusy() && s.adapter.Busy(s.Model) {
		s.pendingSubmit = true
		return s.syncLocked()
	}
	return s.deliverLocked(submit)
}

// deliverLocked writes the compiled tray to the agent as a bracketed paste,
// submitting when asked. A submit then empties the tray, archives the text,
// and returns focus to the agent.
func (s *Session) deliverLocked(submit bool) error {
	text := fold.Compile(s.Tray.Cards, s.latestTurnLocked())
	out := pasteStart + text + pasteEnd
	if submit {
		out += "\r"
	}
	if err := writeAll(s.agent, []byte(out)); err != nil {
		return err
	}
	if !submit {
		return nil
	}
	if s.archive != nil {
		_ = s.archive.Append(text)
	}
	s.Tray = &card.Tray{}
	s.pendingSubmit = false
	s.focus = focusAgent
	s.traySel, s.trayScroll = 0, 0
	s.sel, s.editor = nil, nil
	if err := s.saveLocked(); err != nil {
		return err
	}
	return s.syncLocked()
}

// deliverPendingLocked delivers a held fold once the agent goes idle. It is
// called from HandleOutput after the model is updated.
func (s *Session) deliverPendingLocked() error {
	if !s.pendingSubmit || s.adapter == nil {
		return nil
	}
	if s.adapter.Busy(s.Model) {
		return nil
	}
	return s.deliverLocked(true)
}
