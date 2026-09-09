package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkexe(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLocateSkipsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	shims := filepath.Join(root, "shims")
	real := filepath.Join(root, "bin")
	for _, d := range []string{shims, real} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mkexe(t, shims, "claude")
	want := mkexe(t, real, "claude")
	path := strings.Join([]string{shims, real}, string(os.PathListSeparator))

	got, err := Locate("claude", path, shims, "")
	if err != nil || got != want {
		t.Fatalf("Locate = %q, %v; want %q", got, err, want)
	}
	// Without a skip directory the shim wins, as PATH order says.
	got, err = Locate("claude", path, "", "")
	if err != nil || got != filepath.Join(shims, "claude") {
		t.Fatalf("Locate without skip = %q, %v", got, err)
	}
}

func TestLocateSkipsSymlinkIntoOwnDirectory(t *testing.T) {
	root := t.TempDir()
	shims := filepath.Join(root, "shims")
	linkdir := filepath.Join(root, "links")
	real := filepath.Join(root, "bin")
	for _, d := range []string{shims, linkdir, real} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	shim := mkexe(t, shims, "claude")
	if err := os.Symlink(shim, filepath.Join(linkdir, "claude")); err != nil {
		t.Fatal(err)
	}
	want := mkexe(t, real, "claude")
	path := strings.Join([]string{linkdir, real}, string(os.PathListSeparator))
	got, err := Locate("claude", path, shims, "")
	if err != nil || got != want {
		t.Fatalf("Locate = %q, %v; want %q", got, err, want)
	}
}

func TestLocateSkipsSelfButNotItsDirectory(t *testing.T) {
	bin := t.TempDir()
	self := mkexe(t, bin, "diple")
	if err := os.Symlink(self, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	want := mkexe(t, bin, "codex")
	got, err := Locate("codex", bin, "", self)
	if err != nil || got != want {
		t.Fatalf("Locate codex = %q, %v; want %q", got, err, want)
	}
	if _, err := Locate("claude", bin, "", self); err != ErrNotFound {
		t.Fatalf("a symlink to self must be skipped, got err %v", err)
	}
}

func TestLocateNotFound(t *testing.T) {
	if _, err := Locate("no-such-agent", t.TempDir(), "", ""); err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestLocateExplicitPath(t *testing.T) {
	p := mkexe(t, t.TempDir(), "agent")
	got, err := Locate(p, "", "", "")
	if err != nil || got != p {
		t.Fatalf("Locate = %q, %v", got, err)
	}
}

func TestBypass(t *testing.T) {
	cases := map[string]bool{
		"":                  false,
		"-p hi":             true,
		"--print hi":        true,
		"--version":         true,
		"-v":                true,
		"--help":            true,
		"-h":                true,
		"--model x":         false,
		"--output-format=x": false,
		"--print=true":      true,
		"-- -p":             false,
		"chat --resume":     false,
	}
	for in, want := range cases {
		var args []string
		if in != "" {
			args = strings.Fields(in)
		}
		if got := Bypass(args); got != want {
			t.Errorf("Bypass(%q) = %v, want %v", in, got, want)
		}
	}
}
