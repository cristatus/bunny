package main

import (
	"strings"
	"testing"
)

func TestInitSnippetDedupGuards(t *testing.T) {
	bin, share := "/h/.bunny/bin", "/h/.bunny/share"

	bash := initSnippet("bash", bin, share)
	if !strings.Contains(bash, `case ":$PATH:" in`) || !strings.Contains(bash, bin) {
		t.Error("bash: missing PATH dedup guard")
	}
	if !strings.Contains(bash, `case ":${XDG_DATA_DIRS:-}:" in`) || !strings.Contains(bash, share) {
		t.Error("bash: missing XDG_DATA_DIRS dedup guard")
	}
	if strings.Contains(bash, "fpath") {
		t.Error("bash: should not set fpath")
	}

	zsh := initSnippet("zsh", bin, share)
	if !strings.Contains(zsh, `case ":$PATH:" in`) {
		t.Error("zsh: missing PATH dedup guard")
	}
	if !strings.Contains(zsh, "fpath=(") || !strings.Contains(zsh, share+"/zsh/site-functions") {
		t.Error("zsh: missing fpath guard")
	}
	// Must also load completions when compinit already ran (e.g. oh-my-zsh runs
	// compinit before this snippet) by re-running compinit — for every package's
	// completion in the dir, not only rely on fpath-before-compinit.
	if !strings.Contains(zsh, "$+functions[compdef]") || !strings.Contains(zsh, "compinit -i") {
		t.Error("zsh: missing post-compinit reload (compinit -i)")
	}

	fish := initSnippet("fish", bin, share)
	if !strings.Contains(fish, "contains -- "+bin+" $PATH") {
		t.Error("fish: missing PATH guard")
	}
	// colon-joined, not space-joined:
	if !strings.Contains(fish, "set -gx XDG_DATA_DIRS "+share+":$XDG_DATA_DIRS") {
		t.Error("fish: XDG_DATA_DIRS must be colon-joined")
	}
}
