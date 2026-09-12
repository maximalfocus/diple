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
	changed, err := Install(dir, "/opt/diple/bin/diple", agents, nil, []string{rc})
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
		if IsIdentity(p) {
			t.Fatalf("%s is wrapped, not identity-only", a)
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
	again, err := Install(dir, "/opt/diple/bin/diple", agents, nil, []string{rc})
	if err != nil || len(again) != 0 {
		t.Fatalf("second install changed %v (%v)", again, err)
	}
	twice, err := os.ReadFile(rc)
	if err != nil || string(twice) != string(after) {
		t.Fatalf("startup file changed on the second install")
	}
}

func TestAnIdentityOnlyShimAsksDipleToRunTheAgentAsItself(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, "/opt/diple",
		[]string{"claude"}, []string{"opencode"}, nil); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "opencode")
	body, _ := os.ReadFile(p)
	if !strings.Contains(string(body), `exec "$diple" --identity opencode "$@"`) ||
		!IsIdentity(p) || !IsShim(p) {
		t.Fatalf("identity-only shim:\n%s", body)
	}
	wrapped, identity := Shims(dir)
	if strings.Join(wrapped, ",") != "claude" || strings.Join(identity, ",") != "opencode" {
		t.Fatalf("Shims = %v, %v", wrapped, identity)
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
	if _, err := Install(dir, "/opt/diple",
		[]string{"claude"}, []string{"opencode"}, []string{rc}); err != nil {
		t.Fatal(err)
	}
	// Something of the user's in the same directory stays.
	mine := filepath.Join(dir, "mine")
	if err := os.WriteFile(mine, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(dir, []string{rc}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"claude", "opencode"} {
		if _, err := os.Stat(filepath.Join(dir, a)); !os.IsNotExist(err) {
			t.Fatalf("%s shim survived removal: %v", a, err)
		}
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("removal took the user's own file: %v", err)
	}
	back, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != original {
		t.Fatalf("startup file after removal:\n%q\nwant:\n%q", back, original)
	}
	// And twice is still nothing.
	changed, err := Remove(dir, []string{rc})
	if err != nil || len(changed) != 0 {
		t.Fatalf("second removal changed %v (%v)", changed, err)
	}
}

func TestReportSaysWhatIsFoundShimmedAndWhereTheShimsStand(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "shims")
	real := filepath.Join(home, "bin")
	for _, d := range []string{dir, real} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Install(dir, "/opt/diple",
		[]string{"claude"}, []string{"opencode"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"claude", "opencode", "codex"} {
		if err := os.WriteFile(filepath.Join(real, a), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	found := []string{"claude", "codex", "opencode"}

	ahead := Report(dir, found, dir+string(os.PathListSeparator)+real, nil)
	if !ahead.OnPath || len(ahead.Ahead) != 2 || len(ahead.Behind) != 0 {
		t.Fatalf("ahead = %+v", ahead)
	}
	if strings.Join(ahead.Wrapped, ",") != "claude" ||
		strings.Join(ahead.Identity, ",") != "opencode" ||
		strings.Join(ahead.Unshimmed, ",") != "codex" {
		t.Fatalf("report = %+v", ahead)
	}
	text := ahead.String()
	for _, want := range []string{
		"agents found: claude, codex, opencode", "wrapped: claude",
		"identity-only: opencode", "found without a shim: codex",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("report lacks %q:\n%s", want, text)
		}
	}
	behind := Report(dir, found, real+string(os.PathListSeparator)+dir, nil)
	if !behind.OnPath || len(behind.Behind) != 2 || len(behind.Ahead) != 0 {
		t.Fatalf("behind = %+v", behind)
	}
	off := Report(dir, found, real, nil)
	if off.OnPath || len(off.Ahead) != 0 {
		t.Fatalf("off = %+v", off)
	}
	if !strings.Contains(off.String(), "on PATH: no") {
		t.Fatalf("report:\n%s", off)
	}
}

func TestShimUsesOnlyBuiltinsAndIsRecognisable(t *testing.T) {
	for _, identity := range []bool{false, true} {
		body := Script("claude", "/opt/diple/bin/diple", identity)
		// A shim runs under a PATH that may hold nothing but its own directory
		// and the real agent's, so its code must not call out to anything.
		var code []string
		for _, line := range strings.Split(body, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "#") {
				code = append(code, line)
			}
		}
		script := strings.Join(code, "\n")
		for _, external := range []string{
			"dirname", "basename", "readlink", "realpath", "sed", "awk", "grep", "tr ", "env ",
		} {
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
		if !IsShim(p) || IsIdentity(p) != identity {
			t.Fatalf("shim recognition wrong for identity=%v", identity)
		}
		real := filepath.Join(dir, "real")
		if err := os.WriteFile(real, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if IsShim(real) || IsShim(filepath.Join(dir, "missing")) {
			t.Fatal("IsShim must only recognise Diple's own shims")
		}
	}
}
