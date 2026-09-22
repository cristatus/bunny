package progress

import (
	"io"
	"sync"
)

// transient is a line redrawn in place at the bottom of the terminal: the live
// reporter's active package, or a Status. interrupt runs print with that line
// cleared, then draws it again underneath.
type transient interface {
	interrupt(print func())
}

var (
	transientMu sync.Mutex
	current     transient
)

func show(t transient) {
	transientMu.Lock()
	current = t
	transientMu.Unlock()
}

func hide(t transient) {
	transientMu.Lock()
	if current == t {
		current = nil
	}
	transientMu.Unlock()
}

// lineWriter passes whole writes to w, keeping any transient line showing on
// the same terminal below them.
type lineWriter struct{ w io.Writer }

// Lines returns a writer for messages that share w with the transient lines
// this package draws, such as warnings logged mid-install. Written straight to
// w, a message lands on the end of the half-drawn line and the next redraw
// erases it.
func Lines(w io.Writer) io.Writer { return lineWriter{w} }

func (l lineWriter) Write(p []byte) (int, error) {
	transientMu.Lock()
	t := current
	transientMu.Unlock()
	if t == nil {
		return l.w.Write(p)
	}
	var n int
	var err error
	t.interrupt(func() { n, err = l.w.Write(p) })
	return n, err
}
