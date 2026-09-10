// Package wrap runs one agent session: it forwards the agent's output to the
// host terminal and the user's input to the agent, keeps the screen model
// current, owns the mouse wheel so the user can scroll Diple's scrollback
// while the agent keeps running, and, once the user points at a block,
// draws the selection, toolbar, editor, and tray over a composited screen.
package wrap

import (
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/clip"
	"github.com/maximalfocus/diple/internal/fold"
	"github.com/maximalfocus/diple/internal/keys"
	"github.com/maximalfocus/diple/internal/screen"
)

// foldArchive is the tray's project-local archive.
type foldArchive = fold.Archive

// Envelope sequences: Diple's only additions to the output stream while the
// session is live. They ask the host terminal to report mouse buttons and
// drags in the SGR encoding so Diple can own the wheel and see gestures.
// Motion reporting is what lets Diple know what is under the pointer, which
// is what the raise is made of; it is forwarded unchanged to an agent that
// asked for its own.
const (
	EnvelopeStart = "\x1b[?1003h\x1b[?1006h"
	EnvelopeEnd   = "\x1b[?1006l\x1b[?1003l"
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
	// Plain drops colour from Diple's drawing, leaving the bold, dim,
	// reverse, and underline attributes.
	Plain bool
	// Marks enables the tail mark on anchored blocks, and the raise with it.
	Marks bool
	// NoMotion draws the raise's final frame only.
	NoMotion bool
	// CopyOnSelect copies a selection the moment the button comes up, which
	// is the copy the host used to make and Diple must not cost the user.
	CopyOnSelect bool
	// Clip writes the system clipboard. Diple never reads it.
	Clip *clip.Writer
	// Keys is the binding table this session answers to.
	Keys keys.Table
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
	// raised is the block the pointer rests on, and raiseTag is the tag in
	// force, which the strip underlines and the editor's chip names.
	raised   *raised
	raiseTag card.Tag
	// textSel is a drag selection, which may cross blocks and rows.
	textSel *textSelection
	// press is the button that is down, held until it comes up: a press that
	// moves is a selection, and only one that comes up where it went down is
	// a press on what lies under it. lastPress is the one before it, so a
	// second and third press at the same cell read as one gesture.
	press       *pressState
	lastPress   *pressState
	lastPressAt time.Time
	// clipNote is what the tray status line says when the clipboard ladder
	// reached no rung. copies and lastCopy are what the session put on the
	// clipboard, which a host check reads to prove the gesture arrived.
	clipNote string
	copies   int
	lastCopy string
	editor   *editor
	// suspended holds the editor put away while the agent shows a native
	// prompt, so the note comes back exactly as it was.
	suspended *editor
	prompting bool
	chooser   bool // the free-card kind chooser is showing
	// hidden is the session hotkey's state: the layer is out of the way
	// until it is pressed again, and the tray keeps its cards meanwhile.
	hidden     bool
	search     *searchField
	nav        *navRequest
	focus      focusKind
	traySel    int
	trayScroll int
	highlight  *rowRange
	drag       *dragState

	pendingSubmit bool
	// now is the session's clock, which tests replace to drive the raise.
	now     func() time.Time
	archive *foldArchive
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
		Tray: &card.Tray{}, Marks: true, CopyOnSelect: true, Keys: keys.Defaults(),
		raiseTag: card.DefaultTag, now: time.Now}
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
		s.raised, s.textSel, s.press = nil, nil, nil
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

// clock is the session's own time, which tests replace to drive the raise's
// two frames without waiting for them.
func (s *Session) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// SetClock replaces the session clock, for tests that drive the raise.
func (s *Session) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// Tick advances the raise's frames and expires one the pointer has left. The
// wrapped session calls it on a short timer; it never delays a forwarded byte,
// because it only ever repaints what Diple itself owns.
func (s *Session) Tick() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.raiseTick(s.clock()) {
		return nil
	}
	return s.syncLocked()
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

// Copies reports how many times this session put text on the clipboard, and
// LastCopy what it put there last.
func (s *Session) Copies() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copies
}

