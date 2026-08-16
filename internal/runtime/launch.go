package runtime

import (
	"fmt"
	"os"
	"syscall"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/config"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// Prepared captures everything needed to launch a binary. Built by Prepare,
// consumed by Exec.
type Prepared struct {
	Manifest *manifest.Manifest
	Binary   *manifest.Binary
	BinPath  string
	CmdArgs  []string
	Env      []string
	Vars     map[string]string
}

// Launcher builds launch environments. It carries the four things every launch
// consults: where bunny keeps its files, the catalog (to read the manifests of
// `requires:` providers), the installed state (to resolve which package
// currently provides a capability), and the user's config.
//
// Env precedence, lowest to highest: the host environment, each dependency's
// manifest `env:`, this package's manifest `env:`, then the user's config
// `env:`. Manifests carry the wiring a package cannot run without (JAVA_HOME
// and friends); config is where a user adds anything else, isolation included.
type Launcher struct {
	Paths   *paths.Paths
	Catalog catalog.Loader
	State   *state.State
	Config  *config.Config // nil is valid: manifest env only
}

// Prepare resolves the named binary (or m.Bin[0] if name is empty), expands
// placeholders, ensures the per-app data dirs exist, and pulls env from the
// package's `requires:` chain.
func (l *Launcher) Prepare(m *manifest.Manifest, name string, userArgs []string) (*Prepared, error) {
	if len(m.Bin) == 0 {
		return nil, fmt.Errorf("package %q has no binaries", m.ID)
	}

	var bin *manifest.Binary
	if name == "" {
		bin = &m.Bin[0]
	} else {
		for i := range m.Bin {
			if m.Bin[i].Name == name {
				bin = &m.Bin[i]
				break
			}
		}
		if bin == nil {
			return nil, fmt.Errorf("binary %q not found in package %q", name, m.ID)
		}
	}

	vars := l.Paths.Vars(m.ID, m.Version)
	binPath := manifest.Expand(bin.Path, vars)

	if err := os.MkdirAll(vars["data"], 0755); err != nil {
		return nil, fmt.Errorf("create data directory %s: %w", vars["data"], err)
	}
	dirs := append(append([]string{}, m.Dirs...), l.Config.DirsFor(m.ID, m.Provides)...)
	for _, dir := range dirs {
		dir = manifest.Expand(dir, vars)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	cmdArgs := make([]string, 0, len(bin.Args)+len(userArgs))
	for _, arg := range bin.Args {
		cmdArgs = append(cmdArgs, manifest.Expand(arg, vars))
	}
	cmdArgs = append(cmdArgs, userArgs...)

	env, err := l.buildEnv(m, vars)
	if err != nil {
		return nil, err
	}

	return &Prepared{
		Manifest: m,
		Binary:   bin,
		BinPath:  binPath,
		CmdArgs:  cmdArgs,
		Env:      env,
		Vars:     vars,
	}, nil
}

// PrepareGlobal builds a Prepared for a runtime-installed global executable
// (e.g. an `npm -g` binary) owned by provider package m. It applies the
// provider's env (+ requires chain) and execs exePath directly — no manifest
// bin: lookup, since the executable was installed at runtime, not by bunny.
func (l *Launcher) PrepareGlobal(m *manifest.Manifest, exePath string, userArgs []string) (*Prepared, error) {
	vars := l.Paths.Vars(m.ID, m.Version)
	env, err := l.buildEnv(m, vars)
	if err != nil {
		return nil, err
	}
	return &Prepared{
		Manifest: m,
		BinPath:  exePath,
		CmdArgs:  append([]string{}, userArgs...),
		Env:      env,
		Vars:     vars,
	}, nil
}

// buildEnv layers the launch environment in precedence order: host, then the
// `requires:` chain, then the package's own manifest env with the user's
// config env on top.
func (l *Launcher) buildEnv(m *manifest.Manifest, vars map[string]string) ([]string, error) {
	env, err := l.mergeDepEnv(os.Environ(), m.Requires)
	if err != nil {
		return nil, err
	}
	builder := newEnvBuilder(env)
	builder.Overlay(l.Config.OverlayEnv(m.Env, m.ID, m.Provides), vars)
	return builder.List(), nil
}

// mergeDepEnv resolves each requirement to a provider package and appends that
// package's env (with placeholder expansion). A missing or unreadable
// dependency is a warning, not a hard stop: launching degraded (against
// whatever the host provides) is preferable to refusing to run the program at
// all. `bunny doctor` surfaces unmet requirements for the user to fix.
func (l *Launcher) mergeDepEnv(env []string, reqs []string) ([]string, error) {
	builder := newEnvBuilder(env)
	for _, req := range reqs {
		capability, minMajor, hasMin := manifest.ParseRequirement(req)

		var providerID string
		if hasMin {
			providerID = l.State.ResolveProviderMin(capability, minMajor)
		} else {
			providerID = l.State.ResolveProvider(req)
		}
		if providerID == "" {
			log.Debug("Launching without required dependency", "requires", req)
			continue
		}

		dep, err := l.Catalog.Load(providerID)
		if err != nil {
			log.Debug("Launching without required dependency env (manifest unavailable)", "requires", req, "provider", providerID, "error", err)
			continue
		}
		depVars := l.Paths.Vars(providerID, dep.Version)
		// A dependency's config env applies too, so that pointing, say, the jdk
		// capability at a different JAVA_HOME is honoured by every tool that
		// requires it, not just by launching the jdk directly. Key on what the
		// dependency provides, falling back to the capability that was asked
		// for when the manifest does not say.
		depCapability := dep.Provides
		if depCapability == "" {
			depCapability = capability
		}
		builder.Overlay(l.Config.OverlayEnv(dep.Env, providerID, depCapability), depVars)
	}
	return builder.List(), nil
}

// Exec runs the prepared binary via direct exec. Returns only on failure.
func Exec(p *Prepared) error {
	return directExec(p)
}

func directExec(p *Prepared) error {
	args := append([]string{p.BinPath}, p.CmdArgs...)
	log.Debug("Direct exec", "binary", p.BinPath, "args", p.CmdArgs)
	return syscall.Exec(p.BinPath, args, p.Env)
}
