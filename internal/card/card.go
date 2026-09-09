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

// Kind is a card's kind.
type Kind string

// Card kinds. A note is anchored in the agent's output; a question and an
// instruction are free text the user wrote; an overall card is the one
// closing remark of a tray and always compiles last.
const (
	Note        Kind = "note"
	Question    Kind = "question"
	Instruction Kind = "instruction"
	Overall     Kind = "overall"
)

// FreeKinds are the kinds created from the tray rather than from a block,
// in chooser order.
var FreeKinds = []Kind{Question, Instruction, Overall}

// KindByLetter returns the free kind whose name starts with r, or "".
func KindByLetter(r rune) Kind {
	for _, k := range FreeKinds {
		if rune(k[0]) == r {
			return k
		}
	}
	return ""
}

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

// AttachmentKind distinguishes a reference from captured output.
type AttachmentKind string

// Attachment kinds. A path is a reference Diple never reads, because the
// agent can open it itself; a command is run once, when it is attached, and
// what it printed travels with the card.
const (
	PathAttachment    AttachmentKind = "path"
	CommandAttachment AttachmentKind = "command"
)

// Attachment is what an instruction card carries.
type Attachment struct {
	Kind AttachmentKind `json:"kind"`
	// Spec is the path or command line as the user wrote it, without the
	// marker that chose the kind.
	Spec string `json:"spec"`
	// Output is what a command printed, combined stdout and stderr.
	Output string `json:"output,omitempty"`
	// Status is a command's exit status; -1 when it could not be run.
	Status int `json:"status,omitempty"`
	// Truncated records that Output was cut at the capture cap.
	Truncated bool `json:"truncated,omitempty"`
}

// Card is one unit of the outgoing message.
type Card struct {
	ID     string `json:"id"`
	Kind   Kind   `json:"kind"`
	Tag    Tag    `json:"tag,omitempty"`
	Anchor Anchor `json:"anchor"`
	Text   string `json:"text"`
	// Attachments belong to instruction cards.
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Tray is the ordered list of cards for one agent session.
type Tray struct {
	Cards []*Card `json:"cards"`
	next  int
}

// Add gives a card an id and files it in the tray. An overall card goes
// last and stays there, and every other card goes before it, so a tray can
// never carry two closing remarks or bury the one it has.
func (t *Tray) Add(c *Card) *Card {
	t.next++
	c.ID = time.Now().UTC().Format("20060102T150405") + "-" + itoa(t.next)
	if c.Kind == Overall {
		if existing := t.Overall(); existing != nil {
			existing.Text = c.Text
			return existing
		}
		t.Cards = append(t.Cards, c)
		return c
	}
	if i := t.overallIndex(); i >= 0 {
		t.Cards = append(t.Cards[:i], append([]*Card{c}, t.Cards[i:]...)...)
		return c
	}
	t.Cards = append(t.Cards, c)
	return c
}

// Overall returns the tray's overall card, or nil when it has none.
func (t *Tray) Overall() *Card {
	if i := t.overallIndex(); i >= 0 {
		return t.Cards[i]
	}
	return nil
}

func (t *Tray) overallIndex() int {
	for i, c := range t.Cards {
		if c.Kind == Overall {
			return i
		}
	}
	return -1
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
// The overall card holds the last place: it does not move, and nothing
// moves past it.
func (t *Tray) Move(from, to int) bool {
	n := len(t.Cards)
	if from < 0 || from >= n || to < 0 || to >= n || from == to {
		return false
	}
	if last := t.overallIndex(); last >= 0 && (from == last || to >= last) {
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

// stashPath is the agent's single stash slot, and pendingPath is the tray an
// unstash queued for the agent's next session.
func (s *Store) stashPath(agent string) string {
	return filepath.Join(s.Dir, safeName(agent)+"-stash.json")
}
func (s *Store) pendingPath(agent string) string {
	return filepath.Join(s.Dir, safeName(agent)+"-unstashed.json")
}

func safeName(v string) string {
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

func (s *Store) path(agent, session string) string {
	return filepath.Join(s.Dir, safeName(agent)+"-"+safeName(session)+".json")
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
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	// The session has taken the queued tray over; the queue is spent.
	if err := os.Remove(s.pendingPath(agent)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Load reads the tray for an agent session, then appends any tray an
// unstash queued for this agent. A missing file is an empty tray.
func (s *Store) Load(agent, session string) (*Tray, error) {
	t, err := readTray(s.path(agent, session))
	if err != nil {
		return nil, err
	}
	queued, err := readTray(s.pendingPath(agent))
	if err != nil {
		return nil, err
	}
	for _, c := range queued.Cards {
		t.Add(c)
	}
	return t, nil
}

func readTray(path string) (*Tray, error) {
	t := &Tray{}
	data, err := os.ReadFile(path)
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

func writeTray(path string, t *Tray) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Stash sets the agent's most recently saved tray aside in its one stash
// slot and empties it. It returns the number of cards stashed; an agent
// with no saved tray stashes nothing.
func (s *Store) Stash(agent string) (int, error) {
	src, err := s.newestTray(agent)
	if err != nil || src == "" {
		return 0, err
	}
	t, err := readTray(src)
	if err != nil {
		return 0, err
	}
	if t.Len() == 0 {
		return 0, nil
	}
	if err := writeTray(s.stashPath(agent), t); err != nil {
		return 0, err
	}
	if err := os.Remove(src); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	return t.Len(), nil
}

// Unstash queues the stashed tray for the agent's next session, which takes
// it over when it binds its session id. It returns the number of cards
// restored.
func (s *Store) Unstash(agent string) (int, error) {
	t, err := readTray(s.stashPath(agent))
	if err != nil {
		return 0, err
	}
	if t.Len() == 0 {
		return 0, nil
	}
	queued, err := readTray(s.pendingPath(agent))
	if err != nil {
		return 0, err
	}
	for _, c := range t.Cards {
		queued.Add(c)
	}
	if err := writeTray(s.pendingPath(agent), queued); err != nil {
		return 0, err
	}
	if err := os.Remove(s.stashPath(agent)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	return t.Len(), nil
}

// newestTray is the agent's most recently written session tray, or "" when
// it has none. The stash and the queue are not session trays.
func (s *Store) newestTray(agent string) (string, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	prefix := safeName(agent) + "-"
	newest, newestAt := "", time.Time{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		if name == filepath.Base(s.stashPath(agent)) || name == filepath.Base(s.pendingPath(agent)) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestAt) {
			newest, newestAt = filepath.Join(s.Dir, name), info.ModTime()
		}
	}
	return newest, nil
}
