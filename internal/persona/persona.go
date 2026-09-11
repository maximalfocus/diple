// Package persona keeps the copies of Diple's own binary named exactly as
// the user invoked an agent. Diple re-executes itself through one before it
// wraps, so a host that inspects the process in its pane — by argv[0], by the
// kernel's command name, or by the executable's path — sees the agent the
// user launched, never Diple.
//
// A persona is kept per Diple build and per name in the user cache, written
// atomically and never on PATH. A symlink would leave the command name
// `diple`, and a hard link can report another link's path and outlives an
// upgrade, so a persona is a copy.
package persona

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Dir is where personas are kept: $XDG_CACHE_HOME/diple/persona when that is
// set, on every platform, else the platform's user cache directory.
func Dir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		var err error
		if base, err = os.UserCacheDir(); err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "diple", "persona"), nil
}

// ValidName reports whether name can be a persona: a plain file name, as the
// user typed it.
func ValidName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`) && !strings.ContainsRune(name, 0)
}

// Build identifies a Diple build by the content of its binary, so an
// upgrade, even one that keeps the path, gets fresh personas.
func Build(self string) (string, error) {
	f, err := os.Open(self)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// Ensure returns the persona for name of the Diple binary at self under dir,
// writing it when this build has none. The copy is written beside its final
// name and renamed into place, so a concurrent launch never sees half a
// binary. Personas of other builds are removed once a new build writes its
// first.
func Ensure(dir, self, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("persona: %q is not a command name", name)
	}
	build, err := Build(self)
	if err != nil {
		return "", err
	}
	buildDir := filepath.Join(dir, build)
	final := filepath.Join(buildDir, name)
	if info, err := os.Stat(final); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		return final, nil
	}
	fresh := false
	if _, err := os.Stat(buildDir); errors.Is(err, os.ErrNotExist) {
		fresh = true
	}
	if err := os.MkdirAll(buildDir, 0o700); err != nil {
		return "", err
	}
	src, err := os.Open(self)
	if err != nil {
		return "", err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(buildDir, "."+name+".*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", err
	}
	if fresh {
		prune(dir, build)
	}
	return final, nil
}

// prune removes the personas of every build but keep. It is best effort: a
// build still running keeps its open file whatever happens to its name.
func prune(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() != keep {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}
