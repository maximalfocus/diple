// Package blocks parses the Markdown an agent wrote into the structural
// units a note can point at: paragraphs, headings, list items, code blocks
// and their lines, diff lines, and table rows. It is deliberately a small
// line-oriented parser: coding agents emit plain CommonMark, and the model
// only needs boundaries and text, never rendering.
package blocks

import (
	"strings"
)

// Kind is the structural kind of a block.
type Kind string

// The block kinds of the domain model.
const (
	Paragraph Kind = "paragraph"
	Heading   Kind = "heading"
	ListItem  Kind = "list-item"
	CodeBlock Kind = "code-block"
	CodeLine  Kind = "code-line"
	DiffLine  Kind = "diff-line"
	TableRow  Kind = "table-row"
	ToolCall  Kind = "tool-call"
)

// Block is one structural unit of a turn.
type Block struct {
	Kind Kind
	// Text is the block's content without its Markdown markers: a heading
	// without its hashes, a list item without its bullet or number, a code
	// line as written. A code block's Text is its whole body.
	Text string
	// Ordinal is the 1-based position of a list item among its siblings,
	// so a note can say "option 2". Zero for other kinds.
	Ordinal int
	// Ordered is true for a numbered list item, whose renderer prints the
	// ordinal, and false for a bulleted one.
	Ordered bool
	// Depth is the nesting depth of a list item, 0 for a top-level item.
	Depth int
	// Parent is the index of the enclosing code block for a code or diff
	// line, and -1 otherwise.
	Parent int
	// Lang is the info string of a code block.
	Lang string
}

// Parse splits Markdown into blocks in document order.
func Parse(md string) []Block {
	p := parser{}
	md = strings.TrimSuffix(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	p.parse(strings.Split(md, "\n"))
	return p.out
}

type parser struct {
	out  []Block
	para []string
	// ordinal counters by list depth
	ordinals  []int
	lastDepth int
}

func (p *parser) flushPara() {
	if len(p.para) == 0 {
		return
	}
	text := strings.Join(p.para, " ")
	p.para = nil
	p.out = append(p.out, Block{Kind: Paragraph, Text: strings.TrimSpace(text), Parent: -1})
}

func (p *parser) parse(lines []string) {
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Fenced code block.
		if fence, lang, ok := fenceStart(trimmed); ok {
			p.flushPara()
			p.resetLists()
			var body []string
			j := i + 1
			for ; j < len(lines); j++ {
				if strings.HasPrefix(strings.TrimSpace(lines[j]), fence) {
					break
				}
				body = append(body, lines[j])
			}
			parent := len(p.out)
			p.out = append(p.out, Block{Kind: CodeBlock, Text: strings.Join(body, "\n"), Parent: -1, Lang: lang})
			kind := CodeLine
			if lang == "diff" || lang == "patch" {
				kind = DiffLine
			}
			for _, l := range body {
				p.out = append(p.out, Block{Kind: kind, Text: l, Parent: parent})
			}
			i = j
			continue
		}

		if trimmed == "" {
			p.flushPara()
			continue
		}

		// ATX heading.
		if strings.HasPrefix(trimmed, "#") {
			hashes := 0
			for hashes < len(trimmed) && trimmed[hashes] == '#' {
				hashes++
			}
			if hashes <= 6 && hashes < len(trimmed) && trimmed[hashes] == ' ' {
				p.flushPara()
				p.resetLists()
				text := strings.TrimSpace(strings.TrimRight(trimmed[hashes:], "#"))
				p.out = append(p.out, Block{Kind: Heading, Text: strings.TrimSpace(text), Parent: -1})
				continue
			}
		}

		// Horizontal rule.
		if isRule(trimmed) {
			p.flushPara()
			p.resetLists()
			continue
		}

		// Table row.
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			p.flushPara()
			p.resetLists()
			if isTableSeparator(trimmed) {
				continue
			}
			p.out = append(p.out, Block{Kind: TableRow, Text: trimmed, Parent: -1})
			continue
		}

		// List item.
		if marker, text, ok := listItem(line); ok {
			p.flushPara()
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			depth := indent / 2
			if depth > 8 {
				depth = 8
			}
			for len(p.ordinals) <= depth {
				p.ordinals = append(p.ordinals, 0)
			}
			if depth < p.lastDepth {
				p.ordinals = p.ordinals[:depth+1]
			}
			p.ordinals[depth]++
			p.lastDepth = depth
			ordered := marker[0] >= '0' && marker[0] <= '9'
			b := Block{Kind: ListItem, Text: strings.TrimSpace(text), Ordinal: p.ordinals[depth], Ordered: ordered, Depth: depth, Parent: -1}
			// Continuation lines that are indented and not themselves items
			// belong to this item.
			for i+1 < len(lines) {
				next := lines[i+1]
				nt := strings.TrimSpace(next)
				if nt == "" {
					break
				}
				if _, _, isItem := listItem(next); isItem {
					break
				}
				if _, _, isFence := fenceStart(nt); isFence || strings.HasPrefix(nt, "#") || isRule(nt) {
					break
				}
				nextIndent := len(next) - len(strings.TrimLeft(next, " \t"))
				if nextIndent <= indent {
					break
				}
				b.Text += " " + nt
				i++
			}
			p.out = append(p.out, b)
			continue
		}

		// Blockquote: treat the quoted text as a paragraph line.
		if strings.HasPrefix(trimmed, ">") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
		}
		p.resetLists()
		p.para = append(p.para, trimmed)
	}
	p.flushPara()
}

func (p *parser) resetLists() {
	p.ordinals = p.ordinals[:0]
	p.lastDepth = 0
}

func fenceStart(trimmed string) (fence, lang string, ok bool) {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, f) {
			n := len(f)
			for n < len(trimmed) && trimmed[n] == f[0] {
				n++
			}
			lang := ""
			if fields := strings.Fields(trimmed[n:]); len(fields) > 0 {
				lang = fields[0]
			}
			return trimmed[:n], lang, true
		}
	}
	return "", "", false
}

func isRule(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	c := trimmed[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for _, r := range trimmed {
		if r != rune(c) && r != ' ' {
			return false
		}
	}
	return true
}

func isTableSeparator(trimmed string) bool {
	for _, r := range trimmed {
		if r != '|' && r != '-' && r != ':' && r != ' ' {
			return false
		}
	}
	return true
}

// listItem recognises "- x", "* x", "+ x", "1. x", and "1) x".
func listItem(line string) (marker, text string, ok bool) {
	t := strings.TrimLeft(line, " \t")
	if len(t) >= 2 && (t[0] == '-' || t[0] == '*' || t[0] == '+') && (t[1] == ' ' || t[1] == '\t') {
		return t[:1], t[2:], true
	}
	n := 0
	for n < len(t) && n < 9 && t[n] >= '0' && t[n] <= '9' {
		n++
	}
	if n > 0 && n+1 < len(t) && (t[n] == '.' || t[n] == ')') && (t[n+1] == ' ' || t[n+1] == '\t') {
		return t[:n+1], t[n+2:], true
	}
	return "", "", false
}
