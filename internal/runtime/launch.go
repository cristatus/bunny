package runtime

import (
	"fmt"
	"os"
	"syscall"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/catalog"
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

// Prepare resolves the named binary (or m.Bin[0] if name is empty), expands
// placeholders, ensures the per-app data dirs exist, and pulls env from the
// package's `requires:` chain.
func Prepare(
	p *paths.Paths,
	cat catalog.Loader,
	st *state.State,
	m *manifest.Manifest,
	name string,
	userArgs []string,
) (*Prepared, error) {
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

	vars := p.Vars(m.ID, m.Version)
	binPath := manifest.Expand(bin.Path, vars)

	if err := os.MkdirAll(vars["data"], 0755); err != nil {
		return nil, fmt.Errorf("create data directory %s: %w", vars["data"], err)
	}
	for _, dir := range m.Dirs {
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

	env := os.Environ()
	env, err := mergeDepEnv(env, m.Requires, p, cat, st)
	if err != nil {
		return nil, err
	}
	env = overlayEnv(env, m.Env, vars)

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
func PrepareGlobal(
	p *paths.Paths,
	cat catalog.Loader,
	st *state.State,
	m *manifest.Manifest,
	exePath string,
	userArgs []string,
) (*Prepared, error) {
	vars := p.Vars(m.ID, m.Version)
	env := os.Environ()
	env, err := mergeDepEnv(env, m.Requires, p, cat, st)
	if err != nil {
		return nil, err
	}
	env = overlayEnv(env, m.Env, vars)
	return &Prepared{
		Manifest: m,
		BinPath:  exePath,
		CmdArgs:  append([]string{}, userArgs...),
		Env:      env,
		Vars:     vars,
	}, nil
}

// mergeDepEnv resolves each requirement to a provider package and appends that
// package's `env:` map (with placeholder expansion). A missing or unreadable
// dependency is a warning, not a hard stop: launching degraded (against
// whatever the host provides) is preferable to refusing to run the program at
// all. `bunny doctor` surfaces unmet requirements for the user to fix.
func mergeDepEnv(env []string, reqs []string, p *paths.Paths, cat catalog.Loader, st *state.State) ([]string, error) {
	builder := newEnvBuilder(env)
	for _, req := range reqs {
		capability, minMajor, hasMin := manifest.ParseRequirement(req)

		var providerID string
		if hasMin {
			providerID = st.ResolveProviderMin(capability, minMajor)
		} else {
			providerID = st.ResolveProvider(req)
		}
		if providerID == "" {
			log.Debug("Launching without required dependency", "requires", req)
			continue
		}

		dep, err := cat.Load(providerID)
		if err != nil {
			log.Debug("Launching without required dependency env (manifest unavailable)", "requires", req, "provider", providerID, "error", err)
			continue
		}
		depVars := p.Vars(providerID, dep.Version)
		builder.Overlay(dep.Env, depVars)
	}
	return builder.List(), nil
}

func overlayEnv(env []string, values map[string]string, vars map[string]string) []string {
	builder := newEnvBuilder(env)
	builder.Overlay(values, vars)
	return builder.List()
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
