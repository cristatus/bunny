package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/fsutil"
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
	if a.local != nil {
		if pkgs, err := a.local.List(); err == nil {
			add(pkgs)
		}
	}
	if a.remote != nil {
		if pkgs, err := a.remote.ListCached(); err == nil {
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

// completionCapabilities returns the distinct capabilities provided by
// installed packages, sorted — for `pin`/`unpin` argument completion.
func (a *App) completionCapabilities() []string {
	seen := map[string]bool{}
	var caps []string
	for _, id := range a.State.Installed() {
		if c := a.State.Packages[id].Provides; c != "" && !seen[c] {
			seen[c] = true
			caps = append(caps, c)
		}
	}
	sort.Strings(caps)
	return caps
}

// completionCommands returns every shimmed command name (manifest bin: shims
// and runtime global tools), sorted and deduped — for `which` completion.
func (a *App) completionCommands() []string {
	seen := map[string]bool{}
	var names []string
	for name := range a.State.Commands {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for name := range a.State.GlobalCommands {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// completionCategories returns the distinct package categories in the local
// catalog, sorted — for `list --category` completion.
func (a *App) completionCategories() []string {
	seen := map[string]bool{}
	var cats []string
	for _, p := range a.catalogPackages() {
		if p.Category != "" && !seen[p.Category] {
			seen[p.Category] = true
			cats = append(cats, p.Category)
		}
	}
	sort.Strings(cats)
	return cats
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
	for _, id := range ids {
		fmt.Println(id)
	}
	return nil
}

// CompleteCategoriesCmd is the hidden helper for `list --category` completion.
type CompleteCategoriesCmd struct{}

func (c *CompleteCategoriesCmd) Run(a *App) error {
	for _, cat := range a.completionCategories() {
		fmt.Println(cat)
	}
	return nil
}

// CompleteCapabilitiesCmd is the hidden helper for `pin`/`unpin` completion.
type CompleteCapabilitiesCmd struct{}

func (c *CompleteCapabilitiesCmd) Run(a *App) error {
	for _, cap := range a.completionCapabilities() {
		fmt.Println(cap)
	}
	return nil
}

// CompleteCommandsCmd is the hidden helper for `which` completion.
type CompleteCommandsCmd struct{}

func (c *CompleteCommandsCmd) Run(a *App) error {
	for _, name := range a.completionCommands() {
		fmt.Println(name)
	}
	return nil
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
	"install", "uninstall", "list", "info", "search", "use", "pin", "unpin", "which", "run",
	"update", "doctor", "init", "setup", "clean", "reshim",
	"toolchains", "dev", "completion",
}

// completionGlobalFlags are the top-level flags accepted before any subcommand
// (from the CLI struct in main.go); bash/zsh embed them via __GLOBALS__.
var completionGlobalFlags = []string{"--help", "--log-level", "--version"}

// completionLogLevels are the values --log-level accepts (mirrors the enum on
// CLI.LogLevel in main.go); scripts embed them via __LOGLEVELS__.
var completionLogLevels = []string{"debug", "info", "warn", "error"}

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
            --log-level|-l|--category|-c|--command|--shell) (( i++ )); continue ;;
            -*) continue ;;
        esac
        if [[ -z "$sub" ]]; then sub="$w"; else operand="$w"; break; fi
    done

    # Value completion for the flag immediately before the cursor.
    case "$prev" in
        --log-level|-l) COMPREPLY=( $(compgen -W "__LOGLEVELS__" -- "$cur") ); return ;;
        --category)     COMPREPLY=( $(compgen -W "$(bunny complete-categories 2>/dev/null)" -- "$cur") ); return ;;
        -c) [[ "$sub" == list ]] && { COMPREPLY=( $(compgen -W "$(bunny complete-categories 2>/dev/null)" -- "$cur") ); return; } ;;
        --shell)        COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") ); return ;;
    esac

    # Completing a flag: global flags (accepted anywhere) plus the subcommand's own.
    if [[ "$cur" == -* ]]; then
        local flags="__GLOBALS__"
        case "$sub" in
            install)      flags="$flags --force" ;;
            uninstall)    flags="$flags --purge" ;;
            list)         flags="$flags --category --remote" ;;
            run)          flags="$flags --command" ;;
            setup)        flags="$flags --shell" ;;
            update)       flags="$flags --all --apply" ;;
            clean)        flags="$flags --all" ;;
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
            COMPREPLY=( $(compgen -W "update" -- "$cur") )
        fi
        return
    fi

    # Operand already given → nothing more (run passthrough; single-operand cmds).
    [[ -n "$operand" ]] && return

    case "$sub" in
        install|info|search)                     COMPREPLY=( $(compgen -W "$(bunny complete-ids 2>/dev/null)" -- "$cur") ) ;;
        use)                                      COMPREPLY=( $(compgen -W "$(bunny complete-ids --providers 2>/dev/null)" -- "$cur") ) ;;
        pin|unpin)                                COMPREPLY=( $(compgen -W "$(bunny complete-capabilities 2>/dev/null)" -- "$cur") ) ;;
        which)                                    COMPREPLY=( $(compgen -W "$(bunny complete-commands 2>/dev/null)" -- "$cur") ) ;;
        uninstall|update|clean|reshim|run)        COMPREPLY=( $(compgen -W "$(bunny complete-ids --installed 2>/dev/null)" -- "$cur") ) ;;
        init|completion)                          COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") ) ;;
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
        --log-level|-l|--category|-c|--command|--shell) (( i++ )); continue ;;
        -*) continue ;;
    esac
    if [[ -z $sub ]]; then sub=$w; else operand=$w; break; fi
