// Package fold compiles the tray into the single plain-Markdown message the
// agent reads, and keeps the project-local archive of sent folds. It emits
// no Diple screen row numbers, because the agent cannot see them.
package fold

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/maximalfocus/diple/internal/card"
)

// Compile turns the ordered cards into one message. latestTurn is the newest
// assistant turn ordinal, used to phrase a reference to an earlier turn. The
// overall card is the tray's closing remark, so it is not one of the
// numbered items and always comes last.
func Compile(cards []*card.Card, latestTurn int) string {
	var numbered []*card.Card
	var overall *card.Card
	for _, c := range cards {
		if c.Kind == card.Overall {
			overall = c
			continue
		}
		numbered = append(numbered, c)
	}
	var b strings.Builder
	noun := "items"
	if len(numbered) == 1 {
		noun = "item"
	}
	fmt.Fprintf(&b, "Review (%d %s).\n", len(numbered), noun)
	for i, c := range numbered {
		b.WriteByte('\n')
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(". [")
		b.WriteString(label(c))
		b.WriteString("] ")
		b.WriteString(reference(c, latestTurn))
		// A note quotes what it points at, so its own text follows on the
		// next line; a free card is its text and has nothing to quote.
		if note := strings.TrimSpace(c.Text); note != "" && c.Kind == card.Note {
			b.WriteString("\n   ")
			b.WriteString(note)
		}
		writeAttachments(&b, c)
	}
	if overall != nil {
		if text := strings.TrimSpace(overall.Text); text != "" {
			b.WriteString("\n\nOverall: ")
			b.WriteString(text)
		}
	}
	return b.String()
}

// label is what the entry's brackets carry: a note's tag, and otherwise the
// card's own kind.
func label(c *card.Card) string {
	if c.Kind == card.Note {
		return string(c.Tag)
	}
	return string(c.Kind)
}

// writeAttachments names each attachment under its instruction, and puts a
// captured command's output in a fenced block so the agent reads it as
// output rather than prose.
func writeAttachments(b *strings.Builder, c *card.Card) {
	for _, a := range c.Attachments {
		b.WriteString("\n   attached: ")
		if a.Kind == card.PathAttachment {
			b.WriteString("@")
			b.WriteString(a.Spec)
			continue
		}
		b.WriteString(a.Spec)
		if a.Status != 0 {
			fmt.Fprintf(b, " (exit %d)", a.Status)
		}
		b.WriteString("\n   ```\n")
		for _, line := range strings.Split(a.Output, "\n") {
			b.WriteString("   ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
		if a.Truncated {
			b.WriteString("   … output truncated\n")
		}
		b.WriteString("   ```")
	}
}

func reference(c *card.Card, latestTurn int) string {
	if c.Kind != card.Note {
		return strings.TrimSpace(c.Text)
	}
	prefix := ""
	if t := c.Anchor.Turn; t > 0 && latestTurn > t {
		n := latestTurn - t
		unit := "turns"
		if n == 1 {
			unit = "turn"
		}
		prefix = fmt.Sprintf("In your reply %d %s ago: ", n, unit)
	}
	quote := c.Anchor.Quote
	if c.Tag == "prefer" && c.Anchor.Ordinal > 0 {
		return fmt.Sprintf("%sOption %d of the list starting %q.", prefix, c.Anchor.Ordinal, quote)
	}
	return fmt.Sprintf("%s> %q", prefix, quote)
}

// Archive appends sent folds to one file. It refuses any path outside the
// project directory it was opened for.
type Archive struct {
	Dir  string
	Path string
}

// NewArchive returns an archive writing to name under dir, or an error when
// name would escape dir.
func NewArchive(dir, name string) (*Archive, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	p := filepath.Join(abs, name)
	rel, err := filepath.Rel(abs, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("fold: archive path %q escapes %q", name, dir)
	}
	return &Archive{Dir: abs, Path: p}, nil
}

// Append writes one fold to the archive, separated from the previous one.
func (a *Archive) Append(text string) error {
	f, err := os.OpenFile(a.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	sep := "\n---\n"
	if info, err := f.Stat(); err == nil && info.Size() == 0 {
		sep = ""
	}
	_, err = f.WriteString(sep + time.Now().UTC().Format(time.RFC3339) + "\n\n" + text + "\n")
	return err
}
