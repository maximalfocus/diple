// Package agent locates the real agent executable and recognises the
// invocation forms that bypass the wrapper.
package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when no executable for the agent is on PATH.
var ErrNotFound = errors.New("agent: executable not found on PATH")

// Locate returns the path of the first executable named name on PATH that is
// neither in skipDir nor the file self. Skipping the shim directory and
// Diple's own binary is what lets a same-named shim find the real CLI without
// hard-coding a path, while an agent that shares a directory with Diple (as
// Homebrew installs do) is still found. Entries are compared after resolving
// symlinks so a shim reached through a link is still skipped.
func Locate(name string, path string, skipDir string, self string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		// An explicit path is used as given.
		if isExecutable(name) {
			return name, nil
		}
		return "", ErrNotFound
	}
	skip := canonical(skipDir)
	selfFile := ""
	if self != "" {
		selfFile = resolve(self)
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		if skip != "" && canonical(dir) == skip {
			continue
		}
		candidate := filepath.Join(dir, name)
		if !isExecutable(candidate) {
			continue
		}
		resolved := resolve(candidate)
		// A candidate that resolves into the skipped directory, or to Diple
		// itself, is a shim too.
		if skip != "" && canonical(filepath.Dir(resolved)) == skip {
			continue
		}
		if selfFile != "" && resolved == selfFile {
			continue
		}
		return candidate, nil
	}
	return "", ErrNotFound
}

func canonical(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	return st.Mode()&0o111 != 0
}

// bypassFlags are the invocation forms that mean "not an interactive session"
// for every supported agent: printing a one-shot answer, or version and help.
var bypassFlags = map[string]bool{
	"-p": true, "--print": true,
	"--version": true, "-v": true,
	"--help": true, "-h": true,
}

// Bypass reports whether args describe a non-interactive invocation that the
// wrapper must hand straight to the real agent.
func Bypass(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if bypassFlags[a] {
			return true
		}
		if i := strings.IndexByte(a, '='); i > 0 && bypassFlags[a[:i]] {
			return true
		}
	}
	return false
}
