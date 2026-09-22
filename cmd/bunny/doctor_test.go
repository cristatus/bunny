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

// Every fix used to render as "run '<fix>'", which made prose read as a
// command: "run 'check your kernel version and ...'".
func TestRenderDoctorQuotesOnlyCommandFixes(t *testing.T) {
	var b bytes.Buffer
	renderDoctor(ui.NewWithColor(&b, false), []doctor.Result{
		{Name: "PATH", Severity: doctor.Warn, Fix: "bunny setup"},
		{Name: "sandbox", Severity: doctor.Fail, Fix: "check your kernel version"},
	})
	out := b.String()
	if !strings.Contains(out, "fix: run 'bunny setup'") {
		t.Errorf("a command fix is quoted as one to run:\n%s", out)
	}
	if !strings.Contains(out, "fix: check your kernel version") || strings.Contains(out, "run 'check") {
		t.Errorf("a prose fix is printed as written:\n%s", out)
	}
}