done

# Value completion for the flag before the cursor.
case $prev in
    --log-level|-l) compadd -- __LOGLEVELS__; return ;;
    --category) compadd -- ${(f)"$(bunny complete-categories 2>/dev/null)"}; return ;;
    -c) [[ $sub == list ]] && { compadd -- ${(f)"$(bunny complete-categories 2>/dev/null)"}; return } ;;
    --shell) compadd -- bash zsh fish; return ;;
esac

# Completing a flag: globals (anywhere) plus the subcommand's own.
if [[ $cur == -* ]]; then
    local -a flags
    flags=(__GLOBALS__)
    case $sub in
        install) flags+=(--force) ;;
        uninstall) flags+=(--purge) ;;
        list) flags+=(--category --remote) ;;
        run) flags+=(--command) ;;
        setup) flags+=(--shell) ;;
        update) flags+=(--all --apply) ;;
        clean) flags+=(--all) ;;
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
        compadd -- update
    fi
    return
fi

[[ -n $operand ]] && return

case $sub in
    install|info|search) compadd -- ${(f)"$(bunny complete-ids 2>/dev/null)"} ;;
    use) compadd -- ${(f)"$(bunny complete-ids --providers 2>/dev/null)"} ;;
    pin|unpin) compadd -- ${(f)"$(bunny complete-capabilities 2>/dev/null)"} ;;
    which) compadd -- ${(f)"$(bunny complete-commands 2>/dev/null)"} ;;
    uninstall|update|clean|reshim|run) compadd -- ${(f)"$(bunny complete-ids --installed 2>/dev/null)"} ;;
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
function __bunny_categories
    bunny complete-categories 2>/dev/null
end
function __bunny_capabilities
    bunny complete-capabilities 2>/dev/null
end
function __bunny_commands
    bunny complete-commands 2>/dev/null
end
complete -c bunny -f -n __fish_use_subcommand -a '__SUBCMDS__'
# global flags — accepted anywhere (no subcommand condition)
complete -c bunny -l help -d 'Show help'
complete -c bunny -s l -l log-level -r -f -a '__LOGLEVELS__' -d 'Log level'
complete -c bunny -l version -d 'Print version'
# positional operands per subcommand
complete -c bunny -f -n '__fish_seen_subcommand_from install info search' -a '(__bunny_ids)'
complete -c bunny -f -n '__fish_seen_subcommand_from uninstall update clean reshim run; and not __fish_seen_subcommand_from dev' -a '(__bunny_installed_ids)'
complete -c bunny -f -n '__fish_seen_subcommand_from use; and not __fish_seen_subcommand_from dev' -a '(__bunny_provider_ids)'
complete -c bunny -f -n '__fish_seen_subcommand_from pin unpin' -a '(__bunny_capabilities)'
complete -c bunny -f -n '__fish_seen_subcommand_from which' -a '(__bunny_commands)'
complete -c bunny -f -n '__fish_seen_subcommand_from init completion' -a 'bash zsh fish'
complete -c bunny -f -n '__fish_seen_subcommand_from dev; and not __fish_seen_subcommand_from update' -a update
complete -c bunny -f -n '__fish_seen_subcommand_from dev; and __fish_seen_subcommand_from update' -a '(__bunny_ids)'
# per-subcommand flags
complete -c bunny -n '__fish_seen_subcommand_from install' -s f -l force -d 'Force reinstall'
complete -c bunny -n '__fish_seen_subcommand_from uninstall' -l purge -d 'Also remove the per-app data dir'
complete -c bunny -n '__fish_seen_subcommand_from list' -s c -l category -r -f -a '(__bunny_categories)' -d 'Filter by category'
complete -c bunny -n '__fish_seen_subcommand_from list' -l remote -d 'List all packages in the catalog'
complete -c bunny -n '__fish_seen_subcommand_from run' -s c -l command -r -d 'Specific command to run'
complete -c bunny -n '__fish_seen_subcommand_from setup' -l shell -r -f -a 'bash zsh fish' -d 'Shell to configure'
complete -c bunny -n '__fish_seen_subcommand_from update; and not __fish_seen_subcommand_from dev' -l all -d 'Whole catalog'
complete -c bunny -n '__fish_seen_subcommand_from update; and not __fish_seen_subcommand_from dev' -l apply -d 'Apply available updates'
complete -c bunny -n '__fish_seen_subcommand_from clean' -l all -d 'Drop all download cache'
`

// completionFilePath returns the share/ path where shell's completion file for
// bunny is discovered by `bunny init`'s PATH/XDG/fpath wiring.
func completionFilePath(share, shell string) string {
	switch shell {
	case "zsh":
		return filepath.Join(share, "zsh", "site-functions", "_bunny")
	case "fish":
		return filepath.Join(share, "fish", "vendor_completions.d", "bunny.fish")
	default:
		return filepath.Join(share, "bash-completion", "completions", "bunny")
	}
}

// writeCompletionFile writes bunny's own completion script into share/ for the
// given shell, creating parent dirs. Idempotent: skips the write if the file
// already has the current content.
func writeCompletionFile(share, shell string) error {
	dst := completionFilePath(share, shell)
	want := completionScript(shell)
	if cur, err := os.ReadFile(dst); err == nil && string(cur) == want {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return fsutil.WriteFile(dst, []byte(want), 0644)
}
