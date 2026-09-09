package screen

import (
	"bytes"
	"strings"
	"testing"
)

func feed(s *Screen, parts ...string) {
	for _, p := range parts {
		_, _ = s.Write([]byte(p))
	}
}

func TestPrintAndWrap(t *testing.T) {
	s := New(5, 2)
	feed(s, "abcdefg")
	got := s.Text()
	if got[0] != "abcde" || got[1] != "fg" {
		t.Fatalf("rows = %q", got)
	}
	if !s.Row(0).Wrapped {
		t.Fatal("row 0 should be marked wrapped")
	}
	x, y := s.Cursor()
	if x != 2 || y != 1 {
		t.Fatalf("cursor = %d,%d", x, y)
	}
}

func TestPendingWrapDoesNotWrapOnExactFit(t *testing.T) {
	s := New(3, 2)
	feed(s, "abc\r\nx")
	got := s.Text()
	if got[0] != "abc" || got[1] != "x" {
		t.Fatalf("rows = %q", got)
	}
	if s.Row(0).Wrapped {
		t.Fatal("explicit newline is not a wrap")
	}
}

func TestScrollbackAndScrolledOff(t *testing.T) {
	s := New(6, 2)
	feed(s, "one\r\ntwo\r\nthree\r\nfour")
	if s.HistoryLen() != 2 || s.ScrolledOff() != 2 {
		t.Fatalf("history = %d scrolled = %d", s.HistoryLen(), s.ScrolledOff())
	}
	h := s.History()
	if h[0].String() != "one" || h[1].String() != "two" {
		t.Fatalf("history = %q %q", h[0].String(), h[1].String())
	}
	vp := s.Viewport(1)
	if vp[0].String() != "two" || vp[1].String() != "three" {
		t.Fatalf("viewport(1) = %q %q", vp[0].String(), vp[1].String())
	}
	vp = s.Viewport(99)
	if vp[0].String() != "one" || vp[1].String() != "two" {
		t.Fatalf("viewport clamp = %q %q", vp[0].String(), vp[1].String())
	}
}

func TestSGRAttributesRecorded(t *testing.T) {
	s := New(10, 1)
	feed(s, "\x1b[1;31mA\x1b[38;2;10;20;30;48;5;200mB\x1b[0mC\x1b[4:3mD")
	row := s.Row(0)
	if row.Cells[0].Attr != (Attr{FG: Color{Kind: ColorIndexed, Index: 1}, Flags: Bold}) {
		t.Fatalf("A attr = %+v", row.Cells[0].Attr)
	}
	want := Attr{FG: Color{Kind: ColorRGB, R: 10, G: 20, B: 30}, BG: Color{Kind: ColorIndexed, Index: 200}, Flags: Bold}
	if row.Cells[1].Attr != want {
		t.Fatalf("B attr = %+v", row.Cells[1].Attr)
	}
	if !row.Cells[2].Attr.IsDefault() {
		t.Fatalf("C attr = %+v", row.Cells[2].Attr)
	}
	if row.Cells[3].Attr.Flags&Underline == 0 {
		t.Fatalf("D attr = %+v", row.Cells[3].Attr)
	}
}

func TestColonRGB(t *testing.T) {
	s := New(4, 1)
	feed(s, "\x1b[38:2::1:2:3mX")
	if got := s.Row(0).Cells[0].Attr.FG; got != (Color{Kind: ColorRGB, R: 1, G: 2, B: 3}) {
		t.Fatalf("fg = %+v", got)
	}
}

func TestEmitRoundTripPreservesAttributes(t *testing.T) {
	s := New(12, 3)
	feed(s,
		"\x1b[1;32mgreen\x1b[0m plain\r\n",
		"\x1b[7;38;5;33mrev\x1b[0m \x1b[4mund\x1b[24m 日本\r\n",
		"\x1b[2;3;9mdim\x1b[0m\r\n",
		"tail",
	)
	// The first row scrolled off; capture it and every visible row.
	captured := append(s.History(), s.Rows()...)
	var buf []byte
	for i, l := range captured {
		if i > 0 {
			buf = append(buf, "\r\n"...)
		}
		buf = l.AppendEmit(buf)
	}
	// Replay the emitted bytes into a fresh screen tall enough to hold them all.
	r := New(12, len(captured))
	_, _ = r.Write(buf)
	for i, l := range captured {
		got := r.Row(i)
		got.Wrapped = l.Wrapped // emission does not carry soft-wrap marks
		if !got.Equal(l) {
			t.Fatalf("row %d differs after re-emission:\n want %q\n got  %q", i, l.String(), got.String())
		}
	}
}

