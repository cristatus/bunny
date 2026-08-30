package runtime

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

// gitIdentityVars are the four variables Git reads in place of user.name and
// user.email. Author and committer are separate because Git treats them
// separately; a commit needs both, so all four are set or none are.
var gitIdentityVars = [4]string{
	"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL",
	"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
}

// gitIdentityTimeout bounds the lookup. Reading config is fast, but the
// working directory may sit on an unresponsive network mount, and a launch
// must not hang waiting to learn a name.
const gitIdentityTimeout = 2 * time.Second

// gitIdentityOverrides resolves the host's effective Git identity for dir and
// returns it as environment assignments.
//
// A redirected HOME costs a package its Git identity: Git looks for
// .gitconfig under HOME, which now points into the package's own data
// directory, so `git commit` fails with "Please tell me who you are". Binding
// the host file back does not fix it — HOME still points elsewhere — and an
// include chain can reference paths a policy cannot know.
//
// Asking Git resolves that chain, including `includeIf`, and it is resolved
// per launch in dir because the conditions can key on the repository: an
// identity chosen by remote URL differs between two checkouts. Only the name
// and email cross the boundary. Deliberately left behind is everything that
// would hand over credentials — credential.helper, core.sshCommand,
// url.*.insteadOf, http.extraHeader — and commit.gpgSign, which would only
// fail inside a sandbox whose GnuPG socket is masked.
//
// A missing identity, a missing Git, or a failed lookup returns nil: this is
// a convenience, and a launch is never refused over it.
func gitIdentityOverrides(dir string) map[string]string {
	name, email := gitIdentity(dir)
	if name == "" || email == "" {
		return nil
	}
	return map[string]string{
		"GIT_AUTHOR_NAME": name, "GIT_AUTHOR_EMAIL": email,
		"GIT_COMMITTER_NAME": name, "GIT_COMMITTER_EMAIL": email,
	}
}

// gitIdentity reads user.name and user.email as Git itself resolves them in
// dir. One invocation covers both keys; Git lists matches in increasing
// precedence order, so a later line wins over an earlier one.
func gitIdentity(dir string) (name, email string) {
	ctx, cancel := context.WithTimeout(context.Background(), gitIdentityTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "config", "--get-regexp", `^user\.(name|email)$`)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// Exit status 1 just means neither key is set, which is ordinary.
		log.Debug("No Git identity to pass into the sandbox", "dir", dir, "error", err)
		return "", ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || value == "" {
			continue
		}
		switch key {
		case "user.name":
			name = value
		case "user.email":
			email = value
		}
	}
	return name, email
}

// missingGitIdentity reports which identity variables env does not already
// set. An identity in the environment was chosen deliberately and wins, so
// when all four are present there is nothing to look up — and the lookup forks
// a process, which a launch inside a sandbox would otherwise pay for on every
// nested command, its parent having already supplied them.
func missingGitIdentity(env []string) []string {
	missing := make([]string, 0, len(gitIdentityVars))
	for _, name := range gitIdentityVars {
		if !envHasName(env, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// envHasName reports whether env assigns name, without building a map of the
// whole environment to answer four questions.
func envHasName(env []string, name string) bool {
	prefix := name + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}
