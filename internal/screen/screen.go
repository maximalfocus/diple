package screen

import (
	"sync"
	"unicode/utf8"
)

// DefaultMaxScrollback bounds the scrollback a Screen retains.
const DefaultMaxScrollback = 50000

// DEC private modes the model tracks because Diple must restore them on the
// host terminal after showing scrollback.
const (
	ModeAppCursorKeys  = 1
	ModeOrigin         = 6
	ModeAutoWrap       = 7
	ModeCursorVisible  = 25
	ModeAltScreen47    = 47
	ModeMouseX10       = 9
	ModeMouseNormal    = 1000
	ModeMouseButton    = 1002
	ModeMouseAny       = 1003
	ModeMouseFocus     = 1004
	ModeMouseUTF8      = 1005
	ModeMouseSGR       = 1006
	ModeMouseURXVT     = 1015
	ModeMouseSGRPixels = 1016
	ModeAltScreen1047  = 1047
	ModeSaveCursor1048 = 1048
	ModeAltScreen1049  = 1049
	ModeBracketedPaste = 2004
)

type cursor struct {
	x, y int
}

type buffer struct {
	lines []Line
}

// Screen is the VT screen model. It is safe for one writer and any number of
// readers: Write mutates under the lock and every accessor takes it.
type Screen struct {
	mu sync.Mutex

	cols, rows int

	main, alt  buffer
	altActive  bool
	scrollback []Line
	maxScroll  int
	scrolled   uint64 // lines ever pushed to scrollback

	cur         cursor
	savedMain   savedState
	savedAlt    savedState
	attr        Attr
	pendingWrap bool
	top, bottom int // scroll region, inclusive rows

	modes       map[int]bool
	cursorStyle int

	// OnMode, when set, is called after a DEC private mode is set or reset.
	OnMode func(mode int, set bool)

	p parser
}

type savedState struct {
	cur   cursor
	attr  Attr
	valid bool
}

// New returns an empty screen of the given size with the default modes set.
func New(cols, rows int) *Screen {
	s := &Screen{maxScroll: DefaultMaxScrollback, modes: map[int]bool{}}
	s.modes[ModeAutoWrap] = true
	s.modes[ModeCursorVisible] = true
	s.reset(cols, rows)
	return s
}

func (s *Screen) reset(cols, rows int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	s.cols, s.rows = cols, rows
	s.main = buffer{lines: make([]Line, rows)}
	s.alt = buffer{lines: make([]Line, rows)}
	for i := range s.main.lines {
		s.main.lines[i] = newLine(cols, Attr{})
		s.alt.lines[i] = newLine(cols, Attr{})
	}
	s.cur = cursor{}
	s.attr = Attr{}
	s.top, s.bottom = 0, rows-1
	s.pendingWrap = false
}

// Size returns the screen size in columns and rows.
func (s *Screen) Size() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// Cursor returns the cursor position, zero-based.
func (s *Screen) Cursor() (x, y int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.x, s.cur.y
}

// CursorVisible reports whether the wrapped process has the cursor shown.
func (s *Screen) CursorVisible() bool { return s.Mode(ModeCursorVisible) }

// CursorStyle returns the last DECSCUSR style requested, 0 when none.
func (s *Screen) CursorStyle() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursorStyle
}

// Attr returns the current SGR attribute of the wrapped process.
func (s *Screen) Attr() Attr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attr
}

// Mode reports whether DEC private mode m is set.
func (s *Screen) Mode(m int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modes[m]
}

// AltActive reports whether the alternate screen is active.
func (s *Screen) AltActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.altActive
}

// MouseTracking reports whether the wrapped process asked for mouse events.
func (s *Screen) MouseTracking() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modes[ModeMouseX10] || s.modes[ModeMouseNormal] || s.modes[ModeMouseButton] || s.modes[ModeMouseAny]
}

// ScrolledOff returns how many lines have ever been pushed into scrollback.
func (s *Screen) ScrolledOff() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scrolled
}

