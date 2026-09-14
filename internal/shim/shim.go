// Package shim installs and removes the same-named executables that put
// Diple ahead of the real agent CLIs on PATH. A shim is deliberately tiny: it
// executes Diple with the agent's name, and Diple finds the real CLI by
// skipping the shim's own directory, so no path to an agent is ever written
// down. A shim for an agent Diple has no adapter for is identity-only: it asks
// Diple to run the agent as itself, owning nothing in its pane.
package shim

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// blockStart and blockEnd fence the lines `diple on` adds to a startup file,
// so `diple off` can take out exactly what it put in and nothing else.
const (
	blockStart = "# >>> diple >>>"
	blockEnd   = "# <<< diple <<<"
)

// Dir is the shim directory: $XDG_DATA_HOME/diple/bin, else
// ~/.local/share/diple/bin.
func Dir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "diple", "bin"), nil
}

// StartupFiles are the shell startup files a PATH line belongs in: the ones
// the user already has, or ~/.profile when they have none.
func StartupFiles() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range []string{".zshrc", ".bashrc", ".profile"} {
		p := filepath.Join(home, name)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = append(out, filepath.Join(home, ".profile"))
	}
	return out, nil
}

// Marker appears in every shim Diple writes, so Diple can recognise one and
// refuse to execute it as if it were the real agent.
const Marker = "# diple shim for "

// identityFlag is what an identity-only shim passes to Diple.
const identityFlag = "--identity"

// IsShim reports whether path is one of Diple's own shims. Executing one as
// the real agent would send Diple straight back to itself.
func IsShim(path string) bool {
	return strings.Contains(head(path), Marker)
}

// IsIdentity reports whether path is an identity-only shim.
func IsIdentity(path string) bool {
	h := head(path)
	return strings.Contains(h, Marker) && strings.Contains(h, `exec "$diple" `+identityFlag+" ")
}

// head is the start of a file, enough to hold a whole shim.
func head(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 2048)
	n, _ := f.Read(buf)
	return string(buf[:n])
}

// Script is the shim for one agent. It fails loudly when Diple is gone
// rather than falling back to the name it stands in for, which would send it
// straight back to itself. An identity-only shim asks Diple to run the agent
// as itself.
func Script(agent, diple string, identity bool) string {
	flag := ""
	if identity {
		flag = identityFlag + " "
	}
	return fmt.Sprintf(`#!/bin/sh
%s%s. Installed by "diple on"; remove with "diple off".
# The shim tells Diple which directory to skip when it looks for the real
# %s, so no path to the agent is ever written here and a shim can never find
# itself. Only builtins are used, because a shim must work under a PATH that
# holds nothing but this directory and the real agent's.
case $0 in
*/*) dir=${0%%/*} ;;
*) dir=. ;;
esac
DIPLE_SHIM_DIR=$(CDPATH= cd -- "$dir" && pwd)
export DIPLE_SHIM_DIR
diple=%q
if [ ! -x "$diple" ]; then
	diple=$(command -v diple 2>/dev/null)
fi
if [ -z "$diple" ] || [ ! -x "$diple" ]; then
	echo "diple: the diple binary is gone; run 'diple off' to remove these shims" >&2
	exit 127
fi
exec "$diple" %s%s "$@"
`, Marker, agent, agent, diple, flag, agent)
}

// Install writes a shim for each wrapped and each identity-only agent and adds
// the PATH block to the startup files. It reports the files it changed, and
// is safe to run twice.
func Install(dir, diple string, wrapped, identity []string, startup []string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var changed []string
	write := func(a string, id bool) error {
		p := filepath.Join(dir, a)
		want := Script(a, diple, id)
		if old, err := os.ReadFile(p); err == nil && string(old) == want {
			return nil
		}
		if err := os.WriteFile(p, []byte(want), 0o755); err != nil {
			return err
		}
		changed = append(changed, p)
		return nil
	}
	for _, a := range wrapped {
		if err := write(a, false); err != nil {
			return changed, err
		}
	}
	for _, a := range identity {
		if err := write(a, true); err != nil {
			return changed, err
		}
	}
	for _, f := range startup {
		added, err := addBlock(f, dir)
		if err != nil {
			return changed, err
		}
		if added {
			changed = append(changed, f)
		}
	}
	return changed, nil
}

// Shims lists the agents with a shim in dir, split into wrapped and
// identity-only. Anything in dir that is not Diple's own shim is left out.
func Shims(dir string) (wrapped, identity []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() || !IsShim(p) {
			continue
		}
		if IsIdentity(p) {
			identity = append(identity, e.Name())
		} else {
			wrapped = append(wrapped, e.Name())
		}
	}
	sort.Strings(wrapped)
	sort.Strings(identity)
	return wrapped, identity
}

// Remove deletes every shim Diple wrote in dir and takes the block back out
// of the startup files. It reports what it changed and is safe to run twice.
func Remove(dir string, startup []string) ([]string, error) {
	var changed []string
	wrapped, identity := Shims(dir)
	for _, a := range append(wrapped, identity...) {
		p := filepath.Join(dir, a)
		err := os.Remove(p)
		if err == nil {
			changed = append(changed, p)
			continue
		}
		if !os.IsNotExist(err) {
			return changed, err
		}
	}
	// An empty shim directory is Diple's own leftovers; a directory with
	// anything else in it is the user's and stays.
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
	for _, f := range startup {
		removed, err := removeBlock(f)
		if err != nil {
			return changed, err
		}
		if removed {
			changed = append(changed, f)
		}
	}
	return changed, nil
}

