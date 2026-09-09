package pi

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/record"
	"github.com/maximalfocus/diple/internal/screen"
)

const fixtureVersion = "3"

func loadFixture(t *testing.T, mode string) (*screen.Screen, []string, *adapter.Transcript) {
	t.Helper()
	dir := filepath.Join("testdata", fixtureVersion)
	rec, err := record.Open(filepath.Join(dir, mode+".recording.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s := screen.New(rec.Header.Cols, rec.Header.Rows)
	if err := rec.Replay(s); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, mode+".transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr, err := (&Adapter{}).Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, l := range s.History() {
		rows = append(rows, l.String())
	}
	return s, append(rows, s.Text()...), tr
}

type expectation struct {
	kind        blocks.Kind
	text        string
	first, last int
}

// The rows the recorded reply occupies, read off the replayed fixture. They
// are the reply's own rows, well below the echoed prompt at rows 38..59.
var expected = []expectation{
	{blocks.Heading, "Plan", 68, 68},
	{blocks.ListItem, "Read the config file and note the two ports that the service listens on for HTTP and metrics", 70, 71},
	{blocks.ListItem, "Change the handler", 72, 72},
	{blocks.ListItem, "keep the old route", 73, 73},
	{blocks.ListItem, "add a fallback that logs and returns 404", 74, 74},
	{blocks.ListItem, "Verify", 75, 75},
	{blocks.CodeLine, "func handle(w http.ResponseWriter, r *http.Request) {", 77, 77},
	{blocks.CodeLine, "w.WriteHeader(404)", 78, 78},
	{blocks.CodeLine, "}", 79, 79},
	{blocks.DiffLine, "-    return nil", 83, 83},
	{blocks.DiffLine, `+    return errors.New("boom")`, 84, 84},
	{blocks.Paragraph, "That is the whole plan.", 87, 87},
}

func TestFixtureAlignsTheReplyNotTheEchoedPrompt(t *testing.T) {
	a := &Adapter{}
	s, rows, tr := loadFixture(t, "inline")
	if tr.Version != fixtureVersion || tr.SessionID == "" {
		t.Fatalf("transcript = version %q session %q", tr.Version, tr.SessionID)
	}
	if a.Mode(s) != adapter.ModeInline {
		t.Fatalf("mode = %s, want inline", a.Mode(s))
	}
	// The user's prompt is in the transcript as an echo, and never a turn.
	echoes, turns := 0, 0
	for _, turn := range tr.Turns {
		if turn.Echo {
			echoes++
		} else {
			turns++
		}
	}
	if echoes != 1 || turns != 1 {
		t.Fatalf("transcript has %d echoes and %d turns", echoes, turns)
	}
	al := a.Align(tr, rows)
	if len(al) != 1 || !al[0].Aligned {
		t.Fatalf("alignment = %+v", al)
	}
	for _, want := range expected {
		found := false
		for _, b := range al[0].Blocks {
			if b.Kind != want.kind || strings.TrimSpace(b.Text) != want.text {
				continue
			}
			found = true
			if b.First != want.first || b.Last != want.last {
				t.Fatalf("%s %q rows %d..%d, want %d..%d", want.kind, want.text, b.First, b.Last, want.first, want.last)
			}
			break
		}
		if !found {
			t.Fatalf("no %s block %q in the alignment", want.kind, want.text)
		}
	}
}

func TestScreenFactsComeFromTheRecording(t *testing.T) {
	a := &Adapter{}
	s, _, _ := loadFixture(t, "inline")
	rows := s.Text()
	in := a.InputRow(rows)
	if in < 0 || in >= len(rows) || !strings.Contains(rows[in], "─") {
		t.Fatalf("input row = %d (%q)", in, rows[max(0, in)])
	}
	if a.Busy(s) || a.Prompt(s) {
		t.Fatalf("busy=%v prompt=%v at the end of the recording", a.Busy(s), a.Prompt(s))
	}
	if !a.QueuesWhenBusy() {
		t.Fatal("pi queues input while busy")
	}
	if !a.Busy(showing("── ⠇ Working ──────────────────────────────────────")) {
		t.Fatal("the working line is busy")
	}
	for _, row := range []string{"Allow this command? (y/n)", "  Press enter to continue", "Esc to cancel"} {
		if !a.Prompt(showing(row)) {
			t.Fatalf("not recognised as a prompt: %q", row)
		}
	}
	if a.Prompt(showing("── ⠇ Working ──────────────────────────────────────")) {
		t.Fatal("a working line is not a prompt")
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func showing(row string) *screen.Screen {
	s := screen.New(80, 6)
	_, _ = s.Write([]byte("\x1b[1;1H" + row))
	return s
}

func TestAnotherSessionFormatIsRefusedLoudly(t *testing.T) {
	a := &Adapter{}
	line := `{"type":"session","version":9,"id":"s","timestamp":"2026-09-09T13:30:47.336Z","cwd":"/tmp"}`
	_, err := a.Parse(strings.NewReader(line + "\n"))
	var ve *adapter.VersionError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want a version error", err)
	}
	if ve.Version != "9" || !strings.Contains(ve.Error(), fixtureVersion) {
		t.Fatalf("version error = %q", ve.Error())
	}
}

func TestCorruptTranscriptStillLeavesParagraphs(t *testing.T) {
	a := &Adapter{}
	_, rows, tr := loadFixture(t, "inline")
	for i := range tr.Turns {
		tr.Turns[i].Blocks = blocks.Parse("Something the screen never showed.")
	}
	al := a.Align(tr, rows)
	if len(al) != 1 || al[0].Aligned {
		t.Fatalf("alignment = %+v", al)
	}
	// With no transcript at all, the rendered paragraphs are one turn.
	fb := a.Fallback(rows)
	if len(fb) != 1 || fb[0].Aligned || len(fb[0].Blocks) < 5 {
		t.Fatalf("fallback = %+v", fb)
	}
}

func TestBypassRecognisesPisOwnNonInteractiveForms(t *testing.T) {
	a := &Adapter{}
	for _, args := range [][]string{{"-p", "hello"}, {"--print", "hello"}, {"install", "x"}, {"list"}, {"--version"}, {"--help"}} {
		if !a.Bypass(args) {
			t.Fatalf("%v should bypass", args)
		}
	}
	for _, args := range [][]string{{}, {"--model", "x"}, {"hello"}} {
		if a.Bypass(args) {
			t.Fatalf("%v should not bypass", args)
		}
	}
}

func TestDiscoverBindsTheSessionByDirectoryAndStart(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	// pi names the directory after the resolved path, as Discover does.
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	dir := SessionDir(home, cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	write := func(name, ts string) string {
		p := filepath.Join(dir, name)
		line := `{"type":"session","version":3,"id":"s","timestamp":"` + ts + `","cwd":"` + cwd + `"}` + "\n"
		if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("old.jsonl", start.Add(-time.Hour).UTC().Format(time.RFC3339Nano))
	a := &Adapter{Home: home}
	if _, err := a.Discover(cwd, start); !errors.Is(err, adapter.ErrNoTranscript) {
		t.Fatalf("err = %v, want ErrNoTranscript", err)
	}
	want := write("now.jsonl", start.Add(time.Second).UTC().Format(time.RFC3339Nano))
	got, err := a.Discover(cwd, start)
	if err != nil || got != want {
		t.Fatalf("Discover = %q, %v; want %q", got, err, want)
	}
	// A session of another directory is not this session.
	if _, err := (&Adapter{Home: home}).Discover(t.TempDir(), start); !errors.Is(err, adapter.ErrNoTranscript) {
		t.Fatalf("err = %v, want ErrNoTranscript for another directory", err)
	}
}
