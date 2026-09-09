// Package card holds the units of the outgoing message: cards, their
// anchors into the agent's output, and the tray that orders them. It also
// persists a tray per agent session so it survives restarts.
package card

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximalfocus/diple/internal/blocks"
)

// Kind is a card's kind. Only notes exist until the free kinds land.
type Kind string

// Card kinds.
const (
	Note Kind = "note"
)

// Tag is the tag of a note card, chosen from the toolbar.
type Tag string

// The six tags, in toolbar order. Their index maps to indexed colours 1–6.
var Tags = []Tag{"fix", "question", "reject", "approve", "prefer", "comment"}

// TagByLetter returns the tag whose name starts with r, or "" when none.
func TagByLetter(r rune) Tag {
	for _, t := range Tags {
		if rune(t[0]) == r {
			return t
		}
	}
	return ""
}

// Color returns the indexed colour (1–6) of a tag, 0 when unknown.
func (t Tag) Color() uint8 {
	for i, tag := range Tags {
		if tag == t {
			return uint8(i + 1)
		}
	}
	return 0
}

// Anchor is what a note points at: a block of a turn, optionally narrowed
// to a span of text or a range of code lines, with a quotation for the
// compiled message.
type Anchor struct {
	Turn  int         `json:"turn"`  // assistant turn ordinal
	Block int         `json:"block"` // block index within the turn
	Kind  blocks.Kind `json:"kind"`
	// First and Last are the rows the block occupied when the note was
	// made, as indexes into the history the mode provides.
	First int `json:"first"`
	Last  int `json:"last"`
	// Absolute is First expressed as an absolute row since the session
	// started, valid inline where history only grows.
	Absolute int `json:"absolute"`
	// Quote is the anchored text, trimmed from the end to about 120 runes.
	Quote string `json:"quote"`
	// Ordinal is the list item's ordinal for prefer notes, 0 otherwise.
	Ordinal int `json:"ordinal,omitempty"`
	// Span narrows the anchor to the text between two columns of the
	// block's rows, when the note was made by dragging.
	Span *Span `json:"span,omitempty"`
	// Lines narrows the anchor to a range of code lines by block index.
	Lines *LineRange `json:"lines,omitempty"`
}

// Span is a dragged range: from (Row, Col) to (EndRow, EndCol), inclusive,
// in history rows and screen columns.
type Span struct {
	Row    int `json:"row"`
	Col    int `json:"col"`
	EndRow int `json:"endRow"`
	EndCol int `json:"endCol"`
}

// LineRange is a range of code-line block indexes within the turn.
type LineRange struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

// MaxQuote bounds the quotation length in runes.
const MaxQuote = 120

// Quote trims text from the end to MaxQuote runes with an ellipsis.
func Quote(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) <= MaxQuote {
		return text
	}
	return string(r[:MaxQuote-1]) + "…"
}

// Card is one unit of the outgoing message.
type Card struct {
	ID     string `json:"id"`
	Kind   Kind   `json:"kind"`
	Tag    Tag    `json:"tag,omitempty"`
	Anchor Anchor `json:"anchor"`
	Text   string `json:"text"`
}

// Tray is the ordered list of cards for one agent session.
type Tray struct {
	Cards []*Card `json:"cards"`
	next  int
}

// Add appends a card and gives it an id.
func (t *Tray) Add(c *Card) *Card {
	t.next++
	c.ID = time.Now().UTC().Format("20060102T150405") + "-" + itoa(t.next)
	t.Cards = append(t.Cards, c)
	return c
}

// Delete removes the card at index i.
func (t *Tray) Delete(i int) bool {
	if i < 0 || i >= len(t.Cards) {
		return false
	}
	t.Cards = append(t.Cards[:i], t.Cards[i+1:]...)
	return true
}

// Move moves the card at index from to index to, keeping order otherwise.
func (t *Tray) Move(from, to int) bool {
	n := len(t.Cards)
	if from < 0 || from >= n || to < 0 || to >= n || from == to {
		return false
	}
	c := t.Cards[from]
	t.Cards = append(t.Cards[:from], t.Cards[from+1:]...)
	t.Cards = append(t.Cards[:to], append([]*Card{c}, t.Cards[to:]...)...)
	return true
}

// Len returns the number of cards.
func (t *Tray) Len() int { return len(t.Cards) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Store persists trays per agent session under the user's state directory.
type Store struct {
	Dir string
}

// DefaultStore returns the store under $XDG_STATE_HOME/diple or
// ~/.local/state/diple.
func DefaultStore() (*Store, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return &Store{Dir: filepath.Join(base, "diple", "trays")}, nil
}

func (s *Store) path(agent, session string) string {
	safe := func(v string) string {
		var b strings.Builder
		for _, r := range v {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				b.WriteRune(r)
			} else {
				b.WriteByte('_')
			}
		}
		return b.String()
	}
	return filepath.Join(s.Dir, safe(agent)+"-"+safe(session)+".json")
}

// Save writes the tray for an agent session, or removes the file when the
// tray is empty.
func (s *Store) Save(agent, session string, t *Tray) error {
	if session == "" {
		return nil
	}
	p := s.path(agent, session)
	if t.Len() == 0 {
		err := os.Remove(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Load reads the tray for an agent session; a missing file is an empty tray.
func (s *Store) Load(agent, session string) (*Tray, error) {
	t := &Tray{}
	data, err := os.ReadFile(s.path(agent, session))
	if errors.Is(err, os.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, t); err != nil {
		return nil, err
	}
	t.next = len(t.Cards)
	return t, nil
}