func TestWideRuneWrapsWhole(t *testing.T) {
	s := New(3, 2)
	feed(s, "a日b")
	got := s.Text()
	if got[0] != "a日" || got[1] != "b" {
		t.Fatalf("rows = %q", got)
	}
	row := s.Row(0)
	if row.Cells[1].Width != 2 || row.Cells[2].Width != 0 {
		t.Fatalf("wide cell widths = %d %d", row.Cells[1].Width, row.Cells[2].Width)
	}
	s = New(2, 2)
	feed(s, "a日")
	got = s.Text()
	if got[0] != "a" || got[1] != "日" {
		t.Fatalf("wide at last column: rows = %q", got)
	}
}

func TestCombiningRuneAttaches(t *testing.T) {
	s := New(4, 1)
	feed(s, "éx")
	row := s.Row(0)
	if row.Cells[0].Rune != 'e' || row.Cells[0].Extra != "\u0301" {
		t.Fatalf("cell 0 = %+v", row.Cells[0])
	}
	if row.Cells[1].Rune != 'x' {
		t.Fatalf("cell 1 = %+v", row.Cells[1])
	}
}

func TestCursorMovementAndErase(t *testing.T) {
	s := New(6, 3)
	feed(s, "hello\r\nworld\r\nthird")
	feed(s, "\x1b[2A\x1b[3G\x1b[K")
	got := s.Text()
	if got[0] != "he" || got[1] != "world" {
		t.Fatalf("after EL: %q", got)
	}
	feed(s, "\x1b[2;1H\x1b[J")
	got = s.Text()
	if got[0] != "he" || got[1] != "" || got[2] != "" {
		t.Fatalf("after ED: %q", got)
	}
	feed(s, "\x1b[H\x1b[2J")
	for _, r := range s.Text() {
		if r != "" {
			t.Fatalf("after ED2: %q", s.Text())
		}
	}
}

func TestInkStyleRedraw(t *testing.T) {
	// Ink repaints by moving up, erasing to end of screen, and rewriting.
	s := New(20, 4)
	feed(s, "frame 1 line a\r\nframe 1 line b\r\n")
	feed(s, "\x1b[2A\x1b[G\x1b[J")
	feed(s, "frame 2 line a\r\nframe 2 line b\r\n")
	got := s.Text()
	if got[0] != "frame 2 line a" || got[1] != "frame 2 line b" || got[2] != "" {
		t.Fatalf("rows = %q", got)
	}
	if s.HistoryLen() != 0 {
		t.Fatalf("redraw pushed %d rows to scrollback", s.HistoryLen())
	}
}

func TestScrollRegion(t *testing.T) {
	s := New(3, 4)
	feed(s, "a\r\nb\r\nc\r\nd")
	feed(s, "\x1b[2;3r\x1b[3;1H\n\nx")
	got := s.Text()
	if got[0] != "a" || got[1] != "" || got[2] != "x" || got[3] != "d" {
		t.Fatalf("rows = %q", got)
	}
	if s.HistoryLen() != 0 {
		t.Fatal("region scroll must not enter scrollback")
	}
}

func TestInsertDeleteLinesAndChars(t *testing.T) {
	s := New(5, 3)
	feed(s, "aaa\r\nbbb\r\nccc")
	feed(s, "\x1b[2;1H\x1b[L")
	got := s.Text()
	if got[0] != "aaa" || got[1] != "" || got[2] != "bbb" {
		t.Fatalf("after IL: %q", got)
	}
	feed(s, "\x1b[M")
	got = s.Text()
	if got[1] != "bbb" || got[2] != "" {
		t.Fatalf("after DL: %q", got)
	}
	feed(s, "\x1b[1;2H\x1b[@")
	if got := s.Text()[0]; got != "a aa" {
		t.Fatalf("after ICH: %q", got)
	}
	feed(s, "\x1b[P")
	if got := s.Text()[0]; got != "aaa" {
		t.Fatalf("after DCH: %q", got)
	}
	feed(s, "\x1b[2X")
	if got := s.Text()[0]; got != "a" {
		t.Fatalf("after ECH: %q", got)
	}
}

func TestAlternateScreen(t *testing.T) {
	s := New(4, 2)
	feed(s, "main\r\nrow2")
	feed(s, "\x1b[?1049h")
	if !s.AltActive() {
		t.Fatal("alt not active")
	}
	feed(s, "alt1\r\nalt2\r\nalt3")
	if s.HistoryLen() != 0 {
		t.Fatal("alt screen must not scroll into scrollback")
	}
	if got := s.Text(); got[0] != "alt2" || got[1] != "alt3" {
		t.Fatalf("alt rows = %q", got)
	}
	feed(s, "\x1b[?1049l")
	if got := s.Text(); got[0] != "main" || got[1] != "row2" {
		t.Fatalf("main rows after alt = %q", got)
	}
	x, y := s.Cursor()
	if x != 4-1 || y != 1 {
		t.Fatalf("cursor restored to %d,%d", x, y)
	}
}

