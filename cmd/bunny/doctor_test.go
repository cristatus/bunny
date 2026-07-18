package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/doctor"
	"github.com/cristatus/bunny/internal/ui"
)

func TestRenderDoctorPlain(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	results := []doctor.Result{
		{Name: "BUNNY_HOME", Detail: "/home/x/.bunny", Severity: doctor.OK},
		{Name: "Shims", Detail: "3 shims fail to resolve", Severity: doctor.Fail, Fix: "bunny reshim"},
	}
	renderDoctor(p, results)
	got := b.String()
	if !strings.Contains(got, "✓ BUNNY_HOME") {
		t.Fatalf("missing ok row: %q", got)
	}
	if !strings.Contains(got, "✗ Shims") || !strings.Contains(got, "fix: run 'bunny reshim'") {
		t.Fatalf("missing fail row + fix: %q", got)
	}
	if !strings.Contains(got, "1 error") {
		t.Fatalf("missing summary: %q", got)
	}
}
