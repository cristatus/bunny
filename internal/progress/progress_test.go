package progress

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPlainStartDonePrintsFinalLine(t *testing.T) {
	var b bytes.Buffer
	r := NewPlain(&b)
	r.Start("jdk-21")
	r.Phase("jdk-21", "downloading")
	r.Download("jdk-21", 100, 200) // dropped by plain reporter
	r.Done("jdk-21", "installed", "21.0.11+10")
	got := b.String()
	want := "jdk-21   installed   21.0.11+10\n"
	if got != want {
		t.Fatalf("plain done =\n%q\nwant\n%q", got, want)
	}
}

func TestPlainDonePadsIDColumn(t *testing.T) {
	var b bytes.Buffer
	r := NewPlain(&b)
	r.Begin(context.Background(), []string{"aaaaaa", "fd"}) // id width 6; emits a leading blank
	r.Done("fd", "installed", "10.4.2")
	if got, want := b.String(), "\nfd       installed   10.4.2\n"; got != want {
		t.Fatalf("padded done =\n%q\nwant\n%q", got, want)
	}
}

func TestBarAndPct(t *testing.T) {
	if got := bar(5, 10, 10); got != "█████░░░░░" {
		t.Fatalf("bar(5,10,10) = %q", got)
	}
	if got := bar(0, 0, 4); got != "░░░░" {
		t.Fatalf("bar with unknown total = %q", got)
	}
	if got := bar(30, 10, 10); got != "██████████" {
		t.Fatalf("bar overshoot should clamp full: %q", got)
	}
	if p := pct(1, 4); p != 25 {
		t.Fatalf("pct(1,4) = %d", p)
	}
	if p := pct(1, 0); p != 0 {
		t.Fatalf("pct with zero total = %d", p)
	}
}

func TestFinalLine(t *testing.T) {
	if got := finalLine("jdk-21", "installed", "21.0.11", 0); got != "jdk-21   installed   21.0.11" {
		t.Fatalf("finalLine = %q", got)
	}
	if got := finalLine("fd", "installed", "10.4.2", 6); got != "fd       installed   10.4.2" {
		t.Fatalf("padded finalLine = %q", got)
	}
	if got := finalLine("maven", "removed", "", 0); got != "maven   removed" {
		t.Fatalf("finalLine no version = %q", got)
	}
}

func TestPlainSkipAndFail(t *testing.T) {
	var b bytes.Buffer
	r := NewPlain(&b)
	r.Skip("jdk-21", "installed", "21.0.11")
	r.Fail("broken", errors.New("checksum mismatch"))
	got := b.String()
	if !strings.Contains(got, "jdk-21   installed   21.0.11\n") {
		t.Fatalf("skip line missing: %q", got)
	}
	if !strings.Contains(got, "broken   failed: checksum mismatch\n") {
		t.Fatalf("fail line missing: %q", got)
	}
}

func TestStatusPlainIsNoop(t *testing.T) {
	var b bytes.Buffer
	s := NewStatus(&b) // a bytes.Buffer is not a TTY
	s.Update("checking glab")
	s.Clear()
	if b.Len() != 0 {
		t.Fatalf("non-tty status must be silent, got %q", b.String())
	}
}

func TestStatusTTYUpdatesAndClears(t *testing.T) {
	var b bytes.Buffer
	s := &Status{w: &b, tty: true}
	s.Update("checking glab (1/8)")
	if !strings.Contains(b.String(), "checking glab (1/8)") {
		t.Fatalf("update did not write msg: %q", b.String())
	}
	s.Clear()
	if !strings.HasSuffix(b.String(), "\r\x1b[K") {
		t.Fatalf("clear did not erase line: %q", b.String())
	}
}

// --- liveReporter tests (color off → plain, deterministic) ---

func TestLiveDoneAndSkipLines(t *testing.T) {
	var b bytes.Buffer
	r := &liveReporter{w: &b, idWidth: 4} // color off
	r.Done("fd", "installed", "10.4.2")
	r.Skip("glab", "installed", "0.74.0")
	got := b.String()
	// Finalized lines are committed with a trailing newline as static text.
	if !strings.Contains(got, "fd    ✓ installed") || !strings.Contains(got, "10.4.2\n") {
		t.Fatalf("done line wrong: %q", got)
	}
	if !strings.Contains(got, "glab  · installed") || !strings.Contains(got, "0.74.0\n") {
		t.Fatalf("skip line wrong: %q", got)
	}
}

func TestLiveStatusPadsForTrailing(t *testing.T) {
	var b bytes.Buffer
	r := &liveReporter{w: &b, idWidth: 2}
	// "installed" (9) is padded to the known column width so the version clears
	// the widest possible status ("already installed").
	line := r.compose("jq", "✓", "", "installed", "1.8.2")
	if i := strings.Index(line, "1.8.2"); i < strings.Index(line, "installed")+statusColWidth {
		t.Fatalf("version not cleared of padded status column: %q", line)
	}
}

func TestLiveFitTruncatesToWidth(t *testing.T) {
	r := &liveReporter{width: 10} // color off
	got := r.fit("abcdefghijklmnop")
	if len([]rune(got)) != 9 { // width-1
		t.Fatalf("fit = %q (len %d), want 9 runes", got, len([]rune(got)))
	}
	r.width = 0 // unknown width disables truncation
	if got := r.fit("abcdefghij"); got != "abcdefghij" {
		t.Fatalf("fit with unknown width = %q", got)
	}
}
