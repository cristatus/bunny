package main

import (
	"fmt"

	"github.com/cristatus/bunny/internal/paths"
)

// InitCmd prints the shell snippet that puts bunny's shim dir on PATH (plus,
// for zsh, $fpath for completions). It is pure: `bunny setup` wires it into the
// rc as an eval, so it runs on every shell start and must not have side effects.
//
// Under the XDG layout that is nearly all it has to do, because desktop entries
// and bash/fish completions are written where the system already looks for
// them. A single-root install ($BUNNY_HOME) keeps those files inside the root,
// so the snippet additionally has to put that root on XDG_DATA_DIRS.
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
		out := fmt.Sprintf("contains -- %[1]s $PATH; or set -gx PATH %[1]s $PATH\n", bin)
		if !p.XDG() {
			out += fmt.Sprintf(`set -q XDG_DATA_DIRS[1]; or set -gx XDG_DATA_DIRS /usr/local/share:/usr/share
if not string match -q -- "*:%[1]s:*" ":$XDG_DATA_DIRS:"
    set -gx XDG_DATA_DIRS %[1]s:$XDG_DATA_DIRS
end
`, share)
		}
		return out
	case "zsh":
		// Add bunny's completions dir to fpath. If compinit has already run
		// (e.g. a framework like oh-my-zsh runs it before this snippet), re-run
		// it so it scans the new dir and loads EVERY package's completion there
		// (glab, code, …), not just bunny's own — honoring each file's #compdef
		// tag. If compinit hasn't run yet, the fpath entry alone suffices.
		//
		// zsh is the one shell with no conventional user site-functions dir, so
		// this is needed under both layouts.
		return posixGuards(p) +
			fmt.Sprintf("(( ${fpath[(Ie)%[1]s]} )) || fpath=(%[1]s $fpath)\n", p.ZshCompletions()) +
			"(( $+functions[compdef] )) && { autoload -Uz compinit && compinit -i }\n"
	default:
		return posixGuards(p)
	}
}

// posixGuards is the bash/zsh-shared dedup-guarded PATH prepend, plus the
// XDG_DATA_DIRS prepend a single-root install needs so the desktop can find
// entries and icons stored inside that root.
func posixGuards(p *paths.Paths) string {
	out := fmt.Sprintf(`case ":$PATH:" in
    *":%[1]s:"*) ;;
    *) export PATH="%[1]s:$PATH" ;;
esac
`, p.Bin())
	if p.XDG() {
		return out
	}
	return out + fmt.Sprintf(`case ":${XDG_DATA_DIRS:-}:" in
    *":%[1]s:"*) ;;
    *) export XDG_DATA_DIRS="%[1]s:${XDG_DATA_DIRS:-/usr/local/share:/usr/share}" ;;
esac
`, p.Share())
}
