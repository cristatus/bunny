package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/manifest"

	"github.com/cristatus/bunny/internal/paths"
)

// catalogPackages returns every package known locally — local manifests plus
// the cached remote index, deduped by id. Local-only: it never fetches, and
// swallows errors, so shell completion never blocks or fails.
func (a *App) catalogPackages() []catalog.PackageInfo {
	seen := map[string]bool{}
	var out []catalog.PackageInfo
	add := func(pkgs []catalog.PackageInfo) {
		for _, p := range pkgs {
			if !seen[p.ID] {
				seen[p.ID] = true
				out = append(out, p)
			}
		}
	}
	for _, e := range a.catalogs {
		if pkgs, err := e.listCached(); err == nil {
			add(pkgs)
		}
	}
	return out
}

// completionIDs returns package IDs for shell completion. installed=true →
// installed packages; otherwise the full local catalog.
func (a *App) completionIDs(installed bool) []string {
	if installed {
		return a.State.Installed() // already sorted
	}
	var ids []string
	for _, p := range a.catalogPackages() {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

// completionProviderIDs returns installed package IDs that provide a capability
// — the only packages `bunny use` can switch the active provider to.
func (a *App) completionProviderIDs() []string {
	var ids []string
	for _, id := range a.State.Installed() { // sorted → result stays sorted
		if a.State.Packages[id].Provides != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// completionCapabilities returns the distinct capabilities known from the
// catalog or installed state, sorted.
func (a *App) completionCapabilities() []string {
	seen := map[string]bool{}
	var caps []string
	for _, pkg := range a.catalogPackages() {
		if pkg.Provides != "" && !seen[pkg.Provides] {
			seen[pkg.Provides] = true
			caps = append(caps, pkg.Provides)
		}
	}
	for _, id := range a.State.Installed() {
		if c := a.State.Packages[id].Provides; c != "" && !seen[c] {
			seen[c] = true
			caps = append(caps, c)
		}
	}
	sort.Strings(caps)
	return caps
}

// completionTags returns the distinct tags in the local catalog, sorted, for
// `list --tag` completion.
func (a *App) completionTags() []string {
	seen := map[string]bool{}
	for _, p := range a.catalogPackages() {
		for _, tag := range p.Tags {
			seen[tag] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// completionCatalogs returns the catalog checkouts `bunny dev --catalog`
// accepts. Remotes are left out because the dev commands rewrite a checkout,
// and a checkout that is not on disk with it: either would complete to a name
// the command then rejects.
func (a *App) completionCatalogs() []string {
	var names []string
	for _, e := range a.catalogs {
		if e.local != nil && e.present {
			names = append(names, e.src.Name)
		}
	}
	return names
}

// CompleteIDsCmd is the hidden helper the generated completion scripts call to
// list package IDs. --installed restricts to installed packages; --providers to
// installed packages that provide a capability (the valid `bunny use` targets).
type CompleteIDsCmd struct {
	Installed bool `help:"Only installed packages"`
	Providers bool `help:"Only installed packages that provide a capability"`
}

func (c *CompleteIDsCmd) Run(a *App) error {
	ids := a.completionIDs(c.Installed)
	if c.Providers {
		ids = a.completionProviderIDs()
	}
	printLines(ids)
	return nil
}

// CompleteTagsCmd is the hidden helper for `list --tag` completion.
type CompleteTagsCmd struct{}

func (c *CompleteTagsCmd) Run(a *App) error {
	printLines(a.completionTags())
	return nil
}

// CompleteCapabilitiesCmd is the hidden helper for `pin`/`unpin` completion.
type CompleteCapabilitiesCmd struct{}

func (c *CompleteCapabilitiesCmd) Run(a *App) error {
	printLines(a.completionCapabilities())
	return nil
}

// CompleteCatalogsCmd is the hidden helper for `dev --catalog` completion.
type CompleteCatalogsCmd struct{}

func (c *CompleteCatalogsCmd) Run(a *App) error {
	printLines(a.completionCatalogs())
	return nil
}

func printLines(lines []string) {
	for _, l := range lines {
		fmt.Println(l)
	}
}

// CompletionCmd prints the static shell-completion script for the given shell.
type CompletionCmd struct {
	Shell string `arg:"" optional:"" enum:"bash,zsh,fish" default:"bash" help:"Shell type (bash, zsh, or fish)"`
}

func (c *CompletionCmd) Run(_ *App) error {
	fmt.Print(completionScript(c.Shell))
	return nil
}

// completionSubcommands is the single source of truth for the non-hidden
// subcommands completion offers; each script embeds it via the __SUBCMDS__
// placeholder. Keep in sync with the CLI struct in main.go — the hidden
// complete-ids command is intentionally excluded.
var completionSubcommands = []string{
	"install", "uninstall", "list", "info", "search", "use", "pin", "unpin", "run",
	"sandbox", "update", "doctor", "init", "setup", "clean", "reshim",
	"toolchains", "dev", "completion",
}

// completionGlobalFlags are the top-level flags accepted before any subcommand
// (from the CLI struct in main.go); bash/zsh embed them via __GLOBALS__.
var completionGlobalFlags = []string{"--help", "--log-level", "--no-progress", "--version"}

// completionLogLevels are the values --log-level accepts (mirrors the enum on
// CLI.LogLevel in main.go); scripts embed them via __LOGLEVELS__.
var completionLogLevels = []string{"debug", "info", "warn", "error"}

// completionFilters are the flags catalogFilter contributes to `list` and
// `search` — one list, because the two commands take one filter set; scripts
// embed them via __FILTERS__.
var completionFilters = []string{"--tag", "--capability", "--kind"}

// completionScript returns the completion script for shell. Subcommands and
// flags are static; package-ID arguments call `bunny complete-ids` (catalog)
// or `bunny complete-ids --installed`, so IDs stay current without regenerating.
func completionScript(shell string) string {
	var raw string
	switch shell {
	case "zsh":
		raw = zshCompletion
	case "fish":
		raw = fishCompletion
	default:
		raw = bashCompletion
	}
	raw = strings.ReplaceAll(raw, "__SUBCMDS__", strings.Join(completionSubcommands, " "))
	raw = strings.ReplaceAll(raw, "__GLOBALS__", strings.Join(completionGlobalFlags, " "))
	raw = strings.ReplaceAll(raw, "__FILTERS__", strings.Join(completionFilters, " "))
	// --kind's values are the install kinds themselves, not a list repeated here.
	raw = strings.ReplaceAll(raw, "__KINDS__", strings.Join(manifest.Kinds, " "))
	return strings.ReplaceAll(raw, "__LOGLEVELS__", strings.Join(completionLogLevels, " "))
}

const bashCompletion = `_bunny() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local prev="${COMP_WORDS[COMP_CWORD-1]}"

    # Walk the words before the cursor: find the subcommand (first non-flag
    # word) and whether its operand is already present. Value-taking flags
    # consume the following word so it is not mistaken for the subcommand.
    local sub="" operand="" w i
    for (( i=1; i < COMP_CWORD; i++ )); do
        w="${COMP_WORDS[i]}"
        case "$w" in
            --log-level|-l|--tag|-t|-c|--capability|--kind|--catalog|--command|--shell) (( i++ )); continue ;;
            -*) continue ;;
        esac
        if [[ -z "$sub" ]]; then sub="$w"; else operand="$w"; break; fi
    done

    # Value completion for the flag immediately before the cursor.
    case "$prev" in
        --log-level|-l) COMPREPLY=( $(compgen -W "__LOGLEVELS__" -- "$cur") ); return ;;
        --tag)     COMPREPLY=( $(compgen -W "$(bunny complete-tags 2>/dev/null)" -- "$cur") ); return ;;
        --capability)   COMPREPLY=( $(compgen -W "$(bunny complete-capabilities 2>/dev/null)" -- "$cur") ); return ;;
        --catalog) COMPREPLY=( $(compgen -W "$(bunny complete-catalogs 2>/dev/null)" -- "$cur") ); return ;;
        --kind)    COMPREPLY=( $(compgen -W "__KINDS__" -- "$cur") ); return ;;
        -t) [[ "$sub" == list || "$sub" == search ]] && { COMPREPLY=( $(compgen -W "$(bunny complete-tags 2>/dev/null)" -- "$cur") ); return; } ;;
        --shell)        COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") ); return ;;
    esac

    # Completing a flag: global flags (accepted anywhere) plus the subcommand's own.
    if [[ "$cur" == -* ]]; then
        local flags="__GLOBALS__"
        case "$sub" in
            install)      flags="$flags --force" ;;
            uninstall)    flags="$flags --purge --yes" ;;
            list)         flags="$flags __FILTERS__ --active" ;;
            search)       flags="$flags __FILTERS__ --installed --available" ;;
            run|sandbox)  flags="$flags --command" ;;
            setup)        flags="$flags --shell" ;;
            update)       flags="$flags --apply" ;;
            clean)        flags="$flags --all" ;;
            dev)          flags="$flags --catalog" ;;
        esac
        COMPREPLY=( $(compgen -W "$flags" -- "$cur") )
        return
    fi

    # No subcommand yet → complete subcommand names.
    if [[ -z "$sub" ]]; then
        COMPREPLY=( $(compgen -W "__SUBCMDS__" -- "$cur") )
        return
    fi

    # dev has its own subcommand, then a catalog id.
    if [[ "$sub" == dev ]]; then
        if [[ "$operand" == update ]]; then
            COMPREPLY=( $(compgen -W "$(bunny complete-ids 2>/dev/null)" -- "$cur") )
        elif [[ -z "$operand" ]]; then
            COMPREPLY=( $(compgen -W "update validate" -- "$cur") )
        fi
        return
    fi

    # install/uninstall take multiple ids → keep completing regardless of how
    # many operands are already present (don't stop after the first).
    case "$sub" in
        install)   COMPREPLY=( $(compgen -W "$(bunny complete-ids 2>/dev/null)" -- "$cur") ); return ;;
        uninstall) COMPREPLY=( $(compgen -W "$(bunny complete-ids --installed 2>/dev/null)" -- "$cur") ); return ;;
    esac

    # Single-operand commands: once an operand is present, nothing more.
    [[ -n "$operand" ]] && return

    case "$sub" in
        info|search)              COMPREPLY=( $(compgen -W "$(bunny complete-ids 2>/dev/null)" -- "$cur") ) ;;
        use)                      COMPREPLY=( $(compgen -W "$(bunny complete-ids --providers 2>/dev/null)" -- "$cur") ) ;;
        pin|unpin)                COMPREPLY=( $(compgen -W "$(bunny complete-capabilities 2>/dev/null)" -- "$cur") ) ;;
        update|clean|run|sandbox) COMPREPLY=( $(compgen -W "$(bunny complete-ids --installed 2>/dev/null)" -- "$cur") ) ;;
        reshim)                   COMPREPLY=( $(compgen -W "$(bunny complete-ids --installed 2>/dev/null) $(bunny complete-capabilities 2>/dev/null)" -- "$cur") ) ;;
        init|completion)          COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") ) ;;
    esac
}
complete -F _bunny bunny
`

const zshCompletion = `#compdef bunny
local cur=${words[CURRENT]} prev=${words[CURRENT-1]}
local -a subcmds
subcmds=(__SUBCMDS__)

# Find the subcommand and whether its operand is present; value-taking flags
# consume the following word.
local sub="" operand="" w i
for (( i = 2; i < CURRENT; i++ )); do
    w=${words[i]}
    case $w in
        --log-level|-l|--tag|-t|-c|--capability|--kind|--catalog|--command|--shell) (( i++ )); continue ;;
        -*) continue ;;
    esac
    if [[ -z $sub ]]; then sub=$w; else operand=$w; break; fi
done

# Value completion for the flag before the cursor.
case $prev in
    --log-level|-l) compadd -- __LOGLEVELS__; return ;;
    --tag) compadd -- ${(f)"$(bunny complete-tags 2>/dev/null)"}; return ;;
    --capability) compadd -- ${(f)"$(bunny complete-capabilities 2>/dev/null)"}; return ;;
    --catalog) compadd -- ${(f)"$(bunny complete-catalogs 2>/dev/null)"}; return ;;
    --kind) compadd -- __KINDS__; return ;;
    -t) [[ $sub == list || $sub == search ]] && { compadd -- ${(f)"$(bunny complete-tags 2>/dev/null)"}; return } ;;
    --shell) compadd -- bash zsh fish; return ;;
esac

# Completing a flag: globals (anywhere) plus the subcommand's own.
if [[ $cur == -* ]]; then
    local -a flags
    flags=(__GLOBALS__)
    case $sub in
        install) flags+=(--force) ;;
        uninstall) flags+=(--purge --yes) ;;
        list) flags+=(__FILTERS__ --active) ;;
        search) flags+=(__FILTERS__ --installed --available) ;;
        run|sandbox) flags+=(--command) ;;
        setup) flags+=(--shell) ;;
        update) flags+=(--apply) ;;
        clean) flags+=(--all) ;;
        dev) flags+=(--catalog) ;;
    esac
    compadd -- $flags
    return
fi

if [[ -z $sub ]]; then
    compadd -- $subcmds
    return
fi

if [[ $sub == dev ]]; then
    if [[ $operand == update ]]; then
        compadd -- ${(f)"$(bunny complete-ids 2>/dev/null)"}
    elif [[ -z $operand ]]; then
        compadd -- update validate
    fi
    return
fi

# install/uninstall take multiple ids → keep completing regardless of operands.
case $sub in
    install) compadd -- ${(f)"$(bunny complete-ids 2>/dev/null)"}; return ;;
    uninstall) compadd -- ${(f)"$(bunny complete-ids --installed 2>/dev/null)"}; return ;;
esac

[[ -n $operand ]] && return

case $sub in
    info|search) compadd -- ${(f)"$(bunny complete-ids 2>/dev/null)"} ;;
    use) compadd -- ${(f)"$(bunny complete-ids --providers 2>/dev/null)"} ;;
    pin|unpin) compadd -- ${(f)"$(bunny complete-capabilities 2>/dev/null)"} ;;
    update|clean|run|sandbox) compadd -- ${(f)"$(bunny complete-ids --installed 2>/dev/null)"} ;;
    reshim) compadd -- ${(f)"$(bunny complete-ids --installed 2>/dev/null)"} ${(f)"$(bunny complete-capabilities 2>/dev/null)"} ;;
    init|completion) compadd -- bash zsh fish ;;
esac
`

const fishCompletion = `function __bunny_ids
    bunny complete-ids 2>/dev/null
end
function __bunny_installed_ids
    bunny complete-ids --installed 2>/dev/null
end
function __bunny_provider_ids
    bunny complete-ids --providers 2>/dev/null
end
function __bunny_tags
    bunny complete-tags 2>/dev/null
end
function __bunny_capabilities
    bunny complete-capabilities 2>/dev/null
end

function __bunny_catalogs
    bunny complete-catalogs 2>/dev/null
end
complete -c bunny -f -n __fish_use_subcommand -a '__SUBCMDS__'
# global flags — accepted anywhere (no subcommand condition)
complete -c bunny -l help -d 'Show help'
complete -c bunny -s l -l log-level -r -f -a '__LOGLEVELS__' -d 'Log level'
complete -c bunny -l no-progress -d 'Disable interactive progress output'
complete -c bunny -l version -d 'Print version'
# positional operands per subcommand
complete -c bunny -f -n '__fish_seen_subcommand_from install info search' -a '(__bunny_ids)'
complete -c bunny -f -n '__fish_seen_subcommand_from uninstall update clean reshim run sandbox; and not __fish_seen_subcommand_from dev' -a '(__bunny_installed_ids)'
complete -c bunny -f -n '__fish_seen_subcommand_from use; and not __fish_seen_subcommand_from dev' -a '(__bunny_provider_ids)'
complete -c bunny -f -n '__fish_seen_subcommand_from pin unpin' -a '(__bunny_capabilities)'
complete -c bunny -f -n '__fish_seen_subcommand_from init completion' -a 'bash zsh fish'
complete -c bunny -f -n '__fish_seen_subcommand_from dev; and not __fish_seen_subcommand_from update validate' -a 'update validate'
complete -c bunny -f -n '__fish_seen_subcommand_from dev; and __fish_seen_subcommand_from update' -a '(__bunny_ids)'
# per-subcommand flags
complete -c bunny -n '__fish_seen_subcommand_from install' -s f -l force -d 'Force reinstall'
complete -c bunny -n '__fish_seen_subcommand_from uninstall' -l purge -d "Also remove the package's data dir"
complete -c bunny -n '__fish_seen_subcommand_from uninstall' -s y -l yes -d 'Skip the --purge confirmation prompt'
complete -c bunny -n '__fish_seen_subcommand_from list search' -s t -l tag -r -f -a '(__bunny_tags)' -d 'Filter by tag'
complete -c bunny -n '__fish_seen_subcommand_from list search' -l capability -r -f -a '(__bunny_capabilities)' -d 'Filter by provided capability'
complete -c bunny -n '__fish_seen_subcommand_from list search' -l kind -r -f -a '__KINDS__' -d 'Filter by install kind'
complete -c bunny -n '__fish_seen_subcommand_from list' -l active -d 'Show only active providers'
complete -c bunny -n '__fish_seen_subcommand_from search' -l installed -d 'Show only installed packages'
complete -c bunny -n '__fish_seen_subcommand_from search' -l available -d 'Show only packages that are not installed'
complete -c bunny -n '__fish_seen_subcommand_from run' -s c -l command -r -d 'Specific command to run'
complete -c bunny -n '__fish_seen_subcommand_from sandbox' -s c -l command -r -d 'Specific command to run'
complete -c bunny -f -n '__fish_seen_subcommand_from reshim' -a '(__bunny_capabilities)'
complete -c bunny -n '__fish_seen_subcommand_from setup' -l shell -r -f -a 'bash zsh fish' -d 'Shell to configure'
complete -c bunny -n '__fish_seen_subcommand_from update; and not __fish_seen_subcommand_from dev' -l apply -d 'Apply available updates'
complete -c bunny -n '__fish_seen_subcommand_from clean' -l all -d 'Drop all download cache'
complete -c bunny -n '__fish_seen_subcommand_from dev' -l catalog -r -f -a '(__bunny_catalogs)' -d 'Catalog checkout to act on'
`

// completionFilePath returns where shell looks for bunny's own completion
// file. The per-shell directories come from paths, which is also what installs
// each package's completions, so the two cannot drift.
func completionFilePath(p *paths.Paths, shell string) string {
	switch shell {
	case "zsh":
		return filepath.Join(p.ZshCompletions(), "_bunny")
	case "fish":
		return filepath.Join(p.FishCompletions(), "bunny.fish")
	default:
		return filepath.Join(p.BashCompletions(), "bunny")
	}
}

// writeCompletionFile writes bunny's own completion script for the given
// shell, creating parent dirs. Idempotent: skips the write if the file already
// has the current content.
func writeCompletionFile(p *paths.Paths, shell string) error {
	dst := completionFilePath(p, shell)
	want := completionScript(shell)
	if cur, err := os.ReadFile(dst); err == nil && string(cur) == want {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return fsutil.WriteFile(dst, []byte(want), 0644)
}
