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
	var mu sync.Mutex
	var found []string
	tr := NewTracker(fa, cwd, func(id string) { mu.Lock(); found = append(found, id); mu.Unlock() })
	tr.poll = 10 * time.Millisecond
	writeAt(t, filepath.Join(cwd, "transcript.txt"), "hello", time.Now().Add(-time.Minute))
	tr.Start()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if tx, _ := tr.Transcript(); tx != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	tr.Stop()
	tx, err := tr.Transcript()
	if err != nil || tx == nil || tx.Turns[0].Blocks[0].Text != "hello" {
		t.Fatalf("loop did not discover: %+v %v", tx, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(found) != 1 {
		t.Fatalf("onFound = %v", found)
	}
	// Stop is idempotent.
	tr.Stop()
}
