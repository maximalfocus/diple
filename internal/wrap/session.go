// Package wrap runs one agent session: it forwards the agent's output to the
// host terminal and the user's input to the agent, keeps the screen model
// current, owns the mouse wheel so the user can scroll Diple's scrollback
// while the agent keeps running, and, once the user points at a block,
// draws the selection, toolbar, editor, and tray over a composited screen.
package wrap

import (
	"io"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/fold"
	"github.com/maximalfocus/diple/internal/screen"
)

// foldArchive is the tray's project-local archive.
type foldArchive = fold.Archive

// Envelope sequences: Diple's only additions to the output stream while the
// session is live. They ask the host terminal to report mouse buttons and
// drags in the SGR encoding so Diple can own the wheel and see gestures.
const (
	EnvelopeStart = "\x1b[?1002h\x1b[?1006h"
	EnvelopeEnd   = "\x1b[?1006l\x1b[?1002l"
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

// TranscriptSource provides the latest parsed transcript.
type TranscriptSource interface {
	Transcript() (*adapter.Transcript, error)
}

// Session is the state machine between an agent and a terminal. It is
// pass-through while it owns nothing on screen, and composited once a
// selection, overlay, or tray is showing.
type Session struct {
	mu sync.Mutex

	term  io.Writer // host terminal
	agent io.Writer // agent's PTY
	Model *screen.Screen
	rec   Recorder
	// Tracker follows the session transcript when an adapter is known.
	Tracker *Tracker
	// Source overrides Tracker as the transcript provider, for tests.
	Source TranscriptSource

	adapter   adapter.Adapter
	agentName string
	sessionID string
	// cwd is the session's working directory, where a command attachment
	// runs and where the archive lives.
	cwd   string
	store *card.Store
	// Tray is the session's cards.
	Tray *card.Tray
	// Plain restricts Diple's drawing to reverse and underline.
	Plain bool
	// Marks enables the gutter mark on anchored blocks.
	Marks bool
	// SetPTYRows, when set, is called with the row count the wrapped
	// process should see whenever the tray changes height.
	SetPTYRows func(rows int)

	cols, rows int // physical terminal size
	back       int // rows scrolled up from live; 0 is live
	pending    []byte

	agentMouseReset bool

	// Composited state.
	composited bool
	painted    []screen.Line
	trayH      int
	sel        *selection
	editor     *editor
	chooser    bool // the free-card kind chooser is showing
	search     *searchField
	nav        *navRequest
	focus      focusKind
	traySel    int
	trayScroll int
	highlight  *rowRange
	drag       *dragState

	pendingSubmit bool
	archive       *foldArchive
	// keyErr carries an error raised while handling a key Diple consumed,
	// which has no return path of its own.
	keyErr error
}

type focusKind int

const (
	focusAgent focusKind = iota
	focusTray
)

type rowRange struct {
	first, last int // history rows
}

// NewSession wires a terminal and an agent writer to a fresh model.
func NewSession(term, agent io.Writer, cols, rows int, rec Recorder) *Session {
	s := &Session{term: term, agent: agent, Model: screen.New(cols, rows), rec: rec, cols: cols, rows: rows,
		Tray: &card.Tray{}, Marks: true}
	s.Model.OnMode = s.onMode
	return s
}

// UseAdapter attaches the adapter that knows the agent's rendering.
func (s *Session) UseAdapter(a adapter.Adapter, agentName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adapter = a
	s.agentName = agentName
}

// UseStore attaches tray persistence.
func (s *Session) UseStore(st *card.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = st
}

// UseDir records the session's working directory.
func (s *Session) UseDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cwd = dir
}

// UseArchive attaches the sent-fold archive.
func (s *Session) UseArchive(a *fold.Archive) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.archive = a
}

// SetSessionID binds the session to its transcript id: a stored tray for
// that session is restored, and cards made before discovery are kept.
func (s *Session) SetSessionID(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
	if s.store != nil && id != "" {
		stored, err := s.store.Load(s.agentName, id)
		if err != nil {
			return err
		}
		if stored.Len() > 0 {
			for _, c := range s.Tray.Cards {
				stored.Add(c)
			}
			s.Tray = stored
		}
	}
	err := s.saveLocked()
	if e := s.syncLocked(); e != nil && err == nil {
		err = e
	}
	return err
}

func (s *Session) saveLocked() error {
	if s.store == nil {
		return nil
	}
	return s.store.Save(s.agentName, s.sessionID, s.Tray)
}

func (s *Session) transcript() *adapter.Transcript {
	var src TranscriptSource
	if s.Source != nil {
		src = s.Source
	} else if s.Tracker != nil {
		src = s.Tracker
	}
	if src == nil {
		return nil
	}
	tr, _ := src.Transcript()
	return tr
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

// Start emits the envelope that lets Diple see the mouse.
func (s *Session) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeAll(s.term, []byte(EnvelopeStart))
}

