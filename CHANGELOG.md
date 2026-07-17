# Changelog

All notable changes to Bunny are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/cristatus/bunny/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/cristatus/bunny/releases/tag/v0.1.0
