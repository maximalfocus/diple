// Package screen keeps an in-memory VT screen model of a wrapped process:
// every cell with the character attributes it was drawn with, a complete
// scrollback, the alternate screen, and the cursor. Rows can be re-emitted
// with their original attributes, which is what Diple draws when it shows
// scrollback or returns to the live screen.
package screen

import (
	"strconv"
	"strings"
)

// ColorKind distinguishes the three colour forms SGR can select.
type ColorKind uint8

const (
	// ColorDefault is the terminal's default foreground or background.
	ColorDefault ColorKind = iota
	// ColorIndexed is one of the 256 indexed colours.
	ColorIndexed
	// ColorRGB is a 24-bit colour.
	ColorRGB
)

// Color is a foreground or background colour.
type Color struct {
	Kind    ColorKind
	Index   uint8
	R, G, B uint8
}

// Flags are the boolean SGR attributes.
type Flags uint16

// The SGR attribute bits.
const (
	Bold Flags = 1 << iota
	Dim
	Italic
	Underline
	Blink
	Reverse
	Hidden
	Strike
)

// Attr is the full set of character attributes a cell was drawn with.
type Attr struct {
	FG, BG Color
	Flags  Flags
}

// IsDefault reports whether a is the reset attribute.
func (a Attr) IsDefault() bool { return a == Attr{} }

// SGR returns the escape sequence that selects a starting from a reset state.
func (a Attr) SGR() string {
	var b strings.Builder
	b.WriteString("\x1b[0")
	for bit, code := range flagCodes {
		if a.Flags&bit != 0 {
			b.WriteByte(';')
			b.WriteString(strconv.Itoa(code))
		}
	}
	writeColor(&b, a.FG, 30)
	writeColor(&b, a.BG, 40)
	b.WriteByte('m')
	return b.String()
}

var flagCodes = map[Flags]int{
	Bold: 1, Dim: 2, Italic: 3, Underline: 4, Blink: 5, Reverse: 7, Hidden: 8, Strike: 9,
}

func writeColor(b *strings.Builder, c Color, base int) {
	switch c.Kind {
	case ColorDefault:
		return
	case ColorIndexed:
		switch {
		case c.Index < 8:
			b.WriteByte(';')
			b.WriteString(strconv.Itoa(base + int(c.Index)))
		case c.Index < 16:
			b.WriteByte(';')
			b.WriteString(strconv.Itoa(base + 60 + int(c.Index) - 8))
		default:
			b.WriteByte(';')
			b.WriteString(strconv.Itoa(base + 8))
			b.WriteString(";5;")
			b.WriteString(strconv.Itoa(int(c.Index)))
		}
	case ColorRGB:
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(base + 8))
		b.WriteString(";2;")
		b.WriteString(strconv.Itoa(int(c.R)))
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(int(c.G)))
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(int(c.B)))
	}
}

// Cell is one screen position. Width is 1 for an ordinary rune, 2 for the
// leading half of a wide rune, and 0 for the trailing half, which carries no
// rune of its own.
type Cell struct {
	Rune  rune
	Extra string // combining runes drawn onto this cell, UTF-8 encoded
	Width uint8
	Attr  Attr
}

// Blank returns an empty cell drawn with attr.
func Blank(attr Attr) Cell { return Cell{Rune: ' ', Width: 1, Attr: attr} }

// Equal compares two cells rune for rune and attribute for attribute.
func (c Cell) Equal(o Cell) bool { return c == o }

// isBlank reports whether c is an empty cell in the reset attribute.
func (c Cell) isBlank() bool {
	return c.Rune == ' ' && c.Width == 1 && c.Extra == "" && c.Attr.IsDefault()
}

// Line is one screen row. Wrapped records that the cursor auto-wrapped from
// this row onto the next, so the two are one logical line.
type Line struct {
	Cells   []Cell
	Wrapped bool
}

func newLine(cols int, attr Attr) Line {
	cells := make([]Cell, cols)
	for i := range cells {
		cells[i] = Blank(attr)
	}
	return Line{Cells: cells}
}

// Clone returns a copy of l padded with blank cells to cols columns; a row
// kept in scrollback is stored trimmed of trailing blanks.
func (l Line) Clone(cols int) Line {
	n := len(l.Cells)
	if cols > n {
		n = cols
	}
	cells := make([]Cell, n)
	copy(cells, l.Cells)
	for i := len(l.Cells); i < n; i++ {
		cells[i] = Blank(Attr{})
	}
	return Line{Cells: cells, Wrapped: l.Wrapped}
}

// trimmed returns l without its trailing blank cells, sharing no storage.
func (l Line) trimmed() Line {
	n := len(l.Cells)
	for n > 0 && l.Cells[n-1].isBlank() {
		n--
	}
	cells := make([]Cell, n)
	copy(cells, l.Cells[:n])
	return Line{Cells: cells, Wrapped: l.Wrapped}
}

// Equal compares two rows cell for cell. Trailing blank cells do not count,
// so a trimmed scrollback row equals its padded form.
func (l Line) Equal(o Line) bool {
	if l.Wrapped != o.Wrapped {
		return false
	}
	a, b := l.Cells, o.Cells
	for len(a) > 0 && a[len(a)-1].isBlank() {
		a = a[:len(a)-1]
	}
	for len(b) > 0 && b[len(b)-1].isBlank() {
		b = b[:len(b)-1]
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// String returns the row's text with trailing blanks trimmed.
func (l Line) String() string {
	var b strings.Builder
	for _, c := range l.Cells {
		if c.Width == 0 {
			continue
		}
		b.WriteRune(c.Rune)
		b.WriteString(c.Extra)
	}
	return strings.TrimRight(b.String(), " ")
}

// AppendEmit appends the bytes that redraw l with its original attributes,
// starting from and ending in the reset attribute. The cursor is assumed to
// be at the row's first column.
func (l Line) AppendEmit(buf []byte) []byte {
	cur := Attr{}
	buf = append(buf, "\x1b[0m"...)
	for _, c := range l.Cells {
		if c.Width == 0 {
			continue
		}
		if c.Attr != cur {
			buf = append(buf, c.Attr.SGR()...)
			cur = c.Attr
		}
		buf = appendRune(buf, c.Rune)
		buf = append(buf, c.Extra...)
	}
	if !cur.IsDefault() {
		buf = append(buf, "\x1b[0m"...)
	}
	return buf
}

func appendRune(buf []byte, r rune) []byte {
	if r == 0 {
		r = ' '
	}
	var tmp [4]byte
	n := encodeRune(tmp[:], r)
	return append(buf, tmp[:n]...)
}
