package claude

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

// fixtureVersion is the newest verified version, the one the tests that need
// a single fixture replay.
const fixtureVersion = "2.1.268"

// loadFixture replays the newest recorded session into a screen model and
// returns the rows the mode provides as history: scrollback plus screen
// inline, the visible screen in fullscreen.
func loadFixture(t *testing.T, mode string) (*screen.Screen, []string, *adapter.Transcript) {
	t.Helper()
	return loadFixtureAt(t, fixtureVersion, mode)
}

func loadFixtureAt(t *testing.T, version, mode string) (*screen.Screen, []string, *adapter.Transcript) {
	t.Helper()
	dir := filepath.Join("testdata", version)
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
	a := &Adapter{}
	tr, err := a.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	// A fixture is the record of what a pinned version draws.
	if err := adapter.RequireVerified(a, tr); err != nil {
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

// The expected rows were read off the replayed fixtures by hand: in inline
// mode the turn sits below the echoed prompt in scrollback, in fullscreen
// mode it sits on the alternate screen.
func expected(offset int) []expectation {
	return []expectation{
		{blocks.Heading, "Plan", 0, 0},
		{blocks.ListItem, "Read the config file and note the two ports that the service listens on for HTTP and metrics", 2, 3},
		{blocks.ListItem, "Change the handler", 4, 4},
		{blocks.ListItem, "keep the old route", 5, 5},
		{blocks.ListItem, "add a fallback that logs and returns 404", 6, 6},
		{blocks.ListItem, "Verify", 7, 7},
		{blocks.CodeBlock, "", 8, 10},
		{blocks.CodeLine, "func handle(w http.ResponseWriter, r *http.Request) {", 8, 8},
		{blocks.CodeLine, "    w.WriteHeader(404)", 9, 9},
		{blocks.CodeLine, "}", 10, 10},
		{blocks.CodeBlock, "", 11, 12},
		{blocks.DiffLine, "-    return nil", 11, 11},
		{blocks.DiffLine, "+    return errors.New(\"boom\")", 12, 12},
		{blocks.Paragraph, "That is the whole plan.", 13, 13},
	}
}

// TestFixturesAlignInBothModes replays every pinned fixture in both rendering
// modes and checks each block against the rows read off it by hand.
//
// Covers S-016 T-05.
func TestFixturesAlignInBothModes(t *testing.T) {
	cases := []struct {
		mode   string
		want   adapter.Mode
		offset int
	}{
		{"inline", adapter.ModeInline, 21},
		{"fullscreen", adapter.ModeFullscreen, 3},
	}
	a := &Adapter{}
	// Every verified version keeps its fixtures, and every one must align.
	for _, version := range Verified {
		for _, c := range cases {
			t.Run(version+"/"+c.mode, func(t *testing.T) {
				s, rows, tr := loadFixtureAt(t, version, c.mode)
				if tr.Version != version || len(tr.Turns) != 1 {
					t.Fatalf("transcript version %q turns %d", tr.Version, len(tr.Turns))
				}
				if got := a.Mode(s); got != c.want {
					t.Fatalf("mode = %s, want %s", got, c.want)
				}
				al := a.Align(tr, rows)
				if len(al) != 1 || !al[0].Aligned {
					t.Fatalf("alignment = %+v", al)
				}
				want := expected(c.offset)
				if len(al[0].Blocks) != len(want) {
					for _, b := range al[0].Blocks {
						t.Logf("%s %q %d-%d", b.Kind, b.Text, b.First, b.Last)
					}
					t.Fatalf("%d blocks, want %d", len(al[0].Blocks), len(want))
				}
				for i, w := range want {
					got := al[0].Blocks[i]
					if got.Kind != w.kind || (w.text != "" && got.Text != w.text) {
						t.Fatalf("block %d = %s %q, want %s %q", i, got.Kind, got.Text, w.kind, w.text)
					}
					if got.First != w.first+c.offset || got.Last != w.last+c.offset {
						t.Fatalf("block %d (%s %q) rows %d-%d, want %d-%d", i, got.Kind, got.Text, got.First, got.Last, w.first+c.offset, w.last+c.offset)
					}
					// The rows really carry the block's text.
					if got.Kind != blocks.CodeBlock && !strings.Contains(joinRows(rows, got.First, got.Last), firstWord(got.Text)) {
						t.Fatalf("block %d rows %q do not contain %q", i, joinRows(rows, got.First, got.Last), firstWord(got.Text))
					}
				}
			})
		}
	}
}

func joinRows(rows []string, first, last int) string {
	return strings.Join(rows[first:last+1], "\n")
}

func firstWord(s string) string {
	if f := strings.Fields(strings.Trim(s, "-+ ")); len(f) > 0 {
		return strings.Trim(f[0], "(){}\"")
	}
	return s
}

func TestCorruptTranscriptFallsBackToParagraphs(t *testing.T) {
	a := &Adapter{}
	s, rows, tr := loadFixture(t, "inline")
	_ = s
	// The transcript parses but says something else than the screen shows.
	tr.Turns[0].Blocks = blocks.Parse("Something the screen never showed.")
	al := a.Align(tr, rows)
	if len(al) != 1 || al[0].Aligned {
		t.Fatalf("alignment = %+v", al)
	}
	if len(al[0].Blocks) < 3 || al[0].Blocks[0].Kind != blocks.Paragraph || al[0].Blocks[0].First != 21 {
		t.Fatalf("fallback blocks = %+v", al[0].Blocks)
	}
	if al[0].Blocks[0].Text != "Plan" {
		t.Fatalf("first fallback paragraph = %q", al[0].Blocks[0].Text)
	}
	// The file itself is unreadable: no transcript at all still yields paragraphs.
	if _, err := a.Parse(strings.NewReader("{not json\n")); err == nil {
		t.Fatal("garbage should not parse")
	}
	fb := a.Fallback(rows)
	if len(fb) != 1 || fb[0].Aligned || len(fb[0].Blocks) < 3 || fb[0].Blocks[0].First != 21 {
		t.Fatalf("fallback = %+v", fb)
	}
	last := fb[0].Blocks[len(fb[0].Blocks)-1]
	if last.Last >= 38 { // the region ends at the prompt marker row
		t.Fatalf("fallback ran into the prompt: %+v", last)
	}
}

// TestAnUnverifiedVersionAlignsButNoFixtureTakesIt: a live session at a version
// the fixtures do not pin is parsed and aligned, marked unverified, and only a
// fixture refuses it, naming the version.
//
// Covers S-016 T-03.
func TestAnUnverifiedVersionAlignsButNoFixtureTakesIt(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", fixtureVersion, "inline.transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	other := strings.ReplaceAll(string(data), fixtureVersion, "9.9.9")
	a := &Adapter{}
	tr, err := a.Parse(strings.NewReader(other))
	if err != nil || tr.Version != "9.9.9" || !tr.Unverified {
		t.Fatalf("parse = %+v, %v", tr, err)
	}
	_, rows, pinned := loadFixture(t, "inline")
	if pinned.Unverified {
		t.Fatal("a pinned version is marked unverified")
	}
	al := a.Align(tr, rows)
	if len(al) != 1 || !al[0].Aligned || len(al[0].Blocks) != len(expected(0)) {
		t.Fatalf("alignment = %+v", al)
	}
	err = adapter.RequireVerified(a, tr)
	var ve *adapter.VersionError
	if !errors.As(err, &ve) || ve.Version != "9.9.9" || !strings.Contains(err.Error(), "9.9.9") || !strings.Contains(err.Error(), fixtureVersion) {
		t.Fatalf("err = %v", err)
	}
}

var modes = []struct {
	mode   string
	offset int
}{{"inline", 21}, {"fullscreen", 3}}

// TestFallbackGivesEachListItemItsOwnBlock: with no transcript to align, or one
// that no longer matches, each item of the fixture's tight, nested list is a
// block of its own.
//
// Covers S-016 T-01.
func TestFallbackGivesEachListItemItsOwnBlock(t *testing.T) {
	a := &Adapter{}
	for _, c := range modes {
		_, rows, tr := loadFixture(t, c.mode)
		tr.Turns[0].Blocks = blocks.Parse("Something the screen never showed.")
		for name, al := range map[string][]adapter.TurnAlignment{"missing": a.Fallback(rows), "corrupt": a.Align(tr, rows)} {
			var got []adapter.AlignedBlock
			for _, turn := range al {
				got = append(got, turn.Blocks...)
			}
			for _, w := range expected(c.offset) {
				if w.kind != blocks.ListItem {
					continue
				}
				if !hasSpan(got, w.first+c.offset, w.last+c.offset) {
					t.Fatalf("%s %s: no block covers only %q (rows %d-%d): %+v", c.mode, name, w.text, w.first+c.offset, w.last+c.offset, got)
				}
			}
		}
	}
}

// TestOneUnmatchedBlockKeepsTheOthers: one block the screen draws differently
// from the transcript gives up only its own rows, to a paragraph, and every
// other block keeps the rows it matched.
//
// Covers S-016 T-04.
func TestOneUnmatchedBlockKeepsTheOthers(t *testing.T) {
	a := &Adapter{}
	for _, c := range modes {
		_, rows, tr := loadFixture(t, c.mode)
		want := expected(c.offset)
		const lost = 3 // "keep the old route", on a row of its own
		tr.Turns[0].Blocks[lost].Text = "keep the new route instead"
		al := a.Align(tr, rows)
		if len(al) != 1 || !al[0].Aligned {
			t.Fatalf("%s: alignment = %+v", c.mode, al)
		}
		for i, w := range want {
			if i == lost {
				continue
			}
			if !hasKind(al[0].Blocks, w.kind, w.first+c.offset, w.last+c.offset) {
				t.Fatalf("%s: block %d (%s %q) lost its rows: %+v", c.mode, i, w.kind, w.text, al[0].Blocks)
			}
		}
		row := want[lost].first + c.offset
		if !hasKind(al[0].Blocks, blocks.Paragraph, row, row) {
			t.Fatalf("%s: the unmatched block's row %d is not a paragraph: %+v", c.mode, row, al[0].Blocks)
		}
		// A code line still names its code block.
		for _, b := range al[0].Blocks {
			if b.Kind == blocks.CodeLine && (b.Parent < 0 || al[0].Blocks[b.Parent].Kind != blocks.CodeBlock) {
				t.Fatalf("%s: code line %q lost its block: parent %d", c.mode, b.Text, b.Parent)
			}
		}
	}
}

func hasSpan(bs []adapter.AlignedBlock, first, last int) bool {
	for _, b := range bs {
		if b.First == first && b.Last == last {
			return true
		}
	}
	return false
}

func hasKind(bs []adapter.AlignedBlock, kind blocks.Kind, first, last int) bool {
	for _, b := range bs {
		if b.Kind == kind && b.First == first && b.Last == last {
			return true
		}
	}
	return false
}

func TestParseGroupsByMessageAndSummarisesTools(t *testing.T) {
	transcript := strings.Join([]string{
		`{"type":"user","version":"2.1.266","sessionId":"s1","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","version":"2.1.266","sessionId":"s1","message":{"id":"m1","role":"assistant","content":[{"type":"thinking","thinking":"..."},{"type":"text","text":"First.\n\nSecond."}]}}`,
		`{"type":"assistant","version":"2.1.266","sessionId":"s1","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la\n# more","description":"list"}}]}}`,
		`{"type":"assistant","version":"2.1.266","sessionId":"s1","message":{"id":"m2","role":"assistant","content":[{"type":"text","text":"Done."}]}}`,
	}, "\n")
	tr, err := (&Adapter{}).Parse(strings.NewReader(transcript))
	if err != nil {
		t.Fatal(err)
	}
	if tr.SessionID != "s1" || len(tr.Turns) != 2 {
		t.Fatalf("transcript = %+v", tr)
	}
	b := tr.Turns[0].Blocks
	if len(b) != 3 || b[2].Kind != blocks.ToolCall || b[2].Text != "Bash(ls -la)" {
		t.Fatalf("turn 1 blocks = %+v", b)
	}
	if tr.Turns[1].Ordinal != 2 || tr.Turns[1].Blocks[0].Text != "Done." {
		t.Fatalf("turn 2 = %+v", tr.Turns[1])
	}
}

func TestProjectDirEncoding(t *testing.T) {
	got := ProjectDir("/home/u", "/private/tmp/diple-fixture")
	if got != filepath.Join("/home/u", ".claude", "projects", "-private-tmp-diple-fixture") {
		t.Fatalf("ProjectDir = %q", got)
	}
	if got := filepath.Base(ProjectDir("/h", "/a/.b_c d")); got != "-a--b-c-d" {
		t.Fatalf("encoded = %q", got)
	}
}

func TestDiscover(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	a := &Adapter{Home: home}
	start := time.Now()
	if _, err := a.Discover(cwd, start); !errors.Is(err, adapter.ErrNoTranscript) {
		t.Fatalf("err = %v, want ErrNoTranscript", err)
	}
	real, _ := filepath.EvalSymlinks(cwd)
	dir := ProjectDir(home, real)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := func(at time.Time) []byte {
		return []byte(`{"type":"mode"}` + "\n" + `{"type":"user","timestamp":"` + at.UTC().Format(time.RFC3339Nano) + `"}` + "\n")
	}
	old := filepath.Join(dir, "old.jsonl")
	if err := os.WriteFile(old, stamp(start.Add(-time.Hour)), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := start.Add(-time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Discover(cwd, start); !errors.Is(err, adapter.ErrNoTranscript) {
		t.Fatalf("stale transcript must not be discovered: %v", err)
	}
	// An earlier session's file touched after this session started is still
	// not this session's transcript: its first entry predates the start.
	if err := os.Chtimes(old, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Discover(cwd, start); !errors.Is(err, adapter.ErrNoTranscript) {
		t.Fatalf("earlier session's transcript must not be discovered: %v", err)
	}
	fresh := filepath.Join(dir, "fresh.jsonl")
	if err := os.WriteFile(fresh, stamp(start.Add(time.Second)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := a.Discover(cwd, start)
	if err != nil || got != fresh {
		t.Fatalf("Discover = %q, %v", got, err)
	}
}

func TestRegistered(t *testing.T) {
	a, ok := adapter.For("claude")
	if !ok || a.Name() != "claude" || a.Bypass([]string{"--version"}) != true {
		t.Fatalf("registry = %v %v", a, ok)
	}
}

func TestInputRow(t *testing.T) {
	a := &Adapter{}
	for _, mode := range []string{"inline", "fullscreen"} {
		s, _, _ := loadFixture(t, mode)
		rows := s.Text()
		got := a.InputRow(rows)
		if got < 0 || !strings.HasPrefix(rows[got+1], PromptMarker) || !isRule(rows[got]) {
			t.Fatalf("%s: input row = %d (%q)", mode, got, rows[max(got, 0)])
		}
	}
	if a.InputRow([]string{"nothing", "here"}) != -1 {
		t.Fatal("no prompt must give -1")
	}
	if a.InputRow([]string{"❯ typed"}) != 0 {
		t.Fatal("prompt without a rule is the box itself")
	}
}

func TestWindowedViewportAlignsTheTurnsItShows(t *testing.T) {
	a := &Adapter{}
	for _, mode := range []string{"inline", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			_, rows, tr := loadFixture(t, mode)
			shown := tr.Turns[0]
			// The rows show one turn of a longer conversation, as a
			// fullscreen viewport scrolled into the middle of it does.
			windowed := &adapter.Transcript{Agent: tr.Agent, Version: tr.Version, SessionID: tr.SessionID}
			windowed.Turns = append(windowed.Turns,
				adapter.Turn{Ordinal: 1, ID: "earlier", Blocks: blocks.Parse("An earlier turn the window does not show.")},
				adapter.Turn{Ordinal: 2, ID: shown.ID, Blocks: shown.Blocks},
				adapter.Turn{Ordinal: 3, ID: "later", Blocks: blocks.Parse("A later turn the window does not show.")},
			)
			al := a.Align(windowed, rows)
			if len(al) != 3 {
				t.Fatalf("alignment = %+v", al)
			}
			if !al[1].Aligned || len(al[1].Blocks) != len(shown.Blocks) {
				t.Fatalf("the shown turn did not align: %+v", al[1])
			}
			for _, i := range []int{0, 2} {
				if al[i].Aligned || len(al[i].Blocks) != 0 {
					t.Fatalf("turn %d is not on screen but got %+v", al[i].Turn, al[i])
				}
			}
		})
	}
}

// The dialog footers Claude Code 2.1.266 draws, read off real sessions: a
// permission question, the model and effort choosers, and the workspace
// trust question.
var promptFooters = []string{
	"Esc to cancel · Tab to amend",
	"Enter to set as default · s to use this session only · Esc to cancel",
	"←/→ to adjust · Enter to confirm · s for this session only · Esc to cancel",
	"Enter to confirm · Esc to cancel",
}

// What the same footer row carries when no dialog is up.
var notPrompts = []string{
	"✻ Skedaddling… esc to interrupt · ← for agents",
	"⏵⏵ auto mode on (shift+tab to cycle) · ? for shortcuts",
	"❯ Try \"write a test for <filepath>\"",
	"",
}

func screenShowing(row string) *screen.Screen {
	s := screen.New(80, 6)
	_, _ = s.Write([]byte("\x1b[1;1H" + row))
	return s
}

func TestPromptRecognisesTheAgentsOwnDialogs(t *testing.T) {
	a := &Adapter{}
	for _, row := range promptFooters {
		if !a.Prompt(screenShowing(row)) {
			t.Fatalf("not recognised as a prompt: %q", row)
		}
	}
	for _, row := range notPrompts {
		if a.Prompt(screenShowing(row)) {
			t.Fatalf("mistaken for a prompt: %q", row)
		}
	}
	// A working agent is busy, never prompting.
	busy := screenShowing("✻ Skedaddling… esc to interrupt · ← for agents")
	if !a.Busy(busy) || a.Prompt(busy) {
		t.Fatalf("busy=%v prompt=%v", a.Busy(busy), a.Prompt(busy))
	}
	// Neither recorded fixture shows a dialog.
	for _, mode := range []string{"inline", "fullscreen"} {
		s, _, _ := loadFixture(t, mode)
		if a.Prompt(s) {
			t.Fatalf("%s fixture reported a prompt", mode)
		}
	}
}
