package main

import "testing"

func TestTheClassifierAllowsOnlyDocumentation(t *testing.T) {
	for p, want := range map[string]bool{
		"README.md":                 true,
		"CONTRIBUTING.md":           true,
		"RELEASE.md":                true,
		"AGENTS.md":                 true,
		"LICENSE":                   true,
		"docs/adapters.md":          true,
		"docs/guides/deep.md":       true,
		"docs/acceptance/S-014.md":  false,
		"docs/acceptance/README.md": false,
		"internal/adapter/claude/testdata/README.md":    false,
		"testdata/hosts/tmux/3.6/default.capture.jsonl": false,
		".github/workflows/ci.yml":                      false,
		".github/pull_request_template.md":              false,
		"go.mod":                                        false,
		"go.sum":                                        false,
		"scripts/verify-hosts.sh":                       false,
		"packaging/homebrew/diple.rb":                   false,
		"internal/hostcheck/classify.go":                false,
		"cmd/diple/main.go":                             false,
		"docs/diagram.svg":                              false,
		"./README.md":                                   true,
	} {
		if got := docsOnly(p); got != want {
			t.Errorf("docsOnly(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestOneCodePathMakesTheWholeChangeNeedEvidence(t *testing.T) {
	if p := needsEvidence([]string{"README.md", "", "docs/adapters.md"}); p != "" {
		t.Fatalf("a documentation-only change needs evidence for %q", p)
	}
	if p := needsEvidence([]string{"README.md", "internal/wrap/session.go", "go.mod"}); p != "internal/wrap/session.go" {
		t.Fatalf("needsEvidence = %q, want the first code path", p)
	}
	if p := needsEvidence(nil); p != "" {
		t.Fatalf("an empty change needs evidence for %q", p)
	}
}
