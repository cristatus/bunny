// Command bunny is the bunny CLI: an installer/launcher for portable
// developer tools and SDKs. It dispatches a binary lookup via argv[0]
// when invoked through one of the shim symlinks bunny installs into
// $BUNNY_HOME/bin, and otherwise parses the standard CLI tree.
package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"
)

var version = "dev"

// CLI is the top-level Kong root.
type CLI struct {
	LogLevel string           `short:"l" default:"info" enum:"debug,info,warn,error" help:"Log level"`
	Version  kong.VersionFlag `short:"v" help:"Print version"`

	Install              InstallCmd              `cmd:"" help:"Install a package"`
	Uninstall            UninstallCmd            `cmd:"" help:"Uninstall a package"`
	List                 ListCmd                 `cmd:"" help:"List installed packages; use --remote for the full catalog"`
	Info                 InfoCmd                 `cmd:"" help:"Show package details"`
	Search               SearchCmd               `cmd:"" help:"Search packages"`
	Use                  UseCmd                  `cmd:"" help:"Switch active provider for a capability"`
	Pin                  PinCmd                  `cmd:"" help:"Pin a capability to a version in ./.bunny-version"`
	Unpin                UnpinCmd                `cmd:"" help:"Remove a capability's pin from ./.bunny-version"`
	Which                WhichCmd                `cmd:"" help:"Show which package a shimmed command resolves to"`
	Run                  RunCmd                  `cmd:"" help:"Run an installed package"`
	Update               UpdateCmd               `cmd:"" help:"Check for updates; use --apply to install them"`
	Doctor               DoctorCmd               `cmd:"" help:"Run health checks"`
	Init                 InitCmd                 `cmd:"" help:"Print shell setup snippet"`
	Setup                SetupCmd                `cmd:"" help:"Install session env, completions, and shell rc integration"`
	Clean                CleanCmd                `cmd:"" help:"Prune cache and tmp dirs"`
	Reshim               ReshimCmd               `cmd:"" help:"Regenerate shims for globally-installed executables (npm -g, etc.)"`
	Toolchains           ToolchainsCmd           `cmd:"" help:"Regenerate Gradle/Maven JDK toolchain config from installed JDKs"`
	Completion           CompletionCmd           `cmd:"" help:"Print shell completion script (bash, zsh, or fish)"`
	CompleteIds          CompleteIDsCmd          `cmd:"" hidden:"" help:"List package IDs for shell completion"`
	CompleteCategories   CompleteCategoriesCmd   `cmd:"" hidden:"" help:"List catalog categories for shell completion"`
	CompleteCapabilities CompleteCapabilitiesCmd `cmd:"" hidden:"" help:"List installed-provider capabilities for completion"`
	CompleteCommands     CompleteCommandsCmd     `cmd:"" hidden:"" help:"List shimmed command names for completion"`

	Dev DevCmd `cmd:"" help:"Catalog maintainer commands (rewrite manifests, etc.)"`
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
			log.Fatal("Failed to initialize", "error", err)
		}
		if err := app.RunShim(base, os.Args[1:]); err != nil {
			log.Fatal(err)
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

	level, _ := log.ParseLevel(cli.LogLevel)
	log.SetLevel(level)

	app, err := New()
	if err != nil {
		log.Fatal("Failed to initialize", "error", err)
	}
	ctx.FatalIfErrorf(ctx.Run(app))
}
