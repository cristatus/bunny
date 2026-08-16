package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/ui"
)

// environmentDPath is where systemd's per-user environment generator reads
// bunny's session env, so the graphical session sees the same PATH (and, for a
// single-root install, the same XDG_DATA_DIRS) as a login shell.
func environmentDPath() (string, error) {
	cfg, err := os.UserConfigDir() // $XDG_CONFIG_HOME or ~/.config
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "environment.d", "bunny.conf"), nil
}

// writeEnvironmentD writes ~/.config/environment.d/bunny.conf with the session
// prepends. bunny owns the file; write is idempotent. Under the XDG layout only
// PATH is needed: desktop entries and icons already live in XDG_DATA_HOME,
// which the desktop scans without being told.
func writeEnvironmentD(p *paths.Paths) (string, error) {
	path, err := environmentDPath()
	if err != nil {
		return "", err
	}
	content := "# managed by bunny — do not edit\n"
	if !p.XDG() {
		content += fmt.Sprintf("XDG_DATA_DIRS=%s:${XDG_DATA_DIRS}\n", p.Share())
	}
	content += fmt.Sprintf("PATH=%s:${PATH}\n", p.Bin())
	if cur, err := os.ReadFile(path); err == nil && string(cur) == content {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	return path, fsutil.WriteFile(path, []byte(content), 0644)
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

var bunnyInitRe = regexp.MustCompile(`bunny\s+init`)

// rcHasBunnyInit reports whether an rc already invokes `bunny init` in any form.
func rcHasBunnyInit(content string) bool { return bunnyInitRe.MatchString(content) }

// initEvalLine is the line setup appends to the rc. Absolute bunny path so it
// resolves before PATH is set; fish uses `| source`, others `eval`.
func initEvalLine(bunnyBin, shell string) string {
	if shell == "fish" {
		return fmt.Sprintf("%s init fish | source\n", bunnyBin)
	}
	return fmt.Sprintf("eval \"$(%s init %s)\"\n", bunnyBin, shell)
}

// ensureRcInit appends initEvalLine to rcPath unless an existing bunny init
// line is present. Returns true if it appended. Creates the file/dirs if missing.
func ensureRcInit(rcPath, bunnyBin, shell string) (bool, error) {
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
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	f, err := os.OpenFile(rcPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return false, err
	}
	content := prefix + "# added by bunny setup\n" + initEvalLine(bunnyBin, shell)
	written, writeErr := f.WriteString(content)
	if writeErr == nil && written != len(content) {
		writeErr = io.ErrShortWrite
	}
	closeErr := f.Close()
	err = errors.Join(writeErr, closeErr)
	return err == nil, err
}

// SetupCmd is the one-shot installer: session env (environment.d), shell
// completions, and the shell rc `bunny init` line.
type SetupCmd struct {
	Shell string `help:"Shell to configure: bash, zsh, or fish (auto-detected from $SHELL if omitted)"`
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
		p := ui.New(os.Stdout)
		p.Println()

		envPath, err := writeEnvironmentD(a.Paths)
		if err != nil {
			return fmt.Errorf("write environment.d: %w", err)
		}
		p.Println("wrote session env to " + tildePath(envPath))

		if err := writeCompletionFile(a.Paths, shell); err != nil {
			return fmt.Errorf("write completion: %w", err)
		}

		rcPath, err := rcPathForShell(shell)
		if err != nil {
			return err
		}
		bunnyBin := filepath.Join(bin, "bunny")
		added, err := ensureRcInit(rcPath, bunnyBin, shell)
		if err != nil {
			return fmt.Errorf("configure %s: %w", rcPath, err)
		}
		if added {
			p.Println("added bunny init to " + tildePath(rcPath))
		} else {
			p.Println(tildePath(rcPath) + " already configured")
		}

		sessionVars := "PATH"
		if !a.Paths.XDG() {
			sessionVars = "PATH XDG_DATA_DIRS"
		}
		p.Println()
		p.Println("setup complete — restart your shell to activate bunny,")
		p.Println("or update the current session with:")
		p.Println()
		p.Println("  systemctl --user import-environment " + sessionVars)
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
