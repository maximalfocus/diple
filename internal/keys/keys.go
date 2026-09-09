// Package keys is Diple's binding table: the named gestures the session
// owns, the key each one answers to, and the user's file that changes them.
// A binding is one key — a character, or one of a few named keys, optionally
// with Alt — never a chord or a sequence.
package keys

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Action is one gesture the session owns.
type Action string

// The complete set of gestures. Every mouse gesture has one here, so the
// keyboard can do everything the mouse can.
const (
	Send        Action = "send"           // compile the tray and submit it
	Paste       Action = "paste"          // compile the tray without submitting
	FreeCard    Action = "free-card"      // offer the free card kinds
	Search      Action = "search"         // search the transcript
	NextTurn    Action = "next-turn"      // move the view to the next turn
	PrevTurn    Action = "prev-turn"      // move the view to the previous turn
	SelectBlock Action = "select-block"   // select the topmost block in view
	NextBlock   Action = "next-block"     // select the next block or code line
	PrevBlock   Action = "prev-block"     // select the previous block or code line
	SelectLine  Action = "select-line"    // select a code or diff line
	ExtendLine  Action = "extend-line"    // extend a line range
	Span        Action = "span"           // start a span inside the block
	ExtendChar  Action = "extend-char"    // grow the span by a character
	ShrinkChar  Action = "shrink-char"    // shrink the span by a character
	ExtendWord  Action = "extend-word"    // grow the span by a word
	ShrinkWord  Action = "shrink-word"    // shrink the span by a word
	TrayFocus   Action = "tray-focus"     // move focus between box and tray
	TrayNext    Action = "tray-next"      // select the next card
	TrayPrev    Action = "tray-prev"      // select the previous card
	TrayMoveUp  Action = "tray-move-up"   // reorder the card up
	TrayMoveDn  Action = "tray-move-down" // reorder the card down
	TrayEdit    Action = "tray-edit"      // edit the selected card
	TrayDelete  Action = "tray-delete"    // delete the selected card
	TrayNew     Action = "tray-new"       // offer the free card kinds from the tray
	Cancel      Action = "cancel"         // dismiss what Diple is showing
)

// Actions lists every action in table order.
var Actions = []Action{
	Send, Paste, FreeCard, Search, NextTurn, PrevTurn,
	SelectBlock, NextBlock, PrevBlock, SelectLine, ExtendLine,
	Span, ExtendChar, ShrinkChar, ExtendWord, ShrinkWord,
	TrayFocus, TrayNext, TrayPrev, TrayMoveUp, TrayMoveDn, TrayEdit, TrayDelete, TrayNew,
	Cancel,
}

// Key is one binding: a rune, or a named key, with or without Alt. Alt is
// what a terminal sends as ESC followed by the key.
type Key struct {
	Rune rune
	Alt  bool
}

// Named keys a binding may use, beyond ordinary characters.
var named = map[string]rune{
	"enter": '\r',
	"esc":   0x1b,
	"tab":   '\t',
	"space": ' ',
}

// String renders a key the way the file writes it.
func (k Key) String() string {
	name := string(k.Rune)
	for n, r := range named {
		if r == k.Rune {
			name = n
		}
	}
	if k.Alt {
		return "alt+" + name
	}
	return name
}

// ParseKey reads one key as the file writes it.
func ParseKey(s string) (Key, error) {
	s = strings.TrimSpace(s)
	k := Key{}
	if rest, ok := cutPrefixFold(s, "alt+"); ok {
		k.Alt, s = true, strings.TrimSpace(rest)
	}
	if r, ok := named[strings.ToLower(s)]; ok {
		k.Rune = r
		return k, nil
	}
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 || size != len(s) || r == utf8.RuneError {
		return Key{}, fmt.Errorf("keys: %q is not a single key", s)
	}
	k.Rune = r
	return k, nil
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// Table maps each action to the key that triggers it.
type Table map[Action]Key

// Defaults is the table a user gets without a file: the gestures the
// earlier slices established, plus the keyboard equivalents.
func Defaults() Table {
	return Table{
		Send:        {Rune: '\r', Alt: true},
		Paste:       {Rune: 'p', Alt: true},
		FreeCard:    {Rune: 'n', Alt: true},
		SelectBlock: {Rune: 'k', Alt: true},
		Search:      {Rune: '/'},
		NextTurn:    {Rune: ']'},
		PrevTurn:    {Rune: '['},
		NextBlock:   {Rune: 'j'},
		PrevBlock:   {Rune: 'k'},
		SelectLine:  {Rune: 'L'},
		ExtendLine:  {Rune: 'V'},
		Span:        {Rune: 'v'},
		ExtendChar:  {Rune: 'l'},
		ShrinkChar:  {Rune: 'h'},
		ExtendWord:  {Rune: 'w'},
		ShrinkWord:  {Rune: 'b'},
		TrayFocus:   {Rune: '\t'},
		TrayNext:    {Rune: 'j'},
		TrayPrev:    {Rune: 'k'},
		TrayMoveUp:  {Rune: 'K'},
		TrayMoveDn:  {Rune: 'J'},
		TrayEdit:    {Rune: 'e'},
		TrayDelete:  {Rune: 'd'},
		TrayNew:     {Rune: '+'},
		Cancel:      {Rune: 0x1b},
	}
}

// Key returns the key bound to an action.
func (t Table) Key(a Action) Key { return t[a] }

// Is reports whether k is the key bound to a.
func (t Table) Is(a Action, k Key) bool {
	bound, ok := t[a]
	return ok && bound == k
}

// DefaultPath is the binding file: $XDG_CONFIG_HOME/diple/bindings.conf,
// else ~/.config/diple/bindings.conf.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "diple", "bindings.conf"), nil
}

// Load reads the user's bindings over the defaults. A missing file is not an
// error. Every problem is returned as a complaint rather than a failure: a
// bad line is reported and skipped, because a config file must never cost
// the user their session.
func Load(path string) (Table, []string) {
	t := Defaults()
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return t, []string{fmt.Sprintf("bindings: %v", err)}
		}
		return t, nil
	}
	defer f.Close()
	complaints := Read(f, t)
	return t, complaints
}

// Read applies `action = key` lines to a table, in place.
func Read(r io.Reader, t Table) []string {
	var complaints []string
	sc := bufio.NewScanner(r)
	seen := map[Action]bool{}
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			complaints = append(complaints, fmt.Sprintf("bindings: line %d is not `action = key`: %s", n, line))
			continue
		}
		a := Action(strings.TrimSpace(name))
		if _, known := t[a]; !known {
			complaints = append(complaints, fmt.Sprintf("bindings: line %d: unknown action %q", n, a))
			continue
		}
		k, err := ParseKey(value)
		if err != nil {
			complaints = append(complaints, fmt.Sprintf("bindings: line %d: %v", n, err))
			continue
		}
		if seen[a] {
			complaints = append(complaints, fmt.Sprintf("bindings: line %d: %s is bound twice; keeping the first", n, a))
			continue
		}
		seen[a] = true
		t[a] = k
	}
	if err := sc.Err(); err != nil {
		complaints = append(complaints, fmt.Sprintf("bindings: %v", err))
	}
	return complaints
}

// String renders the whole table, one `action = key` line per action.
func (t Table) String() string {
	var b strings.Builder
	width := 0
	for _, a := range Actions {
		if len(a) > width {
			width = len(a)
		}
	}
	for _, a := range Actions {
		fmt.Fprintf(&b, "%-*s = %s\n", width, a, t[a])
	}
	return b.String()
}
