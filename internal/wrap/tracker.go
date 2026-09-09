package wrap

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/maximalfocus/diple/internal/adapter"
)

// Tracker follows the wrapped session's transcript: it discovers the file
// once the agent writes it, then re-parses it whenever it changes, so the
// session always has the latest turns and blocks without touching the
// agent. It only ever reads.
type Tracker struct {
	adapter adapter.Adapter
	cwd     string
	since   time.Time
	poll    time.Duration
	onFound func(sessionID string)

	mu         sync.Mutex
	path       string
	modTime    time.Time
	size       int64
	transcript *adapter.Transcript
	err        error
	stop       chan struct{}
	done       chan struct{}
}

// NewTracker prepares a tracker for a session that started now in cwd.
// onFound, when set, is called once with the session id after discovery.
func NewTracker(a adapter.Adapter, cwd string, onFound func(sessionID string)) *Tracker {
	return &Tracker{adapter: a, cwd: cwd, since: time.Now(), poll: 500 * time.Millisecond, onFound: onFound,
		stop: make(chan struct{}), done: make(chan struct{})}
}

// Start begins polling in the background.
func (t *Tracker) Start() {
	go t.loop()
}

// Stop ends polling and waits for the loop to exit.
func (t *Tracker) Stop() {
	select {
	case <-t.stop:
	default:
		close(t.stop)
	}
	<-t.done
}

func (t *Tracker) loop() {
	defer close(t.done)
	ticker := time.NewTicker(t.poll)
	defer ticker.Stop()
	for {
		t.tick()
		select {
		case <-t.stop:
			return
		case <-ticker.C:
		}
	}
}

// tick performs one discovery or refresh step.
func (t *Tracker) tick() {
	t.mu.Lock()
	path := t.path
	t.mu.Unlock()
	if path == "" {
		p, err := t.adapter.Discover(t.cwd, t.since)
		if err != nil {
			if !errors.Is(err, adapter.ErrNoTranscript) {
				t.setErr(err)
			}
			return
		}
		path = p
	}
	info, err := os.Stat(path)
	if err != nil {
		t.setErr(err)
		return
	}
	t.mu.Lock()
	unchanged := t.path == path && info.ModTime().Equal(t.modTime) && info.Size() == t.size
	t.mu.Unlock()
	if unchanged {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		t.setErr(err)
		return
	}
	tr, perr := t.adapter.Parse(f)
	_ = f.Close()
	t.mu.Lock()
	first := t.path == ""
	t.path, t.modTime, t.size = path, info.ModTime(), info.Size()
	if perr != nil {
		t.err = perr
	} else {
		t.transcript, t.err = tr, nil
	}
	t.mu.Unlock()
	if first && t.onFound != nil {
		id := ""
		if tr != nil {
			id = tr.SessionID
		}
		t.onFound(id)
	}
}

func (t *Tracker) setErr(err error) {
	t.mu.Lock()
	t.err = err
	t.mu.Unlock()
}

// Path returns the discovered transcript path, empty until discovery.
func (t *Tracker) Path() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.path
}

// Transcript returns the latest parsed transcript and the last error. A
// version the adapter was not verified against is reported as the error
// and leaves the transcript nil, so the session falls back to paragraphs.
func (t *Tracker) Transcript() (*adapter.Transcript, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.transcript, t.err
}
