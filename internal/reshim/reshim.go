// Package reshim computes which runtime-installed global executables should be
// exposed as bunny shims. The planning function is pure (no filesystem or
// state side effects) so it is easy to test; the caller applies the resulting
// add/remove sets to the shim directory and the state registry.
package reshim

import (
	"os"
	"path/filepath"
	"sort"
)

// Provider is one installed capability-providing package whose global-bin
// directories have already been scanned into Tools.
type Provider struct {
	Capability string   // e.g. "node"
	Tools      []string // executable names found in this provider's global-bins
}

// Conflict records two capabilities exposing the same command name; the first
// (by sorted capability order) wins and the second is skipped.
type Conflict struct {
	Command           string
	KeptCapability    string
	SkippedCapability string
}

// Plan computes the desired command→capability registry from providers and
// diffs it against current.
//
//   - protected: command names owned by SDK shims (state.Commands) — never
//     exposed as a global shim.
//   - current: the existing state.GlobalCommands (scoped by the caller; pass
//     only the entries for the capabilities being reshimmed).
//
// Returns add (name→capability to register+shim), remove (names to drop), and
// any name collisions across capabilities (first-wins, deterministic).
func Plan(providers []Provider, protected map[string]bool, current map[string]string) (add map[string]string, remove []string, conflicts []Conflict) {
	add = map[string]string{}
	desired := map[string]string{}

	sorted := append([]Provider(nil), providers...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Capability < sorted[j].Capability })

	for _, p := range sorted {
		tools := append([]string(nil), p.Tools...)
		sort.Strings(tools)
		for _, tool := range tools {
			if protected[tool] {
				continue
			}
			if keptCap, ok := desired[tool]; ok {
				if keptCap != p.Capability {
					conflicts = append(conflicts, Conflict{
						Command:           tool,
						KeptCapability:    keptCap,
						SkippedCapability: p.Capability,
					})
				}
				continue
			}
			desired[tool] = p.Capability
		}
	}

	for name, cap := range desired {
		if current[name] != cap {
			add[name] = cap
		}
	}
	for name := range current {
		if _, ok := desired[name]; !ok {
			remove = append(remove, name)
		}
	}
	sort.Strings(remove)
	return add, remove, conflicts
}

// Executables returns the names of executable regular files (following
// symlinks) in dir, sorted. A missing dir yields an empty slice, not an error.
func Executables(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		info, err := os.Stat(filepath.Join(dir, e.Name())) // Stat follows symlinks
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0111 == 0 {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}
