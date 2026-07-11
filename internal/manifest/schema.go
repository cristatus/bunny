// Package manifest defines the YAML schema bunny consumes for each package
// in the catalog: identity, sources, install steps, and the two flavors
// of run-time portability injection (env vars and CLI args).
//
// The schema is intentionally concrete — env and args are separate
// blocks rather than wrappers around a generic "redirect" type, so the
// reader sees what each block does at a glance.
package manifest

// Manifest is one package's full descriptor.
type Manifest struct {
	// --- Identity ---
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Version     string `yaml:"version"`
	Category    string `yaml:"category,omitempty"`
	Homepage    string `yaml:"homepage,omitempty"`
	License     string `yaml:"license,omitempty"`

	// --- Capability / dependencies ---
	// Provides marks a "virtual capability" so multiple packages can implement
	// the same SDK (e.g. node-22 and node-24 both `provides: node`). The
	// active provider is set by `bunny use`.
	Provides string `yaml:"provides,omitempty"`
	// Requires lists capability names (or specific package IDs) this package
	// depends on at run time. Their `env:` blocks are merged into this
	// package's env when its binary launches.
	Requires []string `yaml:"requires,omitempty"`
	// Toolchains, when set ("gradle" or "maven"), marks this package as a
	// consumer of JDK toolchains: bunny generates the matching build-tool
	// config listing every installed `provides: jdk` package, so a build can
	// resolve its compile/test JDK independently of the tool's runtime JDK.
	Toolchains string `yaml:"toolchains,omitempty"`

	// --- Install ---
	Sources []Source `yaml:"sources"`
	Prepare []string `yaml:"prepare,omitempty"`
	// Dirs are mkdir'd at install time, host paths (typically using {data}).
	Dirs []string `yaml:"dirs,omitempty"`
	// GlobalBins lists directories (under {data}) where this package's package
	// manager drops runtime-installed global executables (e.g. npm -g). `bunny
	// reshim` scans these to expose those executables as shims.
	GlobalBins []string `yaml:"global-bins,omitempty"`

	// --- Run-time portability injection ---
	// Bin lists the runnable binaries this package exposes as shims.
	Bin []Binary `yaml:"bin"`
	// Env is set in the binary's environment on launch.
	Env map[string]string `yaml:"env,omitempty"`

	// --- Desktop integration ---
	Desktop     []DesktopEntry `yaml:"desktop,omitempty"`
	Icons       []Icon         `yaml:"icons,omitempty"`
	Completions *Completions   `yaml:"completions,omitempty"`
}

// Source describes one downloadable archive or file. Each Source may carry
// its own Update block; Sources[0] is primary and bumps Manifest.Version
// when its upstream tag advances.
type Source struct {
	Name   string        `yaml:"name,omitempty"`
	URL    string        `yaml:"url"`
	File   string        `yaml:"file,omitempty"`
	SHA256 string        `yaml:"sha256,omitempty"`
	SHA512 string        `yaml:"sha512,omitempty"`
	Size   int64         `yaml:"size,omitempty"`
	Update *UpdateConfig `yaml:"update,omitempty"`
}

// Binary is one runnable file the package exposes as a shim.
type Binary struct {
	Name string   `yaml:"name"`
	Path string   `yaml:"path"`
	Args []string `yaml:"args,omitempty"`
}

// Icon is a desktop-environment icon to install.
type Icon struct {
	Src  string `yaml:"src"`
	Name string `yaml:"name"`
	Size string `yaml:"size,omitempty"`
}

// DesktopEntry generates an XDG .desktop file for the package.
type DesktopEntry struct {
	ID             string   `yaml:"id"`
	Name           string   `yaml:"name"`
	GenericName    string   `yaml:"genericName,omitempty"`
	Comment        string   `yaml:"comment,omitempty"`
	Exec           string   `yaml:"exec"`
	Icon           string   `yaml:"icon,omitempty"`
	Type           string   `yaml:"type,omitempty"`
	Categories     []string `yaml:"categories,omitempty"`
	MimeTypes      []string `yaml:"mimeType,omitempty"`
	Keywords       []string `yaml:"keywords,omitempty"`
	Terminal       bool     `yaml:"terminal,omitempty"`
	NoDisplay      bool     `yaml:"noDisplay,omitempty"`
	StartupNotify  *bool    `yaml:"startupNotify,omitempty"`
	StartupWMClass string   `yaml:"startupWMClass,omitempty"`
	Actions        []Action `yaml:"actions,omitempty"`
}

// Action is a secondary launch entry inside a desktop file.
type Action struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	Exec string `yaml:"exec,omitempty"`
}

// Completions points at shell-completion files inside the installed package.
type Completions struct {
	Bash string `yaml:"bash,omitempty"`
	Zsh  string `yaml:"zsh,omitempty"`
	Fish string `yaml:"fish,omitempty"`
}

// UpdateConfig describes how the checker discovers a new version for one
// source. Per-source rather than per-manifest so a package with several
// download artefacts can poll each independently.
type UpdateConfig struct {
	Type           string `yaml:"type"`
	Repo           string `yaml:"repo,omitempty"`
	Asset          string `yaml:"asset,omitempty"`
	Root           string `yaml:"root,omitempty"`
	Dist           string `yaml:"dist,omitempty"`
	Component      string `yaml:"component,omitempty"`
	PackageName    string `yaml:"package-name,omitempty"`
	URL            string `yaml:"url,omitempty"`
	TagQuery       string `yaml:"tag-query,omitempty"`
	VersionQuery   string `yaml:"version-query,omitempty"`
	URLQuery       string `yaml:"url-query,omitempty"`
	VersionPattern string `yaml:"version-pattern,omitempty"`
	TagPattern     string `yaml:"tag-pattern,omitempty"`
	URLTemplate    string `yaml:"url-template,omitempty"`
	HashURL        string `yaml:"hash-url,omitempty"`
	HashQuery      string `yaml:"hash-query,omitempty"`
	// Distribution selects the JDK vendor for the foojay checker (e.g. "temurin").
	Distribution string `yaml:"distribution,omitempty"`
}
