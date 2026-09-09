package keys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAndRenderKeys(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Key
	}{
		{"f", Key{Rune: 'f'}},
		{"alt+p", Key{Rune: 'p', Alt: true}},
		{"ALT+enter", Key{Rune: '\r', Alt: true}},
		{"esc", Key{Rune: 0x1b}},
		{"tab", Key{Rune: '\t'}},
		{"[", Key{Rune: '['}},
	} {
		got, err := ParseKey(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseKey(%q) = %+v, %v", tc.in, got, err)
		}
		if again, err := ParseKey(got.String()); err != nil || again != tc.want {
			t.Fatalf("round trip of %q through %q = %+v, %v", tc.in, got.String(), again, err)
		}
	}
	for _, in := range []string{"", "ctrl+a", "abc", "alt+"} {
		if k, err := ParseKey(in); err == nil {
			t.Fatalf("ParseKey(%q) = %+v, want an error", in, k)
		}
	}
}

func TestEveryActionHasADefaultAndIsListed(t *testing.T) {
	d := Defaults()
	if len(d) != len(Actions) {
		t.Fatalf("%d defaults for %d actions", len(d), len(Actions))
	}
	for _, a := range Actions {
		if _, ok := d[a]; !ok {
			t.Fatalf("%s has no default", a)
		}
	}
	table := d.String()
	for _, a := range Actions {
		if !strings.Contains(table, string(a)) {
			t.Fatalf("%s is missing from the printed table", a)
		}
	}
}

func TestLoadReadsTheFileAndSurvivesAMissingOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.conf")
	if table, complaints := Load(path); len(complaints) != 0 || !table.Is(NextTurn, Key{Rune: ']'}) {
		t.Fatalf("a missing file is the defaults: %v", complaints)
	}
	if err := os.WriteFile(path, []byte("search = ?\nsend = alt+m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	table, complaints := Load(path)
	if len(complaints) != 0 {
		t.Fatalf("complaints = %v", complaints)
	}
	if !table.Is(Search, Key{Rune: '?'}) || !table.Is(Send, Key{Rune: 'm', Alt: true}) {
		t.Fatalf("table = %v", table)
	}
	if !table.Is(Paste, Key{Rune: 'p', Alt: true}) {
		t.Fatal("untouched actions keep their defaults")
	}
}
