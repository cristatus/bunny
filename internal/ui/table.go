package ui

import (
	"strings"
	"unicode/utf8"
)

// Cell is one table cell. Style colors the text; Right right-aligns it.
type Cell struct {
	Text  string
	Style Style
	Right bool
}

// dispWidth is a cell's display width in columns. Rune count (not byte length)
// so 1-column multibyte glyphs like "→" align correctly.
func dispWidth(s string) int { return utf8.RuneCountInString(s) }

func padText(s string, w int, right bool) string {
	gap := w - dispWidth(s)
	if gap <= 0 {
		return s
	}
	if right {
		return strings.Repeat(" ", gap) + s
	}
	return s + strings.Repeat(" ", gap)
}

// Table renders a header + rows, columns sized to the widest cell, joined by a
// two-space gutter. The header is plain (no color). Trailing empty cells are
// dropped and the final cell is never right-padded, so lines carry no trailing
// whitespace. Coloring is applied to the padded text.
func (p *Printer) Table(header []string, rows [][]Cell) string {
	cols := len(header)
	widths := make([]int, cols)
	for i, h := range header {
		widths[i] = dispWidth(h)
	}
	for _, r := range rows {
		for i := 0; i < cols && i < len(r); i++ {
			if l := dispWidth(r[i].Text); l > widths[i] {
				widths[i] = l
			}
		}
	}

	var b strings.Builder
	// header — dimmed so the data rows are the brightest thing to scan.
	last := lastNonEmptyHeader(header)
	for i := 0; i <= last; i++ {
		if i > 0 {
			b.WriteString("  ")
		}
		text := header[i]
		if i != last {
			text = padText(header[i], widths[i], false)
		}
		b.WriteString(p.paint(text, Faint))
	}
	b.WriteByte('\n')

	for _, r := range rows {
		rl := lastNonEmptyCell(r)
		if rl < 0 {
			b.WriteByte('\n')
			continue
		}
		for i := 0; i <= rl; i++ {
			if i > 0 {
				b.WriteString("  ")
			}
			c := Cell{}
			if i < len(r) {
				c = r[i]
			}
			w := widths[i]
			if i == rl && !c.Right {
				w = 0 // final column: no trailing pad
			}
			b.WriteString(p.paint(padText(c.Text, w, c.Right), c.Style))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func lastNonEmptyHeader(header []string) int {
	last := 0
	for i, h := range header {
		if h != "" {
			last = i
		}
	}
	return last
}

func lastNonEmptyCell(r []Cell) int {
	last := -1
	for i := range r {
		if r[i].Text != "" {
			last = i
		}
	}
	return last
}

// KVRow is one aligned key/value line for detail output.
type KVRow struct{ Key, Value string }

// KV renders aligned key/value lines. Keys are bold and right-aligned so their
// colons line up: "     Version: 1.2.0".
func (p *Printer) KV(rows []KVRow) string {
	kw := 0
	for _, r := range rows {
		if dispWidth(r.Key) > kw {
			kw = dispWidth(r.Key)
		}
	}
	var b strings.Builder
	for _, r := range rows {
		lead := strings.Repeat(" ", kw-dispWidth(r.Key)) // right-align the key
		b.WriteString(lead)
		b.WriteString(p.paint(r.Key+":", Bold))
		b.WriteString(" ")
		b.WriteString(r.Value)
		b.WriteByte('\n')
	}
	return b.String()
}
