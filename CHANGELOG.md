# Changelog

All notable changes to Bunny are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/cristatus/bunny/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/cristatus/bunny/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/cristatus/bunny/releases/tag/v0.1.0
