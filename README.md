# Bunny 🐰

A toolchain manager for Java and Node developers: a single-binary alternative to SDKMAN. Bunny installs JDKs, Node, Maven, Gradle, and supporting IDEs into standard XDG directories. Packages run directly against your host environment by default (`~/.m2`, `~/.gradle`, `~/.npm`), with an optional per-package sandbox when you want isolated application state or fewer integrations. No sudo or shell hooks.

Bunny currently supports Linux on `x86_64`/`amd64`.

> [!WARNING]
> **Experimental / Pre-1.0**: Bunny is under active development. Command interfaces, configuration schemas, manifest formats, and storage layouts may change between releases. Check the [Changelog](CHANGELOG.md) before upgrading. If you require production-grade stability today, consider [SDKMAN](https://sdkman.io/), [mise](https://mise.jdx.dev/), or [asdf](https://asdf-vm.com/).

```bash
curl -fsSL https://raw.githubusercontent.com/cristatus/bunny/main/install.sh | sh
~/.local/bin/bunny setup && exec $SHELL

bunny install jdk-21 maven gradle node-22 jetbrains-toolbox
bunny use jdk-21
bunny run mvn -version
```

## Why Bunny

Most developers assemble Java and Node workstations using a fragmented mix of `sdkman`, `nvm`, `mise`, `asdf`, Homebrew, Flatpak, and manual tarballs. Bunny unifies this workstation under a single, focused tool:

- **Host-native by default, isolated by choice**: `mvn` uses `~/.m2`, Gradle uses `~/.gradle`, and npm caches in `~/.npm` unless configured otherwise. Data paths can be redirected per package, and trusted applications can opt into an always-on or on-demand bubblewrap sandbox.
- **Single binary & symlink shims**: `bunny init` adds a single PATH export with no shell wrapper functions. Executables dispatch directly through symlinks via `argv[0]`, ensuring consistent behavior across terminals, IDEs, and containers.
- **Per-project version pinning**: Place a `.bunny-version` file in any project root to pin versions without shell hooks. Bunny also reads `.sdkmanrc`, `.tool-versions`, and `.java-version` files without requiring migration.
- **First-class Java toolchains**: Multi-vendor JDK support (Temurin, Corretto, Zulu, GraalVM) powered by the [Foojay Disco API](https://api.foojay.io/). Automated Gradle and Maven toolchain configuration ensures builds compile against the target JDK regardless of the runtime Java version.
- **Curated catalog**: Tailored specifically for Java/Node ecosystems, IDEs, and essential developer CLI utilities. See [bunny-catalog](https://github.com/cristatus/bunny-catalog).
- **Forkable for teams**: List an internal Git repository or HTTP endpoint under `catalogs:` to distribute customized JDKs, corporate certificates, and shared tooling, or list several catalogs to add your own packages beside the public ones without forking.

See the [Portability Model](docs/portability.md) and [Configuration](docs/config.md) for architectural details.

## Quick Start

```bash
# 1. Install bunny binary
curl -fsSL https://raw.githubusercontent.com/cristatus/bunny/main/install.sh | sh

# 2. Configure environment (session env, completions, and shell rc)
~/.local/bin/bunny setup
exec $SHELL
bunny doctor

# 3. Install core toolchains and IDEs
bunny install jdk-21 maven gradle
bunny install node-22 pnpm
bunny install jetbrains-toolbox code

# 4. Verify
mvn -version
java -version
code .
```

To install a specific version of Bunny, prefix the installer: `BUNNY_VERSION=v0.4.0 curl ... | sh`.

To consolidate all files under a single root (useful for CI, containers, and fleet images), set `BUNNY_HOME=/opt/bunny`.

### Optional per-package sandboxing

Sandbox activation is explicit and scoped to exact package IDs. Add an entry to
`~/.config/bunny/config.yaml` to sandbox every launch, or retain a policy for
on-demand use only:

```yaml
sandbox:
  packages:
    code:
      profile: desktop
    codex:
      activation: on-demand
      profile: online-cli
```

```bash
code .                    # always sandboxed
bunny sandbox codex -- .  # sandboxed for this launch only
```

The sandbox isolates application state and can disable network or desktop
integrations, but it is not a hardened boundary for untrusted software. See
[Sandboxing](docs/sandbox.md) for profiles, overrides, nested shims, and the
trust model.

## Java Workflow

```bash
# Install multiple JDKs side-by-side
bunny install jdk-21 jdk-17 corretto-21 graalvm-21

# Set the global default JDK
bunny use jdk-21

# Run a one-off command with a specific JDK
bunny run jdk-17 -- java -version

# Pin versions per project in ./.bunny-version
bunny pin jdk 17
bunny pin maven 3.9
java -version    # Resolves to JDK 17 within the project directory
```

Bunny automatically maintains **Gradle and Maven toolchains**. A project configured for Java 17 compiles against Bunny's JDK 17 even when Gradle runs under JDK 21. See [First-class Java](docs/java.md).

## Node Workflow

```bash
# Install Node versions and package managers
bunny install node-22 node-24 pnpm bun

# Pin Node per project
bunny pin node 22
node --version   # 22.x
```

`npm -g` installs packages into the active Node version's prefix, ensuring global packages remain scoped to their corresponding Node release (similar to `nvm`). Package caches (`~/.npm`, `~/.local/share/pnpm`) remain shared across versions.

## Command Reference

| Command | Description |
| :--- | :--- |
| `bunny install <id...>` | Install one or more packages (`-f/--force` to reinstall) |
| `bunny uninstall <id...>` | Remove packages (`--purge` to also delete associated data) |
| `bunny list` | List installed packages (`-t/--tag`, `--capability`, `--kind`, `--active`) |
| `bunny search [query...]` | Search the catalog, ranked by match strength (`-t/--tag`, `--capability`, `--kind`, `--installed`, `--available`) |
| `bunny info <id>` | Display package details, active provider state, pins, and dependents |
| `bunny use <id>` | Switch the active global provider for a capability (e.g. `jdk-21`) |
| `bunny pin <capability> <version>` | Pin a capability to a version in `./.bunny-version` |
| `bunny unpin <capability>` | Remove a capability pin from `./.bunny-version` |
| `bunny run <id> [-- args]` | Execute a specific package binary using its normal activation policy |
| `bunny sandbox <id> [-- args]` | Run an installed package once with its effective sandbox policy |
| `bunny update` | Check for package updates (`--apply` to install updates) |
| `bunny doctor` | Validate layout, configuration, catalog health, shims, and pins |
| `bunny setup` | Configure user session environment, completions, and shell rc integration |
| `bunny init [shell]` | Output shell initialization snippet (`bash`, `zsh`, `fish`) |
| `bunny completion [shell]` | Output shell tab-completion script (`bash`, `zsh`, `fish`) |
| `bunny clean [id]` | Clean download cache and abandoned staging directories (`--all` to include installed packages) |
| `bunny reshim` | Regenerate shims for globally installed executables (`npm -g`, etc.) |
| `bunny toolchains` | Regenerate Gradle/Maven JDK toolchain configuration from installed JDKs |

Maintainer and catalog-authoring utilities are available under `bunny dev`.

## Documentation

- [First-class Java](docs/java.md): Multi-vendor JDK support, toolchains, and runtime `requires` constraints.
- [Portability Model](docs/portability.md): Default host-native execution, data redirection, and optional runtime isolation.
- [Sandboxing](docs/sandbox.md): Always or on-demand per-package bwrap execution with isolated state, built-in/custom profiles, path masks, and feature controls.
- [Configuration](docs/config.md): `config.yaml` reference, custom install roots, data redirection, and sandbox activation.
- [Per-project Pinning](docs/pinning.md): `.bunny-version`, format interoperability, and IDE setup.
- [Team Deployment](docs/teams.md): Forking catalogs, private hosting, and reproducible environments.
- [Corporate Environments](docs/corporate.md): Proxies, custom CA roots, Maven settings, and air-gapped workflows.
- [Architecture](docs/architecture.md): Package boundaries, state management, and transactional mutations.
- [Changelog](CHANGELOG.md): Release history and migration notes.
- [Roadmap](ROADMAP.md): Project scope, feature pipeline, and non-goals.

## Directory Layout

Bunny adheres strictly to the XDG Base Directory Specification:

```
~/.local/share/bunny/
├── sdk/{id}/               SDKs and toolchains (JDKs, Node, Maven, Gradle)
├── cli/{id}/               Standalone CLI tools (ripgrep, jq, gh)
├── app/{id}/               GUI applications (code, zed, jetbrains-toolbox)
├── data/{id}/              Per-package data directories ({data} placeholder)
├── manifests/{id}.yaml     Install-time manifest snapshots
├── state.json              State database (installed packages, providers, roots)
└── mutation.lock           Lockfile serializing mutating operations

~/.config/bunny/config.yaml User configuration
~/.cache/bunny/             Download cache and catalog index
~/.local/bin/               Bunny binary and symlink shims (argv[0] dispatch)
~/.local/share/applications/ Desktop .desktop entry files
~/.local/share/icons/       Application icons
```

When `$BUNNY_HOME` is set to an absolute path, all directories collapse under that single root.

## Building from Source

```bash
make build      # Output: ./bin/bunny
make test       # Run test suite
make install    # Install binary to ~/.local/bin/bunny
```

## Comparison

| Feature | Bunny | SDKMAN | mise | Homebrew (Linux) | Nix |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Primary Scope** | Java + Node workstation | JVM only | Polyglot runtime | General packages | System / package manager |
| **GUI Editors & IDEs** | Yes | No | No | Partial | Yes |
| **Per-Version Isolation** | Opt-in via config | No | Per-project `[env]` | No | Partial |
| **Per-Package Runtime Sandbox** | Opt-in (bubblewrap) | No | No | No | Yes |
| **Project Pinning** | `.bunny-version`, `.sdkmanrc`, `.tool-versions` | `.sdkmanrc` | `mise.toml`, `.tool-versions` | No | `flake.nix` |
| **Shell Overhead** | None (symlinks) | Shell functions | Shim binary | None | None |
| **Container Friendly** | Yes | Shell-dependent | Yes | Yes | Yes |
| **Single Binary** | Yes | No | Yes | No | No |
| **Forkable Catalog** | Yes (Git / HTTPS) | No | Yes | Tap system | Yes |

## License

[MIT](LICENSE)