// HistoryLen returns the number of scrollback rows retained.
func (s *Screen) HistoryLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.scrollback)
}

// Row returns a copy of visible row y of the active buffer.
func (s *Screen) Row(y int) Line {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active().lines[y].Clone(s.cols)
}

// Rows returns copies of every visible row of the active buffer.
func (s *Screen) Rows() []Line {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Line, s.rows)
	for i, l := range s.active().lines {
		out[i] = l.Clone(s.cols)
	}
	return out
}

// History returns copies of the scrollback rows, oldest first.
func (s *Screen) History() []Line {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Line, len(s.scrollback))
	for i, l := range s.scrollback {
		out[i] = l.Clone(s.cols)
	}
	return out
}

// Viewport returns rows of the combined scrollback and main screen, ending
// `back` rows above the live bottom. back=0 is the live screen. The result
// always has exactly `rows` entries; rows above the start of history are
// blank.
func (s *Screen) Viewport(back int) []Line {
	s.mu.Lock()
	defer s.mu.Unlock()
	if back < 0 {
		back = 0
	}
	if back > len(s.scrollback) {
		back = len(s.scrollback)
	}
	total := len(s.scrollback) + s.rows
	end := total - back
	start := end - s.rows
	out := make([]Line, 0, s.rows)
	for i := start; i < end; i++ {
		switch {
		case i < 0:
			out = append(out, newLine(s.cols, Attr{}))
		case i < len(s.scrollback):
			out = append(out, s.scrollback[i].Clone(s.cols))
		default:
			out = append(out, s.main.lines[i-len(s.scrollback)].Clone(s.cols))
		}
	}
	return out
}

// Text returns the visible rows as trimmed strings, for tests and tooling.
func (s *Screen) Text() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, s.rows)
	for i, l := range s.active().lines {
		out[i] = l.String()
	}
	return out
}

func (s *Screen) active() *buffer {
	if s.altActive {
		return &s.alt
	}
	return &s.main
}

// Resize changes the screen size. Columns are truncated or padded without
// reflow. When rows shrink, rows above the cursor move into scrollback so the
// cursor stays visible; when rows grow, scrollback rows are pulled back in.
func (s *Screen) Resize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cols == s.cols && rows == s.rows {
		return
	}
	resizeCols := func(b *buffer) {
		for i := range b.lines {
			l := &b.lines[i]
			if len(l.Cells) > cols {
				l.Cells = l.Cells[:cols]
				if last := &l.Cells[cols-1]; last.Width == 2 {
					*last = Blank(last.Attr)
				}
			} else {
				for len(l.Cells) < cols {
					l.Cells = append(l.Cells, Blank(Attr{}))
				}
			}
		}
	}
	resizeCols(&s.main)
	resizeCols(&s.alt)
	for i := range s.scrollback {
		if l := &s.scrollback[i]; len(l.Cells) > cols {
			l.Cells = l.Cells[:cols]
		}
	}

	// Main buffer rows: keep the cursor on screen.
	for len(s.main.lines) > rows {
		if s.cur.y > 0 && !s.altActive || s.altActive && s.cursorMainY() > 0 {
			s.pushScrollback(s.main.lines[0])
			s.main.lines = s.main.lines[1:]
			if !s.altActive {
				s.cur.y--
			} else {
				s.savedMain.cur.y--
			}
		} else {
			s.main.lines = s.main.lines[:len(s.main.lines)-1]
		}
	}
	for len(s.main.lines) < rows {
		if n := len(s.scrollback); n > 0 {
			s.main.lines = append([]Line{s.scrollback[n-1].Clone(cols)}, s.main.lines...)
			s.scrollback = s.scrollback[:n-1]
			if !s.altActive {
				s.cur.y++
			} else {
				s.savedMain.cur.y++
			}
		} else {
			s.main.lines = append(s.main.lines, newLine(cols, Attr{}))
		}
	}
	for len(s.alt.lines) > rows {
		s.alt.lines = s.alt.lines[:len(s.alt.lines)-1]
	}
	for len(s.alt.lines) < rows {
		s.alt.lines = append(s.alt.lines, newLine(cols, Attr{}))
	}

	s.cols, s.rows = cols, rows
	s.top, s.bottom = 0, rows-1
	s.clampCursor()
	s.pendingWrap = false
}

