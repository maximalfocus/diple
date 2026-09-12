package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximalfocus/diple/internal/record"
)

// captureWithHost writes a pass-through capture of the agent named agent,
// with what the host called its pane.
func captureWithHost(t *testing.T, agent, identity string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "capture.jsonl")
	w, err := record.Create(p, record.Header{Agent: agent, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	w.Output([]byte(reply))
	w.Host(identity)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestTheHostMustNameTheAgent: R-017. A capture that says the host called
// the pane Diple fails, whatever else it shows.
func TestTheHostMustNameTheAgent(t *testing.T) {
	if f := check("ghostty", captureWithHost(t, "fake", "name=fake state=idle"), false); len(f) != 0 {
		t.Fatalf("the agent's own name failed: %v", f)
	}
	f := check("ghostty", captureWithHost(t, "fake", "name=diple state=unknown"), false)
	want := `the host named the pane "diple", not the agent "fake"`
	if len(f) != 1 || !strings.Contains(f[0], want) {
		t.Fatalf("failures = %v", f)
	}
}

func TestHerdrIdentityFindsThePane(t *testing.T) {
	list := []byte(`{"id":"cli:agent:list","result":{"agents":[
		{"agent":"claude","agent_status":"working","pane_id":"w1:p1"},
		{"agent":"claude","agent_status":"idle","pane_id":"w1:p7","name":"diple-verify"}
	],"type":"agent_list"}}`)
	if got := herdrIdentity(list, "w1:p7", ""); got != "name=claude state=idle" {
		t.Fatalf("by pane: %q", got)
	}
	if got := herdrIdentity(list, "", "diple-verify"); got != "name=claude state=idle" {
		t.Fatalf("by name: %q", got)
	}
	if got := herdrIdentity(list, "w1:p9", ""); got != "" {
		t.Fatalf("an unlisted pane: %q", got)
	}
	if got := herdrIdentity([]byte("not json"), "w1:p7", ""); got != "" {
		t.Fatalf("garbage: %q", got)
	}
}
