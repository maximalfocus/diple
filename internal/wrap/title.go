package wrap

// maxTitle bounds an unfinished title sequence, so output that opens one and
// never closes it cannot grow the buffer without end.
const maxTitle = 4096

// titles picks the agent's window-title sequences — OSC 0, 1 and 2 — out of
// its output, across chunk boundaries. The screen model skips every OSC
// sequence, so while Diple composites or shows scrollback, and does not
// forward the agent's bytes as they come, these are forwarded on their own: a
// host that names or reads the pane by its title keeps seeing the agent's.
// herdr reads codex's state from it.
type titles struct {
	state int    // 0 ground, 1 after ESC, 2 in an OSC, 3 in an OSC after ESC
	buf   []byte // the OSC sequence so far, from its ESC
}

// scan returns every title sequence p completes, each whole and terminated.
func (t *titles) scan(p []byte) [][]byte {
	var out [][]byte
	for _, b := range p {
		switch t.state {
		case 0:
			if b == 0x1b {
				t.state = 1
			}
		case 1:
			t.afterEscape(b)
		case 2:
			switch b {
			case 0x07:
				t.buf = append(t.buf, b)
				out = t.finish(out)
			case 0x1b:
				t.state = 3
			default:
				t.buf = append(t.buf, b)
			}
		case 3:
			if b == '\\' {
				t.buf = append(t.buf, 0x1b, '\\')
				out = t.finish(out)
				continue
			}
			// An ESC that is not a string terminator abandons the sequence
			// and begins whatever follows it.
			t.afterEscape(b)
		}
		if len(t.buf) > maxTitle {
			t.buf, t.state = nil, 0
		}
	}
	return out
}

// afterEscape reads the byte after an ESC: `]` opens an OSC sequence.
func (t *titles) afterEscape(b byte) {
	switch b {
	case ']':
		t.state = 2
		t.buf = append(t.buf[:0], 0x1b, ']')
	case 0x1b:
		t.state = 1
	default:
		t.state = 0
		t.buf = t.buf[:0]
	}
}

// finish ends the sequence in buf and keeps it when it sets a title.
func (t *titles) finish(out [][]byte) [][]byte {
	seq := t.buf
	t.buf, t.state = nil, 0
	if len(seq) >= 4 && (seq[2] == '0' || seq[2] == '1' || seq[2] == '2') && seq[3] == ';' {
		out = append(out, seq)
	}
	return out
}
