# Changelog

All notable changes to Bunny are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `catalogs:` in `config.yaml`: named catalogs in priority order, so an
  organization can add its own packages beside the public ones without
  maintaining a fork. The first catalog listed that carries a package serves it,
  so none can take over a package id held by one above it. An unreachable
  catalog is skipped rather than failing the lookup. `bunny doctor` reports one
  row per catalog, `bunny info`/`list --remote` name the catalog a package came
  from once several are usable, `state.json` records it per install, and install
  and update report a package that changes hands. `bunny dev validate`/`dev
  update` take `--catalog <name>`, completing the checkouts that are actually
  there. See [Configuration](docs/config.md#catalogs).
- Opt-in per-package bubblewrap execution: manifests may recommend policy,
  built-in `desktop`, `online-cli`, and `offline-cli` profiles plus custom
  profiles provide shared defaults, and `sandbox.packages.<id>` both
  activates by default and overrides policy without replacing inherited
  values; `activation: on-demand` retains that policy for
  `bunny sandbox <id>` without changing normal launches. The
  lightweight model isolates package HOME/XDG state and can mask paths or
  disable host integrations; it is not a hardened security boundary. See
  [Sandboxing](docs/sandbox.md).
- `env:` and `dirs:` blocks in `config.yaml` (keyed by package id, capability,
  or `*`) for per-version data isolation; see [Configuration](docs/config.md).
- `install:` in `config.yaml` to set per-kind install roots, e.g.
  `install.sdk: ~/opt` for IDE-visible JDK/build-tool paths.
- `config.example.yaml` reference template and [Configuration](docs/config.md)
  docs; `bunny doctor` reports the config path read, whether or not it exists.
- `{data}` placeholder in `prepare:` steps, expanding to the real data path;
  writes are staged and merged in on commit, so a manifest can seed default
  config in one step.

### Changed

- **Nothing is isolated by default**: `mvn`, `gradle`, npm, pnpm, Yarn, deno,
  and bun use their native caches and install roots; data redirection is opt-in
  via `env:`, and runtime sandboxing is opt-in per package.
- **XDG base directory compliance**: installs and state in
  `~/.local/share/bunny`, config in `~/.config/bunny`, downloads in
  `~/.cache/bunny`, shims in `~/.local/bin`; desktop entries and icons use the
  real XDG dirs, so `bunny init` no longer sets `XDG_DATA_DIRS`.
- A catalog checkout is no longer read from a built-in path: list it under
  `catalogs:` like any other catalog, and `bunny dev` says so rather than
  guessing one.
- `BUNNY_HOME` now collapses the whole layout under one root (containers, CI,
  fleet images) instead of just naming the default.
- Packages install into one of three configurable roots by kind: `sdk/`,
  `cli/`, `app/`, declared via manifest `kind:` (inferred when absent; a
  desktop entry implies `app`).
- `state.json` now records each package's kind and install location, so
  changing an install root only affects new installs.
- Install-time manifest snapshots moved to `manifests/<id>.yaml`, beside
  `state.json` and separate from `{data}`.
- `category:` replaced by `tags:` (filterable via `bunny list --tag` /
  `bunny search`, vocabulary enforced by `dev validate` against `tags.yaml`);
  packages now live in a flat `packages/<id>/` layout.
- `bunny list` shows each package's `kind` instead of tags (`bunny info` still
  prints tags in full); `--remote` reports kind from the index without
  fetching every manifest.
- `-l/--log-level` now replaces progress output (no spinner, status line, or
  summary) instead of competing with it; `debug` logs the resolved layout,
  config path, install roots, catalog source, staging/install/cache paths, and
  every per-package outcome.
- Waiting on the mutation lock says so, instead of appearing to hang.
- `npm -g` installs into node's own prefix, matching `nvm`: globals belong to
  the Node version that installed them.
- Gradle's generated toolchain block goes to whichever `gradle.properties`
  Gradle actually reads, now `~/.gradle/gradle.properties` by default.
- `global-bins:` may point at `{app}` as well as `{data}`, but not `{home}`:
  global shims stay a per-package-tree feature.
- Installs now stage beside their destination, so the completing rename never
  crosses a filesystem; `bunny clean` sweeps every root.
