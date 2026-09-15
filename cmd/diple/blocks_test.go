//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var fixtureTranscript = filepath.Join("..", "..", "internal", "adapter", "claude", "testdata",
	"2.1.268", "inline.transcript.jsonl")

func TestBlocksPrintsFixtureTranscript(t *testing.T) {
	out, err := exec.Command(dipleBin, "blocks", fixtureTranscript).CombinedOutput()
	if err != nil {
		t.Fatalf("blocks failed: %v\n%s", err, out)
	}
	text := string(out)
	if strings.Contains(text, "unverified") {
		t.Fatalf("a pinned version is labelled unverified:\n%s", text)
	}
	for _, want := range []string{
		"agent claude version 2.1.268",
		"turns 1",
		"turn 1",
		"heading    Plan",
		"list-item  [1] Read the config file",
		"list-item  [1] keep the old route",
		"code-block (3 lines, go)",
		"diff-line    -    return nil",
		"paragraph  That is the whole plan.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output lacks %q:\n%s", want, text)
		}
	}
	if err := exec.Command(dipleBin, "blocks").Run(); exitCode(err) != 64 {
		t.Fatalf("blocks without a path exit = %d", exitCode(err))
	}
	if err := exec.Command(dipleBin, "blocks", "/nonexistent/transcript.jsonl").Run(); exitCode(err) != 1 {
		t.Fatalf("blocks with a missing file exit = %d", exitCode(err))
	}
}

// TestBlocksLabelsAnUnverifiedVersion: `diple blocks` parses a transcript from
// a version no fixture pins, and says so.
//
// Covers S-016 T-03.
func TestBlocksLabelsAnUnverifiedVersion(t *testing.T) {
	data, err := os.ReadFile(fixtureTranscript)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	unverified := strings.ReplaceAll(string(data), "2.1.268", "9.9.9")
	if err := os.WriteFile(p, []byte(unverified), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(dipleBin, "blocks", p).CombinedOutput()
	if err != nil {
		t.Fatalf("blocks failed: %v\n%s", err, out)
	}
	for _, want := range []string{"agent claude version 9.9.9 (unverified)", "heading    Plan"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}
