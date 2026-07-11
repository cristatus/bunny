// Package state persists the set of installed packages, the active provider
// for each capability (e.g. "node" → "node-22"), and the command → package
// mapping that shims rely on.
//
// Format: JSON at $BUNNY_HOME/var/state.json. Small enough that JSON is
// fine; SQLite would be over-engineered.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/verparse"
)

const schemaVersion = 1

// State is the durable record of what's installed.
type State struct {
	Version int       `json:"version"`
	Updated time.Time `json:"updated"`

	// Packages keyed by package ID (e.g. "node-22").
	Packages map[string]Package `json:"packages"`

	// Commands maps a binary name (e.g. "node") to the package that owns the shim.
	Commands map[string]string `json:"commands,omitempty"`

	// Providers maps a capability (e.g. "node") to the active package ID.
	// Updated by `bunny use`.
	Providers map[string]string `json:"providers,omitempty"`

	// GlobalCommands maps a runtime-installed global executable name (e.g. "tsc")
	// to the capability whose active/pinned provider runs it (e.g. "node").
	// Populated by `bunny reshim`. Distinct from Commands (manifest bin: shims).
	GlobalCommands map[string]string `json:"globalCommands,omitempty"`
}

// Package is per-package metadata.
type Package struct {
	Version   string    `json:"version"`
	Installed time.Time `json:"installed"`
	Provides  string    `json:"provides,omitempty"`
}

// Empty returns a fresh State with all maps initialized.
func Empty() *State {
	return &State{
		Version:        schemaVersion,
		Packages:       map[string]Package{},
		Commands:       map[string]string{},
		Providers:      map[string]string{},
		GlobalCommands: map[string]string{},
	}
}

// Load reads state from path. A missing file returns Empty().
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Empty(), nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Packages == nil {
		s.Packages = map[string]Package{}
	}
	if s.Commands == nil {
		s.Commands = map[string]string{}
	}
	if s.Providers == nil {
		s.Providers = map[string]string{}
	}
	if s.GlobalCommands == nil {
		s.GlobalCommands = map[string]string{}
	}
	if s.Version != 0 && s.Version != schemaVersion {
		// State written by a newer bunny we cannot safely interpret. This is the
		// one load-time failure kept hard: repairing an unknown schema could
		// silently discard data.
		return nil, fmt.Errorf("unsupported state schema version %d (expected %d)", s.Version, schemaVersion)
	}
	if s.Version == 0 {
		s.Version = schemaVersion
	}
	// Read paths must survive a slightly-inconsistent state file: prune
	// dangling/invalid entries and carry on rather than bricking every command.
	// Mutations stay strict — Save runs full Validate before persisting, so a
	// genuinely broken state can never be written back out.
	// Debug, not Warn: repair does not persist (Load is a read path with no
	// lock), so a lingering inconsistency would otherwise re-log on every single
	// command until a mutation rewrites state.
	for _, note := range s.repair() {
		log.Debug("Repaired inconsistent state", "detail", note)
	}
	return &s, nil
}

// Clone returns a deep copy. Used by the installer to roll back state when
// a multi-step install fails partway through.
func (s *State) Clone() *State {
	out := &State{
		Version:        s.Version,
		Updated:        s.Updated,
		Packages:       make(map[string]Package, len(s.Packages)),
		Commands:       make(map[string]string, len(s.Commands)),
		Providers:      make(map[string]string, len(s.Providers)),
		GlobalCommands: make(map[string]string, len(s.GlobalCommands)),
	}
	for k, v := range s.Packages {
		out.Packages[k] = v
	}
	for k, v := range s.Commands {
		out.Commands[k] = v
	}
	for k, v := range s.Providers {
		out.Providers[k] = v
	}
	for k, v := range s.GlobalCommands {
		out.GlobalCommands[k] = v
	}
	return out
}

