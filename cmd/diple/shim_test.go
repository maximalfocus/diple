package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeClaude writes an executable named claude that reports its own
// arguments, so a test can tell the real binary from the wrapper.
func writeFakeClaude(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "claude")
	body := "#!/bin/sh\necho \"real claude $*\"\nexit 7\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// buildDiple builds the command under test once per test binary.
func buildDiple(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "diple")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestShimRunsTheRealAgentAndBypassesOnRequest(t *testing.T) {
	home := t.TempDir()
	realDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeClaude(t, realDir)
	diple := buildDiple(t)

	env := append(os.Environ(),
		"HOME="+home,
		"XDG_DATA_HOME="+filepath.Join(home, "share"),
		"PATH="+realDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	run := func(extraEnv []string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = append(env, extraEnv...)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out), code
	}

	// `diple on` installs a shim for each agent found that Diple wraps, and
	// adds the PATH block. It looks only at the PATH it is given, which here
	// holds nothing but the test's own agents.
	only := []string{"PATH=" + realDir}
	if out, code := run(only, diple, "on"); code != 0 || !strings.Contains(out, "claude") {
		t.Fatalf("on: code=%d out=%s", code, out)
	}
	shimDir := filepath.Join(home, "share", "diple", "bin")
	shim := filepath.Join(shimDir, "claude")
	if _, err := os.Stat(shim); err != nil {
		t.Fatalf("no shim: %v", err)
	}
	rc := filepath.Join(home, ".profile")
	block, err := os.ReadFile(rc)
	if err != nil || !strings.Contains(string(block), shimDir) {
		t.Fatalf("profile: %v %s", err, block)
	}

	// A shell that has re-read its profile resolves claude to the shim.
	which, code := run(nil, "sh", "-c", ". "+rc+" >/dev/null 2>&1; command -v claude")
	if code != 0 || strings.TrimSpace(which) != shim {
		t.Fatalf("command -v claude = %q (code %d), want %q", strings.TrimSpace(which), code, shim)
	}

	// Through the shim, with no terminal, the real CLI runs unwrapped: same
	// output, same exit status — and with the shim directory ahead of the
	// real one on PATH, which is how a shim is actually used, so a shim can
	// never find itself.
	// A PATH with nothing but the shim directory and the real agent's, which
	// is the harshest case: a shim may not lean on tools it cannot see.
	onPath := []string{"PATH=" + shimDir + string(os.PathListSeparator) + realDir}
	direct, directCode := run(nil, filepath.Join(realDir, "claude"), "--version")
	through, throughCode := run(onPath, shim, "--version")
	if direct != through || directCode != throughCode {
		t.Fatalf("through the shim: %q/%d, direct: %q/%d", through, throughCode, direct, directCode)
	}

	// The same through a shell that resolves `claude` by name.
	byName, byNameCode := run(onPath, "sh", "-c", "claude --version")
	if byName != direct || byNameCode != directCode {
		t.Fatalf("by name: %q/%d, direct: %q/%d", byName, byNameCode, direct, directCode)
	}

	// DIPLE=0 bypasses for one invocation.
	bypass, bypassCode := run(append(onPath, "DIPLE=0"), shim, "--version")
	if bypass != direct || bypassCode != directCode {
		t.Fatalf("DIPLE=0: %q/%d, direct: %q/%d", bypass, bypassCode, direct, directCode)
	}

	// `diple on <agent>` adds an agent Diple has no adapter for as
	// identity-only, and refuses one that is not on PATH.
	opencode := filepath.Join(realDir, "opencode")
	if err := os.WriteFile(opencode, []byte("#!/bin/sh\necho \"real opencode $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, code := run(only, diple, "on", "opencode"); code != 0 || !strings.Contains(out, "opencode") {
		t.Fatalf("on opencode: code=%d out=%s", code, out)
	}
	if out, code := run(only, diple, "on", "no-such-agent-xyz"); code != 127 {
		t.Fatalf("on a missing agent: code=%d out=%s", code, out)
	}
	byNameOC, _ := run(onPath, "sh", "-c", "opencode --flag")
	if strings.TrimSpace(byNameOC) != "real opencode --flag" {
		t.Fatalf("identity-only through its shim: %q", byNameOC)
	}

	// status reports what is found, what is wrapped and what is identity-only.
	st, _ := run([]string{"PATH=" + shimDir + string(os.PathListSeparator) + realDir}, diple, "status")
	for _, want := range []string{"agents found: claude, opencode", "wrapped: claude", "identity-only: opencode",
		"on PATH: yes", "ahead of the real binary: claude, opencode"} {
		if !strings.Contains(st, want) {
			t.Fatalf("status lacks %q:\n%s", want, st)
		}
	}

	// `diple off` takes the shim and the block back out.
	if _, code := run(nil, diple, "off"); code != 0 {
		t.Fatalf("off: code=%d", code)
	}
	for _, p := range []string{shim, filepath.Join(shimDir, "opencode")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("shim %s survived off: %v", p, err)
		}
	}
	after, err := os.ReadFile(rc)
	if err != nil || strings.Contains(string(after), shimDir) {
		t.Fatalf("profile after off: %v %s", err, after)
	}
	which, _ = run(nil, "sh", "-c", ". "+rc+" >/dev/null 2>&1; command -v claude")
	if strings.TrimSpace(which) != filepath.Join(realDir, "claude") {
		t.Fatalf("after off, claude = %q", strings.TrimSpace(which))
	}
}