func (s *Screen) cursorMainY() int {
	if s.altActive {
		return s.savedMain.cur.y
	}
	return s.cur.y
}

func (s *Screen) clampCursor() {
	if s.cur.x >= s.cols {
		s.cur.x = s.cols - 1
	}
	if s.cur.x < 0 {
		s.cur.x = 0
	}
	if s.cur.y >= s.rows {
		s.cur.y = s.rows - 1
	}
	if s.cur.y < 0 {
		s.cur.y = 0
	}
}

func (s *Screen) pushScrollback(l Line) {
	s.scrollback = append(s.scrollback, l.trimmed())
	s.scrolled++
	if len(s.scrollback) > s.maxScroll {
		drop := len(s.scrollback) - s.maxScroll
		s.scrollback = append([]Line(nil), s.scrollback[drop:]...)
	}
}

// Write feeds bytes from the wrapped process into the model. It never fails;
// malformed sequences are skipped so that the model always keeps up with the
// terminal. Partial UTF-8 sequences and escape sequences may span writes.
func (s *Screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range p {
		s.p.feed(s, b)
	}
	return len(p), nil
}

// --- drawing primitives -------------------------------------------------

func (s *Screen) line(y int) *Line { return &s.active().lines[y] }

func (s *Screen) put(r rune) {
	w := runeWidth(r)
	if w == 0 {
		// Combining rune: attach to the cell before the cursor.
		x := s.cur.x - 1
		if s.pendingWrap {
			x = s.cols - 1
		}
		if x < 0 {
			x = 0
		}
		l := s.line(s.cur.y)
		c := &l.Cells[x]
		if c.Width == 0 && x > 0 {
			c = &l.Cells[x-1]
		}
		c.Extra += string(r)
		return
	}
	if s.pendingWrap {
		if s.modes[ModeAutoWrap] {
			s.line(s.cur.y).Wrapped = true
			s.cur.x = 0
			s.lineFeed()
		} else {
			s.cur.x = s.cols - 1
		}
		s.pendingWrap = false
	}
	if w == 2 && s.cur.x == s.cols-1 {
		// A wide rune does not fit in the last column: pad and wrap.
		l := s.line(s.cur.y)
		l.Cells[s.cur.x] = Blank(s.attr)
		if s.modes[ModeAutoWrap] {
			l.Wrapped = true
			s.cur.x = 0
			s.lineFeed()
		} else {
			return
		}
	}
	l := s.line(s.cur.y)
	// Overwriting half of a wide rune blanks the other half.
	if old := l.Cells[s.cur.x]; old.Width == 0 && s.cur.x > 0 {
		l.Cells[s.cur.x-1] = Blank(l.Cells[s.cur.x-1].Attr)
	} else if old.Width == 2 && s.cur.x+1 < s.cols {
		l.Cells[s.cur.x+1] = Blank(l.Cells[s.cur.x+1].Attr)
	}
	l.Cells[s.cur.x] = Cell{Rune: r, Width: uint8(w), Attr: s.attr}
	if w == 2 {
		l.Cells[s.cur.x+1] = Cell{Rune: 0, Width: 0, Attr: s.attr}
	}
	s.cur.x += w
	if s.cur.x >= s.cols {
		s.cur.x = s.cols - 1
		s.pendingWrap = true
	}
}

func (s *Screen) lineFeed() {
	if s.cur.y == s.bottom {
		s.scrollUp(1)
	} else if s.cur.y < s.rows-1 {
		s.cur.y++
	}
}

func (s *Screen) reverseIndex() {
	if s.cur.y == s.top {
		s.scrollDown(1)
	} else if s.cur.y > 0 {
		s.cur.y--
	}
}