// Stop returns the terminal to the agent's live state and emits the
// closing envelope.
func (s *Session) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	if s.composited {
		s.sel, s.editor, s.highlight, s.drag = nil, nil, nil, nil
		s.composited, s.painted = false, nil
		first = s.returnToLiveLocked()
	} else if s.back != 0 {
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

// AgentRows returns the row count the wrapped process is given: the
// terminal's rows less the tray.
func (s *Session) AgentRows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows - s.trayH
}

// Scrolled reports whether the viewport shows scrollback rather than live.
func (s *Session) Scrolled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.back != 0
}

// Composited reports whether Diple owns the screen painting.
func (s *Session) Composited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.composited
}

// HandleOutput takes one chunk from the agent. While pass-through and live
// it is forwarded to the terminal unmodified before the model is updated,
// so forwarding never waits on parsing. While scrolled it only updates the
// model, re-anchoring the viewport so the rows on screen stay put. While
// composited the model is updated and only the rows that changed are
// repainted.
func (s *Session) HandleOutput(p []byte) error {
	if s.rec != nil {
		s.rec.Output(p)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.back == 0 && !s.composited {
		err = writeAll(s.term, p)
	}
	before := s.Model.ScrolledOff()
	s.agentMouseReset = false
	_, _ = s.Model.Write(p)
	if s.back != 0 {
		s.back += int(s.Model.ScrolledOff() - before)
		if s.back > s.Model.HistoryLen() {
			s.back = s.Model.HistoryLen()
		}
		if s.Model.AltActive() {
			// Scrollback is meaningless on the alternate screen.
			if e := s.returnToLiveLocked(); e != nil && err == nil {
				err = e
			}
		}
	}
	if s.agentMouseReset {
		if e := writeAll(s.term, []byte(EnvelopeStart)); e != nil && err == nil {
			err = e
		}
	}
	if e := s.driveNavLocked(); e != nil && err == nil {
		err = e
	}
	if e := s.deliverPendingLocked(); e != nil && err == nil {
		err = e
	}
	if e := s.syncLocked(); e != nil && err == nil {
		err = e
	}
	return err
}

// HandleInput takes one chunk from the user. Diple's own gestures and, while
// the editor or tray has focus, keys are consumed; everything else goes to
// the agent unchanged. Mouse reports that are not Diple's reach the agent
// only when it asked for mouse tracking, with rows mapped past the tray.
func (s *Session) HandleInput(p []byte) error {
	if s.rec != nil {
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
			forward = s.keysLocked(forward, p[:i])
			p = p[i:]
			continue
		}
		// Alt+N opens the free-card chooser whether or not the tray has
		// cards, which is the only way to write the first one.
		if s.editor == nil && s.search == nil && len(p) >= 2 && p[0] == 0x1b && p[1] == 'n' {
			p = p[2:]
			s.chooser = true
			s.sel = nil
			continue
		}
		if s.editor == nil && s.search == nil && s.Tray.Len() > 0 && len(p) >= 2 && p[0] == 0x1b {
			if p[1] == '\r' || p[1] == '\n' {
				p = p[2:]
				if e := s.requestSendLocked(true); e != nil && err == nil {
					err = e
				}
				continue
			}
			if p[1] == 'p' {
				p = p[2:]
				if e := s.requestSendLocked(false); e != nil && err == nil {
					err = e
				}
				continue
			}
		}
		if ev, n, ok, incomplete := parseSGRMouse(p); ok {
			p = p[n:]
			if e := s.mouseLocked(ev, &forward); e != nil && err == nil {
				err = e
			}
			continue
		} else if incomplete && len(p) >= 2 {
			// A partial mouse report waits for its tail; a bare ESC is a
			// key press and is delivered at once.
			s.pending = append([]byte(nil), p...)
			break
		}
		if n, ok := parseEndKey(p); ok && s.back != 0 && s.editor == nil && s.focus == focusAgent {
			p = p[n:]
			if e := s.returnToLiveLocked(); e != nil && err == nil {
				err = e
			}
			continue
		}
		// Any other escape sequence or a bare ESC: one unit up to the next ESC.
		i := 1
		for i < len(p) && p[i] != 0x1b {
			i++
		}
		forward = s.keysLocked(forward, p[:i])
		p = p[i:]
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
	if s.keyErr != nil && err == nil {
		err = s.keyErr
	}
	s.keyErr = nil
	if e := s.syncLocked(); e != nil && err == nil {
		err = e
	}
	return err
}

// keysLocked routes one unit of keyboard input: to the editor, the toolbar,
// the tray, or the agent.
func (s *Session) keysLocked(forward, chunk []byte) []byte {
	s.highlight = nil
	if s.editor != nil {
		s.editorKeysLocked(chunk)
		return forward
	}
	if s.search != nil {
		s.setKeyErr(s.searchKeysLocked(chunk))
		return forward
	}
	// The kind chooser takes one key, whatever has focus; anything typed
	// behind it in the same chunk goes on to the editor it opened.
	if s.chooser {
		s.chooser = false
		r, size := utf8.DecodeRune(chunk)
		if k := card.KindByLetter(r); k != "" {
			s.openFreeEditorLocked(k)
		}
		if len(chunk) > size {
			return s.keysLocked(forward, chunk[size:])
		}
		return forward
	}
	// Any other key ends a jump the agent has not finished scrolling.
	s.nav = nil
	if len(chunk) == 1 && s.navOwnedLocked() {
		switch chunk[0] {
		case '[':
			s.setKeyErr(s.jumpTurnLocked(-1))
			return forward
		case ']':
			s.setKeyErr(s.jumpTurnLocked(1))
			return forward
		case '/':
			s.openSearchLocked()
			return forward
		}
	}
	switch {
	case s.sel != nil:
		if s.toolbarKeyLocked(chunk) {
			return forward
		}
		s.sel = nil
		return append(forward, chunk...)
	case s.focus == focusTray:
		s.trayKeysLocked(chunk)
		return forward
	}
	if len(chunk) == 1 && chunk[0] == '\t' && s.Tray.Len() > 0 {
		s.focus = focusTray
		s.clampTraySel()
		return forward
	}
	return append(forward, chunk...)
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
	if err := s.scrollLocked(delta); err != nil {
		return err
	}
	return s.syncLocked()
}

func (s *Session) scrollLocked(delta int) error {
	target := s.back + delta
	if target < 0 {
		target = 0
	}
	if h := s.Model.HistoryLen(); target > h {
		target = h
	}
	if s.Model.AltActive() {
		target = 0
	}
	if target == s.back {
		return nil
	}
	if target == 0 {
		return s.returnToLiveLocked()
	}
	s.back = target
	if s.composited {
		return nil // syncLocked repaints
	}
	return s.paintViewportLocked()
}

// ReturnToLive shows the live screen again.
func (s *Session) ReturnToLive() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.back == 0 {
		return nil
	}
	if err := s.returnToLiveLocked(); err != nil {
		return err
	}
	return s.syncLocked()
}

