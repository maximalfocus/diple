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

// gesture is what a driven host is asked to deliver: a free card written from
// an empty tray, and a drag across the agent's output, which copies. Both are
// what R-005 and R-015 promise in every host.
var gesture = []string{"\x1b[<0;3;3M", "\x1b[<32;20;3M", "\x1b[<0;20;3m", "\x1bn", "which ports?\r"}

func TestAPassThroughCaptureHasNothingToReport(t *testing.T) {
	if f := check("fake-host", capture(t, reply), false); len(f) != 0 {
		t.Fatalf("failures = %v", f)
	}
}

func TestAGestureMustMakeACard(t *testing.T) {
	// The chooser opens and is dismissed, so nothing is written.
	f := check("fake-host", capture(t, reply, "\x1bn", "\x1b"), false)
	if len(f) == 0 || !strings.Contains(strings.Join(f, "\n"), "no card was made") {
		t.Fatalf("failures = %v", f)
	}
	// The same gesture carried through writes a card and copies.
	if f := check("fake-host", capture(t, reply, gesture...), false); len(f) != 0 {
		t.Fatalf("failures = %v", f)
	}
}

func TestADrivenHostWithNoGestureFailsRatherThanDegrading(t *testing.T) {
	// tmux is driven, so a capture with nothing typed into it means the
	// gesture never arrived, not that the host cannot be typed into. This
	// is the case that used to report a pass.
	f := check("tmux", capture(t, reply), false)
	if len(f) == 0 || !strings.Contains(strings.Join(f, "\n"), "no gesture reached its capture") {
		t.Fatalf("failures = %v", f)
	}
	// The same host driven properly passes.
	if f := check("tmux", capture(t, reply, gesture...), false); len(f) != 0 {
		t.Fatalf("failures = %v", f)
	}
}

func TestAPassThroughHostWithNoGestureIsNotAFailure(t *testing.T) {
	// Ghostty offers no way to type into a running window from outside, so
	// its capture covers forwarding, the envelope, and restore, and that is
	// the whole of what R-014 asks of it.
	if f := check("ghostty", capture(t, reply), false); len(f) != 0 {
		t.Fatalf("failures = %v", f)
	}
}

// TestPlainForbidsColourButKeepsTheAttributes: --plain drops colour entirely
// and leaves the bold, dim, reverse, and underline attributes.
func TestPlainForbidsColourButKeepsTheAttributes(t *testing.T) {
	for _, drawn := range []string{"\x1b[31mfix\x1b[0m", "\x1b[1;33mask\x1b[0m", "\x1b[38;5;9mnote\x1b[0m", "\x1b[92mx\x1b[0m"} {
		if bad := disallowedUnderPlain(drawn); bad == "" {
			t.Fatalf("--plain allowed colour in %q", drawn)
		}
	}
	for _, drawn := range []string{"\x1b[1mbold\x1b[22m", "\x1b[2mdim\x1b[22m", "\x1b[7mreverse\x1b[27m", "\x1b[4munder\x1b[24m", "\x1b[0m"} {
		if bad := disallowedUnderPlain(drawn); bad != "" {
			t.Fatalf("--plain refused %q: %s", drawn, bad)
		}
	}
	// A capture whose drawing carries no colour passes the --plain check.
	if f := check("fake-host", capture(t, reply, gesture...), true); len(f) != 0 {
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

// TestAGestureWithoutACopyIsReported: a copy that no host delivers is no copy,
// so a driven capture that never reached the clipboard fails.
func TestAGestureWithoutACopyIsReported(t *testing.T) {
	f := check("tmux", capture(t, reply, "\x1bn", "which ports?\r"), false)
	if len(f) == 0 || !strings.Contains(strings.Join(f, "\n"), "nothing reached the clipboard") {
		t.Fatalf("failures = %v", f)
	}
}