// LastCopy is the text of the session's most recent copy.
func (s *Session) LastCopy() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCopy
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
	s.updatePromptLocked()
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
		// Alt gestures: the table's Alt bindings, taken only while Diple
		// has no field of its own open and the agent is not asking a
		// question of its own.
		if n, handled, e := s.altGestureLocked(p); handled {
			p = p[n:]
			if e != nil && err == nil {
				err = e
			}
			continue
		}
		// A bracketed paste follows focus. Diple never reads the clipboard:
		// a paste is text the user's own terminal sent because the user
		// asked it to.
		if text, n, ok, incomplete := parsePaste(p); ok {
			p = p[n:]
			if s.pasteLocked(text) {
				continue
			}
			forward = append(forward, pasteStart...)
			forward = append(forward, text...)
			forward = append(forward, pasteEnd...)
			continue
		} else if incomplete && len(p) >= 2 {
			s.pending = append([]byte(nil), p...)
			break
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

// pasteLocked routes a bracketed paste by focus: the native box keeps every
// paste while it has focus, an open editor takes it into its text, and a
// paste arriving on a focused tray with no editor open becomes one free card
// holding it, fenced when it carries more than one line. It reports whether
// Diple took the paste.
func (s *Session) pasteLocked(text string) bool {
	if s.prompting || s.hidden {
		return false
	}
	if s.editor != nil {
		// A pasted newline never submits anything: it joins the text.
		for _, r := range text {
			if r == '\r' || r == '\n' {
				s.editor.text = append(s.editor.text, '\n')
				continue
			}
			if r >= 0x20 {
				s.editor.text = append(s.editor.text, r)
			}
		}
		return true
	}
	if s.focus != focusTray {
		return false
	}
	body := strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if strings.TrimSpace(body) == "" {
		return true
	}
	s.Tray.Add(&card.Card{Kind: card.Free, Text: body, Fenced: strings.Contains(body, "\n")})
	s.clampTraySel()
	_ = s.saveLocked()
	return true
}

// keysLocked routes one unit of keyboard input: to the editor, the toolbar,
// the tray, or the agent.
func (s *Session) keysLocked(forward, chunk []byte) []byte {
	s.highlight = nil
	// A native prompt owns the keyboard until it is answered, and a hidden
	// layer owns nothing at all.
	if s.prompting || s.hidden {
		return append(forward, chunk...)
	}
	if s.editor != nil {
		s.editorKeysLocked(chunk)
		return forward
	}
	if s.search != nil {
		s.setKeyErr(s.searchKeysLocked(chunk))
		return forward
	}
	// Any other key ends a jump the agent has not finished scrolling.
	s.nav = nil
	k, single := keyOf(chunk)
	if single && s.navOwnedLocked() {
		switch {
		case s.Keys.Is(keys.PrevTurn, k):
			s.setKeyErr(s.jumpTurnLocked(-1))
			return forward
		case s.Keys.Is(keys.NextTurn, k):
			s.setKeyErr(s.jumpTurnLocked(1))
			return forward
		case s.Keys.Is(keys.Search, k):
			s.openSearchLocked()
			return forward
		}
	}
	// Tab moves focus between the native box and the tray whatever else is
	// showing, so a raised block never stands in the way of the tray.
	if single && s.Keys.Is(keys.TrayFocus, k) && s.Tray.Len() > 0 && s.focus != focusTray {
		s.sel, s.raised, s.textSel = nil, nil, nil
		s.focus = focusTray
		s.clampTraySel()
		return forward
	}
	switch {
	case s.sel != nil || s.raised != nil || s.textSel != nil:
		if single && s.sel != nil && s.selectionKeyLocked(k) {
			return forward
		}
		if s.stripKeyLocked(chunk) {
			return forward
		}
		// With nothing of Diple's open, Esc reaches the agent unchanged, and
		// so does a second one in every case.
		s.sel, s.raised, s.textSel = nil, nil, nil
		return append(forward, chunk...)
	case s.focus == focusTray:
		s.trayKeysLocked(chunk)
		return forward
	}
	if single && s.Keys.Is(keys.TrayFocus, k) && s.Tray.Len() > 0 {
		s.focus = focusTray
		s.clampTraySel()
		return forward
	}
	return append(forward, chunk...)
}

// keyOf reads one input unit as a single key, which is what a binding is.
func keyOf(chunk []byte) (keys.Key, bool) {
	r, size := utf8.DecodeRune(chunk)
	if size != len(chunk) || r == utf8.RuneError {
		return keys.Key{}, false
	}
	return keys.Key{Rune: r}, true
}

// altGestureLocked takes an ESC-prefixed key from the head of p and runs the
// gesture the table binds it to. It reports how many bytes it consumed and
// whether it handled anything at all.
func (s *Session) altGestureLocked(p []byte) (int, bool, error) {
	if s.prompting || s.search != nil || len(p) < 2 || p[0] != 0x1b {
		return 0, false, nil
	}
	// A CSI or SS3 sequence is a key of the terminal's own, never Alt.
	if p[1] == '[' || p[1] == 'O' {
		return 0, false, nil
	}
	r, size := utf8.DecodeRune(p[1:])
	if size == 0 || r == utf8.RuneError {
		return 0, false, nil
	}
	k := keys.Key{Rune: r, Alt: true}
	n := 1 + size
	// Inside the editor, Alt belongs to the chips and to the send that saves
	// the card first.
	if s.editor != nil {
		if handled, err := s.editorAltLocked(k); handled {
			return n, true, err
		}
		return 0, false, nil
	}
	// Hiding is the one gesture a hidden layer still answers to.
	if s.Keys.Is(keys.Hide, k) {
		s.hidden = !s.hidden
		var err error
		if s.hidden {
			s.sel, s.search, s.highlight = nil, nil, nil
			s.raised, s.textSel, s.press, s.editor = nil, nil, nil, nil
			s.focus = focusAgent
			// Hiding gives the mouse back to the host entirely, so the
			// host's own selection behaves exactly as it does without
			// Diple. Only the modes the agent asked for stay on.
			err = s.releaseMouseLocked()
		} else {
			err = writeAll(s.term, []byte(EnvelopeStart))
		}
		return n, true, err
	}
	if s.hidden {
		return 0, false, nil
	}
	switch {
	case s.Keys.Is(keys.FreeCard, k):
		s.openFreeEditorLocked(false)
		return n, true, nil
	case s.Keys.Is(keys.OverallCard, k):
		s.openFreeEditorLocked(true)
		return n, true, nil
	case s.Keys.Is(keys.SelectBlock, k):
		s.selectTopBlockLocked()
		return n, true, nil
	case s.Tray.Len() > 0 && s.Keys.Is(keys.Send, k):
		return n, true, s.requestSendLocked(true)
	case s.Tray.Len() > 0 && s.Keys.Is(keys.Paste, k):
		return n, true, s.requestSendLocked(false)
	}
	return 0, false, nil
}

// updatePromptLocked follows the agent's own dialogs. While one is showing
// Diple owns nothing: the editor is put away with its text, the selection,
// search field and chooser are dismissed, and focus goes back to the native
// box. When it clears, a put-away editor comes back.
func (s *Session) updatePromptLocked() {
	if s.adapter == nil {
		return
	}
	prompting := s.adapter.Prompt(s.Model)
	if prompting == s.prompting {
		return
	}
	s.prompting = prompting
	if prompting {
		if s.editor != nil {
			s.suspended, s.editor = s.editor, nil
		}
		s.sel, s.search, s.raised, s.textSel = nil, nil, nil, nil
		s.focus = focusAgent
		return
	}
	if s.suspended != nil {
		s.editor, s.suspended = s.suspended, nil
	}
}

// releaseMouseLocked turns Diple's mouse reporting off and re-asserts only
// the modes the wrapped agent asked for, so the host owns the pointer again.
func (s *Session) releaseMouseLocked() error {
	buf := []byte(EnvelopeEnd)
	for _, mode := range []int{screen.ModeMouseNormal, screen.ModeMouseButton, screen.ModeMouseAny, screen.ModeMouseSGR} {
		if s.Model.Mode(mode) {
			buf = append(buf, "\x1b[?"...)
			buf = append(buf, strconv.Itoa(mode)...)
			buf = append(buf, 'h')
		}
	}
	return writeAll(s.term, buf)
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
