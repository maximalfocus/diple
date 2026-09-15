package wrap

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/maximalfocus/diple/internal/adapter"
	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/card"
	"github.com/maximalfocus/diple/internal/screen"
)

// fileAdapter is a minimal adapter whose transcript is a file in cwd.
type fileAdapter struct {
	mu     sync.Mutex
	parses int
}

func (f *fileAdapter) Name() string               { return "fake" }
func (f *fileAdapter) VerifiedVersions() []string { return []string{"1"} }
func (f *fileAdapter) Bypass([]string) bool       { return false }
func (f *fileAdapter) Discover(cwd string, _ time.Time) (string, error) {
	p := filepath.Join(cwd, "transcript.txt")
	if _, err := os.Stat(p); err != nil {
		return "", adapter.ErrNoTranscript
	}
	return p, nil
}
func (f *fileAdapter) Parse(r io.Reader) (*adapter.Transcript, error) {
	f.mu.Lock()
	f.parses++
	f.mu.Unlock()
	data, _ := io.ReadAll(r)
	if string(data) == "bad" {
		return nil, &adapter.VersionError{Agent: "fake", Version: "2", Verified: []string{"1"}}
	}
	return &adapter.Transcript{Agent: "fake", SessionID: "sid", Turns: []adapter.Turn{{Ordinal: 1, Blocks: blocks.Parse(string(data))}}}, nil
}
func (f *fileAdapter) Mode(*screen.Screen) adapter.Mode                            { return adapter.ModeInline }
func (f *fileAdapter) Align(*adapter.Transcript, []string) []adapter.TurnAlignment { return nil }
func (f *fileAdapter) Fallback([]string) []adapter.TurnAlignment                   { return nil }
func (f *fileAdapter) Marker(string) string                                        { return "" }
func (f *fileAdapter) InputRow([]string) int                                       { return -1 }
func (f *fileAdapter) Busy(*screen.Screen) bool                                    { return false }
func (f *fileAdapter) Prompt(*screen.Screen) bool                                  { return false }
func (f *fileAdapter) QueuesWhenBusy() bool                                        { return true }

func (f *fileAdapter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.parses
}

