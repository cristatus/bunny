package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/doctor"
	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/ui"
)

// desktopSession is doctor.DesktopSession, a var so tests can drive both
// branches.
var desktopSession = doctor.DesktopSession

// systemdSession reports whether a systemd user manager is there to read
// environment.d. Without one, setup reaches the desktop through ~/.profile,
// which display managers source at login. A var so tests can drive both.
var systemdSession = func() bool {
	_, ok := doctor.SessionDataDirs()
	return ok
}

// sessionEnvContent is bunny's environment.d file. The system data dirs are
// spelled out as the fallback because an unset XDG_DATA_DIRS means them, and
// a value naming only bunny's share dir would hide every other application.
func sessionEnvContent(p *paths.Paths) string {
	content := "# managed by bunny — do not edit\n"
	if root := pinnedRoot(p); root != "" {
		content += fmt.Sprintf("%s=%s\n", paths.EnvHome, root)
	}
	content += fmt.Sprintf("XDG_DATA_DIRS=%s:${XDG_DATA_DIRS:-/usr/local/share:/usr/share}\n", p.Share())
	content += fmt.Sprintf("PATH=%s:${PATH}\n", p.Bin())
	return content
}

// writeEnvironmentD writes ~/.config/environment.d/bunny.conf and reports
// whether it changed anything.
func writeEnvironmentD(p *paths.Paths) (string, bool, error) {
	path, err := paths.SessionEnvFile()
	if err != nil {
		return "", false, err
	}
	content := sessionEnvContent(p)
	if cur, err := os.ReadFile(path); err == nil && string(cur) == content {
		return path, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", false, err
	}
	return path, true, fsutil.WriteFile(path, []byte(content), 0644)
}

// pinnedRoot is the root the shell setup has to name, or "" for ~/.bunny,
// which every invocation finds without being told.
func pinnedRoot(p *paths.Paths) string {
	if p.DefaultRoot() {
		return ""
	}
	return p.Root
}

// detectShell returns the shell basename from $SHELL, or "" if unknown/unset.
func detectShell() string {
	switch base := filepath.Base(os.Getenv("SHELL")); base {
	case "bash", "zsh", "fish":
		return base
	default:
		return ""
	}
}

// rcPathForShell maps a shell to the rc file bunny appends its init line to.
func rcPathForShell(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	case "bash":
		return filepath.Join(home, ".bashrc"), nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), nil
	default:
		return "", fmt.Errorf("unsupported shell %q", shell)
	}
}

// rcHasBunnyInit reports whether an rc mentions `bunny init` in any form, a
// commented-out line included: setup leaves a line the user disabled alone.
func rcHasBunnyInit(content string) bool { return doctor.BunnyInitRe.MatchString(content) }

// initEvalLine is the line setup appends to the rc. Absolute bunny path so it
// resolves before PATH is set; fish uses `| source`, others `eval`.
//
// A custom root is pinned as $BUNNY_HOME on the invocation; root is "" for
// ~/.bunny. `bunny init` resolves the root from the environment it runs in, and
// an rc runs in shells that never had the variable: an unpinned call there
// emits the snippet for ~/.bunny, away from the root the install lives under.
// The snippet a pinned call emits exports the root onward, so this one line
// establishes the root for the whole shell.
func initEvalLine(root, bunnyBin, shell string) string {
	word, quote := shellWord, shellQuote
	if shell == "fish" {
		word, quote = fishWord, fishQuote
	}
	cmd := word(bunnyBin)
	if root != "" {
		cmd = fmt.Sprintf("env %s=%s %s", paths.EnvHome, quote(root), cmd)
	}
	if shell == "fish" {
		return fmt.Sprintf("%s init fish | source\n", cmd)
	}
	return fmt.Sprintf("eval \"$(%s init %s)\"\n", cmd, shell)
}

// blankLinePrefix returns the newlines to prepend so the appended block is
// separated from existing rc content by exactly one blank line: nothing for
// an empty file, and no more than needed when the file already ends in one or
// more blank lines.
func blankLinePrefix(data string) string {
	if data == "" {
		return ""
	}
	switch trailing := len(data) - len(strings.TrimRight(data, "\n")); {
	case trailing >= 2:
		return ""
	case trailing == 1:
		return "\n"
	default:
		return "\n\n"
	}
}

// ensureRcInit appends initEvalLine to rcPath unless an existing bunny init
// line is present. Returns true if it appended. Creates the file/dirs if missing.
func ensureRcInit(rcPath, root, bunnyBin, shell string) (bool, error) {
	data, err := os.ReadFile(rcPath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if rcHasBunnyInit(string(data)) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(rcPath), 0755); err != nil {
		return false, err
	}
	prefix := blankLinePrefix(string(data))
	f, err := os.OpenFile(rcPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return false, err
	}
	content := prefix + "# added by bunny setup\n" + initEvalLine(root, bunnyBin, shell)
	written, writeErr := f.WriteString(content)
	if writeErr == nil && written != len(content) {
		writeErr = io.ErrShortWrite
	}
	closeErr := f.Close()
	err = errors.Join(writeErr, closeErr)
	return err == nil, err
}

