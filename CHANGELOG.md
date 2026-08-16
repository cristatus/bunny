# Changelog

All notable changes to Bunny are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `env:` and `dirs:` blocks in `config.yaml`, keyed by package id, capability,
  or `*`. This is where per-version data isolation now lives; see
  [Configuration](docs/config.md) for recipes reproducing the old defaults.
- `install:` in `config.yaml` sets where each kind of package is installed.
  `install.sdk: ~/opt` puts every JDK and build tool where an IDE's file
  picker can reach it.
- `config.example.yaml`, a commented copy of every setting, to copy to
  `~/.config/bunny/config.yaml`. `bunny doctor` reports the path bunny reads,
  whether or not the file exists.
- [Configuration](docs/config.md), documenting the file, the env precedence
  order, and the placeholders.

### Changed

- **Nothing is isolated by default.** `mvn` fills `~/.m2`, `gradle` uses
  `~/.gradle`, and npm, pnpm, Yarn, deno, and bun use their native caches and
  install roots. Per-version isolation is now opt-in through `env:`.
- **Bunny follows the XDG base directories**: installs, catalog, and state in
  `~/.local/share/bunny`, config in `~/.config/bunny`, downloads in
  `~/.cache/bunny`, shims in `~/.local/bin`. Desktop entries and icons go to
  the real XDG directories, so `bunny init` no longer sets `XDG_DATA_DIRS` and
  an installed IDE appears in the launcher without logging out.
- `BUNNY_HOME` now collapses the whole layout under one root instead of naming
  the default, for containers, CI, and fleet images.
- `category:` is replaced by `tags:`, a list describing what a package is.
  `bunny list --tag java` filters on them, the vocabulary is declared in the
  catalog's `tags.yaml` and enforced by `dev validate`, and packages live in a
  flat `packages/<id>/` directory whose path the index records explicitly, so
  the catalog's layout no longer dictates URLs or install locations.
- Packages install into one of three roots by kind: `sdk/`, `cli/`, `app/`,
  each configurable. Manifests declare `kind:`; an undeclared one is inferred,
  and a desktop entry implies `app`.
- `state.json` records each package's kind, and a path only when it sits
  outside the default root. Changing a root affects the next install only.
- `npm -g` installs into node's own prefix, matching `nvm`: globals belong to
  the Node version that installed them.
- Gradle's generated toolchain block goes to whichever `gradle.properties`
  Gradle actually reads, now `~/.gradle/gradle.properties` by default.
- `global-bins:` may point at `{app}` as well as `{data}`, but not `{home}`:
  global shims stay a per-package-tree feature.
- Installs stage inside their destination root, so the rename that completes
  an install never crosses a filesystem. `bunny clean` sweeps every root.
- `bunny setup` skips `environment.d/bunny.conf` when the systemd session
  already exports the shim dir, since the generator does not deduplicate.
- `bunny doctor` reports the active layout and checks each root it writes to.

### Fixed

- `bunny install --force` no longer deletes a directory bunny did not create.
  Install trees carry a `.bunny-package` marker, and force-replacement and
  uninstall verify ownership first.
- Desktop entries carry an `X-Bunny-Package` key, and bunny only overwrites or
  removes entries that have it, now that they share
  `~/.local/share/applications` with everything else.
- Shim install and removal verify that an existing symlink is one bunny
  created, instead of assuming every symlink in the shim dir is bunny's.
- A package with no `kind:` is no longer assumed to be a cli tool, which put
  GUI editors beside `ripgrep`.
- A clean first install of a GUI package writes its desktop entry and icon.
  Integration resolved `{app}` before state recorded the install location.
- `make install` copies the binary to `~/.local/bin`, or `$BUNNY_HOME/bin`.

### Migration

None. Bunny is pre-1.0 and this release changes the on-disk layout without an
automatic migration: an existing `~/.bunny` is no longer read. Delete it and
reinstall, which is quick and leaves no residue now that nothing is isolated.

## [0.4.0] - 2026-08-05

### Added

- `bunny dev validate` checks every local catalog manifest against
  `index.json` without network access, for catalog CI.

### Fixed

- `bunny dev update` only rewrites a package's secondary sources (e.g. a
  bundled plugin) when its primary source also advanced, instead of bumping
  them independently.
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
- Long `list` and `search` output uses an external pager on interactive
  terminals. Configure it with `BUNNY_PAGER` or `PAGER`, or control it with
  `--pager=auto|always|never` and `--no-pager`.

### Changed

- `bunny use` now identifies the activated capability, replaced provider, and
  regenerated shims.
- Catalog index summaries now carry `provides` and `requires` metadata so
  capability-aware discovery works without downloading every manifest.

### Fixed

- Update detection compares versions by precedence rather than exact string
  match, so a differing-but-not-newer upstream version (such as a vendor JDK
  respin) is no longer offered as a downgrade, while build-number-only bumps
  are still detected.
- JDK update discovery now selects standard non-JavaFX archives consistently
  and can verify vendor releases through configured checksum endpoints or
  GitHub's published SHA-256 release-asset digests when Foojay exposes only
  SHA-1 or no checksum.
- Download progress bars render as one continuous block on color terminals,
  removing the faint seams that repeated block glyphs left in some fonts.

## [0.2.0] - 2026-07-18

### Added

- Spartan, information-dense command output with TTY-aware semantic color,
  aligned tables and detail views, and clean errors with typo suggestions.
- Interactive per-package progress for install, uninstall, and update workflows,
  with stable plain output for pipes and `--no-progress`.
- Resilient batch installs that skip packages already at the requested version
  and continue after individual package failures.

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
  through `.bunny-version`, `.tool-versions`, `.sdkmanrc`, and `.java-version`.
- Isolated per-version data and environment handling for Java and Node
  toolchains without shell hooks.
- Gradle and Maven JDK toolchain generation across installed JDK providers.
- Desktop entries, icons, shell completions, environment setup, diagnostics,
  cache cleanup, and global-tool reshim support.
- Automated upstream update checks and catalog-maintainer rewrite commands.
- Linux `amd64` release archives with SHA-256 checksums.

### Reliability and security

- Atomic state and generated-file replacement, schema validation, and
  cross-process mutation locking.
- Staged install, uninstall, and provider-switch operations with compensating
  rollback on failure.
- SHA-256/SHA-512 artifact verification, bounded downloads and metadata,
  resumable transfers, retry handling, timeouts, and cancellation.
- Strict manifest, path, command, environment, and integration ownership
  validation.
- Install-time preparation isolation through Bubblewrap where required.

[Unreleased]: https://github.com/cristatus/bunny/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/cristatus/bunny/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/cristatus/bunny/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/cristatus/bunny/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/cristatus/bunny/releases/tag/v0.1.0
