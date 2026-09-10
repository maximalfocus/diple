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

	"github.com/maximalfocus/diple/internal/blocks"
	"github.com/maximalfocus/diple/internal/card"
)

// Compile turns the ordered cards into one message. latestTurn is the newest
// assistant turn ordinal, used to phrase a reference to an earlier turn. The
// overall card is the tray's closing remark, so it is not one of the numbered
// items and always comes last.
//
// Nothing announces the message: the first card's own shape says what this
// is, and the last number says how many there are. A tray of one card
// compiles to that card alone, with no number in front of it, because a
// numbered list of one item is a list only in form.
func Compile(cards []*card.Card, latestTurn int) string {
	var numbered []*card.Card
	var overall *card.Card
	for _, c := range cards {
		if c.Overall {
			overall = c
			continue
		}
		numbered = append(numbered, c)
	}
	var b strings.Builder
	for i, c := range numbered {
		if i > 0 {
			b.WriteByte('\n')
		}
		indent := ""
		if len(numbered) > 1 {
			b.WriteString(strconv.Itoa(i + 1))
			b.WriteString(". ")
			indent = "   "
		}
		writeCard(&b, c, latestTurn, indent)
	}
	if overall != nil {
		if text := strings.TrimSpace(overall.Text); text != "" {
			if len(numbered) > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString("Overall: ")
			b.WriteString(text)
		}
	}
	return b.String()
}

// writeCard writes one entry. An anchored card leads with its tag and what it
// points at, and its own words follow underneath; a free card is its text and
// has neither tag nor anchor.
func writeCard(b *strings.Builder, c *card.Card, latestTurn int, indent string) {
	if c.Kind == card.Anchored {
		b.WriteByte('[')
		b.WriteString(string(c.Tag))
		b.WriteString("] ")
		b.WriteString(reference(c, latestTurn))
		if note := strings.TrimSpace(c.Text); note != "" {
			b.WriteByte('\n')
			b.WriteString(indent)
			b.WriteString(indentLines(note, indent))
		}
		writeAttachments(b, c, indent)
		return
	}
	text := strings.TrimSpace(c.Text)
	if c.Fenced {
		b.WriteString("```\n")
		for _, line := range strings.Split(text, "\n") {
			b.WriteString(indent)
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteString(indent)
		b.WriteString("```")
	} else {
		b.WriteString(indentLines(text, indent))
	}
	writeAttachments(b, c, indent)
}

// indentLines keeps a card's later lines under the first one, so a card that
// holds more than one line still reads as one entry.
func indentLines(text, indent string) string {
	if indent == "" || !strings.Contains(text, "\n") {
		return text
	}
	return strings.ReplaceAll(text, "\n", "\n"+indent)
}

// writeAttachments names each attachment under its card, and puts a
// captured command's output in a fenced block so the agent reads it as
// output rather than prose.
func writeAttachments(b *strings.Builder, c *card.Card, indent string) {
	for _, a := range c.Attachments {
		b.WriteString("\n" + indent + "attached: ")
		if a.Kind == card.PathAttachment {
			b.WriteString("@")
			b.WriteString(a.Spec)
			continue
		}
		b.WriteString(a.Spec)
		if a.Status != 0 {
			fmt.Fprintf(b, " (exit %d)", a.Status)
		}
		b.WriteString("\n" + indent + "```\n")
		for _, line := range strings.Split(a.Output, "\n") {
			b.WriteString(indent)
			b.WriteString(line)
			b.WriteByte('\n')
		}
		if a.Truncated {
			b.WriteString(indent + "… output truncated\n")
		}
		b.WriteString(indent + "```")
	}
}

func reference(c *card.Card, latestTurn int) string {
	prefix := ""
	if t := c.Anchor.Turn; t > 0 && latestTurn > t {
		n := latestTurn - t
		unit := "turns"
		if n == 1 {
			unit = "turn"
		}
		prefix = fmt.Sprintf("In your reply %d %s ago: ", n, unit)
	}
	// The anchor says what it points at, independently of the tag: a path and
	// line range where the diff named one, a list ordinal on a list item, and
	// otherwise the quotation.
	if p := c.Anchor.Path; p != "" && c.Anchor.LineFirst > 0 {
		if c.Anchor.LineLast > c.Anchor.LineFirst {
			return fmt.Sprintf("%s%s:%d-%d", prefix, p, c.Anchor.LineFirst, c.Anchor.LineLast)
		}
		return fmt.Sprintf("%s%s:%d", prefix, p, c.Anchor.LineFirst)
	}
	quote := c.Anchor.Quote
	if c.Anchor.Ordinal > 0 && c.Anchor.Kind == blocks.ListItem {
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
