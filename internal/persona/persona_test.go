package persona

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeDiple writes a file standing in for Diple's binary.
func fakeDiple(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "diple")
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAPersonaIsACopyNamedAsTyped(t *testing.T) {
	dir := t.TempDir()
	self := fakeDiple(t, "build one")
	p, err := Ensure(dir, self, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "claude" {
		t.Fatalf("persona %s is not named as typed", p)
	}
	info, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatalf("persona is not an executable regular file: %v", info.Mode())
	}
	if got, _ := os.ReadFile(p); string(got) != "build one" {
		t.Fatalf("persona content = %q", got)
	}
	// An alias a host knows stays that alias.
	if a, err := Ensure(dir, self, "claude-code"); err != nil || filepath.Base(a) != "claude-code" {
		t.Fatalf("alias persona = %s, %v", a, err)
	}
	// The same build and name reuse the persona.
	if again, err := Ensure(dir, self, "claude"); err != nil || again != p {
		t.Fatalf("second Ensure = %s, %v; want %s", again, err, p)
	}
}

func TestAnUpgradeGetsAFreshPersonaAndDropsTheOld(t *testing.T) {
	dir := t.TempDir()
	self := fakeDiple(t, "build one")
	old, err := Ensure(dir, self, "claude")
	if err != nil {
		t.Fatal(err)
	}
	// The upgrade keeps the path and changes the binary.
	if err := os.WriteFile(self, []byte("build two"), 0o755); err != nil {
		t.Fatal(err)
	}
	fresh, err := Ensure(dir, self, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if fresh == old {
		t.Fatal("the upgraded build reused the old persona")
	}
	if got, _ := os.ReadFile(fresh); string(got) != "build two" {
		t.Fatalf("fresh persona content = %q", got)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("the old build's persona is still there: %v", err)
	}
}

func TestConcurrentLaunchesNeverSeeHalfAPersona(t *testing.T) {
	dir := t.TempDir()
	content := string(make([]byte, 1<<20))
	self := fakeDiple(t, content)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := Ensure(dir, self, "codex")
			if err != nil {
				errs <- err
				return
			}
			if info, err := os.Stat(p); err != nil || info.Size() != int64(len(content)) {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("a launch saw a broken persona: %v", err)
	}
	// Nothing but the persona is left in the build directory.
	entries, _ := os.ReadDir(filepath.Dir(filepath.Join(dir, "x")))
	for _, e := range entries {
		sub, _ := os.ReadDir(filepath.Join(dir, e.Name()))
		for _, f := range sub {
			if f.Name() != "codex" {
				t.Fatalf("leftover %s in the build directory", f.Name())
			}
		}
	}
}

func TestOnlyPlainCommandNamesArePersonas(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`} {
		if ValidName(bad) {
			t.Errorf("%q accepted as a persona name", bad)
		}
		if _, err := Ensure(t.TempDir(), fakeDiple(t, "x"), bad); err == nil {
			t.Errorf("Ensure accepted %q", bad)
		}
	}
	for _, good := range []string{"claude", "claude-code", "opencode2", "pi"} {
		if !ValidName(good) {
			t.Errorf("%q refused", good)
		}
	}
}
