package main

import (
	"fmt"

	"github.com/cristatus/bunny/internal/paths"
)

// InitCmd prints the shell snippet that puts bunny's shim dir on PATH, plus
// $fpath for zsh completions. It is pure: `bunny setup` wires it into the rc
// as an eval, so it runs on every shell start and must not have side effects.
//
// Under XDG that is nearly all it does, since desktop entries and bash/fish
// completions go where the system already looks. A single-root install keeps
// those inside the root, so the snippet also sets XDG_DATA_DIRS, and re-exports
// $BUNNY_HOME so the root survives into shells that were not the one setup ran
// in.
type InitCmd struct {
	Shell string `arg:"" optional:"" enum:"bash,zsh,fish" default:"bash" help:"Shell type (bash, zsh, or fish)"`
}

func (c *InitCmd) Run(a *App) error {
	fmt.Print(initSnippet(a.Paths, c.Shell))
	return nil
}

// initSnippet returns the dedup-guarded shell setup for shell. Each prepend is
// guarded so re-evaluation (or values already inherited from the session, e.g.
// environment.d) does not stack duplicates.
func initSnippet(p *paths.Paths, shell string) string {
	bin, share := p.Bin(), p.Share()
	switch shell {
	case "fish":
		pathGuard := fmt.Sprintf("contains -- %[1]s $PATH; or set -gx PATH %[1]s $PATH\n", bin)
		if p.XDG() {
			return pathGuard
		}
		return rootExport(p, "test -n \"$%[1]s\"; or set -gx %[1]s %[2]s\n") +
			pathGuard +
			fmt.Sprintf(`set -q XDG_DATA_DIRS[1]; or set -gx XDG_DATA_DIRS /usr/local/share:/usr/share
if not string match -q -- "*:%[1]s:*" ":$XDG_DATA_DIRS:"
    set -gx XDG_DATA_DIRS %[1]s:$XDG_DATA_DIRS
end
`, share)
	case "zsh":
		// Add bunny's completions dir to fpath. If compinit already ran (say
		// oh-my-zsh ran it before this snippet), re-run it so it scans the new
		// dir and loads every package's completion there, not just bunny's,
		// honoring each file's #compdef tag. zsh is the one shell with no
		// conventional user site-functions dir, so this applies to both layouts.
		return posixGuards(p) +
			fmt.Sprintf("(( ${fpath[(Ie)%[1]s]} )) || fpath=(%[1]s $fpath)\n", p.ZshCompletions()) +
			"(( $+functions[compdef] )) && { autoload -Uz compinit && compinit -i }\n"
	default:
		return posixGuards(p)
	}
}

// posixGuards is the bash/zsh-shared dedup-guarded PATH prepend, plus the
// $BUNNY_HOME re-export and XDG_DATA_DIRS prepend a single-root install needs so
// the layout resolves and the desktop can find entries and icons inside its root.
func posixGuards(p *paths.Paths) string {
	pathGuard := fmt.Sprintf(`case ":$PATH:" in
    *":%[1]s:"*) ;;
    *) export PATH="%[1]s:$PATH" ;;
esac
`, p.Bin())
	if p.XDG() {
		return pathGuard
	}
	return rootExport(p, "export %[1]s=\"${%[1]s:-%[2]s}\"\n") +
		pathGuard +
		fmt.Sprintf(`case ":${XDG_DATA_DIRS:-}:" in
    *":%[1]s:"*) ;;
    *) export XDG_DATA_DIRS="%[1]s:${XDG_DATA_DIRS:-/usr/local/share:/usr/share}" ;;
esac
`, p.Share())
}

// rootExport formats the assignment that re-establishes $BUNNY_HOME, given a
// shell-specific template taking the variable name as %[1]s and the root as
// %[2]s. Every invocation resolves the layout from this variable, shims
// included, so a single-root install that only put its bin dir on PATH would
// leave those shims reading the XDG layout instead. The template assigns only
// when the variable is unset or empty, matching what paths.Resolve treats as
// absent and leaving a deliberate override in place.
func rootExport(p *paths.Paths, template string) string {
	return fmt.Sprintf(template, paths.EnvHome, p.Root)
}
