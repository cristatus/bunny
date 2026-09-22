package progress

import (
	"bytes"
	"strings"
	"testing"
)

// A warning logged mid-install must land on its own line, with the active
// package's line drawn again below it. Written straight to the terminal it is
// appended to the half-drawn progress line, and the next redraw erases it.
func TestLinesKeepTheLiveLineBelowAMessage(t *testing.T) {
	var buf bytes.Buffer
	r := newLive(&buf)
	r.idWidth = len("tool")
	show(r)
	t.Cleanup(func() { hide(r) })
	r.Start("tool")
	active := r.activeLineLocked()

	buf.Reset()
	if _, err := Lines(&buf).Write([]byte("WARN left a file alone\n")); err != nil {
		t.Fatal(err)
	}
	want := "\r\x1b[KWARN left a file alone\n\r\x1b[K" + active
	if got := buf.String(); got != want {
		t.Errorf("output = %q\nwant     %q", got, want)
	}

	// Once the reporter is closed the writer passes straight through.
	r.Close()
	buf.Reset()
	Lines(&buf).Write([]byte("WARN later\n"))
	if got := buf.String(); !strings.HasPrefix(got, "WARN later") {
		t.Errorf("after Close = %q, want the message unchanged", got)
	}
}
