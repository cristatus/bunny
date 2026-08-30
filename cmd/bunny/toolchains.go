package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/toolchains"
	"github.com/cristatus/bunny/internal/verparse"
)

// ToolchainsCmd regenerates Gradle/Maven JDK toolchain config from the installed JDKs.
type ToolchainsCmd struct{}

func (c *ToolchainsCmd) Run(a *App) error {
	return a.withMutation(a.context(), func() error {
		jdks, err := a.regenerateToolchains()
		if err != nil {
			return err
		}
		log.Info("Regenerated JDK toolchain config", "jdks", jdks)
		p := a.status()
		p.Println()
		p.Println("regenerated JDK toolchain config")
		return nil
	})
}

// installedJDKs returns every installed package that provides the `jdk`
// capability, as toolchain entries (home + major version), in sorted-id order.
func (a *App) installedJDKs() ([]toolchains.JDK, error) {
	var out []toolchains.JDK
	for _, id := range a.State.Installed() { // Installed() is sorted
		m, err := a.loadInstalledManifest(id)
		if err != nil {
			return nil, fmt.Errorf("load installed manifest %s: %w", id, err)
		}
		if m.Provides != "jdk" {
			continue
		}
		out = append(out, toolchains.JDK{
			Home:   a.Paths.AppDir(id),
			Major:  verparse.Major(m.Version),
			Vendor: jdkVendor(m),
		})
	}
	return out, nil
}

// gradleUserHome resolves the directory Gradle will actually read
// gradle.properties from, so the managed toolchain block lands where it has an
// effect. An explicit GRADLE_USER_HOME wins in launch precedence order
// (manifest, then user config); otherwise Gradle's own default applies, which
// by default means the real ~/.gradle rather than anything bunny owns.
func (a *App) gradleUserHome(m *manifest.Manifest, vars map[string]string) string {
	env := a.Config.OverlayEnv(m.Env, m.ID, m.Provides)
	if home := manifest.Expand(env["GRADLE_USER_HOME"], vars); home != "" {
		return home
	}
	if home := os.Getenv("GRADLE_USER_HOME"); home != "" {
		return home
	}
	return filepath.Join(vars["home"], ".gradle")
}

// regenerateToolchains writes JDK-toolchain config for every installed package
// that declares `toolchains:`, listing all installed `provides: jdk` packages.
// No-op when no consumer (or no JDK) is installed.
// jdkVendor reports the distribution name to publish for a JDK, or "" when the
// manifest does not say. It reads the primary source's update distribution,
// which tells two installs of the same major version apart.
//
// That field is the foojay checker's, so a JDK tracked by another backend has
// none and is published without a vendor — a pom naming it then matches
// nothing. See docs/java.md.
func jdkVendor(m *manifest.Manifest) string {
	if len(m.Sources) == 0 || m.Sources[0].Update == nil {
		return ""
	}
	return m.Sources[0].Update.Distribution
}

// regenerateToolchains rewrites every installed toolchain consumer's config and
// reports how many JDKs it published, so a caller does not reload the installed
// manifests to learn the count.
func (a *App) regenerateToolchains() (int, error) {
	jdks, err := a.installedJDKs()
	if err != nil {
		return 0, err
	}
	homes := make([]string, 0, len(jdks))
	for _, j := range jdks {
		homes = append(homes, j.Home)
	}
	mavenXML := toolchains.MavenToolchainsXML(jdks)
	for _, id := range a.State.Installed() {
		m, err := a.loadInstalledManifest(id)
		if err != nil {
			return 0, fmt.Errorf("load installed manifest %s: %w", id, err)
		}
		if m.Toolchains == "" {
			continue
		}
		switch m.Toolchains {
		case "gradle":
			home := a.gradleUserHome(m, a.Paths.Vars(id, m.Version))
			if err := os.MkdirAll(home, 0755); err != nil {
				return 0, err
			}
			path := filepath.Join(home, "gradle.properties")
			existing, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				return 0, err
			}
			content := toolchains.MergeGradleProperties(string(existing), homes)
			if err := fsutil.WriteFile(path, []byte(content), 0644); err != nil {
				return 0, err
			}
			log.Debug("Wrote Gradle toolchain config", "path", path, "jdks", len(homes))
		case "maven":
			dir := a.Paths.AppData(id)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return 0, err
			}
			path := filepath.Join(dir, "toolchains.xml")
			if err := fsutil.WriteFile(path, []byte(mavenXML), 0644); err != nil {
				return 0, err
			}
			log.Debug("Wrote Maven toolchain config", "path", path, "jdks", len(jdks))
		}
	}
	return len(jdks), nil
}
