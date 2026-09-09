// Package wrap runs one agent session: it forwards the agent's output to the
// host terminal and the user's input to the agent, keeps the screen model
// current, and owns the mouse wheel so the user can scroll Diple's scrollback
// while the agent keeps running.
package wrap

import (
	"io"
	"strconv"
	"sync"

	"github.com/maximalfocus/diple/internal/screen"
)

// Envelope sequences: Diple's only additions to the output stream while the
// session is live. They ask the host terminal to report mouse buttons in the
// SGR encoding so Diple can own the wheel.
const (
	EnvelopeStart = "\x1b[?1000h\x1b[?1006h"
	EnvelopeEnd   = "\x1b[?1006l\x1b[?1000l"
)

// WheelLines is how many rows one wheel notch scrolls.
const WheelLines = 3

// Recorder receives the session's events for a fixture. All methods must be
// safe to call from several goroutines.
type Recorder interface {
	Output(p []byte)
	Input(p []byte)
	Resize(cols, rows int)
	Transcript(sessionID string)
}

// Session is the live/scrolled state machine between an agent and a terminal.
type Session struct {
	mu sync.Mutex

	term  io.Writer // host terminal
	agent io.Writer // agent's PTY
	Model *screen.Screen
	rec   Recorder
	// Tracker follows the session transcript when an adapter is known.
	Tracker *Tracker

	cols, rows int
	back       int // rows scrolled up from live; 0 is live
	scrolledAt uint64
	pending    []byte // incomplete mouse report waiting for its tail

	agentMouseReset bool // the agent reset a mouse mode in the last chunk
}

// NewSession wires a terminal and an agent writer to a fresh model.
func NewSession(term, agent io.Writer, cols, rows int, rec Recorder) *Session {
	s := &Session{term: term, agent: agent, Model: screen.New(cols, rows), rec: rec, cols: cols, rows: rows}
	s.Model.OnMode = s.onMode
	return s
}

func (s *Session) onMode(mode int, set bool) {
	if set {
		return
	}
	switch mode {
	case screen.ModeMouseNormal, screen.ModeMouseButton, screen.ModeMouseAny, screen.ModeMouseSGR, screen.ModeMouseX10:
		s.agentMouseReset = true
	}
}

// Start emits the envelope that lets Diple see the wheel.
func (s *Session) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeAll(s.term, []byte(EnvelopeStart))
}

// Stop returns the terminal to live if needed and emits the closing envelope.
func (s *Session) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	if s.back != 0 {
		first = s.returnToLiveLocked()
	}
	if err := writeAll(s.term, []byte(EnvelopeEnd)); err != nil && first == nil {
		first = err
	}
	return first
}

// HistoryRows returns the rows the rendering mode provides as history, as
// plain text: scrollback plus the screen inline, the visible screen when
// the agent is on the alternate screen.
func (s *Session) HistoryRows() []string {
	var rows []string
	if !s.Model.AltActive() {
		for _, l := range s.Model.History() {
			rows = append(rows, l.String())
		}
	}
	return append(rows, s.Model.Text()...)
}

// Scrolled reports whether the viewport shows scrollback rather than live.
func (s *Session) Scrolled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.back != 0
}

