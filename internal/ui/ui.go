// Package ui renders bunny's static, user-facing result output on stdout.
// It is deliberately spartan: two semantic colors (green good, red bad) plus a
// faint style, emitted only to a color-capable TTY. Piped or NO_COLOR output is
// plain text with zero escape sequences.
package ui

import (
	"fmt"
	"io"
	"os"
)

// Style selects one of bunny's few color roles.
type Style int

const (
	Plain Style = iota // default terminal foreground
	Good               // green — installed, active, passed
	Bad                // red — failed, error
	Faint              // dim — genuinely secondary text
	Bold               // bold — emphasis (e.g. detail keys); an attribute, not a color
)

const (
	ansiReset = "\x1b[0m"
	ansiGreen = "\x1b[32m"
	ansiRed   = "\x1b[31m"
	ansiDim   = "\x1b[2m"
	ansiBold  = "\x1b[1m"
)

// Printer writes result output to a bound writer, coloring only when the writer
// is a color-capable terminal.
type Printer struct {
	w     io.Writer
	color bool
}

// New binds a Printer to w, auto-detecting whether to emit color.
func New(w io.Writer) *Printer { return &Printer{w: w, color: colorEnabled(w)} }

// NewWithColor binds a Printer with color forced on or off (used in tests).
func NewWithColor(w io.Writer, color bool) *Printer { return &Printer{w: w, color: color} }

// colorEnabled reports whether ANSI color should be emitted to w: w must be a
// character device, NO_COLOR must be unset, and TERM must not be "dumb".
func colorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (p *Printer) paint(s string, st Style) string {
	if !p.color || st == Plain || s == "" {
		return s
	}
	switch st {
	case Good:
		return ansiGreen + s + ansiReset
	case Bad:
		return ansiRed + s + ansiReset
	case Faint:
		return ansiDim + s + ansiReset
	case Bold:
		return ansiBold + s + ansiReset
	default:
		return s
	}
}

func (p *Printer) Print(a ...any)                 { fmt.Fprint(p.w, a...) }
func (p *Printer) Println(a ...any)               { fmt.Fprintln(p.w, a...) }
func (p *Printer) Printf(format string, a ...any) { fmt.Fprintf(p.w, format, a...) }

// PaintStatus styles a short status token (installed/failed/etc).
func (p *Printer) PaintStatus(s string, st Style) string { return p.paint(s, st) }

// Sym colors a status glyph with the given style.
func (p *Printer) Sym(glyph string, st Style) string { return p.paint(glyph, st) }

// Faint styles s as secondary text.
func (p *Printer) Faint(s string) string { return p.paint(s, Faint) }

// Fatal prints a clean, un-timestamped error to the Printer's writer and exits.
func (p *Printer) Fatal(err error) {
	fmt.Fprintln(p.w, p.paint("error: ", Bad)+err.Error())
	os.Exit(1)
}

// Fatal is the package-level convenience: report err on stderr and exit 1.
func Fatal(err error) { New(os.Stderr).Fatal(err) }
