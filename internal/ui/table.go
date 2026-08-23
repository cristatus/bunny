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

// minFitColumn is the narrowest a column is worth shrinking to. Below it the
// text is clipped past being readable, so a wrapped line is the lesser evil.
const minFitColumn = 24

// fitWidth returns the width the final column must shrink to for a table of
// the given column widths to fit a terminal of term columns. It returns 0 when
// nothing should be clipped: the table already fits, term is 0 (not a terminal,
// or a width it cannot report), or too little room is left for clipped text to
// still read.
func fitWidth(term int, widths []int) int {
	if len(widths) < 2 {
		return 0
	}
	room := term
	for _, c := range widths[:len(widths)-1] {
		room -= c + 2 // the column, plus the gutter that follows it
	}
	if room < minFitColumn || room >= widths[len(widths)-1] {
		return 0
	}
	return room
}

// clip shortens s to w columns, marking the cut with an ellipsis.
func clip(s string, w int) string {
	if w <= 0 || dispWidth(s) <= w {
		return s
	}
	return strings.TrimRight(string([]rune(s)[:w-1]), " ") + "…"
}

// Table renders a header + rows, columns sized to the widest cell, joined by a
// two-space gutter. The header is plain (no color). Trailing empty cells are
// dropped and the final cell is never right-padded, so lines carry no trailing
// whitespace. Coloring is applied to the padded text.
//
// A final column of free text is clipped to what the terminal has left, so one
// long description cannot wrap every row. Piped output is never clipped: a
// script reading it wants the whole text, not the layout.
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

	// fitCol is the column that gives up room when the table is too wide.
	fitCol := -1
	if room := fitWidth(TermWidth(p.w), widths); room > 0 {
		fitCol, widths[cols-1] = cols-1, room
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
			text := c.Text
			if i == fitCol {
				text = clip(text, widths[i])
			}
			w := widths[i]
			if i == rl && !c.Right {
				w = 0 // final column: no trailing pad
			}
			b.WriteString(p.paint(padText(text, w, c.Right), c.Style))
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
		lead := strings.Repeat(" ", kw-dispWidth(r.Key))
		b.WriteString(lead)
		b.WriteString(p.paint(r.Key+":", Bold))
		b.WriteString(" ")
		b.WriteString(r.Value)
		b.WriteByte('\n')
	}
	return b.String()
}
