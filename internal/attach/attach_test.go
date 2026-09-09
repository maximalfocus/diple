package attach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/card"
)

func TestParseFormsAndRejections(t *testing.T) {
	a, ok := Parse("  @src/auth.ts ")
	if !ok || a.Kind != card.PathAttachment || a.Spec != "src/auth.ts" {
		t.Fatalf("path: %+v ok=%v", a, ok)
	}
	a, ok = Parse("!git diff --stat")
	if !ok || a.Kind != card.CommandAttachment || a.Spec != "git diff --stat" {
		t.Fatalf("command: %+v ok=%v", a, ok)
	}
	for _, in := range []string{"", "plain text", "@", "!", "@  ", "x@y"} {
		if _, ok := Parse(in); ok {
			t.Fatalf("%q is not an attachment", in)
		}
	}
}

func TestCaptureRunsOnceInTheSessionDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "here.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := Parse("!ls")
	a = Capture(a, dir)
	if a.Status != 0 || !strings.Contains(a.Output, "here.txt") || a.Truncated {
		t.Fatalf("capture: %+v", a)
	}
	// A path is a reference: nothing is read and nothing is captured.
	p, _ := Parse("@here.txt")
	p = Capture(p, dir)
	if p.Output != "" || p.Status != 0 {
		t.Fatalf("path capture: %+v", p)
	}
}

func TestCaptureKeepsFailureAndTruncatesOutput(t *testing.T) {
	a, _ := Parse("!echo boom >&2; exit 3")
	a = Capture(a, t.TempDir())
	if a.Status != 3 || !strings.Contains(a.Output, "boom") {
		t.Fatalf("failed command: %+v", a)
	}
	big, _ := Parse("!yes abcdefghij | head -2000")
	big = Capture(big, t.TempDir())
	if !big.Truncated || len([]rune(big.Output)) != MaxOutput {
		t.Fatalf("truncation: truncated=%v runes=%d", big.Truncated, len([]rune(big.Output)))
	}
}
