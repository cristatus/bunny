package main

import "fmt"

// InitCmd prints the shell snippet that puts $BUNNY_HOME/bin on PATH and
// $BUNNY_HOME/share on XDG_DATA_DIRS (plus, for zsh, $fpath for completions).
// It is pure: `bunny setup` wires it into the rc as an eval, so it runs on
// every shell start and must not have side effects.
type InitCmd struct {
	Shell string `arg:"" optional:"" enum:"bash,zsh,fish" default:"bash" help:"Shell type (bash, zsh, or fish)"`
}

func (c *InitCmd) Run(a *App) error {
	fmt.Print(initSnippet(c.Shell, a.Paths.Bin(), a.Paths.Share()))
	return nil
}

// initSnippet returns the dedup-guarded shell setup for shell. Each prepend is
// guarded so re-evaluation (or values already inherited from the session, e.g.
// environment.d) does not stack duplicates.
func initSnippet(shell, bin, share string) string {
	switch shell {
	case "fish":
		return fmt.Sprintf(`contains -- %[1]s $PATH; or set -gx PATH %[1]s $PATH
set -q XDG_DATA_DIRS[1]; or set -gx XDG_DATA_DIRS /usr/local/share:/usr/share
if not string match -q -- "*:%[2]s:*" ":$XDG_DATA_DIRS:"
    set -gx XDG_DATA_DIRS %[2]s:$XDG_DATA_DIRS
end
`, bin, share)
	case "zsh":
		// Add bunny's completions dir to fpath. If compinit has already run
		// (e.g. a framework like oh-my-zsh runs it before this snippet), re-run
		// it so it scans the new dir and loads EVERY package's completion there
		// (glab, code, …), not just bunny's own — honoring each file's #compdef
		// tag. If compinit hasn't run yet, the fpath entry alone suffices.
		return posixGuards(bin, share) +
			fmt.Sprintf("(( ${fpath[(Ie)%[1]s]} )) || fpath=(%[1]s $fpath)\n", share+"/zsh/site-functions") +
			"(( $+functions[compdef] )) && { autoload -Uz compinit && compinit -i }\n"
	default:
		return posixGuards(bin, share)
	}
}

// posixGuards is the bash/zsh-shared PATH + XDG_DATA_DIRS dedup-guarded prepend.
func posixGuards(bin, share string) string {
	return fmt.Sprintf(`case ":$PATH:" in
    *":%[1]s:"*) ;;
    *) export PATH="%[1]s:$PATH" ;;
esac
case ":${XDG_DATA_DIRS:-}:" in
    *":%[2]s:"*) ;;
    *) export XDG_DATA_DIRS="%[2]s:${XDG_DATA_DIRS:-/usr/local/share:/usr/share}" ;;
esac
`, bin, share)
}
