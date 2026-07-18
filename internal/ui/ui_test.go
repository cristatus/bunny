package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestPaintPlainEmitsNoANSI(t *testing.T) {
	var b bytes.Buffer
	p := NewWithColor(&b, false)
	got := p.paint("ok", Good)
	if got != "ok" {
		t.Fatalf("plain paint = %q, want %q", got, "ok")
	}
}

func TestPaintColorWrapsGoodInGreen(t *testing.T) {
	var b bytes.Buffer
	p := NewWithColor(&b, true)
	got := p.paint("ok", Good)
	if !strings.HasPrefix(got, "\x1b[32m") || !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("colored paint = %q, want green-wrapped", got)
	}
}

func TestPaintColorWrapsBadInRed(t *testing.T) {
	var b bytes.Buffer
	p := NewWithColor(&b, true)
	if got := p.paint("no", Bad); !strings.HasPrefix(got, "\x1b[31m") {
		t.Fatalf("bad paint = %q, want red-wrapped", got)
	}
}
