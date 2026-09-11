package runtime

import (
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/cristatus/bunny/internal/manifest"
)

// envBuilder keeps one value per key while preserving the base environment's
// order. Later overlays replace earlier values, giving launch preparation the
// explicit precedence host < dependencies < package.
type envBuilder struct {
	order  []string
	values map[string]string
	// injected names the variables bunny set for this launch rather than
	// inherited from the host: a dependency's env, the package's manifest
	// env, the user's config overlay. A sandbox environment policy governs
	// inherited variables only, so it has to be able to tell them apart.
	injected map[string]bool
}

func newEnvBuilder(base []string) *envBuilder {
	b := &envBuilder{values: make(map[string]string, len(base)), injected: map[string]bool{}}
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		b.Set(key, value)
	}
	return b
}

func (b *envBuilder) Set(key, value string) {
	if _, exists := b.values[key]; !exists {
		b.order = append(b.order, key)
	}
	b.values[key] = value
}

func (b *envBuilder) Overlay(values map[string]string, vars map[string]string) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.Set(key, manifest.Expand(values[key], vars))
		b.injected[key] = true
	}
}

// Injected lists the names bunny set, sorted.
func (b *envBuilder) Injected() []string { return slices.Sorted(maps.Keys(b.injected)) }

func (b *envBuilder) List() []string {
	out := make([]string, 0, len(b.order))
	for _, key := range b.order {
		out = append(out, key+"="+b.values[key])
	}
	return out
}
