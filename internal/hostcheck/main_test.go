package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/record"
)

// capture writes a synthetic capture: what an agent drew, and what a user
// typed into the host.
func capture(t *testing.T, out string, typed ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "capture.jsonl")
	w, err := record.Create(p, record.Header{Agent: "fake", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	w.Output([]byte(out))
	for _, in := range typed {
		w.Input([]byte(in))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

const reply = "\x1b[1m⏺ Plan\x1b[0m\r\n\r\n  That is the whole plan.\r\n\r\n❯ "

func TestAPassThroughCaptureHasNothingToReport(t *testing.T) {
	if f := check("fake-host", capture(t, reply), false); len(f) != 0 {
		t.Fatalf("failures = %v", f)
	}
}

func TestAGestureMustMakeACard(t *testing.T) {
	// The chooser opens and is dismissed, so nothing is written.
	f := check("fake-host", capture(t, reply, "\x1bn", "z"), false)
	if len(f) == 0 || !strings.Contains(strings.Join(f, "\n"), "no card was made") {
		t.Fatalf("failures = %v", f)
	}
	// The same gesture carried through writes a question card.
	if f := check("fake-host", capture(t, reply, "\x1bn", "q", "which ports?\r"), false); len(f) != 0 {
		t.Fatalf("failures = %v", f)
	}
}

func TestPlainIsCheckedAgainstTheDrawingMode(t *testing.T) {
	p := capture(t, reply, "\x1bn", "q", "which ports?\r")
	// The capture was not recorded with --plain, so checking it as if it
	// were reports the attribute Diple used.
	f := check("fake-host", p, true)
	if len(f) == 0 || !strings.Contains(strings.Join(f, "\n"), "--plain drawing used") {
		t.Fatalf("failures = %v", f)
	}
}

func TestABrokenCaptureIsReportedNotIgnored(t *testing.T) {
	p := filepath.Join(t.TempDir(), "broken.jsonl")
	if err := os.WriteFile(p, []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := check("fake-host", p, false)
	if len(f) != 1 || !strings.Contains(f[0], "cannot read the capture") {
		t.Fatalf("failures = %v", f)
	}
}