// scrollUp removes n rows at the top of the scroll region and adds blank rows
// at the bottom. Rows leaving a full-height region enter scrollback.
func (s *Screen) scrollUp(n int) {
	b := s.active()
	if n > s.bottom-s.top+1 {
		n = s.bottom - s.top + 1
	}
	for i := 0; i < n; i++ {
		if s.top == 0 && s.bottom == s.rows-1 && !s.altActive {
			s.pushScrollback(b.lines[0])
		}
		copy(b.lines[s.top:s.bottom], b.lines[s.top+1:s.bottom+1])
		b.lines[s.bottom] = newLine(s.cols, Attr{BG: s.attr.BG})
	}
}

func (s *Screen) scrollDown(n int) {
	b := s.active()
	if n > s.bottom-s.top+1 {
		n = s.bottom - s.top + 1
	}
	for i := 0; i < n; i++ {
		copy(b.lines[s.top+1:s.bottom+1], b.lines[s.top:s.bottom])
		b.lines[s.top] = newLine(s.cols, Attr{BG: s.attr.BG})
	}
}

func (s *Screen) eraseAttr() Attr { return Attr{BG: s.attr.BG} }

func (s *Screen) eraseLine(y, from, to int) {
	l := s.line(y)
	if from < 0 {
		from = 0
	}
	if to > s.cols {
		to = s.cols
	}
	for x := from; x < to; x++ {
		l.Cells[x] = Blank(s.eraseAttr())
	}
	if to == s.cols {
		l.Wrapped = false
	}
}

func (s *Screen) moveTo(x, y int) {
	if s.modes[ModeOrigin] {
		y += s.top
		if y > s.bottom {
			y = s.bottom
		}
	}
	s.cur.x, s.cur.y = x, y
	s.clampCursor()
	s.pendingWrap = false
}

func (s *Screen) setMode(m int, set bool) {
	switch m {
	case ModeAltScreen47, ModeAltScreen1047, ModeAltScreen1049:
		s.switchAlt(set, m == ModeAltScreen1049)
	case ModeSaveCursor1048:
		if set {
			s.saveCursor()
		} else {
			s.restoreCursor()
		}
	case ModeOrigin:
		s.modes[m] = set
		s.moveTo(0, 0)
	default:
		s.modes[m] = set
	}
	if s.OnMode != nil {
		s.OnMode(m, set)
	}
}

func (s *Screen) switchAlt(on bool, withCursor bool) {
	if on == s.altActive {
		return
	}
	if on {
		if withCursor {
			s.saveCursor()
		}
		s.altActive = true
		s.modes[ModeAltScreen1049] = true
		for i := range s.alt.lines {
			s.alt.lines[i] = newLine(s.cols, Attr{})
		}
		s.cur = cursor{}
		s.pendingWrap = false
	} else {
		s.altActive = false
		s.modes[ModeAltScreen1049] = false
		if withCursor {
			s.restoreCursor()
		}
	}
	s.top, s.bottom = 0, s.rows-1
}

func (s *Screen) saveCursor() {
	st := savedState{cur: s.cur, attr: s.attr, valid: true}
	if s.altActive {
		s.savedAlt = st
	} else {
		s.savedMain = st
	}
}

func (s *Screen) restoreCursor() {
	st := s.savedMain
	if s.altActive {
		st = s.savedAlt
	}
	if !st.valid {
		s.cur = cursor{}
		s.attr = Attr{}
	} else {
		s.cur = st.cur
		s.attr = st.attr
	}
	s.clampCursor()
	s.pendingWrap = false
}

func (s *Screen) tab() {
	x := s.cur.x
	x = (x/8 + 1) * 8
	if x >= s.cols {
		x = s.cols - 1
	}
	s.cur.x = x
	s.pendingWrap = false
}

func (s *Screen) backTab(n int) {
	for ; n > 0; n-- {
		if s.cur.x == 0 {
			break
		}
		s.cur.x = ((s.cur.x - 1) / 8) * 8
	}
	s.pendingWrap = false
}

