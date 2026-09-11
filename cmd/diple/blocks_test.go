//go:build unix

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlocksPrintsFixtureTranscript(t *testing.T) {
	transcript := filepath.Join("..", "..", "internal", "adapter", "claude", "testdata", "2.1.268", "inline.transcript.jsonl")
	out, err := exec.Command(dipleBin, "blocks", transcript).CombinedOutput()
	if err != nil {
		t.Fatalf("blocks failed: %v\n%s", err, out)
	}
	text := string(out)
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
