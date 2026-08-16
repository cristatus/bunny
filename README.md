# Bunny 🐰

A toolchain manager for Java and Node developers: a single-binary alternative to sdkman. Bunny installs JDKs, Node, Maven, Gradle, and the editors/IDEs that target them into standard XDG directories, then execs them directly against your normal environment: `~/.m2`, `~/.gradle`, and `~/.npm` stay exactly where every other tool expects them, and per-version isolation is available as an opt-in when you want it. No sudo, no shell hooks, one Go binary, with identical behavior in your terminal, your IDE, your CI pipeline, and over SSH.

Bunny currently supports Linux on `x86_64`/`amd64`.

> [!WARNING]
> **Work in progress, and highly experimental.** Bunny is pre-1.0 and nothing
> about it is stable yet: command names and flags, the manifest schema, the
> config format, and the on-disk layout all still change between releases,
> sometimes without a migration path. A recent release moved every installed
> package to new directories and simply stopped reading the old ones.
>
> Expect to reinstall your packages occasionally, and to re-read the changelog
> before upgrading. Do not build anything you care about on top of the manifest
> or config formats until 1.0.
>
> There is no support commitment. Issues and pull requests are welcome, but
> there is no release cadence, no backporting, and no guarantee that a bug
> affecting you gets fixed. If you need a toolchain manager you can rely on
> today, use [sdkman](https://sdkman.io/), [mise](https://mise.jdx.dev/), or
> [asdf](https://asdf-vm.com/).

```bash
curl -fsSL https://raw.githubusercontent.com/cristatus/bunny/main/install.sh | sh
~/.local/bin/bunny setup && exec $SHELL

bunny install jdk-21 maven gradle node-22 jetbrains-toolbox
bunny use jdk-21
bunny run mvn -version
```

## Why bunny

Most developers assemble a Java + Node workstation from some combination of `sdkman`, `nvm`, `mise`, `asdf`, Linuxbrew, Flatpak, distro packages, and hand-downloaded tarballs, each strong in its own niche. Bunny's goal is to cover that whole workstation, SDKs and the editors and IDEs that target them, from a single tool.

Its scope is deliberately narrow:

- **No surprises in `$HOME`.** Nothing is redirected by default: `mvn` fills `~/.m2`, `gradle` uses `~/.gradle`, `npm` caches in `~/.npm`, exactly as if you had installed them yourself. Per-version isolation is one opt-in block in `~/.config/bunny/config.yaml` when you want it, and never a thing you have to discover after the fact.
- **One binary, symlink shims.** `bunny init` adds a single env-only line (PATH, plus zsh `fpath`), with no command-wrapping shell functions. Every tool dispatches through a real symlink via `argv[0]`, so what runs is exactly what's on disk, in any terminal, IDE, or container.
- **Per-project version pinning.** Place a `.bunny-version` file in a project root and `mvn`, `node`, and `java` resolve to that project's pinned versions automatically, with no shell hooks and no `cd` listeners. Bunny also reads `.sdkmanrc`, `.tool-versions`, and `.java-version`, so existing projects work without conversion.
- **First-class Java.** Multiple JDK vendors (Temurin, Corretto, Zulu, GraalVM) via the [Foojay](https://api.foojay.io/) API; generated Gradle/Maven **toolchains**, so a build compiles against the correct JDK regardless of which one launched it; and `requires: ["jdk>=17"]` constraints that select a satisfying JDK at run time. See [First-class Java](docs/java.md).
- **Bounded, curated catalog.** The Java and Node ecosystems, plus the editors and IDEs used to write Java and Node code. It does not attempt parity with brew or nixpkgs. See [bunny-catalog](https://github.com/cristatus/bunny-catalog).
- **Forkable for teams.** Point `catalog.remote` at your team's internal git repository, vendor a corporate JDK with custom certificates, and onboarding reduces to a single `curl | sh`. See [Team deployment](docs/teams.md).

Portability without surprises: bunny isolates nothing by default. SDKs and GUI apps alike exec directly against the host's normal `$HOME` and write where they natively would. Redirection is opt-in, per package or per capability, in `~/.config/bunny/config.yaml`. See [Portability model](docs/portability.md) and [Configuration](docs/config.md).

## Quick start

```bash
# Install bunny itself (downloads the latest release, verifies checksum)
curl -fsSL https://raw.githubusercontent.com/cristatus/bunny/main/install.sh | sh

# One-step setup: session env (so the desktop sees bunny's apps), shell
# completions, and your shell rc. Auto-detects your shell.
~/.local/bin/bunny setup
exec $SHELL          # or: systemctl --user import-environment PATH
bunny doctor         # verify environment

# Install a Java + Node workstation
bunny install jdk-21 maven gradle
bunny install node-22 pnpm
bunny install jetbrains-toolbox code

# Run
mvn -version
java -version
code .
```

Pin a bunny version with `BUNNY_VERSION=v0.4.0 curl ... | sh`. Set `BUNNY_HOME=/opt/bunny` to collapse everything under one root instead of the XDG directories.

## A typical Java workflow

```bash
bunny install jdk-21 jdk-17 maven gradle    # multiple JDKs side-by-side
bunny install corretto-21 graalvm-21         # other vendors, same `jdk` slot
bunny use jdk-21                              # set JDK 21 as the global default
bunny run jdk-17 -- java -version             # one-off run with JDK 17

# Per-project pin, written to $PROJECT_ROOT/.bunny-version
bunny pin jdk 17
bunny pin maven 3.9
java -version    # → 17, even though the global default is 21
```

The shim walks up from the current directory looking for a pin file and falls back to the global default. It behaves identically in IntelliJ's embedded terminal, in CI, and under `make`: no shell hooks are involved.

Bunny also configures **Gradle/Maven toolchains**, so the JDK a build *compiles* with is independent of the JDK that *launched* it: a module targeting 17 builds against bunny's JDK 17 even when everything runs under 21. The configuration is regenerated automatically as you install JDKs. See [First-class Java](docs/java.md).

## A typical Node workflow

```bash
bunny install node-22 node-24 pnpm bun

# In project A: pin Node 22
bunny pin node 22
node --version    # → 22.x

# In project B: pin Node 24
bunny pin node 24
node --version    # → 24.x
```

`npm -g` installs into node's own prefix, so globals belong to the Node version that installed them and go away when it does, the same as `nvm`. The npm cache, pnpm store, and Yarn cache stay at their native host paths and are shared, since their contents are version-agnostic. To split them per version anyway, see [Configuration](docs/config.md).

## Commands

```
bunny install <id>          install a package
bunny uninstall <id>        remove (use --purge to also drop the package's data dir)
bunny list                  list kind, capability, version, and active provider
bunny list --remote         list the full catalog (--tag java / --capability jdk / --active)
bunny info <id>             show capability, requirements, pins, and dependents
bunny search <query>        search names, descriptions, tags, capabilities, and requirements
bunny use <id>              switch active SDK version (e.g. jdk-17 → jdk-21)
bunny pin <cap> <version>   pin a capability to a version in ./.bunny-version
bunny unpin <cap>           remove a capability's pin from ./.bunny-version
bunny run <id> [-- args]    run a package binary
bunny update                check for updates (installed packages)
bunny update --apply        apply available updates
bunny doctor                health check (layout, config, catalog, install roots, shims, pins)
bunny setup                 one-step: session env (desktop) + completions + shell rc
bunny init <shell>          print the shell setup snippet (used by setup / eval)
bunny completion <shell>    print the shell completion script (bash, zsh, fish)
bunny clean                 prune download cache and abandoned staged installs
bunny reshim                regenerate shims for globally-installed executables (npm -g, etc.)
bunny toolchains            regenerate Gradle/Maven JDK toolchain config from installed JDKs
```

`bunny setup` also drops bunny's own completion where your shell already looks for it, so after setup, `bunny <TAB>` completes subcommands and `bunny install <TAB>` completes package IDs (installed-only for `uninstall`/`use`/`run`).

Maintainer/CI commands live under `bunny dev`.

`list` and `search` write plain text to stdout, so pipe them wherever you
like: `bunny list --remote | less`, or narrow the result with `--tag`,
`--capability`, and `--active`.

## Documentation

- [First-class Java](docs/java.md): multi-vendor JDKs, Gradle/Maven toolchains, `requires` version constraints
- [Portability model](docs/portability.md): nothing is isolated by default; SDKs and GUI apps run native against host paths
- [Configuration](docs/config.md): `config.yaml`, install locations, the catalog remote, and opting into per-version data isolation (see [`config.example.yaml`](config.example.yaml))
- [Per-project pinning](docs/pinning.md): `.bunny-version` plus `.sdkmanrc`/`.tool-versions`/`.java-version`, lookup order, IDE integration tips
- [Team deployment](docs/teams.md): fork the catalog, host internally, onboard with one command
- [Corporate environments](docs/corporate.md): proxies, custom CA bundles, `~/.m2/settings.xml`, internal artifact repos
- [Architecture](docs/architecture.md): package boundaries, state ownership, and mutation transactions
- [Changelog](CHANGELOG.md): notable changes in each release
- [Roadmap](ROADMAP.md): what's next, what's deliberately out of scope

## Directory layout

Bunny follows the XDG base directories, so its files sit where a Linux desktop
already looks for them. Desktop entries are found with no `XDG_DATA_DIRS`
plumbing, and `~/.local/bin` is on `PATH` on most distributions.

```
~/.local/share/bunny/
├── sdk/{id}/               JDKs, Node, Maven, Gradle: anything needing a path
├── cli/{id}/               plain commands (ripgrep, jq, gh)
├── app/{id}/               GUI applications (code, zed, jetbrains-toolbox)
├── data/{id}/              per-package data, the {data} placeholder
├── manifests/{id}.yaml     install-time snapshots (drive runtime + uninstall)
├── catalog/packages/{id}/  optional local manifests (override remote)
├── state.json              installed packages, kinds, providers, locations
└── mutation.lock           serializes state-changing commands

~/.config/bunny/config.yaml     user config
~/.cache/bunny/                 download cache and catalog index
~/.local/bin/                   bunny binary + symlink shims (dispatch via argv[0])
~/.local/share/applications/    .desktop files, alongside everything else's
~/.local/share/icons/           icons
```

The three install roots let you move a whole class at once: `install.sdk: ~/opt`
puts every JDK and build tool where an IDE's file picker can reach it. See
[Configuration](docs/config.md).

Set `BUNNY_HOME` and the layout collapses under that single root instead, which
is what containers, CI, and fleet images want.

## Building from source

```bash
make build      # → ./bin/bunny
make test
make install    # copy ./bin/bunny → ~/.local/bin/bunny
```

## Comparison

| | bunny | sdkman | mise | brew (Linux) | nix |
|---|---|---|---|---|---|
| Java + Node toolchain | first-class | Java only | yes | yes | yes |
| GUI editors / IDEs | yes | no | no | partial | yes |
| Per-version SDK isolation | opt-in (config) | no | per-project `[env]` | no | partial |
| Per-project version pinning | `.bunny-version` (+ reads `.sdkmanrc` / `.tool-versions`) | `.sdkmanrc` | `mise.toml` | no | `flake.nix` |
| Shell startup cost | none (symlink shims) | bash function | shim binary | none | none |
| Container-friendly | yes | via shell hooks | yes | yes | yes |
| Single binary | yes | no | yes | no | no |
| Forkable team catalog | yes | no | yes | tap-style | yes |
| Catalog size | ~85, growing | ~50 (JVM only) | thousands (varies) | tens of thousands | 100k+ |

Bunny is intentionally narrower than mise/brew/nix and intentionally broader than sdkman.

## License

MIT
