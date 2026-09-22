package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// Every invocation resolves the layout from $BUNNY_HOME, shims included, so the
// snippet has to re-establish it. A snippet that only put the bin dir on PATH
// would give a fresh shell bunny's binary and shims while they all read the XDG
// layout, where nothing is installed.
func TestInitSnippetSingleRootReexportsRoot(t *testing.T) {
	p := paths.At("/h/.bunny")
	for _, c := range []struct{ shell, want string }{
		{"bash", `[ -n "${BUNNY_HOME:-}" ] || export BUNNY_HOME=/h/.bunny`},
		{"zsh", `[ -n "${BUNNY_HOME:-}" ] || export BUNNY_HOME=/h/.bunny`},
		{"fish", `test -n "$BUNNY_HOME"; or set -gx BUNNY_HOME /h/.bunny`},
	} {
		snippet := initSnippet(p, c.shell)
		if !strings.Contains(snippet, c.want) {
			t.Errorf("%s: want %q in:\n%s", c.shell, c.want, snippet)
		}
		// The root has to be set before anything derived from it is used.
		if i, j := strings.Index(snippet, "BUNNY_HOME"), strings.Index(snippet, p.Bin()); i > j {
			t.Errorf("%s: root must be exported before the PATH prepend:\n%s", c.shell, snippet)
		}
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
		// Exporting the root is what selects the single-root layout, so the XDG
		// snippet must not mention it at all.
		if strings.Contains(snippet, paths.EnvHome) {
			t.Errorf("%s: XDG layout should not set %s:\n%s", shell, paths.EnvHome, snippet)
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

// Unlike desktop entries and icons, man implementations do not search the
// XDG data dirs on their own — MANPATH is the only lever, so the guard must
// be present under both layouts, not gated on single-root the way
// XDG_DATA_DIRS is.
func TestInitSnippetSetsManpathInEveryLayout(t *testing.T) {
	for _, p := range []*paths.Paths{
		paths.At("/h/.bunny"),
		func() *paths.Paths {
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
			return p
		}(),
	} {
		man := p.ManPages()
		if bash := initSnippet(p, "bash"); !strings.Contains(bash, `case ":${MANPATH:-}:" in`) || !strings.Contains(bash, man) {
			t.Errorf("bash: missing MANPATH dedup guard for %s:\n%s", man, bash)
		}
		if zsh := initSnippet(p, "zsh"); !strings.Contains(zsh, `case ":${MANPATH:-}:" in`) || !strings.Contains(zsh, man) {
			t.Errorf("zsh: missing MANPATH dedup guard for %s:\n%s", man, zsh)
		}
		if fish := initSnippet(p, "fish"); !strings.Contains(fish, "set -gx MANPATH "+man) {
			t.Errorf("fish: missing MANPATH guard for %s:\n%s", man, fish)
		}
	}
}

// The snippet and the rc line are shell code with paths in them. A HOME or
// $BUNNY_HOME holding a space or a quote split PATH or broke the eval, and
// zsh's compinit guard left status 1 behind when compinit had not run, which a
// prompt showing $? reports on every new shell. Run both for real.
func TestInitSnippetSurvivesTheShell(t *testing.T) {
	root := filepath.Join(t.TempDir(), `it's a $dir "x"`)
	p := paths.At(root)
	for _, shell := range []string{"bash", "zsh"} {
		exe, err := exec.LookPath(shell)
		if err != nil {
			t.Logf("%s not installed", shell)
			continue
		}
		args := []string{"-c", initSnippet(p, shell) + `rc=$?; printf '%s\n' "$rc" "$BUNNY_HOME" "${PATH%%:*}"`}
		if shell == "zsh" {
			args = append([]string{"-f"}, args...)
		}
		cmd := exec.Command(exe, args...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", shell, err, out)
		}
		got := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		if want := []string{"0", root, p.Bin()}; !slices.Equal(got, want) {
			t.Errorf("%s: status, BUNNY_HOME, first PATH entry = %q, want %q", shell, got, want)
		}

		// The rc line runs bunny from its own path, with the root pinned, and
		// evals what it prints.
		bunny := filepath.Join(root, "bin", "bunny")
		seen := filepath.Join(filepath.Dir(bunny), "seen")
		os.MkdirAll(filepath.Dir(bunny), 0755)
		os.WriteFile(bunny, []byte("#!/bin/sh\nprintf '%s' \"$BUNNY_HOME\" > \"$(dirname \"$0\")/seen\"\necho 'export EVALED=ran'\n"), 0755)
		line := exec.Command(exe, "-c", initEvalLine(root, bunny, shell)+`printf '%s\n' "$EVALED"`)
		line.Env = cmd.Env
		out, err = line.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "ran" {
			t.Errorf("%s: rc line gave %q, %v; want the snippet evaluated", shell, out, err)
		}
		if got, _ := os.ReadFile(seen); string(got) != root {
			t.Errorf("%s: bunny ran with BUNNY_HOME=%q, want %q", shell, got, root)
		}
	}
}

// Sourcing the snippet again, as a nested shell or a re-read rc does, must not
// grow the search paths. zsh's fpath guard kept a quoted path's quotes inside
// its (Ie) subscript, so for a path that needs quoting it never matched.
func TestInitSnippetIsIdempotentInZsh(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	p := paths.At(filepath.Join(t.TempDir(), `it's a dir`))
	snippet := initSnippet(p, "zsh")
	count := "t=" + shellWord(p.ZshCompletions()) + "; b=" + shellWord(p.Bin()) +
		`; n=0; for e in $fpath; do [[ "$e" == "$t" ]] && (( n += 1 )); done` +
		`; m=0; for e in $path; do [[ "$e" == "$b" ]] && (( m += 1 )); done; print $n $m`
	cmd := exec.Command(zsh, "-f", "-c", snippet+snippet+count)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "1 1" {
		t.Errorf("fpath and PATH entries after sourcing twice = %q, want \"1 1\"", got)
	}
}
