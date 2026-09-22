package main

import (
	"fmt"
	"strings"

	"github.com/cristatus/bunny/internal/paths"
)

// InitCmd prints the shell snippet that puts bunny's shim dir on PATH, plus
// $fpath for zsh completions. It must be pure (no side effects): `bunny setup`
// wires it into the rc as an eval, so it runs on every shell start.
//
// Under XDG that's nearly all it does. A single-root install also sets
// XDG_DATA_DIRS (so desktop entries inside the root are found) and re-exports
// $BUNNY_HOME so the root survives into shells that never ran setup.
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
	switch shell {
	case "fish":
		pathGuard := fmt.Sprintf("contains -- %[1]s $PATH; or set -gx PATH %[1]s $PATH\n", fishWord(p.Bin()))
		manGuard := fishManGuard(p)
		if p.XDG() {
			return pathGuard + manGuard
		}
		return rootExport("test -n \"$%[1]s\"; or set -gx %[1]s %[2]s\n", fishWord(p.Root)) +
			pathGuard +
			fmt.Sprintf(`set -q XDG_DATA_DIRS[1]; or set -gx XDG_DATA_DIRS /usr/local/share:/usr/share
if not string match -q -- "*:%[1]s:*" ":$XDG_DATA_DIRS:"
    set -gx XDG_DATA_DIRS %[2]s:$XDG_DATA_DIRS
end
`, fishDQ(p.Share()), fishWord(p.Share())) +
			manGuard
	case "zsh":
		// Add bunny's completions dir to fpath. If compinit already ran (say
		// oh-my-zsh ran it before this snippet), re-run it so it scans the new
		// dir and loads every package's completion there, not just bunny's,
		// honoring each file's #compdef tag. zsh is the one shell with no
		// conventional user site-functions dir, so this applies to both layouts.
		// An if, not `(( … )) && …`: that form leaves status 1 when compinit
		// has not run, and a prompt showing $? then reports an error on every
		// new shell.
		return posixGuards(p) +
			fmt.Sprintf("(( ${fpath[(Ie)%[1]s]} )) || fpath=(%[1]s $fpath)\n", shellWord(p.ZshCompletions())) +
			"if (( $+functions[compdef] )); then autoload -Uz compinit && compinit -i; fi\n"
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
`, dqEscape(p.Bin()))
	manGuard := posixManGuard(p)
	if p.XDG() {
		return pathGuard + manGuard
	}
	// Not "${BUNNY_HOME:-<root>}": bash reads a ' inside a double-quoted
	// ${…:-word} as a quote, so a root with an apostrophe never closes.
	return rootExport("[ -n \"${%[1]s:-}\" ] || export %[1]s=%[2]s\n", shellWord(p.Root)) +
		pathGuard +
		fmt.Sprintf(`case ":${XDG_DATA_DIRS:-}:" in
    *":%[1]s:"*) ;;
    *) export XDG_DATA_DIRS="%[1]s:${XDG_DATA_DIRS:-/usr/local/share:/usr/share}" ;;
esac
`, dqEscape(p.Share())) +
		manGuard
}

// posixManGuard prepends bunny's man root to $MANPATH, in bash/zsh. man(1)
// treats a MANPATH that begins or ends with a colon (or was unset, since
// ${MANPATH:-} then expands to "") as "also search the system defaults here",
// so this never hides pages outside bunny's own — unlike XDG_DATA_DIRS, man
// implementations do not search the XDG data dirs on their own, MANPATH is
// the only lever.
func posixManGuard(p *paths.Paths) string {
	return fmt.Sprintf(`case ":${MANPATH:-}:" in
    *":%[1]s:"*) ;;
    *) export MANPATH="%[1]s:${MANPATH:-}" ;;
esac
`, dqEscape(p.ManPages()))
}

// fishManGuard is posixManGuard's fish equivalent. fish treats MANPATH as a
// path variable it joins with ':' on export, so appending "" as the final
// element reproduces the trailing-colon-means-defaults form when MANPATH was
// previously unset.
func fishManGuard(p *paths.Paths) string {
	return fmt.Sprintf(`if set -q MANPATH[1]
    contains -- %[1]s $MANPATH; or set -gx MANPATH %[1]s $MANPATH
else
    set -gx MANPATH %[1]s ""
end
`, fishWord(p.ManPages()))
}

// rootExport formats the assignment that re-establishes $BUNNY_HOME, given a
// shell-specific template taking the variable name as %[1]s and the root,
// already quoted for that shell, as %[2]s. Every invocation resolves the
// layout from this variable, shims included, so a single-root install that
// only put its bin dir on PATH would leave those shims reading the XDG layout
// instead. The template assigns only when the variable is unset or empty,
// matching what paths.Resolve treats as absent and leaving a deliberate
// override in place.
func rootExport(template, root string) string {
	return fmt.Sprintf(template, paths.EnvHome, root)
}

// The snippet is shell code, so a path in it has to survive the shell's
// parsing: a HOME or $BUNNY_HOME with a space or a quote in it otherwise
// splits PATH or breaks the eval. Each helper leaves a plain path as it is.

// dqEscape escapes s for use inside POSIX double quotes.
func dqEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s)
}

// shellWord renders s as one POSIX shell word, quoting it only if it needs it.
func shellWord(s string) string {
	if plainWord(s) {
		return s
	}
	return shellQuote(s)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// fishWord renders s as one fish word, quoting it only if it needs it.
func fishWord(s string) string {
	if plainWord(s) {
		return s
	}
	return fishQuote(s)
}

// fishQuote single-quotes s for fish, where only \ and ' are special inside.
func fishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(s) + "'"
}

// fishDQ escapes s for use inside fish double quotes.
func fishDQ(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`).Replace(s)
}

func plainWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._+-:@%,=", r)) {
			return false
		}
	}
	return true
}
