package card

import (
	"os"
	"testing"
	"time"
)

func trayOf(texts ...string) *Tray {
	t := &Tray{}
	for _, s := range texts {
		t.Add(&Card{Kind: Free, Text: s})
	}
	return t
}

// TestStashTakesTheMostRecentSessionsAgent: nothing assumes one agent. The
// agent whose session tray was written last is meant, and a tie or no tray
// at all is ambiguous.
func TestStashTakesTheMostRecentSessionsAgent(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	agents := []string{"claude", "codex", "pi"}
	if _, ok := st.NewestAgent(agents); ok {
		t.Fatal("no tray at all is ambiguous")
	}
	if err := st.Save("claude", "s1", trayOf("one")); err != nil {
		t.Fatal(err)
	}
	if err := st.Save("codex", "s2", trayOf("two")); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	if err := os.Chtimes(st.path("claude", "s1"), base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(st.path("codex", "s2"),
		base.Add(time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if a, ok := st.NewestAgent(agents); !ok || a != "codex" {
		t.Fatalf("NewestAgent = %q, %v; want codex", a, ok)
	}
	// Two trays written at the same instant: ask.
	if err := os.Chtimes(st.path("claude", "s1"),
		base.Add(time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if a, ok := st.NewestAgent(agents); ok {
		t.Fatalf("a tie was taken as %q", a)
	}
}

func TestUnstashTakesTheOneAgentWithAStash(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	agents := []string{"claude", "codex"}
	if _, ok := st.StashedAgent(agents); ok {
		t.Fatal("no stash is ambiguous")
	}
	if err := st.Save("codex", "s", trayOf("x")); err != nil {
		t.Fatal(err)
	}
	if n, err := st.Stash("codex"); err != nil || n != 1 {
		t.Fatalf("stash = %d, %v", n, err)
	}
	if a, ok := st.StashedAgent(agents); !ok || a != "codex" {
		t.Fatalf("StashedAgent = %q, %v", a, ok)
	}
	if err := st.Save("claude", "s", trayOf("y")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Stash("claude"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.StashedAgent(agents); ok {
		t.Fatal("two stashes are ambiguous")
	}
}
