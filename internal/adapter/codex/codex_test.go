package codex

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

const fixtureVersion = "0.153.4"

// loadFixture replays a recorded Codex session into a screen model and
// returns the rows the mode provides as history.
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
	return s, historyRows(s), tr
}

func historyRows(s *screen.Screen) []string {
	var rows []string
	if !s.AltActive() {
		for _, l := range s.History() {
			rows = append(rows, l.String())
		}
	}
	return append(rows, s.Text()...)
}

type expectation struct {
	kind        blocks.Kind
	text        string
	first, last int
}

// The rows the recorded reply occupies, read off the replayed fixture.
var expected = []expectation{
	{blocks.Heading, "Plan", 0, 0},
	{blocks.ListItem, "Read the config file and note the two ports that the service listens on for HTTP and metrics", 2, 3},
	{blocks.ListItem, "Change the handler", 5, 5},
	{blocks.ListItem, "keep the old route", 6, 6},
	{blocks.ListItem, "add a fallback that logs and returns 404", 7, 7},
	{blocks.ListItem, "Verify", 9, 9},
	{blocks.CodeLine, "func handle(w http.ResponseWriter, r *http.Request) {", 11, 11},
	{blocks.CodeLine, "w.WriteHeader(404)", 12, 12},
	{blocks.CodeLine, "}", 13, 13},
	{blocks.DiffLine, "-    return nil", 15, 15},
	{blocks.DiffLine, `+    return errors.New("boom")`, 16, 16},
	{blocks.Paragraph, "That is the whole plan.", 18, 18},
}

func TestFixtureAlignsEveryBlockToItsRows(t *testing.T) {
	a := &Adapter{}
	s, rows, tr := loadFixture(t, "inline")
	if tr.Version != fixtureVersion || tr.SessionID == "" || len(tr.Turns) != 1 {
		t.Fatalf("transcript = version %q session %q turns %d", tr.Version, tr.SessionID, len(tr.Turns))
	}
	if a.Mode(s) != adapter.ModeInline {
		t.Fatalf("mode = %s, want inline", a.Mode(s))
	}
	al := a.Align(tr, rows)
	if len(al) != 1 || !al[0].Aligned {
		t.Fatalf("alignment = %+v", al)
	}
	got := al[0].Blocks
	for _, want := range expected {
		found := false
		for _, b := range got {
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
			t.Fatalf("no %s block %q in %s", want.kind, want.text, dump(got))
		}
	}
}

func dump(bs []adapter.AlignedBlock) string {
	var b strings.Builder
	for _, x := range bs {
		b.WriteString("\n  " + string(x.Kind) + " " + strings.TrimSpace(x.Text) + "  rows " + itoa(x.First) + ".." + itoa(x.Last))
	}
	return b.String()
}

func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func TestScreenFactsComeFromTheRecording(t *testing.T) {
	a := &Adapter{}
	s, _, _ := loadFixture(t, "inline")
	rows := s.Text()
	in := a.InputRow(rows)
	if in < 0 || !strings.Contains(rows[in], "Ask Codex") {
		t.Fatalf("input row = %d (%q)", in, rowAt(rows, in))
	}
	// The recorded session ends idle, with no dialog up.
	if a.Busy(s) || a.Prompt(s) {
		t.Fatalf("busy=%v prompt=%v at the end of the recording", a.Busy(s), a.Prompt(s))
	}
	if !a.QueuesWhenBusy() {
		t.Fatal("Codex queues input while busy")
	}
	// The dialogs and the working line, as the CLI draws them.
	for _, row := range []string{
		"  Do you trust the contents of this directory?   Press enter to continue",
		"› 1. Yes, continue    Esc to cancel",
	} {
		if !a.Prompt(showing(row)) {
			t.Fatalf("not recognised as a prompt: %q", row)
		}
	}
	for _, row := range []string{"• Working (4s · esc to interrupt)", "Working  esc to interrupt"} {
		if !a.Busy(showing(row)) {
			t.Fatalf("not recognised as busy: %q", row)
		}
	}
	if a.Prompt(showing("• Working (4s · esc to interrupt)")) {
		t.Fatal("a working line is not a prompt")
	}
}

func showing(row string) *screen.Screen {
	s := screen.New(80, 6)
	_, _ = s.Write([]byte("\x1b[1;1H" + row))
	return s
}

func rowAt(rows []string, i int) string {
	if i < 0 || i >= len(rows) {
		return ""
	}
	return rows[i]
}

func TestAnotherVersionIsRefusedLoudly(t *testing.T) {
	a := &Adapter{}
	line := `{"type":"session_meta","payload":{"session_id":"s","cwd":"/tmp","cli_version":"0.99.0"}}`
	_, err := a.Parse(strings.NewReader(line + "\n"))
	var ve *adapter.VersionError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want a version error", err)
	}
	if ve.Version != "0.99.0" || !strings.Contains(ve.Error(), fixtureVersion) {
		t.Fatalf("version error = %q", ve.Error())
	}
}

func TestCorruptTranscriptStillLeavesParagraphs(t *testing.T) {
	a := &Adapter{}
	_, rows, tr := loadFixture(t, "inline")
	tr.Turns[0].Blocks = blocks.Parse("Something the screen never showed.")
	al := a.Align(tr, rows)
	if len(al) != 1 || al[0].Aligned || len(al[0].Blocks) < 3 {
		t.Fatalf("alignment = %+v", al)
	}
	if al[0].Blocks[0].First != 0 {
		t.Fatalf("fallback blocks start at %d", al[0].Blocks[0].First)
	}
	// With no transcript at all, the same rows still yield paragraphs.
	fb := a.Fallback(rows)
	if len(fb) != 1 || fb[0].Aligned || len(fb[0].Blocks) < 3 {
		t.Fatalf("fallback = %+v", fb)
	}
}

func TestBypassRecognisesCodexsOwnNonInteractiveForms(t *testing.T) {
	a := &Adapter{}
	for _, args := range [][]string{{"exec", "do the thing"}, {"--version"}, {"-h"}, {"--help"}} {
		if !a.Bypass(args) {
			t.Fatalf("%v should bypass", args)
		}
	}
	for _, args := range [][]string{{}, {"--model", "gpt-6"}, {"resume"}} {
		if a.Bypass(args) {
			t.Fatalf("%v should not bypass", args)
		}
	}
}

func TestDiscoverBindsTheSessionByDirectoryAndStart(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".codex", "sessions", "2026", "09", "09")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	start := time.Now()
	write := func(name, dirField, ts string) string {
		p := filepath.Join(dir, name)
		line := `{"timestamp":"` + ts + `","type":"session_meta","payload":{"session_id":"s","cwd":"` + dirField + `","cli_version":"` + fixtureVersion + `","timestamp":"` + ts + `"}}` + "\n"
		if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// An older session in the same directory, and a current one elsewhere.
	write("rollout-old.jsonl", cwd, start.Add(-time.Hour).UTC().Format(time.RFC3339Nano))
	write("rollout-other.jsonl", filepath.Join(home, "elsewhere"), start.UTC().Format(time.RFC3339Nano))
	a := &Adapter{Home: home}
	if _, err := a.Discover(cwd, start); !errors.Is(err, adapter.ErrNoTranscript) {
		t.Fatalf("err = %v, want ErrNoTranscript", err)
	}
	want := write("rollout-now.jsonl", cwd, start.Add(time.Second).UTC().Format(time.RFC3339Nano))
	got, err := a.Discover(cwd, start)
	if err != nil || got != want {
		t.Fatalf("Discover = %q, %v; want %q", got, err, want)
	}
}
