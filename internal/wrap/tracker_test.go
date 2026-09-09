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

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestTrackerDiscoversAndRefreshes(t *testing.T) {
	cwd := t.TempDir()
	fa := &fileAdapter{}
	var found []string
	var mu sync.Mutex
	tr := NewTracker(fa, cwd, func(id string) { mu.Lock(); found = append(found, id); mu.Unlock() })
	tr.poll = 20 * time.Millisecond
	tr.Start()
	defer tr.Stop()

	time.Sleep(60 * time.Millisecond)
	if tr.Path() != "" {
		t.Fatal("discovered before the file existed")
	}
	p := filepath.Join(cwd, "transcript.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { tx, _ := tr.Transcript(); return tx != nil })
	tx, err := tr.Transcript()
	if err != nil || tx.Turns[0].Blocks[0].Text != "hello" || tr.Path() != p {
		t.Fatalf("transcript = %+v err = %v path = %q", tx, err, tr.Path())
	}
	mu.Lock()
	if len(found) != 1 || found[0] != "sid" {
		t.Fatalf("onFound = %v", found)
	}
	mu.Unlock()

	// A change re-parses; an unchanged file does not.
	fa.mu.Lock()
	before := fa.parses
	fa.mu.Unlock()
	time.Sleep(60 * time.Millisecond)
	fa.mu.Lock()
	if fa.parses != before {
		t.Fatalf("re-parsed an unchanged file: %d -> %d", before, fa.parses)
	}
	fa.mu.Unlock()
	if err := os.WriteFile(p, []byte("hello again"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p, future, future)
	waitUntil(t, func() bool { tx, _ := tr.Transcript(); return tx != nil && tx.Turns[0].Blocks[0].Text == "hello again" })

	// A version error is surfaced and the last good transcript is kept.
	if err := os.WriteFile(p, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(4 * time.Second)
	_ = os.Chtimes(p, later, later)
	waitUntil(t, func() bool { _, err := tr.Transcript(); return err != nil })
	tx, err = tr.Transcript()
	var ve *adapter.VersionError
	if !errors.As(err, &ve) || tx == nil {
		t.Fatalf("transcript = %v err = %v", tx, err)
	}
}