// block is the exact text `diple on` adds.
func block(dir string) string {
	return blockStart + "\nexport PATH=\"" + dir + ":$PATH\"\n" + blockEnd + "\n"
}

func addBlock(file, dir string) (bool, error) {
	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	text := string(data)
	if strings.Contains(text, blockStart) {
		if strings.Contains(text, block(dir)) {
			return false, nil
		}
		// The block is there for another directory: replace it.
		if _, err := removeBlock(file); err != nil {
			return false, err
		}
		data, err = os.ReadFile(file)
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		text = string(data)
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += block(dir)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(file, []byte(text), 0o644)
}

func removeBlock(file string) (bool, error) {
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	lines := strings.Split(string(data), "\n")
	var out []string
	inside, removed := false, false
	for _, l := range lines {
		switch {
		case strings.TrimSpace(l) == blockStart:
			inside, removed = true, true
			continue
		case strings.TrimSpace(l) == blockEnd:
			inside = false
			continue
		case inside:
			continue
		}
		out = append(out, l)
	}
	if !removed {
		return false, nil
	}
	text := strings.Join(out, "\n")
	for strings.HasSuffix(text, "\n\n") {
		text = strings.TrimSuffix(text, "\n")
	}
	return true, os.WriteFile(file, []byte(text), 0o644)
}

// Status is what `diple status` reports.
type Status struct {
	Dir       string
	Found     []string // catalogued agents with a real executable on PATH
	Wrapped   []string // agents with a shim that Diple wraps
	Identity  []string // agents with an identity-only shim
	Unshimmed []string // found agents that have no shim
	OnPath    bool     // the shim directory is on PATH
	Ahead     []string // agents whose shim comes before the real binary
	Behind    []string // agents whose real binary comes first
	Files     []string // startup files carrying the block
}

// Report reads the current state from the shim directory, PATH, and the
// startup files. found is the catalogued agents on PATH.
func Report(dir string, found []string, path string, startup []string) Status {
	st := Status{Dir: dir, Found: append([]string(nil), found...)}
	st.Wrapped, st.Identity = Shims(dir)
	shimmed := map[string]bool{}
	for _, a := range append(append([]string(nil), st.Wrapped...), st.Identity...) {
		shimmed[a] = true
	}
	for _, a := range found {
		if !shimmed[a] {
			st.Unshimmed = append(st.Unshimmed, a)
		}
	}
	dirs := filepath.SplitList(path)
	shimAt := -1
	for i, d := range dirs {
		if sameDir(d, dir) {
			shimAt = i
			break
		}
	}
	st.OnPath = shimAt >= 0
	installed := append(append([]string(nil), st.Wrapped...), st.Identity...)
	sort.Strings(installed)
	for _, a := range installed {
		realAt := -1
		for i, d := range dirs {
			if i == shimAt {
				continue
			}
			if isExecutable(filepath.Join(d, a)) {
				realAt = i
				break
			}
		}
		switch {
		case shimAt < 0:
			st.Behind = append(st.Behind, a)
		case realAt < 0 || shimAt < realAt:
			st.Ahead = append(st.Ahead, a)
		default:
			st.Behind = append(st.Behind, a)
		}
	}
	for _, f := range startup {
		if data, err := os.ReadFile(f); err == nil && strings.Contains(string(data), blockStart) {
			st.Files = append(st.Files, f)
		}
	}
	sort.Strings(st.Found)
	return st
}

// String renders the report the way `diple status` prints it.
func (s Status) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "shim directory: %s\n", s.Dir)
	fmt.Fprintf(&b, "agents found: %s\n", list(s.Found))
	fmt.Fprintf(&b, "wrapped: %s\n", list(s.Wrapped))
	fmt.Fprintf(&b, "identity-only: %s\n", list(s.Identity))
	if len(s.Unshimmed) > 0 {
		fmt.Fprintf(&b, "found without a shim: %s (`diple on <agent>` adds one)\n", list(s.Unshimmed))
	}
	if s.OnPath {
		fmt.Fprintf(&b, "on PATH: yes\n")
	} else {
		fmt.Fprintf(&b, "on PATH: no (run `diple on`, then re-read your profile)\n")
	}
	if len(s.Ahead) > 0 {
		fmt.Fprintf(&b, "ahead of the real binary: %s\n", list(s.Ahead))
	}
	if len(s.Behind) > 0 {
		fmt.Fprintf(&b, "behind the real binary: %s\n", list(s.Behind))
	}
	fmt.Fprintf(&b, "startup files with the diple block: %s\n", list(s.Files))
	return b.String()
}

func list(v []string) string {
	if len(v) == 0 {
		return "none"
	}
	return strings.Join(v, ", ")
}

func sameDir(a, b string) bool {
	if a == "" {
		a = "."
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}
	return ra == rb
}

func isExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}
