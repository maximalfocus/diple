package wrap

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/screen"
)

type memRecorder struct {
	out, in []byte
	resizes [][2]int
}

func (r *memRecorder) Output(p []byte)       { r.out = append(r.out, p...) }
func (r *memRecorder) Input(p []byte)        { r.in = append(r.in, p...) }
func (r *memRecorder) Resize(cols, rows int) { r.resizes = append(r.resizes, [2]int{cols, rows}) }
func (r *memRecorder) Transcript(string)     {}

func newTestSession(cols, rows int) (*Session, *bytes.Buffer, *bytes.Buffer) {
	term := &bytes.Buffer{}
	agent := &bytes.Buffer{}
	return NewSession(term, agent, cols, rows, nil), term, agent
}

// fixture is a synthetic agent stream in the style of an Ink-based CLI.
func fixture() [][]byte {
	var chunks [][]byte
	for i := 1; i <= 12; i++ {
		chunks = append(chunks, []byte("\x1b[1;36m›\x1b[0m turn "+strings.Repeat("x", i)+"\r\n"))
	}
	chunks = append(chunks, []byte("\x1b[2A\x1b[G\x1b[J\x1b[38;2;200;100;50mredraw a\x1b[0m\r\n\x1b[7mredraw b\x1b[0m\r\n"))
	chunks = append(chunks, []byte("\x1b[?25l\x1b[2K> \x1b[?25h"))
	return chunks
}

