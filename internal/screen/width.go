package screen

import (
	"unicode"
	"unicode/utf8"
)

// runeWidth returns the number of columns r occupies: 0 for combining and
// zero-width runes, 2 for East Asian wide and fullwidth runes and emoji
// presentation, and 1 otherwise. It is deliberately a small, dependency-free
// table: the model only needs the same answer the host terminal gives for the
// runes coding agents actually print.
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 1
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return 0
	case r == 0x200b || r == 0x200c || r == 0x200d || r == 0x2060 || r == 0xfeff:
		return 0
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r):
		return 0
	case r >= 0xfe00 && r <= 0xfe0f: // variation selectors
		return 0
	case r >= 0x1100 && r <= 0x115f,
		r >= 0x2e80 && r <= 0x303e,
		r >= 0x3041 && r <= 0x33ff,
		r >= 0x3400 && r <= 0x4dbf,
		r >= 0x4e00 && r <= 0x9fff,
		r >= 0xa000 && r <= 0xa4cf,
		r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe30 && r <= 0xfe4f,
		r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1f64f,
		r >= 0x1f680 && r <= 0x1f6ff,
		r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x1fa70 && r <= 0x1faff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

func encodeRune(p []byte, r rune) int { return utf8.EncodeRune(p, r) }
