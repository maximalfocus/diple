package record

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximalfocus/diple/internal/screen"
)

func TestRoundTripAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	w, err := Create(path, Header{Agent: "fake", Args: []string{"--flag"}, Cols: 10, Rows: 2, Term: "xterm"})
	if err != nil {
		t.Fatal(err)
	}
	w.Output([]byte("hello\r\n"))
	w.Input([]byte("q"))
	w.Resize(6, 2)
	w.Output([]byte("\x1b[1mworld\x1b[0m"))
	w.Transcript("abc-123")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	rec, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Header.Agent != "fake" || rec.Header.Cols != 10 || rec.Header.Format != Format {
		t.Fatalf("header = %+v", rec.Header)
	}
	if len(rec.Events) != 5 || rec.Events[1].Kind != KindInput || rec.Events[2].Cols != 6 {
		t.Fatalf("events = %+v", rec.Events)
	}
	if rec.Events[4].Kind != KindTranscript || rec.Events[4].Session != "abc-123" {
		t.Fatalf("transcript event = %+v", rec.Events[4])
	}
	if !bytes.Equal(rec.Output(), []byte("hello\r\n\x1b[1mworld\x1b[0m")) {
		t.Fatalf("output = %q", rec.Output())
	}

	// Replay reproduces the same model as feeding the events live.
	live := screen.New(rec.Header.Cols, rec.Header.Rows)
	_, _ = live.Write([]byte("hello\r\n"))
	live.Resize(6, 2)
	_, _ = live.Write([]byte("\x1b[1mworld\x1b[0m"))

	replayed := screen.New(rec.Header.Cols, rec.Header.Rows)
	if err := rec.Replay(replayed); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if !live.Row(i).Equal(replayed.Row(i)) {
			t.Fatalf("row %d differs: %q vs %q", i, live.Row(i).String(), replayed.Row(i).String())
		}
	}
}

func TestReadRejectsOtherFormat(t *testing.T) {
	_, err := Read(bytes.NewReader([]byte(`{"format":99,"agent":"x","cols":1,"rows":1}` + "\n")))
	if err == nil {
		t.Fatal("expected a format error")
	}
}

func TestReadRejectsUnknownKind(t *testing.T) {
	data := `{"format":1,"agent":"x","cols":1,"rows":1}` + "\n" + `{"at":0,"kind":"bogus"}` + "\n"
	_, err := Read(bytes.NewReader([]byte(data)))
	if err == nil {
		t.Fatal("expected an unknown-kind error")
	}
}

func TestCreateIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	w, err := Create(path, Header{Agent: "x", Cols: 1, Rows: 1})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("fixture mode = %v, want owner-only", st.Mode().Perm())
	}
}