// HandleOutput takes one chunk from the agent. While live it is forwarded to
// the terminal unmodified before the model is updated, so forwarding never
// waits on parsing. While scrolled it only updates the model, and the
// viewport is re-anchored so the rows on screen stay put.
func (s *Session) HandleOutput(p []byte) error {
	if s.rec != nil {
		s.rec.Output(p)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.back == 0 {
		err = writeAll(s.term, p)
	}
	before := s.Model.ScrolledOff()
	s.agentMouseReset = false
	_, _ = s.Model.Write(p)
	if s.back != 0 {
		// Rows that scrolled off the top pushed our anchor further up.
		s.back += int(s.Model.ScrolledOff() - before)
		if s.back > s.Model.HistoryLen() {
			s.back = s.Model.HistoryLen()
		}
		if s.Model.AltActive() {
			// The agent switched screens under us; scrollback is meaningless
			// on the alternate screen, so fall back to live and forward.
			if e := s.returnToLiveLocked(); e != nil && err == nil {
				err = e
			}
		}
	} else if s.agentMouseReset {
		// The agent turned mouse reporting off; Diple still needs the wheel.
		if e := writeAll(s.term, []byte(EnvelopeStart)); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// HandleInput takes one chunk from the user. Wheel reports drive Diple's
// scrollback; while scrolled, End returns to live and any other key snaps to
// live first, matching what terminals do; everything else goes to the agent
// unchanged. Mouse reports that are not the wheel reach the agent only when
// it asked for mouse tracking, since an agent that did not would read them as
// keystrokes.
func (s *Session) HandleInput(p []byte) error {
	if s.rec != nil {
		// The fixture keeps what the user typed, including the gestures
		// Diple consumes, so a replay can drive the same session.
		s.rec.Input(p)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) > 0 {
		p = append(s.pending, p...)
		s.pending = nil
	}
	var forward []byte
	var err error
	for len(p) > 0 {
		if p[0] != 0x1b {
			i := 1
			for i < len(p) && p[i] != 0x1b {
				i++
			}
			forward, p = s.keys(forward, p[:i]), p[i:]
			continue
		}
		if ev, n, ok, incomplete := parseSGRMouse(p); ok {
			p = p[n:]
			if e := s.mouseLocked(ev, &forward); e != nil && err == nil {
				err = e
			}
			continue
		} else if incomplete {
			s.pending = append([]byte(nil), p...)
			break
		}
		if n, ok := parseEndKey(p); ok && s.back != 0 {
			p = p[n:]
			if e := s.returnToLiveLocked(); e != nil && err == nil {
				err = e
			}
			continue
		}
		// Any other escape sequence or a bare ESC: forward up to the next ESC.
		i := 1
		for i < len(p) && p[i] != 0x1b {
			i++
		}
		forward, p = s.keys(forward, p[:i]), p[i:]
	}
	if len(forward) > 0 {
		if s.back != 0 {
			if e := s.returnToLiveLocked(); e != nil && err == nil {
				err = e
			}
		}
		if e := writeAll(s.agent, forward); e != nil && err == nil {
			err = e
		}
	}
	return err
}

func (s *Session) keys(forward, chunk []byte) []byte {
	return append(forward, chunk...)
}

func (s *Session) mouseLocked(ev mouseEvent, forward *[]byte) error {
	tracking := s.Model.MouseTracking()
	if ev.isWheel() && !(tracking && s.Model.AltActive()) {
		if ev.release {
			return nil
		}
		if ev.wheelUp() {
			return s.scrollLocked(WheelLines)
		}
		return s.scrollLocked(-WheelLines)
	}
	if tracking {
		*forward = append(*forward, encodeSGRMouse(ev)...)
	}
	return nil
}

func encodeSGRMouse(ev mouseEvent) []byte {
	b := append([]byte("\x1b[<"), strconv.Itoa(ev.button)...)
	b = append(b, ';')
	b = append(b, strconv.Itoa(ev.x)...)
	b = append(b, ';')
	b = append(b, strconv.Itoa(ev.y)...)
	if ev.release {
		return append(b, 'm')
	}
	return append(b, 'M')
}

// Scroll moves the viewport by delta rows (positive is up into history).
func (s *Session) Scroll(delta int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scrollLocked(delta)
}

func (s *Session) scrollLocked(delta int) error {
	target := s.back + delta
	if target < 0 {
		target = 0
	}
	if h := s.Model.HistoryLen(); target > h {
		target = h
	}
	if target == s.back {
		return nil
	}
	if target == 0 {
		return s.returnToLiveLocked()
	}
	s.back = target
	return s.paintViewportLocked()
}

// ReturnToLive shows the live screen again.
func (s *Session) ReturnToLive() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.back == 0 {
		return nil
	}
	return s.returnToLiveLocked()
}

// Resize records a new terminal size in the model and repaints if scrolled.
func (s *Session) Resize(cols, rows int) error {
	if s.rec != nil {
		s.rec.Resize(cols, rows)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cols, s.rows = cols, rows
	s.Model.Resize(cols, rows)
	if s.back == 0 {
		return nil
	}
	if s.back > s.Model.HistoryLen() {
		s.back = s.Model.HistoryLen()
	}
	return s.paintViewportLocked()
}

// paintViewportLocked redraws the whole terminal from scrollback. Every row
// is emitted with its original attributes and the cursor is hidden, since it
// belongs to the live screen.
func (s *Session) paintViewportLocked() error {
	rows := s.Model.Viewport(s.back)
	buf := make([]byte, 0, 64*len(rows)*s.cols)
	buf = append(buf, "\x1b[?25l"...)
	for i, l := range rows {
		buf = append(buf, "\x1b["...)
		buf = append(buf, strconv.Itoa(i+1)...)
		buf = append(buf, ";1H"...)
		buf = l.AppendEmit(buf)
	}
	return writeAll(s.term, buf)
}

// returnToLiveLocked repaints the live screen from the model and restores
// the terminal state the agent expects: cursor position, visibility, style,
// current attribute, and the modes it may have changed while scrolled.
func (s *Session) returnToLiveLocked() error {
	s.back = 0
	m := s.Model
	rows := m.Rows()
	buf := make([]byte, 0, 64*len(rows)*s.cols)
	buf = append(buf, "\x1b[?25l"...)
	for i, l := range rows {
		buf = append(buf, "\x1b["...)
		buf = append(buf, strconv.Itoa(i+1)...)
		buf = append(buf, ";1H"...)
		buf = l.AppendEmit(buf)
	}
	x, y := m.Cursor()
	buf = append(buf, "\x1b["...)
	buf = append(buf, strconv.Itoa(y+1)...)
	buf = append(buf, ';')
	buf = append(buf, strconv.Itoa(x+1)...)
	buf = append(buf, 'H')
	buf = append(buf, m.Attr().SGR()...)
	for _, mode := range []int{screen.ModeAppCursorKeys, screen.ModeBracketedPaste, screen.ModeMouseNormal,
		screen.ModeMouseButton, screen.ModeMouseAny, screen.ModeMouseSGR, screen.ModeMouseFocus} {
		if m.Mode(mode) {
			buf = append(buf, "\x1b[?"...)
			buf = append(buf, strconv.Itoa(mode)...)
			buf = append(buf, 'h')
		}
	}
	// Diple's own envelope must survive whatever the agent set.
	buf = append(buf, EnvelopeStart...)
	if st := m.CursorStyle(); st != 0 {
		buf = append(buf, "\x1b["...)
		buf = append(buf, strconv.Itoa(st)...)
		buf = append(buf, " q"...)
	}
	if m.CursorVisible() {
		buf = append(buf, "\x1b[?25h"...)
	}
	return writeAll(s.term, buf)
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}
