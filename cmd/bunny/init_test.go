package main

import (
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/paths"
)

// A single-root install keeps desktop entries and completions inside the root,
// so the shell has to be told about it via XDG_DATA_DIRS.
func TestInitSnippetSingleRootDedupGuards(t *testing.T) {
	p := paths.At("/h/.bunny")
	bin, share := p.Bin(), p.Share()

	bash := initSnippet(p, "bash")
	if !strings.Contains(bash, `case ":$PATH:" in`) || !strings.Contains(bash, bin) {
		t.Error("bash: missing PATH dedup guard")
	}
	if !strings.Contains(bash, `case ":${XDG_DATA_DIRS:-}:" in`) || !strings.Contains(bash, share) {
		t.Error("bash: missing XDG_DATA_DIRS dedup guard")
	}
	if strings.Contains(bash, "fpath") {
		t.Error("bash: should not set fpath")
	}

	zsh := initSnippet(p, "zsh")
	if !strings.Contains(zsh, `case ":$PATH:" in`) {
		t.Error("zsh: missing PATH dedup guard")
	}
	if !strings.Contains(zsh, "fpath=(") || !strings.Contains(zsh, p.ZshCompletions()) {
		t.Error("zsh: missing fpath guard")
	}
	// Must also load completions when compinit already ran (e.g. oh-my-zsh runs
	// compinit before this snippet) by re-running compinit — for every package's
	// completion in the dir, not only rely on fpath-before-compinit.
	if !strings.Contains(zsh, "$+functions[compdef]") || !strings.Contains(zsh, "compinit -i") {
		t.Error("zsh: missing post-compinit reload (compinit -i)")
	}

	fish := initSnippet(p, "fish")
	if !strings.Contains(fish, "contains -- "+bin+" $PATH") {
		t.Error("fish: missing PATH guard")
	}
	// colon-joined, not space-joined:
	if !strings.Contains(fish, "set -gx XDG_DATA_DIRS "+share+":$XDG_DATA_DIRS") {
		t.Error("fish: XDG_DATA_DIRS must be colon-joined")
	}
}

// Under XDG, desktop entries and bash/fish completions are already in
// locations the system scans, so the snippet must not touch XDG_DATA_DIRS.
// Shrinking what a user has to paste into their rc is the point of the layout.
func TestInitSnippetXDGDropsDataDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(paths.EnvHome, "")
	for _, v := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, "")
	}
	p, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}

	for _, shell := range []string{"bash", "zsh", "fish"} {
		snippet := initSnippet(p, shell)
		if strings.Contains(snippet, "XDG_DATA_DIRS") {
			t.Errorf("%s: XDG layout should not set XDG_DATA_DIRS:\n%s", shell, snippet)
		}
		if !strings.Contains(snippet, p.Bin()) {
			t.Errorf("%s: missing PATH entry for %s", shell, p.Bin())
		}
	}

	// zsh has no conventional user site-functions dir, so fpath is still needed.
	if zsh := initSnippet(p, "zsh"); !strings.Contains(zsh, p.ZshCompletions()) {
		t.Error("zsh: fpath entry is required under XDG too")
	}
	// bash and fish read their completion dirs natively; nothing to wire up.
	if bash := initSnippet(p, "bash"); strings.Contains(bash, "bash-completion") {
		t.Error("bash: completions are discovered natively under XDG")
	}
}