func TestLiveOutputIsByteIdentical(t *testing.T) {
	s, term, _ := newTestSession(40, 6)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	var want []byte
	for _, c := range fixture() {
		want = append(want, c...)
		if err := s.HandleOutput(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	got := term.Bytes()
	if !bytes.HasPrefix(got, []byte(EnvelopeStart)) || !bytes.HasSuffix(got, []byte(EnvelopeEnd)) {
		t.Fatalf("envelope missing: %q", got)
	}
	body := got[len(EnvelopeStart) : len(got)-len(EnvelopeEnd)]
	if !bytes.Equal(body, want) {
		t.Fatalf("forwarded output differs from the agent's:\n want %q\n got  %q", want, body)
	}
}

func TestBareEscapeIsDeliveredAtOnce(t *testing.T) {
	s, _, agent := newTestSession(40, 6)
	if err := s.HandleInput([]byte("\x1b")); err != nil {
		t.Fatal(err)
	}
	if agent.String() != "\x1b" {
		t.Fatalf("agent got %q", agent.String())
	}
}

func TestKeysForwardedUnchangedWhileLive(t *testing.T) {
	s, _, agent := newTestSession(40, 6)
	in := []byte("hello\x1b[A\x1b\x1b[F\x03")
	if err := s.HandleInput(in); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(agent.Bytes(), in) {
		t.Fatalf("agent got %q, want %q", agent.Bytes(), in)
	}
}

func TestMouseSwallowedUnlessAgentTracks(t *testing.T) {
	s, _, agent := newTestSession(40, 6)
	click := []byte("\x1b[<0;5;3M\x1b[<0;5;3m")
	if err := s.HandleInput(click); err != nil {
		t.Fatal(err)
	}
	if agent.Len() != 0 {
		t.Fatalf("agent without tracking received %q", agent.Bytes())
	}
	_ = s.HandleOutput([]byte("\x1b[?1000h"))
	if err := s.HandleInput(click); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(agent.Bytes(), click) {
		t.Fatalf("agent with tracking received %q", agent.Bytes())
	}
}

func TestSplitMouseReportAcrossReads(t *testing.T) {
	s, _, agent := newTestSession(40, 6)
	_ = s.HandleOutput([]byte("\x1b[?1000h"))
	_ = s.HandleInput([]byte("\x1b[<0;5"))
	if agent.Len() != 0 {
		t.Fatalf("partial report leaked: %q", agent.Bytes())
	}
	_ = s.HandleInput([]byte(";3Mx"))
	if got := agent.String(); got != "\x1b[<0;5;3Mx" {
		t.Fatalf("agent got %q", got)
	}
}

func TestWheelShowsScrollbackAndOutputStaysPut(t *testing.T) {
	s, term, agent := newTestSession(40, 4)
	for i := 1; i <= 8; i++ {
		_ = s.HandleOutput([]byte("\x1b[3" + string(rune('0'+i%8)) + "mline " + strings.Repeat("x", i) + "\x1b[0m\r\n"))
	}
	term.Reset()
	if err := s.HandleInput([]byte("\x1b[<64;10;2M")); err != nil {
		t.Fatal(err)
	}
	if !s.Scrolled() {
		t.Fatal("wheel-up did not scroll")
	}
	if agent.Len() != 0 {
		t.Fatalf("wheel leaked to agent: %q", agent.Bytes())
	}
	painted := term.String()
	if !strings.Contains(painted, "line xx") || !strings.HasPrefix(painted, "\x1b[?25l\x1b[1;1H") {
		t.Fatalf("viewport paint = %q", painted)
	}

	// New output while scrolled: nothing reaches the terminal, model advances.
	term.Reset()
	before := s.back
	_ = s.HandleOutput([]byte("new 1\r\nnew 2\r\n"))
	if term.Len() != 0 {
		t.Fatalf("output while scrolled reached the terminal: %q", term.Bytes())
	}
	if s.back != before+2 {
		t.Fatalf("viewport moved: back %d -> %d", before, s.back)
	}
	if got := s.Model.Text()[3]; got != "" || s.Model.Text()[2] != "new 2" {
		t.Fatalf("model did not advance: %q", s.Model.Text())
	}

	// End returns to live, repainting the live screen with original attributes.
	term.Reset()
	if err := s.HandleInput([]byte("\x1b[F")); err != nil {
		t.Fatal(err)
	}
	if s.Scrolled() || agent.Len() != 0 {
		t.Fatalf("End did not return to live cleanly (agent got %q)", agent.Bytes())
	}
	replay := screen.New(40, 4)
	_, _ = replay.Write(term.Bytes())
	for i, want := range s.Model.Rows() {
		got := replay.Row(i)
		got.Wrapped = want.Wrapped
		if !got.Equal(want) {
			t.Fatalf("live repaint row %d differs: %q vs %q", i, got.String(), want.String())
		}
	}
	x, y := replay.Cursor()
	mx, my := s.Model.Cursor()
	if x != mx || y != my {
		t.Fatalf("cursor after repaint %d,%d want %d,%d", x, y, mx, my)
	}
	if !strings.HasSuffix(term.String(), "\x1b[?25h") {
		t.Fatalf("cursor not shown again: %q", term.String())
	}
}

func TestScrolledRowsMatchCapturedRows(t *testing.T) {
	s, term, _ := newTestSession(30, 3)
	var captured []screen.Line
	lines := []string{"\x1b[1;31mred bold\x1b[0m", "\x1b[4mund\x1b[24m plain", "\x1b[48;5;17mbg\x1b[0m", "\x1b[3mitalic\x1b[0m", "last"}
	for _, l := range lines {
		_ = s.HandleOutput([]byte(l + "\r\n"))
	}
	captured = s.Model.History()
	if len(captured) != 3 {
		t.Fatalf("history = %d", len(captured))
	}
	term.Reset()
	if err := s.Scroll(3); err != nil {
		t.Fatal(err)
	}
	replay := screen.New(30, 3)
	_, _ = replay.Write(term.Bytes())
	for i := 0; i < 3; i++ {
		want := captured[i]
		got := replay.Row(i)
		got.Wrapped = want.Wrapped
		if !got.Equal(want) {
			t.Fatalf("scrolled row %d differs: %q vs %q", i, got.String(), want.String())
		}
	}
}

func TestTypingWhileScrolledSnapsToLive(t *testing.T) {
	s, _, agent := newTestSession(20, 2)
	for i := 0; i < 5; i++ {
		_ = s.HandleOutput([]byte("row\r\n"))
	}
	_ = s.Scroll(2)
	if !s.Scrolled() {
		t.Fatal("not scrolled")
	}
	_ = s.HandleInput([]byte("k"))
	if s.Scrolled() || agent.String() != "k" {
		t.Fatalf("scrolled=%v agent=%q", s.Scrolled(), agent.String())
	}
}

func TestEndForwardedWhileLive(t *testing.T) {
	s, _, agent := newTestSession(20, 2)
	_ = s.HandleInput([]byte("\x1b[F"))
	if agent.String() != "\x1b[F" {
		t.Fatalf("agent got %q", agent.String())
	}
}

func TestWheelForwardedOnAltScreenWithTracking(t *testing.T) {
	s, _, agent := newTestSession(20, 2)
	for i := 0; i < 5; i++ {
		_ = s.HandleOutput([]byte("row\r\n"))
	}
	_ = s.HandleOutput([]byte("\x1b[?1049h\x1b[?1000h"))
	_ = s.HandleInput([]byte("\x1b[<64;1;1M"))
	if s.Scrolled() || agent.String() != "\x1b[<64;1;1M" {
		t.Fatalf("scrolled=%v agent=%q", s.Scrolled(), agent.String())
	}
}

func TestAgentMouseResetIsReenabled(t *testing.T) {
	s, term, _ := newTestSession(20, 2)
	_ = s.HandleOutput([]byte("\x1b[?1000l"))
	if got := term.String(); got != "\x1b[?1000l"+EnvelopeStart {
		t.Fatalf("terminal got %q", got)
	}
}

func TestAltScreenWhileScrolledReturnsToLive(t *testing.T) {
	s, term, _ := newTestSession(20, 2)
	for i := 0; i < 5; i++ {
		_ = s.HandleOutput([]byte("row\r\n"))
	}
	_ = s.Scroll(2)
	term.Reset()
	_ = s.HandleOutput([]byte("\x1b[?1049h"))
	if s.Scrolled() || term.Len() == 0 {
		t.Fatal("alt screen switch should return to live and repaint")
	}
}

func TestResizeRepaintsWhenScrolled(t *testing.T) {
	rec := &memRecorder{}
	term := &bytes.Buffer{}
	s := NewSession(term, &bytes.Buffer{}, 20, 3, rec)
	for i := 0; i < 6; i++ {
		_ = s.HandleOutput([]byte("row\r\n"))
	}
	_ = s.Scroll(2)
	term.Reset()
	if err := s.Resize(30, 4); err != nil {
		t.Fatal(err)
	}
	if term.Len() == 0 {
		t.Fatal("resize while scrolled should repaint")
	}
	if len(rec.resizes) != 1 || rec.resizes[0] != [2]int{30, 4} {
		t.Fatalf("recorder resizes = %v", rec.resizes)
	}
	if c, r := s.Model.Size(); c != 30 || r != 4 {
		t.Fatalf("model size = %dx%d", c, r)
	}
}

func TestRecorderSeesOutputAndInput(t *testing.T) {
	rec := &memRecorder{}
	s := NewSession(&bytes.Buffer{}, &bytes.Buffer{}, 20, 3, rec)
	_ = s.HandleOutput([]byte("out"))
	_ = s.HandleInput([]byte("in"))
	_ = s.HandleInput([]byte("\x1b[<64;1;1M")) // consumed by Diple, still recorded
	if string(rec.out) != "out" || string(rec.in) != "in\x1b[<64;1;1M" {
		t.Fatalf("recorder = %q %q", rec.out, rec.in)
	}
}

func TestStopWhileScrolledReturnsToLiveFirst(t *testing.T) {
	s, term, _ := newTestSession(20, 2)
	for i := 0; i < 5; i++ {
		_ = s.HandleOutput([]byte("row\r\n"))
	}
	_ = s.Scroll(2)
	term.Reset()
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if s.Scrolled() || !strings.HasSuffix(term.String(), EnvelopeEnd) {
		t.Fatalf("stop left %q", term.String())
	}
}

func TestParseSGRMouse(t *testing.T) {
	ev, n, ok, inc := parseSGRMouse([]byte("\x1b[<65;12;34mrest"))
	if !ok || inc || n != len("\x1b[<65;12;34m") || ev.button != 65 || ev.x != 12 || ev.y != 34 || !ev.release || !ev.wheelDown() {
		t.Fatalf("parse = %+v %d %v %v", ev, n, ok, inc)
	}
	if _, _, ok, inc := parseSGRMouse([]byte("\x1b[<6")); ok || !inc {
		t.Fatal("prefix should be incomplete")
	}
	if _, _, ok, inc := parseSGRMouse([]byte("\x1b[")); ok || !inc {
		t.Fatal("bare CSI prefix should be incomplete")
	}
	if _, _, ok, inc := parseSGRMouse([]byte("\x1b[A")); ok || inc {
		t.Fatal("arrow key is neither")
	}
	if _, _, ok, inc := parseSGRMouse([]byte("\x1b[<a;1;1M")); ok || inc {
		t.Fatal("garbage should be rejected")
	}
	ev, _, ok, _ = parseSGRMouse([]byte("\x1b[<68;1;1M"))
	if !ok || !ev.wheelUp() {
		t.Fatalf("shift+wheel-up not recognised: %+v", ev)
	}
}

func BenchmarkForward(b *testing.B) {
	s, term, _ := newTestSession(120, 40)
	chunk := []byte(strings.Repeat("\x1b[1;32m› \x1b[0mline of \x1b[38;2;1;2;3magent output\x1b[0m\r\n", 8))
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		term.Reset()
		_ = s.HandleOutput(chunk)
	}
}
