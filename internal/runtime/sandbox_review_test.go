package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAutomaticGrantsDoNotUnmaskProtectedRoots(t *testing.T) {
	p, home := hardenedPrepared(t)
	alias := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", home, filepath.Dir(home), alias} {
		t.Run(path, func(t *testing.T) {
			p.Vars["app"] = path
			p.DepRoots = []string{path}
			p.LayoutRoots = []string{path}
			if grants := autoReadGrants(p, protectedRoots(home)); slices.Contains(grants, path) {
				t.Fatalf("automatic grant unmasks protected root: %v", grants)
			}
		})
	}
}

func TestNestedContextDescribesOnlyEnforcedRestrictions(t *testing.T) {
	p, _ := hardenedPrepared(t)
	parent := outerContext()
	plan, err := buildSandboxPlan(p, finalized(t, &PackageSandbox{Boundary: "hardened", Home: "isolated"}), "/work", parent.HostHome, parent)
	if err != nil {
		t.Fatal(err)
	}
	if plan.context.Boundary != parent.Boundary || !slices.Equal(plan.context.DisabledFeatures, parent.DisabledFeatures) {
		t.Fatalf("unapplied restrictions reported as enforced: %+v", plan.context)
	}
}

func TestHomeSetupCannotWriteThroughPayloadSymlinks(t *testing.T) {
	for _, name := range []string{"home", ".config", ".cache", ".local"} {
		t.Run(name, func(t *testing.T) {
			data, outside := t.TempDir(), t.TempDir()
			home := filepath.Join(data, "home")
			link := home
			if name != "home" {
				if err := os.Mkdir(home, 0755); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(home, name)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			if err := ensureIsolatedHome(home); err == nil {
				t.Fatal("setup accepted a symlink outside HOME")
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("setup modified outside directory: %v", entries)
			}
		})
	}
}

func TestHomeSetupCreatesXDGDirectories(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := ensureIsolatedHome(home); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".config", ".cache", ".local/share"} {
		info, err := os.Stat(filepath.Join(home, name))
		if err != nil || !info.IsDir() {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
}
