package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/cristatus/bunny/internal/doctor"
)

// DoctorCmd renders the diagnostic table.
type DoctorCmd struct{}

func (c *DoctorCmd) Run(a *App) error {
	results := doctor.RunAll(a.Paths)
	if cwd, err := os.Getwd(); err == nil {
		results = append(results, doctor.PinResolution(a.State, cwd)...)
	}
	maxName := 0
	for _, r := range results {
		if len(r.Name) > maxName {
			maxName = len(r.Name)
		}
	}
	failures := 0
	for _, r := range results {
		mark := "✓"
		switch r.Severity {
		case doctor.Warn:
			mark = "!"
		case doctor.Fail:
			mark = "✗"
			failures++
		}
		pad := strings.Repeat(" ", maxName-len(r.Name))
		fmt.Printf("  %s %s%s  %s\n", mark, r.Name, pad, r.Detail)
	}
	if failures > 0 {
		return fmt.Errorf("%d check(s) failed", failures)
	}
	return nil
}