- `bunny setup` skips `environment.d/bunny.conf` when the systemd session
  already exports the shim dir (the generator doesn't dedupe).
- `bunny doctor` now reports the active layout, config path, catalog in use,
  and effective install root per kind.

### Removed

- Paging of `list`/`search` output (`--pager`, `--no-pager`, `BUNNY_PAGER`,
  `PAGER`) — use `| less` instead; both commands still write plain text to
  stdout.

### Fixed

- An unreachable catalog no longer reports packages as absent: an index or
  fetch failure now says the catalog is unavailable, and only a status that
  means the catalog looked and has nothing there is treated as absence.
- The `html` update checker now picks the newest version among every match of
  `version-pattern`, not the first one, so listing pages that order entries
  oldest-first (Apache directory indexes, Maven `maven-metadata.xml`) no
  longer report a stale version.
- Bunny shims launched inside a sandboxed application's terminal now retain
  the real XDG/BUNNY_HOME layout, give an always-sandboxed child package its
  own isolated HOME, and inherit outer restrictions without an unnecessary
  nested bubblewrap layer. Unsandboxed runtime-installed tools such as an npm
  global keep their provider's configured data/cache paths while inheriting
  the enclosing application's HOME and restrictions.
- **Ownership checks before removing/replacing shared directories**: install
  trees require a `.bunny-package` marker, desktop entries an
  `X-Bunny-Package` key, and shims must resolve into bunny's own bin dir or
  the running binary; uninstall removes only the icon extension the manifest
  declares.
- A single-root `BUNNY_HOME` install now survives into a new shell: `bunny
  setup` records it in `environment.d` and the `bunny init` rc line;
  previously a shell with only the bin dir on `PATH` fell back to an empty
  XDG layout.
- `BUNNY_HOME` must be an absolute path; bunny and `install.sh` now reject a
  relative one instead of resolving it against the working directory.
- `bunny doctor` now warns when the running binary belongs to an install other
  than the active layout, naming the root and the fix.
- Repointing an install root no longer strands existing packages of that kind
  or lets `--force` replace an unrelated directory.
- `prepare:` writes to `{work}` no longer get lost to a masked tmpfs staging
  root under `$HOME`.
- Progress output and log records no longer interleave on stderr.
- A package with no `kind:` is no longer assumed to be a cli tool, which put
  GUI editors beside `ripgrep`.
- A first install of a GUI package now correctly writes its desktop entry and
  icon (previously `{app}` resolved before state recorded the location).
- `make install` copies the binary to `~/.local/bin`, or `$BUNNY_HOME/bin`.

### Migration

None. Pre-1.0 layout change: an existing `~/.bunny` is no longer read. Delete
it and reinstall, which is quick and leaves no residue now that nothing is
isolated.

## [0.4.0] - 2026-08-05

### Added

- `bunny dev validate`: validates local catalog manifests against
  `index.json` offline, for catalog CI.

### Fixed

- `bunny dev update` now only rewrites secondary sources (e.g. a bundled
  plugin) when the primary source also advanced.
- Catalog index writes no longer HTML-escape `requires` operators, so
  constraints like `jdk>=17` survive a rewrite intact.
- JDK update checks now follow Foojay's `checksum_uri` when its inline
  checksum is blank (affects some vendor builds, e.g. JBR JCEF).

## [0.3.0] - 2026-07-22

### Added

- `bunny list` now shows each package's category, provided capability, and
  whether it is the active provider; `--capability` and `--active` narrow the
  installed or remote view.
- Package search includes provided capabilities and runtime requirements, and
  `bunny info` reports active-provider state, project pins, requirements, and
  reverse dependencies.
- External pager for `list`/`search` on interactive terminals
  (`BUNNY_PAGER`/`PAGER`, `--pager=auto|always|never`, `--no-pager`).

### Changed

- `bunny use` now identifies the activated capability, replaced provider, and
  regenerated shims.
- Catalog index summaries now carry `provides` and `requires` metadata so
  capability-aware discovery works without downloading every manifest.

### Fixed

- Update detection now compares versions by precedence rather than exact
  string match, so a vendor JDK respin isn't offered as a downgrade while
  build-number-only bumps are still detected.
- JDK update discovery now consistently selects non-JavaFX archives and
  verifies vendor releases via configured checksum endpoints or GitHub
  SHA-256 release digests when Foojay exposes only SHA-1.
- Download progress bars render as one continuous block on color terminals,
  removing the faint seams repeated block glyphs left in some fonts.

## [0.2.0] - 2026-07-18

### Added

- Spartan, information-dense command output with TTY-aware semantic color,
  aligned tables and detail views, and clean errors with typo suggestions.
- Interactive per-package progress for install, uninstall, and update
  workflows, with stable plain output for pipes and `--no-progress`.
- Resilient batch installs that skip packages already at the requested
  version and continue after individual package failures.

### Changed

- `bunny update` now compares installed versions with the curated catalog;
  upstream discovery remains a maintainer operation under `bunny dev update`.
- `bunny dev update` checks independent upstream sources concurrently.
- Help and shell completion now follow the command workflow more closely,
  including completion for multiple install and uninstall operands.
- Logging is disabled by default and can be enabled explicitly with `-l`.

### Removed

- `bunny update --all`; whole-catalog upstream discovery now belongs to
  `bunny dev update`.
- `bunny which`, because exposing an underlying executable path lets callers
  bypass Bunny's launcher environment and per-version data isolation.

### Security

- Catalog updates now require checksums published by the upstream project;
  hashes computed from an unverified download are no longer accepted.

## [0.1.0] - 2026-07-17

Initial public release.

### Added

- Install, update, uninstall, list, search, and run workflows for curated
  standalone developer tools and SDKs.
- Local and remote catalogs with local overrides, an offline-capable index
  cache, and install-time manifest snapshots.
- Command shims, active capability providers, and per-project version pinning
  through `.bunny-version`, `.tool-versions`, `.sdkmanrc`, and
  `.java-version`.
- Isolated per-version data and environment handling for Java and Node
  toolchains without shell hooks.
- Gradle and Maven JDK toolchain generation across installed JDK providers.
- Desktop entries, icons, shell completions, environment setup, diagnostics,
  cache cleanup, and global-tool reshim support.
- Automated upstream update checks and catalog-maintainer rewrite commands.
- Linux `amd64` release archives with SHA-256 checksums.
- Atomic state and generated-file replacement, schema validation, and
  cross-process mutation locking.
- Staged install, uninstall, and provider-switch operations with compensating
  rollback on failure.
- Bounded, resumable downloads with retry handling, timeouts, and
  cancellation.

### Security

- SHA-256/SHA-512 artifact verification against upstream-published checksums.
- Strict manifest, path, command, environment, and integration ownership
  validation.
- Install-time `prepare:` steps isolated via Bubblewrap where required.

[Unreleased]: https://github.com/cristatus/bunny/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/cristatus/bunny/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/cristatus/bunny/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/cristatus/bunny/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/cristatus/bunny/releases/tag/v0.1.0