func (s *Screen) insertLines(n int) {
	if s.cur.y < s.top || s.cur.y > s.bottom {
		return
	}
	saveTop := s.top
	s.top = s.cur.y
	s.scrollDown(n)
	s.top = saveTop
	s.cur.x = 0
	s.pendingWrap = false
}

func (s *Screen) deleteLines(n int) {
	if s.cur.y < s.top || s.cur.y > s.bottom {
		return
	}
	saveTop := s.top
	s.top = s.cur.y
	b := s.active()
	if n > s.bottom-s.top+1 {
		n = s.bottom - s.top + 1
	}
	for i := 0; i < n; i++ {
		copy(b.lines[s.top:s.bottom], b.lines[s.top+1:s.bottom+1])
		b.lines[s.bottom] = newLine(s.cols, s.eraseAttr())
	}
	s.top = saveTop
	s.cur.x = 0
	s.pendingWrap = false
}

func (s *Screen) insertChars(n int) {
	l := s.line(s.cur.y)
	if n > s.cols-s.cur.x {
		n = s.cols - s.cur.x
	}
	copy(l.Cells[s.cur.x+n:], l.Cells[s.cur.x:s.cols-n])
	for x := s.cur.x; x < s.cur.x+n; x++ {
		l.Cells[x] = Blank(s.eraseAttr())
	}
	s.pendingWrap = false
}

func (s *Screen) deleteChars(n int) {
	l := s.line(s.cur.y)
	if n > s.cols-s.cur.x {
		n = s.cols - s.cur.x
	}
	copy(l.Cells[s.cur.x:], l.Cells[s.cur.x+n:])
	for x := s.cols - n; x < s.cols; x++ {
		l.Cells[x] = Blank(s.eraseAttr())
	}
	s.pendingWrap = false
}

func (s *Screen) eraseChars(n int) {
	s.eraseLine(s.cur.y, s.cur.x, s.cur.x+n)
	s.pendingWrap = false
}

func (s *Screen) eraseDisplay(mode int) {
	switch mode {
	case 0:
		s.eraseLine(s.cur.y, s.cur.x, s.cols)
		for y := s.cur.y + 1; y < s.rows; y++ {
			s.eraseLine(y, 0, s.cols)
		}
	case 1:
		for y := 0; y < s.cur.y; y++ {
			s.eraseLine(y, 0, s.cols)
		}
		s.eraseLine(s.cur.y, 0, s.cur.x+1)
	case 2:
		for y := 0; y < s.rows; y++ {
			s.eraseLine(y, 0, s.cols)
		}
	case 3:
		if !s.altActive {
			s.scrollback = nil
		}
	}
	s.pendingWrap = false
}

func (s *Screen) fullReset() {
	cols, rows := s.cols, s.rows
	s.altActive = false
	s.modes = map[int]bool{ModeAutoWrap: true, ModeCursorVisible: true}
	s.savedMain, s.savedAlt = savedState{}, savedState{}
	s.cursorStyle = 0
	s.reset(cols, rows)
}

