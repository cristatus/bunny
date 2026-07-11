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
		if err := a.regenerateToolchains(); err != nil {
			return err
		}
		log.Info("Regenerated JDK toolchain config")
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
			Home:  a.Paths.AppDir(id),
			Major: verparse.Major(m.Version),
		})
	}
	return out, nil
}

// regenerateToolchains writes JDK-toolchain config for every installed package
// that declares `toolchains:`, listing all installed `provides: jdk` packages.
// No-op when no consumer (or no JDK) is installed.
func (a *App) regenerateToolchains() error {
	jdks, err := a.installedJDKs()
	if err != nil {
		return err
	}
	homes := make([]string, 0, len(jdks))
	for _, j := range jdks {
		homes = append(homes, j.Home)
	}
	for _, id := range a.State.Installed() {
		m, err := a.loadInstalledManifest(id)
		if err != nil {
			return fmt.Errorf("load installed manifest %s: %w", id, err)
		}
		if m.Toolchains == "" {
			continue
		}
		vars := a.Paths.Vars(id, m.Version)
		switch m.Toolchains {
		case "gradle":
			home := manifest.Expand(m.Env["GRADLE_USER_HOME"], vars)
			if home == "" {
				home = a.Paths.AppData(id)
			}
			if err := os.MkdirAll(home, 0755); err != nil {
				return err
			}
			path := filepath.Join(home, "gradle.properties")
			existing, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			content := toolchains.MergeGradleProperties(string(existing), homes)
			if err := fsutil.WriteFile(path, []byte(content), 0644); err != nil {
				return err
			}
			log.Debug("Wrote Gradle toolchain config", "path", path, "jdks", len(homes))
		case "maven":
			dir := a.Paths.AppData(id)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return err
			}
			path := filepath.Join(dir, "toolchains.xml")
			if err := fsutil.WriteFile(path, []byte(toolchains.MavenToolchainsXML(jdks)), 0644); err != nil {
				return err
			}
			log.Debug("Wrote Maven toolchain config", "path", path, "jdks", len(jdks))
		}
	}
	return nil
}
