package runtime

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// gitRepo makes a repository with a local identity, which outranks whatever
// global config the machine running the test happens to have.
func gitRepo(t *testing.T, name, email string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", name},
		{"config", "user.email", email},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// Asking Git for the identity is what makes an include chain resolve, so the
// test asserts on config Git itself had to read rather than on a file path.
func TestGitIdentityOverridesResolvesThroughGit(t *testing.T) {
	dir := gitRepo(t, "Ada Lovelace", "ada@example.org")
	got := gitIdentityOverrides(dir)
	want := map[string]string{
		"GIT_AUTHOR_NAME": "Ada Lovelace", "GIT_AUTHOR_EMAIL": "ada@example.org",
		"GIT_COMMITTER_NAME": "Ada Lovelace", "GIT_COMMITTER_EMAIL": "ada@example.org",
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
}

// A commit needs both halves, so half an identity is no identity: passing one
// through would replace "who are you?" with a commit attributed to a name and
// an empty address.
func TestGitIdentityOverridesNeedsBothHalves(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	// Detach from any config the host machine has, so "unset" really is unset.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if got := gitIdentityOverrides(dir); got != nil {
		t.Errorf("no identity must yield no variables, got %v", got)
	}
	cmd = exec.Command("git", "config", "user.name", "Ada Lovelace")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v: %s", err, out)
	}
	if got := gitIdentityOverrides(dir); got != nil {
		t.Errorf("a name with no email must yield no variables, got %v", got)
	}
}

// The identity follows the redirect that caused the problem, not one profile:
// every home mode that repoints HOME gets it, and shared, which still reads
// the host config through the real HOME, is left alone.
func TestSandboxInjectsGitIdentityWhereHomeIsRedirected(t *testing.T) {
	dir := gitRepo(t, "Ada Lovelace", "ada@example.org")
	for _, home := range []string{"isolated", "ephemeral", "clean"} {
		p, hostHome := hardenedPrepared(t)
		policy := finalized(t, &PackageSandbox{Home: home})
		plan, err := buildSandboxPlan(p, policy, dir, hostHome, sandboxContext{})
		if err != nil {
			t.Fatalf("home %s: %v", home, err)
		}
		if !slices.Contains(plan.env, "GIT_AUTHOR_EMAIL=ada@example.org") {
			t.Errorf("home %s must carry the host identity: %v", home, plan.env)
		}
	}

	p, hostHome := hardenedPrepared(t)
	policy := finalized(t, &PackageSandbox{Home: "shared"})
	plan, err := buildSandboxPlan(p, policy, dir, hostHome, sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(plan.env, func(e string) bool { return strings.HasPrefix(e, "GIT_AUTHOR_") }) {
		t.Errorf("home: shared reads the host config already and needs no injection: %v", plan.env)
	}
}

// An identity in the environment was set on purpose — by the shell, or by
// config env: — and must outrank one Bunny inferred from the working
// directory.
func TestSandboxKeepsAnExplicitGitIdentity(t *testing.T) {
	dir := gitRepo(t, "Ada Lovelace", "ada@example.org")
	p, hostHome := hardenedPrepared(t)
	p.Env = append(p.Env, "GIT_AUTHOR_NAME=Explicit Choice")

	policy := finalized(t, &PackageSandbox{Home: "isolated"})
	plan, err := buildSandboxPlan(p, policy, dir, hostHome, sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.env, "GIT_AUTHOR_NAME=Explicit Choice") {
		t.Errorf("an explicit identity must survive: %v", plan.env)
	}
	// The half that was not set still gets filled in, so a commit works.
	if !slices.Contains(plan.env, "GIT_AUTHOR_EMAIL=ada@example.org") {
		t.Errorf("the unset half should still be supplied: %v", plan.env)
	}
}
