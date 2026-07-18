package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/cristatus/bunny/internal/ui"
)

func TestInstallSummarySingular(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	got := installSummary(p, "installed", 1, 0, 3100*time.Millisecond)
	if !strings.Contains(got, "installed 1 package in 3.1s") {
		t.Fatalf("summary = %q", got)
	}
}

func TestInstallSummaryWithFailures(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	got := installSummary(p, "installed", 2, 1, 5*time.Second)
	if !strings.Contains(got, "installed 2 packages") || !strings.Contains(got, "1 failed") {
		t.Fatalf("summary = %q", got)
	}
}
