// Package progress renders install/uninstall/update progress. Callers drive a
// per-package Reporter through phases. On a terminal it animates a single line
// for the active package in place (spinner + phase, or a download byte bar) and
// commits each finished package as static text; on a pipe only the final line
// per package is printed, keeping piped output clean.
package progress

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// barWidth is the character width of the download progress bar.
const barWidth = 20

// Reporter receives per-package install progress. Begin primes the batch and
// returns a context the UI can cancel (Ctrl+C); a package then moves Start →
// Phase/Download… → Done|Skip|Fail; Close tears down the UI at the end.
type Reporter interface {
	Begin(parent context.Context, ids []string) context.Context
	Start(pkg string)
	Phase(pkg, name string)
	Download(pkg string, done, total int64)
	Done(pkg, status, version string)
	Skip(pkg, status, version string)
	Fail(pkg string, err error)
	Close()
}

// New returns a TTY-aware Reporter: an animated single line on a terminal,
// plain final-lines-only otherwise.
func New(w io.Writer) Reporter {
	if isTTY(w) {
		return newLive(w)
	}
	return &plainReporter{w: w}
}

// NewPlain always prints final lines only (used by --no-progress and tests).
func NewPlain(w io.Writer) Reporter { return &plainReporter{w: w} }

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// termWidth returns the writer's terminal column count, or 0 when unknown.
func termWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		return 0
	}
	return int(ws.Col)
}

// mib formats a byte count as a MiB value (binary, matching mise).
func mib(n int64) float64 { return float64(n) / (1024 * 1024) }

// maxLen is the length of the longest string in ids.
func maxLen(ids []string) int {
	w := 0
	for _, id := range ids {
		if len(id) > w {
			w = len(id)
		}
	}
	return w
}

// finalLine is the resolved line for a completed package: "<id>   <status>
// <version>", used by the plain reporter.
func finalLine(pkg, status, version string, idWidth int) string {
	s := finalLinePad(pkg, idWidth) + "   " + status
	if version != "" {
		s += "   " + version
	}
	return s
}

// plainReporter prints only the resolved line for each package; phase/download
// updates are dropped so piped output stays clean.
type plainReporter struct {
	w       io.Writer
	idWidth int
}

func (r *plainReporter) Begin(parent context.Context, ids []string) context.Context {
	r.idWidth = maxLen(ids)
	fmt.Fprintln(r.w) // blank line above the list, matching the summary's below
	return parent
}
func (r *plainReporter) Start(pkg string)                       {}
func (r *plainReporter) Phase(pkg, name string)                 {}
func (r *plainReporter) Download(pkg string, done, total int64) {}
func (r *plainReporter) Close()                                 {}
func (r *plainReporter) Done(pkg, status, version string) {
	fmt.Fprintln(r.w, finalLine(pkg, status, version, r.idWidth))
}
func (r *plainReporter) Skip(pkg, status, version string) {
	line := finalLinePad(pkg, r.idWidth) + "   " + status
	if version != "" {
		line += "   " + version
	}
	fmt.Fprintln(r.w, line)
}
func (r *plainReporter) Fail(pkg string, err error) {
	fmt.Fprintf(r.w, "%s   failed: %s\n", finalLinePad(pkg, r.idWidth), err)
}

func finalLinePad(pkg string, idWidth int) string {
	if pad := idWidth - len(pkg); pad > 0 {
		return pkg + strings.Repeat(" ", pad)
	}
	return pkg
}

// Status is a transient single-line activity indicator for phases whose result
// is rendered separately (e.g. the update check, which ends in a table). On a
// TTY it rewrites one line in place and erases it on Clear; off a TTY it is a
// no-op, so piped output stays clean.
type Status struct {
	w      io.Writer
	tty    bool
	active bool
}

// NewStatus returns a transient status line bound to w.
func NewStatus(w io.Writer) *Status { return &Status{w: w, tty: isTTY(w)} }

// Update replaces the current status line with msg (no-op when not a TTY).
func (s *Status) Update(msg string) {
	if !s.tty {
		return
	}
	fmt.Fprintf(s.w, "\r\x1b[K%s", msg)
	s.active = true
}

// Clear erases the status line if one is showing.
func (s *Status) Clear() {
	if !s.tty || !s.active {
		return
	}
	fmt.Fprint(s.w, "\r\x1b[K")
	s.active = false
}

// bar renders a fixed-width filled/empty progress bar.
func bar(done, total int64, width int) string {
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	filled := int(int64(width) * done / total)
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// pct returns the integer percentage of done/total, clamped to 0..100.
func pct(done, total int64) int {
	if total <= 0 {
		return 0
	}
	p := int(done * 100 / total)
	if p > 100 {
		return 100
	}
	if p < 0 {
		return 0
	}
	return p
}