func (s *Screen) applySGR(params [][]int) {
	if len(params) == 0 {
		s.attr = Attr{}
		return
	}
	for i := 0; i < len(params); i++ {
		p := params[i]
		n := 0
		if len(p) > 0 {
			n = p[0]
		}
		switch {
		case n == 0:
			s.attr = Attr{}
		case n == 1:
			s.attr.Flags |= Bold
		case n == 2:
			s.attr.Flags |= Dim
		case n == 3:
			s.attr.Flags |= Italic
		case n == 4:
			s.attr.Flags |= Underline
		case n == 5 || n == 6:
			s.attr.Flags |= Blink
		case n == 7:
			s.attr.Flags |= Reverse
		case n == 8:
			s.attr.Flags |= Hidden
		case n == 9:
			s.attr.Flags |= Strike
		case n == 21 || n == 24:
			s.attr.Flags &^= Underline
		case n == 22:
			s.attr.Flags &^= Bold | Dim
		case n == 23:
			s.attr.Flags &^= Italic
		case n == 25:
			s.attr.Flags &^= Blink
		case n == 27:
			s.attr.Flags &^= Reverse
		case n == 28:
			s.attr.Flags &^= Hidden
		case n == 29:
			s.attr.Flags &^= Strike
		case n >= 30 && n <= 37:
			s.attr.FG = Color{Kind: ColorIndexed, Index: uint8(n - 30)}
		case n == 38 || n == 48:
			c, used := parseExtendedColor(params, i)
			if n == 38 {
				s.attr.FG = c
			} else {
				s.attr.BG = c
			}
			i += used
		case n == 39:
			s.attr.FG = Color{}
		case n >= 40 && n <= 47:
			s.attr.BG = Color{Kind: ColorIndexed, Index: uint8(n - 40)}
		case n == 49:
			s.attr.BG = Color{}
		case n >= 90 && n <= 97:
			s.attr.FG = Color{Kind: ColorIndexed, Index: uint8(n - 90 + 8)}
		case n >= 100 && n <= 107:
			s.attr.BG = Color{Kind: ColorIndexed, Index: uint8(n - 100 + 8)}
		}
	}
}

// parseExtendedColor decodes the 38/48 forms: `38;5;n`, `38;2;r;g;b`,
// `38:5:n`, `38:2::r:g:b`. It returns the colour and how many further
// semicolon parameters were consumed.
func parseExtendedColor(params [][]int, i int) (Color, int) {
	p := params[i]
	if len(p) > 1 {
		// Colon form: everything is in this parameter.
		switch p[1] {
		case 5:
			if len(p) >= 3 {
				return Color{Kind: ColorIndexed, Index: uint8(p[2])}, 0
			}
		case 2:
			rest := p[2:]
			if len(rest) >= 4 { // colour space id present
				rest = rest[1:]
			}
			if len(rest) >= 3 {
				return Color{Kind: ColorRGB, R: uint8(rest[0]), G: uint8(rest[1]), B: uint8(rest[2])}, 0
			}
		}
		return Color{}, 0
	}
	at := func(j int) int {
		if j < len(params) && len(params[j]) > 0 {
			return params[j][0]
		}
		return 0
	}
	switch at(i + 1) {
	case 5:
		return Color{Kind: ColorIndexed, Index: uint8(at(i + 2))}, 2
	case 2:
		return Color{Kind: ColorRGB, R: uint8(at(i + 2)), G: uint8(at(i + 3)), B: uint8(at(i + 4))}, 4
	}
	return Color{}, 0
}

// --- parser ---------------------------------------------------------------

type parserState uint8

const (
	stGround parserState = iota
	stEscape
	stEscapeIntermediate
	stCSI
	stOSC
	stOSCEscape
	stString // DCS, SOS, PM, APC: consumed until ST
	stStringEscape
)

type parser struct {
	state        parserState
	params       [][]int
	curParam     []int
	haveParam    bool
	intermediate []byte
	private      byte
	utf8buf      [4]byte
	utf8n        int
	utf8need     int
}

func (p *parser) resetSeq() {
	p.params = p.params[:0]
	p.curParam = nil
	p.haveParam = false
	p.intermediate = p.intermediate[:0]
	p.private = 0
}

