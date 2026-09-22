// Package reshim computes which runtime-installed global executables should be
// exposed as bunny shims. The planning function is pure (no filesystem or
// state side effects) so it is easy to test; the caller applies the resulting
// add/remove sets to the shim directory and the state registry.
package reshim

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// Provider is one installed capability-providing package whose global-bin
// directories have already been scanned into Tools.
type Provider struct {
	Capability string   // e.g. "node"
	Tools      []string // executable names found in this provider's global-bins
}

// Conflict records two capabilities exposing the same command name: the one
// that owns it now keeps it, or for a new name the first by sorted capability
// order, and the other is skipped.
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
// any name collisions across capabilities. A collision goes to the name's
// current owner while it still provides the tool, so a scoped and a full
// reshim agree on who owns it; a new name goes to the first capability in
// sorted order.
func Plan(providers []Provider, protected map[string]bool, current map[string]string) (add map[string]string, remove []string, conflicts []Conflict) {
	add = map[string]string{}
	desired := map[string]string{}

	sorted := slices.Clone(providers)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Capability < sorted[j].Capability })

	claims := map[string][]string{} // tool → claiming capabilities, sorted
	for _, p := range sorted {
		tools := slices.Clone(p.Tools)
		sort.Strings(tools)
		for _, tool := range slices.Compact(tools) {
			if !protected[tool] && !slices.Contains(claims[tool], p.Capability) {
				claims[tool] = append(claims[tool], p.Capability)
			}
		}
	}
	for _, tool := range slices.Sorted(maps.Keys(claims)) {
		caps := claims[tool]
		kept := caps[0]
		if slices.Contains(caps, current[tool]) {
			kept = current[tool]
		}
		desired[tool] = kept
		for _, c := range caps {
			if c != kept {
				conflicts = append(conflicts, Conflict{Command: tool, KeptCapability: kept, SkippedCapability: c})
			}
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
