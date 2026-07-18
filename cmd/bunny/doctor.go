package main

import (
	"fmt"
	"os"

	"github.com/cristatus/bunny/internal/doctor"
	"github.com/cristatus/bunny/internal/ui"
)

// DoctorCmd renders the diagnostic checks.
type DoctorCmd struct{}

func (c *DoctorCmd) Run(a *App) error {
	results := doctor.RunAll(a.Paths)
	if cwd, err := os.Getwd(); err == nil {
		results = append(results, doctor.PinResolution(a.State, cwd)...)
	}
	p := ui.New(os.Stdout)
	p.Println()
	_, failures := renderDoctor(p, results)
	if failures > 0 {
		return fmt.Errorf("%d check(s) failed", failures)
	}
	return nil
}

// renderDoctor prints each check with a status glyph and, when present, an
// indented fix line; then a summary. Returns the warning and failure counts.
func renderDoctor(p *ui.Printer, results []doctor.Result) (warnings, failures int) {
	maxName := 0
	for _, r := range results {
		if len(r.Name) > maxName {
			maxName = len(r.Name)
		}
	}
	for _, r := range results {
		glyph, style := "✓", ui.Good
		switch r.Severity {
		case doctor.Warn:
			glyph, style = "⚠", ui.Plain
			warnings++
		case doctor.Fail:
			glyph, style = "✗", ui.Bad
			failures++
		}
		pad := ""
		if gap := maxName - len(r.Name); gap > 0 {
			pad = fmt.Sprintf("%*s", gap, "")
		}
		p.Printf("%s %s%s  %s\n", p.Sym(glyph, style), r.Name, pad, r.Detail)
		if r.Fix != "" {
			p.Println(p.Faint(fmt.Sprintf("  fix: run '%s'", r.Fix)))
		}
	}
	p.Println()
	switch {
	case failures == 0 && warnings == 0:
		p.Println("all checks passed")
	default:
		p.Printf("%d warnings, %d errors\n", warnings, failures)
	}
	return warnings, failures
}
