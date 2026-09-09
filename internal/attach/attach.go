// Package attach captures what an instruction card's attachment refers to.
// A path is only ever a reference: Diple never reads it, because the agent
// can open it itself. A command is different — the user asked for its output,
// so Diple runs it once, when it is attached, and the card carries what it
// printed.
package attach

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/maximalfocus/diple/internal/card"
)

// MaxOutput is how much of a command's output a card carries. Beyond it the
// capture is cut and marked, so one chatty command cannot fill a message.
const MaxOutput = 4000

// Timeout bounds a capture. A command that hangs must not wedge the session,
// so it is killed and attached with what it printed.
const Timeout = 10 * time.Second

// Parse reads one attachment line as the user typed it: `@path` is a
// reference and `!command` is output to capture. Anything else is not an
// attachment.
func Parse(line string) (card.Attachment, bool) {
	line = strings.TrimSpace(line)
	if len(line) < 2 {
		return card.Attachment{}, false
	}
	spec := strings.TrimSpace(line[1:])
	if spec == "" {
		return card.Attachment{}, false
	}
	switch line[0] {
	case '@':
		return card.Attachment{Kind: card.PathAttachment, Spec: spec}, true
	case '!':
		return card.Attachment{Kind: card.CommandAttachment, Spec: spec}, true
	}
	return card.Attachment{}, false
}

// Capture runs a command attachment in dir and fills in what it printed. A
// path attachment is returned untouched. A command that fails or cannot be
// run attaches its status and whatever it printed, never nothing.
func Capture(a card.Attachment, dir string) card.Attachment {
	if a.Kind != card.CommandAttachment {
		return a
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", a.Spec)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	a.Output, a.Truncated = truncate(string(out))
	switch {
	case err == nil:
		a.Status = 0
	case cmd.ProcessState != nil:
		a.Status = cmd.ProcessState.ExitCode()
	default:
		a.Status = -1
		if a.Output == "" {
			a.Output = err.Error()
		}
	}
	return a
}

func truncate(s string) (string, bool) {
	s = strings.TrimRight(s, "\n")
	r := []rune(s)
	if len(r) <= MaxOutput {
		return s, false
	}
	return string(r[:MaxOutput]), true
}
