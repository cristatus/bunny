package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/ui"
)

func TestPinConfirmationLine(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	p.Print(pinConfirmation("jdk", "21"))
	if got := b.String(); !strings.Contains(got, "pinned jdk to 21") {
		t.Fatalf("pin line = %q", got)
	}
}
