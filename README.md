# Bunny 🐰

A toolchain manager for Java and Node developers — a single-binary alternative to sdkman that installs into a self-contained home and keeps each SDK version's data cleanly isolated.

Bunny installs JDKs, Node, Maven, Gradle, and the editors and IDEs that target them into a self-contained `~/.bunny`. Each SDK version receives its own scoped data directory (`~/.m2`, `~/.gradle`, `~/.npm`), so JDK 21's caches never mix with JDK 25's, and `bunny uninstall` leaves your home directory clean. No sudo, no shell hooks, one Go binary — with identical behavior in your terminal, your IDE, your CI pipeline, and over SSH.

Bunny currently supports Linux on `x86_64`/`amd64`.

```bash
curl -fsSL https://raw.githubusercontent.com/cristatus/bunny/main/install.sh | sh
~/.bunny/bin/bunny setup && exec $SHELL

bunny install jdk-21 maven gradle node-22 jetbrains-toolbox
bunny use jdk-21
bunny run mvn -version
```

## Why bunny

Most developers assemble a Java + Node workstation from some combination of `sdkman`, `nvm`, `mise`, `asdf`, Linuxbrew, Flatpak, distro packages, and hand-downloaded tarballs — each strong in its own niche. Bunny's goal is to cover that whole workstation, SDKs and the editors and IDEs that target them, from a single tool.

Its scope is deliberately narrow:

- **Per-tool data isolation.** `~/.m2`, `~/.gradle`, and Node's npm/pnpm/Yarn caches are redirected to bunny's per-app data directory through environment variables (`MAVEN_ARGS`, `GRADLE_USER_HOME`, `NPM_CONFIG_*`, `PNPM_*`, `YARN_*`) declared in each manifest. Switching from JDK 21 to 25 leaves Gradle daemons intact, and uninstalling Node 22 removes its caches with it — no orphaned multi-gigabyte `~/.m2` left behind.
- **One binary, symlink shims.** `bunny init` adds a single env-only line (PATH, `XDG_DATA_DIRS`, and the zsh `fpath`), with no command-wrapping shell functions. Every tool dispatches through a real symlink via `argv[0]`, so what runs is exactly what is on disk — in any terminal, IDE, or container.
- **Per-project version pinning.** Place a `.bunny-version` file in a project root and `mvn`, `node`, and `java` resolve to that project's pinned versions automatically — no shell hooks, no `cd` listeners. Bunny also reads `.sdkmanrc`, `.tool-versions`, and `.java-version`, so existing projects work without conversion.
- **First-class Java.** Multiple JDK vendors (Temurin, Corretto, Zulu, GraalVM) via the [Foojay](https://api.foojay.io/) API; generated Gradle/Maven **toolchains**, so a build compiles against the correct JDK regardless of which one launched it; and `requires: ["jdk>=17"]` constraints that select a satisfying JDK at run time. See [First-class Java](docs/java.md).
- **Bounded, curated catalog.** The Java and Node ecosystems, plus the editors and IDEs used to write Java and Node code. It does not attempt parity with brew or nixpkgs. See [bunny-catalog](https://github.com/cristatus/bunny-catalog).
- **Forkable for teams.** Point `catalog.remote` at your team's internal git repository, vendor a corporate JDK with custom certificates, and onboarding reduces to a single `curl | sh`. See [Team deployment](docs/teams.md).

Portability without surprises: bunny isolates only where the upstream tool does not isolate itself. SDKs (node, jdk, gradle, maven, and so on) share one global directory per version, so bunny redirects them through the launcher's `env:`; GUI applications (VS Code, Cursor, Zed, JetBrains Toolbox) already namespace their own configuration, so bunny runs them natively against their normal host paths. In both cases applications exec directly, so any tool you spawn sees the host's normal `$HOME` layout. See [Portability model](docs/portability.md).

## Quick start

```bash
# Install bunny itself (downloads the latest release, verifies checksum)
curl -fsSL https://raw.githubusercontent.com/cristatus/bunny/main/install.sh | sh

# One-step setup: session env (so the desktop sees bunny's apps),
# shell completions, and your shell rc. Auto-detects your shell.
~/.bunny/bin/bunny setup
exec $SHELL          # or: systemctl --user import-environment PATH XDG_DATA_DIRS
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

Pin a bunny version with `BUNNY_VERSION=v0.2.0 curl ... | sh`, or pick a different install root with `BUNNY_HOME=/opt/bunny`.

## A typical Java workflow

```bash
bunny install jdk-21 jdk-17 maven gradle    # multiple JDKs side-by-side
bunny install corretto-21 graalvm-21         # other vendors, same `jdk` slot
bunny use jdk-21                              # set JDK 21 as the global default
bunny run jdk-17 -- java -version             # one-off run with JDK 17

# Per-project pin: drop this in $PROJECT_ROOT/.bunny-version
echo "jdk 17" > .bunny-version
echo "maven 3.9" >> .bunny-version
java -version    # → 17, even though the global default is 21
```

The shim walks up from the current directory looking for a pin file and falls back to the global default. It behaves identically in IntelliJ's embedded terminal, in CI, and under `make` — no shell hooks are involved.

Bunny also configures **Gradle/Maven toolchains**, so the JDK a build *compiles* with is independent of the JDK that *launched* it: a module targeting 17 builds against bunny's JDK 17 even when everything runs under 21. The configuration is regenerated automatically as you install JDKs. See [First-class Java](docs/java.md).

## A typical Node workflow

```bash
bunny install node-22 node-24 pnpm bun

# In project A: pin Node 22
echo "node 22" > .bunny-version
node --version    # → 22.x

# In project B: pin Node 24
echo "node 24" > .bunny-version
node --version    # → 24.x
```

Each Node version's npm prefix and cache, pnpm store, and Yarn cache and global folder are isolated per package (via `NPM_CONFIG_*`, `PNPM_*`, `YARN_*`), so a cache populated under Node 18 never leaks into your Node 22 environment.

## Commands

```
bunny install <id>          install a package
bunny uninstall <id>        remove (use --purge to also drop per-version SDK data)
bunny list                  list installed packages (--remote for full catalog)
bunny info <id>             show package details
bunny search <query>        substring search across catalog
bunny use <id>              switch active SDK version (e.g. jdk-17 → jdk-21)
bunny run <id> [-- args]    run a package binary
bunny update                check for updates (installed packages)
bunny update --apply        apply available updates
bunny doctor                health check (bwrap, PATH, XDG_DATA_DIRS, shims)
bunny setup                 one-step: session env (desktop) + completions + shell rc
bunny init <shell>          print the shell setup snippet (used by setup / eval)
bunny completion <shell>    print the shell completion script (bash, zsh, fish)
bunny clean                 prune download cache and tmp dirs
bunny reshim                regenerate shims for globally-installed executables (npm -g, etc.)
bunny toolchains            regenerate Gradle/Maven JDK toolchain config from installed JDKs
```

`bunny setup` also drops bunny's own completion into `share/`, discovered by the same PATH/XDG/fpath wiring — so after setup, `bunny <TAB>` completes subcommands and `bunny install <TAB>` completes package IDs (installed-only for `uninstall`/`use`/`run`).

Maintainer/CI commands live under `bunny dev`.

## Documentation

- [First-class Java](docs/java.md) — multi-vendor JDKs, Gradle/Maven toolchains, `requires` version constraints
- [Portability model](docs/portability.md) — isolate only where the upstream tool doesn't: SDKs redirected per-version via `env:`, GUI apps run native
- [Per-project pinning](docs/pinning.md) — `.bunny-version` plus `.sdkmanrc`/`.tool-versions`/`.java-version`, lookup order, IDE integration tips
- [Team deployment](docs/teams.md) — fork the catalog, host internally, onboard with one command
- [Corporate environments](docs/corporate.md) — proxies, custom CA bundles, `~/.m2/settings.xml`, internal artifact repos
- [Architecture](docs/architecture.md) — package boundaries, state ownership, and mutation transactions
- [Changelog](CHANGELOG.md) — notable changes in each release
- [Roadmap](ROADMAP.md) — what's next, what's deliberately out of scope

## Directory layout

```
~/.bunny/
├── app/{id}/                   installed app files
├── bin/                        bunny binary + symlink shims
│   ├── bunny
│   ├── java -> bunny           shims dispatch via argv[0]
│   └── code  -> bunny
├── catalog/{category}/{id}/    optional local manifests (override remote)
├── share/                      icons, .desktop files, completions
├── config.yaml                 user config (catalog URL)
└── var/
    ├── app/{id}/               manifest-defined per-version SDK data (e.g. redirected .m2/, .gradle/)
    │   └── manifest.yaml       install-time manifest cache (drives runtime + uninstall)
    ├── cache/                  download cache
    ├── mutation.lock           serializes state-changing commands
    ├── state.json              installed packages, providers
    └── tmp/                    temp build dirs
```

## Building from source

```bash
make build      # → ./bin/bunny
make test
make install    # copy ./bin/bunny → ~/.bunny/bin/bunny
```

## Comparison

| | bunny | sdkman | mise | brew (Linux) | nix |
|---|---|---|---|---|---|
| Java + Node toolchain | first-class | Java only | yes | yes | yes |
| GUI editors / IDEs | yes | no | no | partial | yes |
| Per-version SDK isolation | yes (env) | no | no | no | partial |
| Per-project version pinning | `.bunny-version` (+ reads `.sdkmanrc` / `.tool-versions`) | `.sdkmanrc` | `mise.toml` | no | `flake.nix` |
| Shell startup cost | none (symlink shims) | bash function | shim binary | none | none |
| Container-friendly | yes | via shell hooks | yes | yes | yes |
| Single binary | yes | no | yes | no | no |
| Forkable team catalog | yes | no | yes | tap-style | yes |
| Catalog size | ~35, growing | ~50 (JVM only) | thousands (varies) | tens of thousands | 100k+ |

Bunny is intentionally narrower than mise/brew/nix and intentionally broader than sdkman.

## License

MIT
