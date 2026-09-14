// Package record writes and reads session fixtures: the bytes a wrapped
// agent emitted, the bytes the user typed, and every resize, each stamped
// with the time since the session started. A fixture replays into a screen
// model so later work can test alignment against real sessions.
package record

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Format is the fixture format version written in the header.
const Format = 1

// Event kinds.
const (
	KindOutput     = "out"        // bytes from the agent to the terminal
	KindInput      = "in"         // bytes from the user to the agent
	KindResize     = "resize"     // the terminal changed size
	KindTranscript = "transcript" // the session transcript was discovered
	// KindHost is what the host called the wrapped pane, and the state it
	// reported, as `name=<agent> state=<state>`. The host check appends it
	// to a live capture; R-017 requires the name to be the agent's.
	KindHost = "host"
)

// Header is the first line of a fixture.
type Header struct {
	Format int      `json:"format"`
	Agent  string   `json:"agent"`
	Args   []string `json:"args"`
	Cols   int      `json:"cols"`
	Rows   int      `json:"rows"`
	Term   string   `json:"term,omitempty"`
}

// Event is one line after the header.
type Event struct {
	At      time.Duration `json:"at"`
	Kind    string        `json:"kind"`
	Data    []byte        `json:"data,omitempty"`
	Cols    int           `json:"cols,omitempty"`
	Rows    int           `json:"rows,omitempty"`
	Session string        `json:"session,omitempty"` // transcript session id
}

type wireEvent struct {
	At      int64  `json:"at"`
	Kind    string `json:"kind"`
	Data    string `json:"data,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Session string `json:"session,omitempty"`
}

// Writer appends events to a fixture. It is safe for concurrent use.
type Writer struct {
	mu    sync.Mutex
	w     *bufio.Writer
	c     io.Closer
	start time.Time
	err   error
}

// Create opens path for writing and writes the header.
func Create(path string, h Header) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	w := &Writer{w: bufio.NewWriter(f), c: f, start: time.Now()}
	h.Format = Format
	if err := json.NewEncoder(w.w).Encode(h); err != nil {
		_ = f.Close()
		return nil, err
	}
	return w, nil
}

func (w *Writer) write(ev wireEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return
	}
	w.err = json.NewEncoder(w.w).Encode(ev)
}

func (w *Writer) since() int64 { return time.Since(w.start).Nanoseconds() }

// Output records bytes the agent emitted.
func (w *Writer) Output(p []byte) {
	w.write(wireEvent{At: w.since(), Kind: KindOutput, Data: base64.StdEncoding.EncodeToString(p)})
}

// Input records bytes the user typed.
func (w *Writer) Input(p []byte) {
	w.write(wireEvent{At: w.since(), Kind: KindInput, Data: base64.StdEncoding.EncodeToString(p)})
}

// Resize records a terminal size change.
func (w *Writer) Resize(cols, rows int) {
	w.write(wireEvent{At: w.since(), Kind: KindResize, Cols: cols, Rows: rows})
}

// Transcript records that the session transcript was discovered. Only the
// session id is kept, never the path, so a fixture carries no home directory.
func (w *Writer) Transcript(sessionID string) {
	w.write(wireEvent{At: w.since(), Kind: KindTranscript, Session: sessionID})
}

// Host records what the host called the wrapped pane, and its state.
func (w *Writer) Host(identity string) {
	data := base64.StdEncoding.EncodeToString([]byte(identity))
	w.write(wireEvent{At: w.since(), Kind: KindHost, Data: data})
}

// Close flushes and closes the fixture, returning the first write error.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.w.Flush(); err != nil && w.err == nil {
		w.err = err
	}
	if err := w.c.Close(); err != nil && w.err == nil {
		w.err = err
	}
	return w.err
}

// Recording is a fixture read back into memory.
type Recording struct {
	Header Header
	Events []Event
}

// Read parses a fixture.
func Read(r io.Reader) (*Recording, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("record: empty fixture")
	}
	rec := &Recording{}
	if err := json.Unmarshal(sc.Bytes(), &rec.Header); err != nil {
		return nil, fmt.Errorf("record: header: %w", err)
	}
	if rec.Header.Format != Format {
		return nil, fmt.Errorf("record: fixture format %d, this build reads %d", rec.Header.Format, Format)
	}
	for line := 2; sc.Scan(); line++ {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var we wireEvent
		if err := json.Unmarshal(sc.Bytes(), &we); err != nil {
			return nil, fmt.Errorf("record: line %d: %w", line, err)
		}
		ev := Event{At: time.Duration(we.At), Kind: we.Kind, Cols: we.Cols, Rows: we.Rows, Session: we.Session}
		if we.Data != "" {
			data, err := base64.StdEncoding.DecodeString(we.Data)
			if err != nil {
				return nil, fmt.Errorf("record: line %d: %w", line, err)
			}
			ev.Data = data
		}
		switch ev.Kind {
		case KindOutput, KindInput, KindResize, KindTranscript, KindHost:
		default:
			return nil, fmt.Errorf("record: line %d: unknown event kind %q", line, ev.Kind)
		}
		rec.Events = append(rec.Events, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return rec, nil
}

// Open reads the fixture at path.
func Open(path string) (*Recording, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Read(f)
}

// Output returns the concatenated agent output of the recording.
func (r *Recording) Output() []byte {
	var out []byte
	for _, ev := range r.Events {
		if ev.Kind == KindOutput {
			out = append(out, ev.Data...)
		}
	}
	return out
}

// Sink receives a replay: the two calls a screen model needs.
type Sink interface {
	Write(p []byte) (int, error)
	Resize(cols, rows int)
}

// Replay feeds the recording's output and resize events into sink in order.
func (r *Recording) Replay(sink Sink) error {
	for _, ev := range r.Events {
		switch ev.Kind {
		case KindOutput:
			if _, err := sink.Write(ev.Data); err != nil {
				return err
			}
		case KindResize:
			sink.Resize(ev.Cols, ev.Rows)
		}
	}
	return nil
}

// A throwaway change that carries no host evidence, so gate is red.