func (p *parser) feed(s *Screen, b byte) {
	// UTF-8 continuation handling happens only in ground state.
	if p.state == stGround && p.utf8need > 0 {
		if b&0xC0 == 0x80 {
			p.utf8buf[p.utf8n] = b
			p.utf8n++
			if p.utf8n == p.utf8need {
				r, _ := utf8.DecodeRune(p.utf8buf[:p.utf8n])
				p.utf8n, p.utf8need = 0, 0
				s.put(r)
			}
			return
		}
		// Invalid continuation: emit replacement and reprocess b.
		p.utf8n, p.utf8need = 0, 0
		s.put(utf8.RuneError)
	}

	switch p.state {
	case stGround:
		switch {
		case b == 0x1b:
			p.state = stEscape
			p.resetSeq()
		case b < 0x20 || b == 0x7f:
			s.control(b)
		case b < 0x80:
			s.put(rune(b))
		case b&0xE0 == 0xC0:
			p.utf8buf[0], p.utf8n, p.utf8need = b, 1, 2
		case b&0xF0 == 0xE0:
			p.utf8buf[0], p.utf8n, p.utf8need = b, 1, 3
		case b&0xF8 == 0xF0:
			p.utf8buf[0], p.utf8n, p.utf8need = b, 1, 4
		default:
			s.put(utf8.RuneError)
		}
	case stEscape:
		switch {
		case b == '[':
			p.state = stCSI
		case b == ']':
			p.state = stOSC
		case b == 'P' || b == 'X' || b == '^' || b == '_':
			p.state = stString
		case b >= 0x20 && b <= 0x2f:
			p.intermediate = append(p.intermediate, b)
			p.state = stEscapeIntermediate
		case b == 0x1b:
			// Stay in escape state.
		case b < 0x20:
			s.control(b)
		default:
			s.escDispatch(b)
			p.state = stGround
		}
	case stEscapeIntermediate:
		switch {
		case b >= 0x20 && b <= 0x2f:
			p.intermediate = append(p.intermediate, b)
		case b == 0x1b:
			p.state = stEscape
			p.resetSeq()
		default:
			// Charset designations and similar: nothing to model.
			p.state = stGround
		}
	case stCSI:
		switch {
		case b >= '0' && b <= '9':
			if !p.haveParam {
				p.curParam = append(p.curParam, 0)
				p.haveParam = true
			}
			last := &p.curParam[len(p.curParam)-1]
			*last = *last*10 + int(b-'0')
			if *last > 1<<20 {
				*last = 1 << 20
			}
		case b == ':':
			if !p.haveParam {
				p.curParam = append(p.curParam, 0)
				p.haveParam = true
			}
			p.curParam = append(p.curParam, 0)
		case b == ';':
			if !p.haveParam {
				p.curParam = []int{0}
			}
			p.params = append(p.params, p.curParam)
			p.curParam = nil
			p.haveParam = false
		case b >= '<' && b <= '?':
			p.private = b
		case b >= 0x20 && b <= 0x2f:
			p.intermediate = append(p.intermediate, b)
		case b >= 0x40 && b <= 0x7e:
			if p.haveParam {
				p.params = append(p.params, p.curParam)
			} else if len(p.params) > 0 {
				p.params = append(p.params, []int{0})
			}
			s.csiDispatch(p, b)
			p.state = stGround
		case b == 0x1b:
			p.state = stEscape
			p.resetSeq()
		case b < 0x20:
			s.control(b)
		default:
			p.state = stGround
		}
	case stOSC:
		switch b {
		case 0x07:
			p.state = stGround
		case 0x1b:
			p.state = stOSCEscape
		}
	case stOSCEscape:
		if b == '\\' {
			p.state = stGround
		} else {
			p.state = stEscape
			p.resetSeq()
			p.feed(s, b)
		}
	case stString:
		if b == 0x1b {
			p.state = stStringEscape
		} else if b == 0x07 {
			p.state = stGround
		}
	case stStringEscape:
		if b == '\\' {
			p.state = stGround
		} else {
			p.state = stEscape
			p.resetSeq()
			p.feed(s, b)
		}
	}
}

func (s *Screen) control(b byte) {
	switch b {
	case 0x08: // BS
		if s.cur.x > 0 {
			s.cur.x--
		}
		s.pendingWrap = false
	case 0x09: // HT
		s.tab()
	case 0x0a, 0x0b, 0x0c: // LF, VT, FF
		s.lineFeed()
		s.pendingWrap = false
	case 0x0d: // CR
		s.cur.x = 0
		s.pendingWrap = false
	}
}

func (s *Screen) escDispatch(b byte) {
	switch b {
	case '7':
		s.saveCursor()
	case '8':
		s.restoreCursor()
	case 'D':
		s.lineFeed()
		s.pendingWrap = false
	case 'E':
		s.cur.x = 0
		s.lineFeed()
		s.pendingWrap = false
	case 'M':
		s.reverseIndex()
		s.pendingWrap = false
	case 'c':
		s.fullReset()
	}
}

