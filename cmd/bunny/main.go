// Command bunny is the bunny CLI: an installer/launcher for portable
// developer tools and SDKs. It dispatches a binary lookup via argv[0]
// when invoked through one of the shim symlinks bunny installs into
// $BUNNY_HOME/bin, and otherwise parses the standard CLI tree.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/ui"
)

var version = "dev"

// CLI is the top-level Kong root.
type CLI struct {
	LogLevel   string           `short:"l" placeholder:"level" help:"Enable diagnostics at level: debug, info, warn, error"`
	NoProgress bool             `help:"Disable interactive progress output"`
	Version    kong.VersionFlag `short:"v" help:"Print version"`

	// Listed flat in workflow order (Kong preserves declaration order); no
	// groups. Maintainer/completion commands are hidden from the top-level help.
	Install    InstallCmd    `cmd:"" help:"Install a package"`
	Uninstall  UninstallCmd  `cmd:"" help:"Uninstall a package"`
	List       ListCmd       `cmd:"" help:"List installed packages; use --remote for the full catalog"`
	Search     SearchCmd     `cmd:"" help:"Search packages"`
	Info       InfoCmd       `cmd:"" help:"Show package details"`
	Update     UpdateCmd     `cmd:"" help:"Check for updates; use --apply to install them"`
	Use        UseCmd        `cmd:"" help:"Switch active provider for a capability"`
	Pin        PinCmd        `cmd:"" help:"Pin a capability to a version in ./.bunny-version"`
	Unpin      UnpinCmd      `cmd:"" help:"Remove a capability's pin from ./.bunny-version"`
	Run        RunCmd        `cmd:"" help:"Run an installed package"`
	Doctor     DoctorCmd     `cmd:"" help:"Run health checks"`
	Clean      CleanCmd      `cmd:"" help:"Prune cache and tmp dirs"`
	Reshim     ReshimCmd     `cmd:"" help:"Regenerate shims for globally-installed executables (npm -g, etc.)"`
	Toolchains ToolchainsCmd `cmd:"" help:"Regenerate Gradle/Maven JDK toolchain config from installed JDKs"`
	Setup      SetupCmd      `cmd:"" help:"Install session env, completions, and shell rc integration"`
	Init       InitCmd       `cmd:"" help:"Print shell setup snippet"`
	Completion CompletionCmd `cmd:"" help:"Print shell completion script (bash, zsh, or fish)"`

	CompleteIds          CompleteIDsCmd          `cmd:"" hidden:"" help:"List package IDs for shell completion"`
	CompleteCategories   CompleteCategoriesCmd   `cmd:"" hidden:"" help:"List catalog categories for shell completion"`
	CompleteCapabilities CompleteCapabilitiesCmd `cmd:"" hidden:"" help:"List installed-provider capabilities for completion"`

	Dev DevCmd `cmd:"" hidden:"" help:"Catalog maintainer commands (rewrite manifests, etc.)"`
}

func main() {
	// Shim dispatch — when argv[0] is a symlink under $BUNNY_HOME/bin (e.g.
	// `node`, `code`), resolve the owning package and exec its binary
	// instead of Kong-parsing as the bunny CLI. Only the literal "bunny"
	// name (and `*.test` go-test binaries) is treated as the CLI itself,
	// so a future shim called e.g. "bunnydb" wouldn't be silently swallowed.
	if base := filepath.Base(os.Args[0]); base != "bunny" && !strings.HasSuffix(base, ".test") {
		app, err := New()
		if err != nil {
			ui.Fatal(err)
		}
		if err := app.RunShim(base, os.Args[1:]); err != nil {
			ui.Fatal(err)
		}
		return
	}

	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("bunny"),
		kong.Description("A toolchain manager for Java and Node developers."),
		kong.Vars{"version": version},
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{Compact: true}),
	)

	// Logging is off by default; -l turns it on at the requested level. Genuine
	// failures still surface via ui.Fatal (returned errors), not the log channel.
	if cli.LogLevel == "" {
		log.SetLevel(log.FatalLevel + 1)
	} else {
		level, err := log.ParseLevel(cli.LogLevel)
		if err != nil {
			ui.Fatal(fmt.Errorf("invalid log level %q (want: debug, info, warn, or error)", cli.LogLevel))
		}
		log.SetLevel(level)
	}

	app, err := New()
	if err != nil {
		ui.Fatal(err)
	}
	app.NoProgress = cli.NoProgress
	if err := ctx.Run(app); err != nil {
		if errors.Is(err, errHandled) {
			os.Exit(1) // already reported (per-package lines + summary)
		}
		ui.Fatal(err)
	}
}
