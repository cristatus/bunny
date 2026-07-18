package progress

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	ansiReset = "\x1b[0m"
	ansiGreen = "\x1b[32m"
	ansiRed   = "\x1b[31m"
	ansiDim   = "\x1b[2m"

	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// knownStatuses is every status label the UI shows. The status column is sized
// to the widest of these so the trailing column (bar/version) aligns and no
// status overflows. Keep in sync with the phase names and skip reasons callers
// pass.
var knownStatuses = []string{
	"preparing", "downloading", "extracting", "installing",
	"installed", "removed", "updated", "pending", "failed",
	"missing", "changed", "stale",
}

var statusColWidth = maxLen(knownStatuses)

// liveReporter animates a single line for the active package via in-place \r
// rewrites; each completed package is finalized as ordinary static text. Only
// the one active line is ever redrawn, so terminal reflow/resize can't corrupt
// the settled history above it. A ticker advances the spinner.
type liveReporter struct {
	w       io.Writer
	color   bool
	idWidth int

	mu     sync.Mutex
	width  int // refreshed each tick so the active line tracks resizes
	stop   chan struct{}
	active bool
	pkg    string
	phase  string
	done   int64
	total  int64
	hasBar bool
	frame  int
}

func newLive(w io.Writer) *liveReporter {
	return &liveReporter{w: w, color: os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"}
}

func (r *liveReporter) Begin(parent context.Context, ids []string) context.Context {
	r.idWidth = maxLen(ids)
	r.width = termWidth(r.w)
	r.stop = make(chan struct{})
	fmt.Fprint(r.w, hideCursor) // stop the blinking cursor flashing/jumping over redraws
	r.watchSignals()            // restore the cursor if the process is interrupted
	fmt.Fprintln(r.w)           // blank line above the list, matching the summary's below
	go r.tick()
	return parent
}

// watchSignals restores the cursor before a SIGINT/SIGTERM kills the process, so
// an interrupted install never leaves the terminal cursor hidden.
func (r *liveReporter) watchSignals() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-r.stop:
			signal.Stop(sig)
		case <-sig:
			fmt.Fprint(r.w, showCursor)
			os.Exit(130)
		}
	}()
}

func (r *liveReporter) tick() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			r.mu.Lock()
			r.width = termWidth(r.w) // pick up resizes
			if r.active {
				r.frame = (r.frame + 1) % len(spinnerFrames) // keep spinning, even during download
				r.redrawLocked()
			}
			r.mu.Unlock()
		}
	}
}

func (r *liveReporter) Start(pkg string) {
	r.mu.Lock()
	r.pkg, r.phase, r.done, r.total, r.hasBar, r.active = pkg, "preparing", 0, 0, false, true
	r.redrawLocked()
	r.mu.Unlock()
}

func (r *liveReporter) Phase(pkg, name string) {
	r.mu.Lock()
	r.phase, r.hasBar, r.done, r.total = name, false, 0, 0
	r.redrawLocked()
	r.mu.Unlock()
}

func (r *liveReporter) Download(pkg string, done, total int64) {
	r.mu.Lock()
	r.phase, r.done, r.total, r.hasBar = "downloading", done, total, total > 0
	r.redrawLocked()
	r.mu.Unlock()
}

func (r *liveReporter) Done(pkg, status, version string) {
	r.finish(r.compose(pkg, "✓", ansiGreen, status, version))
}

// Skip finalizes a package that was not acted on (e.g. already installed, or an
// update skipped for a reason). The whole line is dimmed with a "·" indicator to
// recede behind the bright, freshly-acted-on packages; a version is shown when
// one is known.
func (r *liveReporter) Skip(pkg, status, version string) {
	s := status
	trailing := ""
	if version != "" {
		s = padStatus(status)
		trailing = "  " + version
	}
	line := finalLinePad(pkg, r.idWidth) + "  · " + s + trailing
	r.finish(r.fit(r.dimLine(line)))
}

// dimLine dims an entire (uncolored) line when color is enabled.
func (r *liveReporter) dimLine(s string) string {
	if !r.color {
		return s
	}
	return ansiDim + s + ansiReset
}

func (r *liveReporter) Fail(pkg string, err error) {
	line := finalLinePad(pkg, r.idWidth) + "  " + r.paint(ansiRed, "✗") + " failed: " + err.Error()
	r.finish(r.fit(line))
}

func (r *liveReporter) Close() {
	if r.stop != nil {
		close(r.stop)
	}
	fmt.Fprint(r.w, showCursor)
}

// finish clears the animated line and writes the settled line, committing it as
// static text so subsequent output starts on a fresh line.
func (r *liveReporter) finish(line string) {
	r.mu.Lock()
	fmt.Fprintf(r.w, "\r\x1b[K%s\n", line)
	r.active = false
	r.mu.Unlock()
}

func (r *liveReporter) redrawLocked() { fmt.Fprintf(r.w, "\r\x1b[K%s", r.activeLineLocked()) }

func (r *liveReporter) activeLineLocked() string {
	sp := spinnerFrames[r.frame]
	switch {
	case r.hasBar:
		trailing := fmt.Sprintf("%s  %3d%%   %s", bar(r.done, r.total, barWidth),
			pct(r.done, r.total), r.paint(ansiDim, fmt.Sprintf("%.1f/%.1f MiB", mib(r.done), mib(r.total))))
		return r.compose(r.pkg, sp, "", "downloading", trailing)
	case r.phase == "downloading" && r.done > 0:
		return r.compose(r.pkg, sp, "", "downloading", r.paint(ansiDim, fmt.Sprintf("%.1f MiB", mib(r.done))))
	default:
		return r.compose(r.pkg, sp, "", r.phase, "")
	}
}

// compose builds a fixed-column line: "<id>  <ind> <status>  <trailing>". The
// status is padded to the known column width only when a trailing value
// follows, so lines without one carry no trailing whitespace.
func (r *liveReporter) compose(pkg, ind, indColor, status, trailing string) string {
	s := status
	if trailing != "" {
		s = padStatus(status)
	}
	line := finalLinePad(pkg, r.idWidth) + "  " + r.paint(indColor, ind) + " " + s
	if trailing != "" {
		line += "  " + trailing
	}
	return r.fit(line)
}

func (r *liveReporter) paint(color, s string) string {
	if !r.color || color == "" || s == "" {
		return s
	}
	return color + s + ansiReset
}

// fit truncates a line to one column short of the terminal width so the active
// line never wraps (which would break the in-place \r rewrite). ANSI escapes
// are preserved and don't count toward the width.
func (r *liveReporter) fit(s string) string {
	max := r.width - 1
	if max <= 0 {
		return s
	}
	var b strings.Builder
	visible, inEsc, truncated := 0, false, false
	for _, c := range s {
		if inEsc {
			b.WriteRune(c)
			if c == 'm' {
				inEsc = false
			}
			continue
		}
		if c == '\x1b' {
			inEsc = true
			b.WriteRune(c)
			continue
		}
		if visible >= max {
			truncated = true
			break
		}
		b.WriteRune(c)
		visible++
	}
	if truncated && r.color {
		b.WriteString(ansiReset) // guard against a severed color run
	}
	return b.String()
}

func padStatus(s string) string {
	if len(s) >= statusColWidth {
		return s
	}
	return s + strings.Repeat(" ", statusColWidth-len(s))
}