func param(p [][]int, i, def int) int {
	if i < len(p) && len(p[i]) > 0 && p[i][0] != 0 {
		return p[i][0]
	}
	return def
}

func (s *Screen) csiDispatch(p *parser, final byte) {
	ps := p.params
	if p.private == '?' {
		switch final {
		case 'h', 'l':
			for _, m := range ps {
				if len(m) > 0 {
					s.setMode(m[0], final == 'h')
				}
			}
		}
		return
	}
	if p.private != 0 {
		return
	}
	if len(p.intermediate) > 0 {
		if p.intermediate[0] == ' ' && final == 'q' {
			s.cursorStyle = param(ps, 0, 0)
		}
		return
	}
	switch final {
	case '@':
		s.insertChars(param(ps, 0, 1))
	case 'A':
		s.cur.y -= param(ps, 0, 1)
		if s.cur.y < s.top && s.cur.y+param(ps, 0, 1) >= s.top {
			s.cur.y = s.top
		}
		s.clampCursor()
		s.pendingWrap = false
	case 'B', 'e':
		s.cur.y += param(ps, 0, 1)
		if s.cur.y > s.bottom && s.cur.y-param(ps, 0, 1) <= s.bottom {
			s.cur.y = s.bottom
		}
		s.clampCursor()
		s.pendingWrap = false
	case 'C', 'a':
		s.cur.x += param(ps, 0, 1)
		s.clampCursor()
		s.pendingWrap = false
	case 'D':
		s.cur.x -= param(ps, 0, 1)
		s.clampCursor()
		s.pendingWrap = false
	case 'E':
		s.cur.x = 0
		s.cur.y += param(ps, 0, 1)
		s.clampCursor()
		s.pendingWrap = false
	case 'F':
		s.cur.x = 0
		s.cur.y -= param(ps, 0, 1)
		s.clampCursor()
		s.pendingWrap = false
	case 'G', '`':
		s.moveTo(param(ps, 0, 1)-1, s.cur.y)
	case 'H', 'f':
		s.moveTo(param(ps, 1, 1)-1, param(ps, 0, 1)-1)
	case 'I':
		for n := param(ps, 0, 1); n > 0; n-- {
			s.tab()
		}
	case 'J':
		s.eraseDisplay(param(ps, 0, 0))
	case 'K':
		switch param(ps, 0, 0) {
		case 0:
			s.eraseLine(s.cur.y, s.cur.x, s.cols)
		case 1:
			s.eraseLine(s.cur.y, 0, s.cur.x+1)
		case 2:
			s.eraseLine(s.cur.y, 0, s.cols)
		}
		s.pendingWrap = false
	case 'L':
		s.insertLines(param(ps, 0, 1))
	case 'M':
		s.deleteLines(param(ps, 0, 1))
	case 'P':
		s.deleteChars(param(ps, 0, 1))
	case 'S':
		s.scrollUp(param(ps, 0, 1))
	case 'T':
		s.scrollDown(param(ps, 0, 1))
	case 'X':
		s.eraseChars(param(ps, 0, 1))
	case 'Z':
		s.backTab(param(ps, 0, 1))
	case 'd':
		s.moveTo(s.cur.x, param(ps, 0, 1)-1)
	case 'h', 'l':
		// ANSI modes (insert mode and friends) are not modelled.
	case 'm':
		s.applySGR(ps)
	case 'r':
		top := param(ps, 0, 1) - 1
		bottom := param(ps, 1, s.rows) - 1
		if top < 0 {
			top = 0
		}
		if bottom >= s.rows {
			bottom = s.rows - 1
		}
		if top < bottom {
			s.top, s.bottom = top, bottom
			s.moveTo(0, 0)
		}
	case 's':
		s.saveCursor()
	case 'u':
		s.restoreCursor()
	}
}
