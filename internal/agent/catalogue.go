package agent

import "sort"

// Catalogue is the agent CLIs Diple knows by name, aligned with those herdr's
// agent detection recognises. An agent here with an adapter is wrapped; any
// other is identity-only: it runs as itself, so the host names it, and Diple
// owns nothing in its pane. `diple on` installs shims for catalogued agents
// found on PATH that have an adapter; `diple on <agent>` adds any other.
var Catalogue = []string{
	"agy", "amp", "claude", "cline", "codex", "copilot", "cursor", "devin",
	"droid", "gemini", "grok", "hermes", "kilo", "kimi", "kiro", "maki",
	"opencode", "pi", "qodercli", "qwen",
}

// Catalogued reports whether name is in the catalogue.
func Catalogued(name string) bool {
	i := sort.SearchStrings(Catalogue, name)
	return i < len(Catalogue) && Catalogue[i] == name
}

// Found returns the catalogued agents that have a real executable on PATH,
// skipping the shim directory and Diple itself as Locate does.
func Found(path, skipDir, self string) []string {
	var out []string
	for _, name := range Catalogue {
		if _, err := Locate(name, path, skipDir, self); err == nil {
			out = append(out, name)
		}
	}
	return out
}