// Save atomically writes state to path.
func (s *State) Save(path string) error {
	s.Version = schemaVersion
	s.Updated = time.Now()
	if err := s.Validate(); err != nil {
		return fmt.Errorf("validate state before save: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFile(path, data, 0644)
}

// Validate rejects unsafe or internally inconsistent persisted state before
// any path derived from it can be used by an install or uninstall operation.
func (s *State) Validate() error {
	if s.Version != schemaVersion {
		return fmt.Errorf("unsupported schema version %d", s.Version)
	}
	for id, pkg := range s.Packages {
		if err := manifest.ValidateID(id); err != nil {
			return fmt.Errorf("package %q: %w", id, err)
		}
		if pkg.Version == "" {
			return fmt.Errorf("package %q has an empty version", id)
		}
		if pkg.Provides != "" {
			if err := manifest.ValidateID(pkg.Provides); err != nil {
				return fmt.Errorf("package %q provides invalid capability %q: %w", id, pkg.Provides, err)
			}
		}
	}
	for command, owner := range s.Commands {
		if !safeCommandName(command) {
			return fmt.Errorf("unsafe command name %q", command)
		}
		if _, ok := s.Packages[owner]; !ok {
			return fmt.Errorf("command %q references missing package %q", command, owner)
		}
	}
	for capability, provider := range s.Providers {
		if err := manifest.ValidateID(capability); err != nil {
			return fmt.Errorf("invalid provider capability %q: %w", capability, err)
		}
		pkg, ok := s.Packages[provider]
		if !ok {
			return fmt.Errorf("provider %q references missing package %q", capability, provider)
		}
		if pkg.Provides != capability {
			return fmt.Errorf("provider %q points to %q which provides %q", capability, provider, pkg.Provides)
		}
	}
	for command, capability := range s.GlobalCommands {
		if !safeCommandName(command) || capability == "" {
			return fmt.Errorf("invalid global command mapping %q -> %q", command, capability)
		}
		if err := manifest.ValidateID(capability); err != nil {
			return fmt.Errorf("invalid global command capability %q: %w", capability, err)
		}
	}
	return nil
}

func safeCommandName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsAny(name, `/\\`)
}

// repair prunes referentially-inconsistent entries so a slightly-corrupt state
// file does not brick read-only commands. It returns a note per pruned entry
// (empty when the state was already consistent). It only removes recoverable
// inconsistencies — the kinds Validate rejects that can be dropped without
// guessing intent — leaving the survivors internally consistent so a later Save
// passes Validate. It never invents data.
func (s *State) repair() []string {
	var notes []string
	// Packages first: dropping a bad package cascades into the command and
	// provider passes below, which key off s.Packages.
	for id, pkg := range s.Packages {
		switch {
		case manifest.ValidateID(id) != nil:
			delete(s.Packages, id)
			notes = append(notes, fmt.Sprintf("dropped package with invalid id %q", id))
		case pkg.Version == "":
			delete(s.Packages, id)
			notes = append(notes, fmt.Sprintf("dropped package %q with empty version", id))
		case pkg.Provides != "" && manifest.ValidateID(pkg.Provides) != nil:
			delete(s.Packages, id)
			notes = append(notes, fmt.Sprintf("dropped package %q with invalid capability %q", id, pkg.Provides))
		}
	}
	for command, owner := range s.Commands {
		switch {
		case !safeCommandName(command):
			delete(s.Commands, command)
			notes = append(notes, fmt.Sprintf("dropped unsafe command name %q", command))
		case !s.IsInstalled(owner):
			delete(s.Commands, command)
			notes = append(notes, fmt.Sprintf("dropped command %q owned by missing package %q", command, owner))
		}
	}
	for capability, provider := range s.Providers {
		pkg, ok := s.Packages[provider]
		switch {
		case manifest.ValidateID(capability) != nil:
			delete(s.Providers, capability)
			notes = append(notes, fmt.Sprintf("dropped provider with invalid capability %q", capability))
		case !ok:
			delete(s.Providers, capability)
			notes = append(notes, fmt.Sprintf("dropped provider %q pointing at missing package %q", capability, provider))
		case pkg.Provides != capability:
			delete(s.Providers, capability)
			notes = append(notes, fmt.Sprintf("dropped provider %q pointing at %q which provides %q", capability, provider, pkg.Provides))
		}
	}
	for command, capability := range s.GlobalCommands {
		if !safeCommandName(command) || manifest.ValidateID(capability) != nil {
			delete(s.GlobalCommands, command)
			notes = append(notes, fmt.Sprintf("dropped invalid global command %q -> %q", command, capability))
		}
	}
	return notes
}

// --- Mutations ---

// SetInstalled records a package install. If `provides` is set, the package
// becomes the active provider for that capability only when no provider is
// active yet; installing an additional provider (e.g. a second JDK) leaves the
// current one active — switch with `bunny use`.
func (s *State) SetInstalled(id, version, provides string) {
	s.Packages[id] = Package{
		Version:   version,
		Installed: time.Now(),
		Provides:  provides,
	}
	if provides != "" {
		if _, ok := s.Providers[provides]; !ok {
			s.Providers[provides] = id
		}
	}
}

// SetUninstalled removes a package and any commands/providers it owned. When
// the package was the active provider for a capability, it also promotes the
// next-best installed provider into the provider pointer.
//
// That fallback selection is deliberate and load-bearing, not just a
// convenience: Installer.checkReverseDependencies simulates an uninstall on a
// cloned state and then asks IsSatisfied(req) for bare-capability requirements,
// which reads Providers[capability]. Without the promotion here the simulation
// would wrongly report a still-satisfiable requirement as broken. The installer
// re-affirms the pointer (and wires the fallback's shims/commands) via
// SetProviderCommands on the real state; do not move this promotion out.
func (s *State) SetUninstalled(id string) {
	pkg, ok := s.Packages[id]
	if !ok {
		return
	}
	delete(s.Packages, id)
	if pkg.Provides != "" && s.Providers[pkg.Provides] == id {
		delete(s.Providers, pkg.Provides)
		if fallback := s.ResolveProviderMin(pkg.Provides, 0); fallback != "" {
			s.Providers[pkg.Provides] = fallback
		}
	}
	for cmd, pkgID := range s.Commands {
		if pkgID == id {
			delete(s.Commands, cmd)
		}
	}
}

// SetCommand registers a command name as owned by a package.
func (s *State) SetCommand(name, pkgID string) {
	s.Commands[name] = pkgID
}

// SetCommands replaces every command owned by pkgID with names.
func (s *State) SetCommands(pkgID string, names []string) {
	for cmd, owner := range s.Commands {
		if owner == pkgID {
			delete(s.Commands, cmd)
		}
	}
	for _, name := range names {
		s.Commands[name] = pkgID
	}
}

// SetProviderCommands switches a capability to pkgID and replaces every
// command owned by any provider of that capability. This prevents commands
// unique to the previously active version from remaining live after a switch.
func (s *State) SetProviderCommands(capability, pkgID string, names []string) error {
	pkg, ok := s.Packages[pkgID]
	if !ok {
		return errors.New("package not installed")
	}
	if pkg.Provides != capability {
		return fmt.Errorf("package %q provides %q, not %q", pkgID, pkg.Provides, capability)
	}
	for command, owner := range s.Commands {
		if ownerPkg, ok := s.Packages[owner]; ok && ownerPkg.Provides == capability {
			delete(s.Commands, command)
		}
	}
	for _, name := range names {
		s.Commands[name] = pkgID
	}
	s.Providers[capability] = pkgID
	return nil
}

// SetGlobalCommand registers a global executable under a capability.
func (s *State) SetGlobalCommand(name, capability string) {
	s.GlobalCommands[name] = capability
}

// RemoveGlobalCommand unregisters a global executable.
func (s *State) RemoveGlobalCommand(name string) {
	delete(s.GlobalCommands, name)
}

// SetProvider sets which installed package is the active provider for a
// capability. Used by `bunny use`. The package must already be installed.
func (s *State) SetProvider(capability, pkgID string) error {
	pkg, ok := s.Packages[pkgID]
	if !ok {
		return errors.New("package not installed")
	}
	if pkg.Provides != capability {
		return fmt.Errorf("package %q provides %q, not %q", pkgID, pkg.Provides, capability)
	}
	s.Providers[capability] = pkgID
	return nil
}

// --- Queries ---

// GlobalCommandCapability returns the capability for a registered global command.
func (s *State) GlobalCommandCapability(name string) (string, bool) {
	c, ok := s.GlobalCommands[name]
	return c, ok
}

// GlobalCommandNames returns sorted registered global command names.
func (s *State) GlobalCommandNames() []string {
	names := make([]string, 0, len(s.GlobalCommands))
	for name := range s.GlobalCommands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsInstalled returns true if the package is recorded as installed.
func (s *State) IsInstalled(id string) bool {
	_, ok := s.Packages[id]
	return ok
}

// CommandOwner returns the package that owns the given command.
func (s *State) CommandOwner(name string) (string, bool) {
	id, ok := s.Commands[name]
	return id, ok
}

// CommandsForCapability returns the sorted command names whose owning package
// provides the given capability.
func (s *State) CommandsForCapability(capability string) []string {
	var names []string
	for command, owner := range s.Commands {
		if pkg, ok := s.Packages[owner]; ok && pkg.Provides == capability {
			names = append(names, command)
		}
	}
	sort.Strings(names)
	return names
}

// CommandsOwnedBy returns sorted command names currently owned by pkgID.
func (s *State) CommandsOwnedBy(pkgID string) []string {
	var names []string
	for name, owner := range s.Commands {
		if owner == pkgID {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// ResolveProvider returns the installed package that satisfies a requirement
// (either an explicit package ID, or a capability name).
func (s *State) ResolveProvider(req string) string {
	if s.IsInstalled(req) {
		return req
	}
	return s.Providers[req]
}

// ResolveProviderMin returns an installed package providing `capability` whose
// major version is >= minMajor: the active provider if it satisfies, else the
// highest-major satisfying installed provider, else "". Deterministic (scans
// installed ids in sorted order).
func (s *State) ResolveProviderMin(capability string, minMajor int) string {
	if active := s.Providers[capability]; active != "" {
		if verparse.MajorInt(s.Packages[active].Version) >= minMajor {
			return active
		}
	}
	best, bestMajor := "", -1
	for _, id := range s.Installed() { // sorted → deterministic tie-break
		pkg := s.Packages[id]
		if pkg.Provides != capability {
			continue
		}
		if m := verparse.MajorInt(pkg.Version); m >= minMajor && m > bestMajor {
			best, bestMajor = id, m
		}
	}
	return best
}

// IsSatisfied checks if a requirement (package ID or capability) is met.
func (s *State) IsSatisfied(req string) bool {
	return s.ResolveProvider(req) != ""
}

// Installed returns sorted package IDs.
func (s *State) Installed() []string {
	ids := make([]string, 0, len(s.Packages))
	for id := range s.Packages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