// Resize records a new terminal size in the model and repaints if needed.
func (s *Session) Resize(cols, rows int) error {
	if s.rec != nil {
		s.rec.Resize(cols, rows)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cols, s.rows = cols, rows
	s.Model.Resize(cols, rows-s.trayH)
	if s.back > s.Model.HistoryLen() {
		s.back = s.Model.HistoryLen()
	}
	s.painted = nil
	if s.composited {
		return s.syncLocked()
	}
	if s.back == 0 {
		return nil
	}
	return s.paintViewportLocked()
}

// paintViewportLocked redraws the whole terminal from scrollback in
// pass-through mode. Every row is emitted with its original attributes and
// the cursor is hidden, since it belongs to the live screen.
func (s *Session) paintViewportLocked() error {
	rows := s.Model.Viewport(s.back)
	buf := make([]byte, 0, 64*len(rows)*s.cols)
	buf = append(buf, "\x1b[?25l"...)
	for i, l := range rows {
		buf = appendRowAt(buf, i, l)
	}
	return writeAll(s.term, buf)
}

// returnToLiveLocked repaints the live screen from the model and restores
// the terminal state the agent expects: cursor position, visibility, style,
// current attribute, and the modes it may have changed meanwhile.
func (s *Session) returnToLiveLocked() error {
	s.back = 0
	if s.composited {
		s.painted = nil
		return nil // syncLocked repaints
	}
	m := s.Model
	rows := m.Rows()
	buf := make([]byte, 0, 64*len(rows)*s.cols)
	buf = append(buf, "\x1b[?25l"...)
	for i, l := range rows {
		buf = appendRowAt(buf, i, l)
	}
	x, y := m.Cursor()
	buf = appendCursor(buf, x, y)
	buf = append(buf, m.Attr().SGR()...)
	buf = appendModes(buf, m)
	return writeAll(s.term, buf)
}

func appendRowAt(buf []byte, row int, l screen.Line) []byte {
	buf = append(buf, "\x1b["...)
	buf = append(buf, strconv.Itoa(row+1)...)
	buf = append(buf, ";1H"...)
	return l.AppendEmit(buf)
}

func appendCursor(buf []byte, x, y int) []byte {
	buf = append(buf, "\x1b["...)
	buf = append(buf, strconv.Itoa(y+1)...)
	buf = append(buf, ';')
	buf = append(buf, strconv.Itoa(x+1)...)
	return append(buf, 'H')
}

// appendModes re-asserts the agent's DEC modes and cursor state, then
// Diple's own envelope, which must survive whatever the agent set.
func appendModes(buf []byte, m *screen.Screen) []byte {
	for _, mode := range []int{screen.ModeAppCursorKeys, screen.ModeBracketedPaste, screen.ModeMouseNormal,
		screen.ModeMouseButton, screen.ModeMouseAny, screen.ModeMouseSGR, screen.ModeMouseFocus} {
		if m.Mode(mode) {
			buf = append(buf, "\x1b[?"...)
			buf = append(buf, strconv.Itoa(mode)...)
			buf = append(buf, 'h')
		}
	}
	buf = append(buf, EnvelopeStart...)
	if st := m.CursorStyle(); st != 0 {
		buf = append(buf, "\x1b["...)
		buf = append(buf, strconv.Itoa(st)...)
		buf = append(buf, " q"...)
	}
	if m.CursorVisible() {
		buf = append(buf, "\x1b[?25h"...)
	}
	return buf
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
