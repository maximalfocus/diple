package agent

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestTheCatalogueIsSortedAndHoldsTheAdapterAgents(t *testing.T) {
	if !sort.StringsAreSorted(Catalogue) {
		t.Fatal("the catalogue must stay sorted: Catalogued searches it")
	}
	for _, a := range []string{"claude", "codex", "pi", "opencode"} {
		if !Catalogued(a) {
			t.Errorf("%s is not catalogued", a)
		}
	}
	if Catalogued("diple") || Catalogued("sh") {
		t.Error("a name herdr does not recognise is catalogued")
	}
}

func TestFoundListsOnlyAgentsOnPathOutsideTheShimDirectory(t *testing.T) {
	real := t.TempDir()
	shims := t.TempDir()
	for _, p := range []string{
		filepath.Join(real, "codex"), filepath.Join(shims, "claude"),
		filepath.Join(real, "not-an-agent"),
	} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := Found(shims+string(os.PathListSeparator)+real, shims, "")
	if len(got) != 1 || got[0] != "codex" {
		t.Fatalf("Found = %v, want only the real codex", got)
	}
}