// writeAt writes p and gives it a distinct modification time, so a change
// is visible even on filesystems with coarse timestamps.
func writeAt(t *testing.T, p string, data string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerTickDiscoversRefreshesAndKeepsLastGood(t *testing.T) {
	cwd := t.TempDir()
	fa := &fileAdapter{}
	var found []string
	tr := NewTracker(fa, cwd, func(id string) { found = append(found, id) })

	// Nothing to discover yet.
	tr.tick()
	if tr.Path() != "" || fa.count() != 0 {
		t.Fatalf("discovered before the file existed: path %q parses %d", tr.Path(), fa.count())
	}

	// The file appears: discovered and parsed once, onFound called once.
	p := filepath.Join(cwd, "transcript.txt")
	base := time.Now().Add(-time.Minute)
	writeAt(t, p, "hello", base)
	tr.tick()
	tx, err := tr.Transcript()
	if err != nil || tx == nil || tx.Turns[0].Blocks[0].Text != "hello" || tr.Path() != p || fa.count() != 1 {
		t.Fatalf("after discovery: transcript %+v err %v path %q parses %d", tx, err, tr.Path(), fa.count())
	}
	if len(found) != 1 || found[0] != "sid" {
		t.Fatalf("onFound = %v", found)
	}

	// An unchanged file is not parsed again.
	tr.tick()
	tr.tick()
	if fa.count() != 1 {
		t.Fatalf("re-parsed an unchanged file: %d", fa.count())
	}

	// A change is picked up.
	writeAt(t, p, "hello again", base.Add(time.Second))
	tr.tick()
	tx, err = tr.Transcript()
	if err != nil || tx.Turns[0].Blocks[0].Text != "hello again" || fa.count() != 2 {
		t.Fatalf("after change: transcript %+v err %v parses %d", tx, err, fa.count())
	}
	if len(found) != 1 {
		t.Fatalf("onFound called again: %v", found)
	}

	// A version the adapter refuses is surfaced; the last good transcript stays.
	writeAt(t, p, "bad", base.Add(2*time.Second))
	tr.tick()
	tx, err = tr.Transcript()
	var ve *adapter.VersionError
	if !errors.As(err, &ve) || tx == nil || tx.Turns[0].Blocks[0].Text != "hello again" {
		t.Fatalf("after bad version: transcript %+v err %v", tx, err)
	}
}

func TestTrackerLoopRunsAndStops(t *testing.T) {
	cwd := t.TempDir()
	fa := &fileAdapter{}
	// The loop announces the transcript it discovers, after storing it, so the
	// test waits on that rather than on the clock. A second announcement must
	// never block the loop, so it is counted, not waited for.
	found := make(chan string, 4)
	tr := NewTracker(fa, cwd, func(id string) {
		select {
		case found <- id:
		default:
		}
	})
	tr.poll = 10 * time.Millisecond
	writeAt(t, filepath.Join(cwd, "transcript.txt"), "hello", time.Now().Add(-time.Minute))
	tr.Start()
	// The deadline only detects a loop that never discovers anything.
	select {
	case <-found:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never discovered the transcript")
	}
	tr.Stop()
	tx, err := tr.Transcript()
	if err != nil || tx == nil || tx.Turns[0].Blocks[0].Text != "hello" {
		t.Fatalf("loop did not discover: %+v %v", tx, err)
	}
	if n := len(found); n != 0 {
		t.Fatalf("onFound was called %d more times", n)
	}
	// Stop is idempotent.
	tr.Stop()
}

// TestSessionKeyFallsBackToTheTranscriptFile: a transcript Diple could not
// parse still identifies its session by the file it lives in. What a
// transcript's contents decide is what can be aligned, never whether the cards
// written against it are worth keeping.
func TestSessionKeyFallsBackToTheTranscriptFile(t *testing.T) {
	parsed := &adapter.Transcript{SessionID: "sid"}
	if got := sessionKey(parsed, "/a/b/other.jsonl"); got != "sid" {
		t.Fatalf("a parsed transcript keeps its own id: %q", got)
	}
	if got := sessionKey(nil, "/a/b/e2cd5ede-a2c8.jsonl"); got != "e2cd5ede-a2c8" {
		t.Fatalf("an unparsable transcript keys on its file: %q", got)
	}
	if got := sessionKey(&adapter.Transcript{}, "/a/b/c.jsonl"); got != "c" {
		t.Fatalf("a transcript with no id of its own keys on its file: %q", got)
	}
	if got := sessionKey(nil, ""); got != "" {
		t.Fatalf("no transcript at all is no key: %q", got)
	}
}

// TestTrackerBindsASessionEvenWhenTheVersionIsRefused is the tracker's half of
// the same thing: a version the adapter does not pin is reported loudly, and
// the session is still named.
func TestTrackerBindsASessionEvenWhenTheVersionIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, filepath.Join(dir, "transcript.txt"), "bad", time.Now())
	got := make(chan string, 1)
	tr := NewTracker(&fileAdapter{}, dir, func(id string) { got <- id })
	tr.poll = 5 * time.Millisecond
	tr.since = time.Now().Add(-time.Hour)
	tr.Start()
	defer tr.Stop()
	select {
	case id := <-got:
		if id != "transcript" {
			t.Fatalf("session id = %q, want the transcript file's own name", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the tracker never bound a session")
	}
	// The refusal is still reported, and still names the version.
	if _, err := tr.Transcript(); err == nil {
		t.Fatal("a refused version must still be reported")
	} else {
		var ve *adapter.VersionError
		if !errors.As(err, &ve) || ve.Version != "2" {
			t.Fatalf("err = %v, want a version error naming 2", err)
		}
	}
}

// TestTrayPersistsUnderARefusedVersion is the journey the acceptance run found
// broken: a card made against a transcript the adapter refuses on version was
// lost the moment the agent exited, because the session had no name to be
// saved under.
func TestTrayPersistsUnderARefusedVersion(t *testing.T) {
	dir := t.TempDir()
	store := &card.Store{Dir: filepath.Join(dir, "trays")}
	writeAt(t, filepath.Join(dir, "transcript.txt"), "bad", time.Now())

	bind := func() *Session {
		s, _, _ := newTestSession(80, 24)
		s.UseAdapter(&fileAdapter{}, "fake")
		s.UseStore(store)
		got := make(chan string, 1)
		tr := NewTracker(&fileAdapter{}, dir, func(id string) { got <- id })
		tr.poll = 5 * time.Millisecond
		tr.since = time.Now().Add(-time.Hour)
		s.Tracker = tr
		tr.Start()
		t.Cleanup(tr.Stop)
		select {
		case id := <-got:
			if err := s.SetSessionID(id); err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the tracker never bound a session")
		}
		return s
	}

	first := bind()
	first.Tray.Add(&card.Card{Kind: card.Anchored, Tag: "fix", Text: "survives a restart",
		Anchor: card.Anchor{Turn: 1, Quote: "ports"}})
	first.mu.Lock()
	err := first.saveLocked()
	first.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	// The agent exits and starts again against the same transcript.
	second := bind()
	if second.Tray.Len() != 1 || second.Tray.Cards[0].Text != "survives a restart" ||
		second.Tray.Cards[0].Tag != "fix" || second.Tray.Cards[0].Anchor.Quote != "ports" {
		t.Fatalf("the tray did not come back: %+v", second.Tray.Cards)
	}

	// And stash reaches this session's own tray, not some older one's.
	n, err := store.Stash("fake")
	if err != nil || n != 1 {
		t.Fatalf("stash: n=%d err=%v", n, err)
	}
	if n, err := store.Unstash("fake"); err != nil || n != 1 {
		t.Fatalf("unstash: n=%d err=%v", n, err)
	}
	third := bind()
	if third.Tray.Len() != 1 || third.Tray.Cards[0].Text != "survives a restart" {
		t.Fatalf("the unstashed tray did not reach the next session: %+v", third.Tray.Cards)
	}
}
