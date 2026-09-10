package wrap

import "bytes"

// mouseEvent is one decoded SGR mouse report.
type mouseEvent struct {
	button  int // raw button code with modifier bits
	x, y    int // 1-based
	release bool
}

func (m mouseEvent) wheelUp() bool   { return m.button&^0x1c == 64 }
func (m mouseEvent) wheelDown() bool { return m.button&^0x1c == 65 }
func (m mouseEvent) isWheel() bool   { return m.wheelUp() || m.wheelDown() }

// parseSGRMouse decodes an SGR mouse report at the start of p. It returns
// the event and the sequence length, or ok=false when p does not start with
// a complete report. incomplete is true when p is a proper prefix of one.
func parseSGRMouse(p []byte) (ev mouseEvent, n int, ok bool, incomplete bool) {
	const prefix = "\x1b[<"
	if !bytes.HasPrefix(p, []byte(prefix)) {
		if len(p) < len(prefix) && bytes.HasPrefix([]byte(prefix), p) {
			return ev, 0, false, true
		}
		return ev, 0, false, false
	}
	i := len(prefix)
	var fields [3]int
	for f := 0; f < 3; f++ {
		start := i
		for i < len(p) && p[i] >= '0' && p[i] <= '9' {
			fields[f] = fields[f]*10 + int(p[i]-'0')
			i++
			if fields[f] > 1<<20 {
				return ev, 0, false, false
			}
		}
		if i == start {
			if i == len(p) {
				return ev, 0, false, true
			}
			return ev, 0, false, false
		}
		if i == len(p) {
			return ev, 0, false, true
		}
		switch {
		case f < 2 && p[i] == ';':
			i++
		case f == 2 && (p[i] == 'M' || p[i] == 'm'):
			ev = mouseEvent{button: fields[0], x: fields[1], y: fields[2], release: p[i] == 'm'}
			return ev, i + 1, true, false
		default:
			return ev, 0, false, false
		}
	}
	return ev, 0, false, false
}

// endKeys are the byte sequences terminals send for the End key.
var endKeys = [][]byte{
	[]byte("\x1b[F"),
	[]byte("\x1bOF"),
	[]byte("\x1b[4~"),
	[]byte("\x1b[8~"),
}

// parseEndKey reports whether p starts with an End key sequence and its
// length.
func parseEndKey(p []byte) (n int, ok bool) {
	for _, k := range endKeys {
		if bytes.HasPrefix(p, k) {
			return len(k), true
		}
	}
	return 0, false
}

// parsePaste decodes a bracketed paste at the start of p. Diple keeps
// bracketed paste on for its own input so it can tell a paste from typing,
// and so a pasted newline never submits anything.
func parsePaste(p []byte) (text string, n int, ok bool, incomplete bool) {
	start, end := []byte(pasteStart), []byte(pasteEnd)
	if !bytes.HasPrefix(p, start) {
		if len(p) < len(start) && bytes.HasPrefix(start, p) {
			return "", 0, false, true
		}
		return "", 0, false, false
	}
	body := p[len(start):]
	i := bytes.Index(body, end)
	if i < 0 {
		// The tail has not arrived yet; wait for it rather than treating the
		// paste as typing.
		return "", 0, false, true
	}
	return string(body[:i]), len(start) + i + len(end), true, false
}
