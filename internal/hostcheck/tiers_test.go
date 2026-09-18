package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const head = "0123456789abcdef0123456789abcdef01234567"

// hostCapture writes a capture a host of that class would pass with: a driven
// host carries the gesture, a pass-through host only the agent's reply.
func hostCapture(t *testing.T, host string) string {
	t.Helper()
	if drivenHosts[host] {
		return capture(t, reply, gesture...)
	}
	return capture(t, reply)
}

// committed files a capture for every replayed host and drawing mode under a
// fresh directory, leaving out the one named by skip.
func committed(t *testing.T, skip string) string {
	t.Helper()
	dir := t.TempDir()
	for _, h := range replayed() {
		for _, m := range drawingModes {
			if h+" "+m == skip {
				continue
			}
			data, err := os.ReadFile(hostCapture(t, h))
			if err != nil {
				t.Fatal(err)
			}
			d := filepath.Join(dir, h, "1.0")
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d, m+".capture.jsonl"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

// TestCommittedCapturesReplay is tier 3 inside the unit suite: the captures
// committed under testdata/hosts replay against the current implementation.
func TestCommittedCapturesReplay(t *testing.T) {
	if f := replayCommitted(filepath.Join("..", "..", "testdata", "hosts"), io.Discard); len(f) != 0 {
		t.Fatalf("committed captures do not replay:\n%s", strings.Join(f, "\n"))
	}
}

func TestReplayRequiresEveryHostInBothModes(t *testing.T) {
	if f := replayCommitted(committed(t, ""), io.Discard); len(f) != 0 {
		t.Fatalf("a complete set failed: %v", f)
	}
	f := replayCommitted(committed(t, "iterm2 plain"), io.Discard)
	if len(f) != 1 || f[0] != "iterm2 plain: no committed capture" {
		t.Fatalf("failures = %v", f)
	}
}

func TestAReplayRegressionIsReported(t *testing.T) {
	dir := committed(t, "")
	// A driven host's capture that no longer carries its card.
	data, err := os.ReadFile(capture(t, reply))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kitty", "1.0", "default.capture.jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	f := replayCommitted(dir, io.Discard)
	if len(f) != 1 || !strings.HasPrefix(f[0], "kitty 1.0 default: ") {
		t.Fatalf("failures = %v", f)
	}
}

// evidence writes a report for every claimed host and drawing mode at sha,
// leaving out the one named by skip. A held host gets none: the gate wants
// none.
func evidence(t *testing.T, sha, skip string) string {
	t.Helper()
	dir := t.TempDir()
	for _, h := range portability {
		for _, m := range drawingModes {
			if h+" "+m == skip {
				continue
			}
			r := report{Format: reportFormat, SHA: sha, Host: h, Version: "1.0", Mode: m, Machine: "test", Created: time.Now().UTC()}
			if err := writeReport(hostCapture(t, h), dir, r); err != nil {
				t.Fatalf("%s %s: %v", h, m, err)
			}
		}
	}
	return dir
}

func hasFailure(failures []string, sub string) bool {
	for _, f := range failures {
		if strings.Contains(f, sub) {
			return true
		}
	}
	return false
}

func TestEvidenceAtTheHeadForEveryHostPasses(t *testing.T) {
	if f := verifyEvidence(head, []string{evidence(t, head, "")}, time.Now(), defaultMaxAge, io.Discard); len(f) != 0 {
		t.Fatalf("failures = %v", f)
	}
}

func TestMachinesAggregate(t *testing.T) {
	// One machine without kitty, another with only it: together they cover
	// the list, and neither is waived.
	linux := evidence(t, head, "kitty default")
	mac := t.TempDir()
	r := report{
		Format: reportFormat, SHA: head, Host: "kitty", Version: "0.48.2",
		Mode: "default", Machine: "mac", Created: time.Now().UTC(),
	}
	if err := writeReport(hostCapture(t, "kitty"), mac, r); err != nil {
		t.Fatal(err)
	}
	one := verifyEvidence(head, []string{linux}, time.Now(), defaultMaxAge, io.Discard)
	if !hasFailure(one, "incomplete: no passing evidence for kitty default") {
		t.Fatalf("one machine alone passed: %v", one)
	}
	if f := verifyEvidence(head, []string{linux, mac}, time.Now(), defaultMaxAge, io.Discard); len(f) != 0 {
		t.Fatalf("aggregated evidence failed: %v", f)
	}
}

func TestMissingIncompleteStaleExpiredAndTamperedEvidenceFail(t *testing.T) {
	other := strings.Repeat("f", 40)
	if f := verifyEvidence(head, nil, time.Now(), defaultMaxAge, io.Discard); !hasFailure(f, "no host evidence") {
		t.Errorf("missing: %v", f)
	}
	if f := verifyEvidence(head, []string{evidence(t, head, "tmux plain")}, time.Now(), defaultMaxAge, io.Discard); !hasFailure(f, "incomplete: no passing evidence for tmux plain") {
		t.Errorf("incomplete: %v", f)
	}
	if f := verifyEvidence(head, []string{evidence(t, other, "")}, time.Now(), defaultMaxAge, io.Discard); !hasFailure(f, "stale: the report is for ffffffffffff") {
		t.Errorf("stale: %v", f)
	}
	later := time.Now().Add(defaultMaxAge + time.Hour)
	if f := verifyEvidence(head, []string{evidence(t, head, "")}, later, defaultMaxAge, io.Discard); !hasFailure(f, "expired") {
		t.Errorf("expired: %v", f)
	}
	dir := evidence(t, head, "")
	if err := os.WriteFile(filepath.Join(dir, "herdr-plain.capture.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f := verifyEvidence(head, []string{dir}, time.Now(), defaultMaxAge, io.Discard); !hasFailure(f, "herdr 1.0 plain: the capture's digest does not match its report") {
		t.Errorf("tampered: %v", f)
	}
	if f := verifyEvidence(head, []string{t.TempDir()}, time.Now(), defaultMaxAge, io.Discard); !hasFailure(f, "the evidence holds no report") {
		t.Errorf("empty bundle: %v", f)
	}
}

// TestTheGateDemandsTheClaimedHostsAndNoHeldOne is what S-019 narrowed. The
// evidence a developer can record with nobody at the keyboard is exactly the
// evidence the gate asks for.
//
// Covers S-019 T-01.
func TestTheGateDemandsTheClaimedHostsAndNoHeldOne(t *testing.T) {
	f := verifyEvidence(head, []string{evidence(t, head, "")}, time.Now(), defaultMaxAge, io.Discard)
	if len(f) != 0 {
		t.Fatalf("evidence for the claimed hosts alone failed: %v", f)
	}
	for _, h := range held {
		for _, m := range drawingModes {
			if hasFailure(f, h+" "+m) {
				t.Errorf("the gate waits on the held host %s %s", h, m)
			}
		}
	}
	missing := evidence(t, head, "kitty plain")
	f = verifyEvidence(head, []string{missing}, time.Now(), defaultMaxAge, io.Discard)
	if !hasFailure(f, "incomplete: no passing evidence for kitty plain") {
		t.Fatalf("a missing claimed host passed: %v", f)
	}
}

// TestAHeldHostKeepsReplaying is the other half of the narrowing: nothing is
// deleted that S-020 would have to record again, and tier 3 costs nobody a
// keystroke.
//
// Covers S-019 T-02.
func TestAHeldHostKeepsReplaying(t *testing.T) {
	if f := replayCommitted(committed(t, ""), io.Discard); len(f) != 0 {
		t.Fatalf("a complete set failed: %v", f)
	}
	f := replayCommitted(committed(t, "ghostty default"), io.Discard)
	if len(f) != 1 || f[0] != "ghostty default: no committed capture" {
		t.Fatalf("failures = %v", f)
	}
}

func TestAFailingCaptureGetsNoReport(t *testing.T) {
	dir := t.TempDir()
	r := report{Format: reportFormat, SHA: head, Host: "kitty", Version: "1.0", Mode: "default", Created: time.Now().UTC()}
	if err := writeReport(capture(t, reply), dir, r); err == nil {
		t.Fatal("a driven host with no gesture was given a report")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files written for a failing capture: %v", entries)
	}
	r.SHA = "HEAD"
	if err := writeReport(hostCapture(t, "kitty"), dir, r); err == nil {
		t.Fatal("a report was bound to something other than a full SHA")
	}
}
