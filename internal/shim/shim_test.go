package shim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPutsShimsAndThePathLineInPlaceIdempotently(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "share", "diple", "bin")
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte("# mine\nexport EDITOR=vi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agents := []string{"claude", "codex"}
	changed, err := Install(dir, "/opt/diple/bin/diple", agents, []string{rc})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 3 {
		t.Fatalf("changed = %v", changed)
	}
	for _, a := range agents {
		p := filepath.Join(dir, a)
		info, err := os.Stat(p)
		if err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("%s: %v mode=%v", p, err, info)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, `exec "$diple" `+a+` "$@"`) || !strings.Contains(text, `diple="/opt/diple/bin/diple"`) {
			t.Fatalf("%s shim:\n%s", a, text)
		}
		// The real CLI's own path is never written into a shim.
		if strings.Contains(text, "/usr/local/bin/"+a) || strings.Contains(text, "/usr/bin/"+a) {
			t.Fatalf("%s shim hard-codes the real binary:\n%s", a, text)
		}
	}
	after, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "export EDITOR=vi") || !strings.Contains(string(after), dir+":$PATH") {
		t.Fatalf("startup file:\n%s", after)
	}
	// Running it twice changes nothing further.
	again, err := Install(dir, "/opt/diple/bin/diple", agents, []string{rc})
	if err != nil || len(again) != 0 {
		t.Fatalf("second install changed %v (%v)", again, err)
	}
	twice, err := os.ReadFile(rc)
	if err != nil || string(twice) != string(after) {
		t.Fatalf("startup file changed on the second install")
	}
}

func TestRemoveTakesBackExactlyWhatWasAdded(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "share", "diple", "bin")
	rc := filepath.Join(home, ".bashrc")
	original := "# mine\nexport EDITOR=vi\n"
	if err := os.WriteFile(rc, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	agents := []string{"claude"}
	if _, err := Install(dir, "/opt/diple", agents, []string{rc}); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(dir, agents, []string{rc}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude")); !os.IsNotExist(err) {
		t.Fatalf("shim survived removal: %v", err)
	}
	back, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != original {
		t.Fatalf("startup file after removal:\n%q\nwant:\n%q", back, original)
	}
	// And twice is still nothing.
	changed, err := Remove(dir, agents, []string{rc})
	if err != nil || len(changed) != 0 {
		t.Fatalf("second removal changed %v (%v)", changed, err)
	}
}

func TestReportSaysWhereTheShimsStandOnPath(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "shims")
	real := filepath.Join(home, "bin")
	for _, d := range []string{dir, real} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p string) {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "claude"))
	write(filepath.Join(real, "claude"))
	agents := []string{"claude", "codex"}

	ahead := Report(dir, agents, dir+string(os.PathListSeparator)+real, nil)
	if !ahead.OnPath || len(ahead.Ahead) != 1 || len(ahead.Behind) != 0 || len(ahead.Missing) != 1 {
		t.Fatalf("ahead = %+v", ahead)
	}
	behind := Report(dir, agents, real+string(os.PathListSeparator)+dir, nil)
	if !behind.OnPath || len(behind.Behind) != 1 || len(behind.Ahead) != 0 {
		t.Fatalf("behind = %+v", behind)
	}
	off := Report(dir, agents, real, nil)
	if off.OnPath || len(off.Ahead) != 0 {
		t.Fatalf("off = %+v", off)
	}
	if !strings.Contains(off.String(), "on PATH: no") {
		t.Fatalf("report:\n%s", off)
	}
}

func TestShimUsesOnlyBuiltinsAndIsRecognisable(t *testing.T) {
	body := Script("claude", "/opt/diple/bin/diple")
	// A shim runs under a PATH that may hold nothing but its own directory
	// and the real agent's, so its code must not call out to anything.
	var code []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			code = append(code, line)
		}
	}
	script := strings.Join(code, "\n")
	for _, external := range []string{"dirname", "basename", "readlink", "realpath", "sed", "awk", "grep", "tr ", "env "} {
		if strings.Contains(script, external) {
			t.Fatalf("the shim calls %s; only builtins are safe:\n%s", external, script)
		}
	}
	if !strings.Contains(body, Marker) {
		t.Fatalf("a shim must be recognisable:\n%s", body)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "claude")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsShim(p) {
		t.Fatal("IsShim did not recognise a shim")
	}
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if IsShim(real) || IsShim(filepath.Join(dir, "missing")) {
		t.Fatal("IsShim must only recognise Diple's own shims")
	}
}