func TestModesAndCallback(t *testing.T) {
	s := New(4, 2)
	var seen []int
	s.OnMode = func(m int, set bool) {
		if set {
			seen = append(seen, m)
		} else {
			seen = append(seen, -m)
		}
	}
	feed(s, "\x1b[?25l\x1b[?2004h\x1b[?1000;1006h\x1b[?1000l")
	if s.CursorVisible() {
		t.Fatal("cursor should be hidden")
	}
	if !s.Mode(ModeBracketedPaste) || !s.Mode(ModeMouseSGR) || s.Mode(ModeMouseNormal) {
		t.Fatalf("modes wrong: %v", s.modes)
	}
	want := []int{-25, 2004, 1000, 1006, -1000}
	if len(seen) != len(want) {
		t.Fatalf("callbacks = %v", seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("callbacks = %v", seen)
		}
	}
	feed(s, "\x1b[3 q")
	if s.CursorStyle() != 3 {
		t.Fatalf("cursor style = %d", s.CursorStyle())
	}
}

func TestSplitSequencesAcrossWrites(t *testing.T) {
	s := New(8, 1)
	feed(s, "\x1b[", "1;3", "1m", "x", "\xe6\x97", "\xa5")
	row := s.Row(0)
	if row.Cells[0].Attr.Flags&Bold == 0 || row.Cells[1].Rune != '日' {
		t.Fatalf("row = %+v", row.Cells[:3])
	}
}

func TestOSCAndDCSIgnored(t *testing.T) {
	s := New(8, 1)
	feed(s, "\x1b]0;title\x07a\x1b]8;;http://x\x1b\\b\x1bP+q\x1b\\c")
	if got := s.Text()[0]; got != "abc" {
		t.Fatalf("row = %q", got)
	}
}

func TestResizeKeepsCursorAndHistory(t *testing.T) {
	s := New(4, 3)
	feed(s, "a\r\nb\r\nc")
	s.Resize(4, 2)
	if got := s.Text(); got[0] != "b" || got[1] != "c" {
		t.Fatalf("after shrink rows = %q", got)
	}
	if s.HistoryLen() != 1 {
		t.Fatalf("history = %d", s.HistoryLen())
	}
	_, y := s.Cursor()
	if y != 1 {
		t.Fatalf("cursor y = %d", y)
	}
	s.Resize(6, 3)
	if got := s.Text(); got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("after grow rows = %q", got)
	}
	if len(s.Row(0).Cells) != 6 {
		t.Fatalf("cols = %d", len(s.Row(0).Cells))
	}
}

func TestEraseScrollback(t *testing.T) {
	s := New(4, 1)
	feed(s, "a\r\nb\r\nc")
	if s.HistoryLen() != 2 {
		t.Fatal("expected history")
	}
	feed(s, "\x1b[3J")
	if s.HistoryLen() != 0 {
		t.Fatal("3J should clear scrollback")
	}
}

func TestMouseTracking(t *testing.T) {
	s := New(4, 1)
	if s.MouseTracking() {
		t.Fatal("no tracking by default")
	}
	feed(s, "\x1b[?1002h")
	if !s.MouseTracking() {
		t.Fatal("tracking after 1002h")
	}
}

func TestAttrSGR(t *testing.T) {
	a := Attr{FG: Color{Kind: ColorIndexed, Index: 9}, BG: Color{Kind: ColorRGB, R: 1, G: 2, B: 3}, Flags: Bold | Underline}
	got := a.SGR()
	for _, want := range []string{"\x1b[0", ";1", ";4", ";91", ";48;2;1;2;3", "m"} {
		if !strings.Contains(got, want) {
			t.Fatalf("SGR %q lacks %q", got, want)
		}
	}
	if !bytes.HasPrefix([]byte(got), []byte("\x1b[0;")) {
		t.Fatalf("SGR = %q", got)
	}
}

func BenchmarkWrite(b *testing.B) {
	s := New(120, 40)
	chunk := []byte(strings.Repeat("\x1b[1;32mline of \x1b[0m\x1b[38;2;1;2;3mcoloured text\x1b[0m\r\n", 20))
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Write(chunk)
	}
}
