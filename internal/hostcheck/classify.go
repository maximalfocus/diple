package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// classifyMain reads changed paths, as arguments or one per line on stdin, and
// prints docs-only when every one may land on tiers 1 to 4 alone, or evidence
// and the first path that needs live host evidence. Which tests bear on a
// change is decided by the paths it touches, never by its author.
func classifyMain(args []string) int {
	paths := args
	if len(paths) == 0 {
		var err error
		if paths, err = readLines(os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if p := needsEvidence(paths); p != "" {
		fmt.Printf("evidence %s\n", p)
		return 0
	}
	fmt.Println("docs-only")
	return 0
}

func readLines(r io.Reader) ([]string, error) {
	var lines []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

// needsEvidence returns the first changed path outside the documentation
// allowlist, or "" when there is none.
func needsEvidence(paths []string) string {
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" && !docsOnly(p) {
			return p
		}
	}
	return ""
}

// docsOnly reports whether a changed path is documentation that no build,
// test, or host reads: LICENSE, the top-level Markdown files, and Markdown
// under docs/ apart from the acceptance case lists, which a tier-2 test reads.
// Everything else — code, dependencies, scripts, fixtures, workflow and build
// configuration, and this classifier itself — needs live host evidence.
func docsOnly(p string) bool {
	p = path.Clean(strings.ReplaceAll(p, `\`, "/"))
	if p == "LICENSE" {
		return true
	}
	if path.Ext(p) != ".md" {
		return false
	}
	dir := path.Dir(p)
	if dir == "." {
		return true
	}
	inDocs := dir == "docs" || strings.HasPrefix(dir, "docs/")
	inAcceptance := dir == "docs/acceptance" || strings.HasPrefix(dir, "docs/acceptance/")
	return inDocs && !inAcceptance
}