// staleRcInit returns the bunny init line rcPath runs when it was written for
// another root, and "" otherwise. What decides the root is the one the line
// pins, or that it pins none: an unpinned line left in place after a reinstall
// under a custom BUNNY_HOME emits the ~/.bunny snippet in every shell.
// How the line reaches bunny, by absolute path or through PATH, does not
// matter, and a commented-out line runs nothing. Setup does not rewrite a line
// the user may have edited, but "already configured" alone would claim a
// shell setup that points somewhere else.
func staleRcInit(rcPath, root, shell string) string {
	data, err := os.ReadFile(rcPath)
	if err != nil {
		return ""
	}
	quote := shellQuote
	if shell == "fish" {
		quote = fishQuote
	}
	pin := paths.EnvHome + "=" + quote(root)
	var stale string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || !doctor.BunnyInitRe.MatchString(line) {
			continue
		}
		pinned := strings.Contains(line, paths.EnvHome+"=")
		if (root == "" && !pinned) || (root != "" && strings.Contains(line, pin)) {
			return ""
		}
		if stale == "" {
			stale = line
		}
	}
	return stale
}

// SetupCmd is the one-shot installer: shell completions, the shell rc `bunny
// init` line and, on a desktop, the session env (environment.d, or ~/.profile
// without a systemd user manager).
type SetupCmd struct {
	Shell   string `help:"Shell to configure: bash, zsh, or fish (auto-detected from $SHELL if omitted)"`
	Desktop bool   `help:"Write the desktop session env even when no graphical session is detected"`
}

func (c *SetupCmd) Run(a *App) error {
	shell := c.Shell
	if shell == "" {
		shell = detectShell()
	}
	if shell == "" {
		return fmt.Errorf("could not detect shell; pass --shell bash|zsh|fish")
	}

	// Validate the shell is supported before even creating the mutation lock.
	switch shell {
	case "bash", "zsh", "fish":
	default:
		return fmt.Errorf("unsupported shell %q; use bash, zsh, or fish", c.Shell)
	}

	return a.withMutation(a.context(), func() error {
		bin := a.Paths.Bin()
		p := a.status()
		p.Println()

		desktop := c.Desktop || desktopSession()
		wroteEnv := false
		switch {
		case desktop && systemdSession():
			envPath, wrote, err := writeEnvironmentD(a.Paths)
			if err != nil {
				return fmt.Errorf("write environment.d: %w", err)
			}
			wroteEnv = wrote
			if wrote {
				log.Info("Wrote session env", "path", envPath)
				p.Println("wrote session env to " + tildePath(envPath))
			} else {
				p.Println("session env " + tildePath(envPath) + " up to date")
			}
		case desktop:
			profile, err := paths.ProfileFile()
			if err != nil {
				return err
			}
			added, err := ensureRcInit(profile, pinnedRoot(a.Paths), filepath.Join(bin, "bunny"), "sh")
			if err != nil {
				return fmt.Errorf("configure %s: %w", profile, err)
			}
			wroteEnv = added
			if added {
				log.Info("Added bunny init to the login profile", "path", profile)
				p.Println("added bunny init to " + tildePath(profile) + " for the desktop session")
			} else {
				p.Println(tildePath(profile) + " already configured")
			}
		}

		if err := writeCompletionFile(a.Paths, shell); err != nil {
			return fmt.Errorf("write completion: %w", err)
		}
		log.Info("Wrote shell completion", "shell", shell, "path", completionFilePath(a.Paths, shell))

		rcPath, err := rcPathForShell(shell)
		if err != nil {
			return err
		}
		bunnyBin := filepath.Join(bin, "bunny")
		root := pinnedRoot(a.Paths)
		added, err := ensureRcInit(rcPath, root, bunnyBin, shell)
		if err != nil {
			return fmt.Errorf("configure %s: %w", rcPath, err)
		}
		if added {
			log.Info("Added bunny init", "rc", rcPath, "binary", bunnyBin)
			p.Println("added bunny init to " + tildePath(rcPath))
		} else {
			log.Info("Shell rc already configured", "rc", rcPath)
			p.Println(tildePath(rcPath) + " already configured")
			if line := staleRcInit(rcPath, root, shell); line != "" {
				ui.Notice(fmt.Sprintf("%s runs %q, not what this install writes: %s",
					tildePath(rcPath), line, strings.TrimSpace(initEvalLine(root, bunnyBin, shell))))
			}
		}

		p.Println()
		p.Println("setup complete — restart your shell to activate bunny")
		if wroteEnv {
			p.Println("log out and back in once so the desktop finds bunny's applications")
		}
		return nil
	})
}

// tildePath abbreviates a leading $HOME with ~ for friendlier output; returns
// the path unchanged when it isn't under $HOME.
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}
