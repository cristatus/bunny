# Changelog

All notable changes to Bunny are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Warnings are shown by default, as `WARN …` lines on stderr. Before, they
  appeared only with `-l`, which hid several failures that are reported
  nowhere else: a skipped integration file, a rollback that could not
  restore shims, an unreachable catalog, or a pin of a version that is not
  installed. `-l` still enables full diagnostics.

### Fixed

- Hardened sandbox: on hosts where `/home` is a symlink (Fedora Silverblue,
  Kinoite), launching from `$HOME` or granting `/var/home` exposed the real
  home. Both are now recognised as the host home.
- Sandbox: a package could replace its isolated home with a symlink to the
  host home, so a later `home: ephemeral` launch seeded from the real home
  and `persist` could bind real files such as `~/.ssh`. A symlinked or
  non-directory isolated home is now refused.
- Hardened sandbox: a write grant covering `~/.config` let the package
  rewrite bunny's `config.yaml`, including its own sandbox policy. The file
  now stays read-only under any writable grant.
- Hardened sandbox: an `SSH_AUTH_SOCK` naming a directory such as `/home` was
  bound back read-write. Only an actual socket is bound now.
- Install-time `prepare:` steps could reach the session bus under `/run`
  (and through it start host processes), read the real home, and inherit
  the user's environment, tokens included. `/run` and the home are now
  hidden, and steps start from a minimal environment.
- `bunny run --explain` reported a hardened working directory as read-only
  when a launch from the home had left it unmounted, and reported D-Bus as
  masked where the session bus uses an abstract address, which host
  networking still reaches.
- Reinstalling, updating or uninstalling a package no longer overwrites or
  deletes an icon, shell completion or man page that another tool had already
  put in place. Bunny left such a file alone at install, but still treated the
  path as its own afterwards. It now records the files it actually writes
  (`files` in `state.json`) and touches only those. Packages installed before
  this change keep the old behaviour until their next install.
- A reinstall or uninstall no longer deletes a directory named `<package>.old`
  or `<package>.delete` next to the install tree unless bunny created it. With
  an install root such as `~/Applications`, such a directory may be yours.
- `bunny update` and `bunny dev update` missed new releases of packages whose
  latest GitHub release falls outside their tag pattern and whose asset name
  contains `{version}`, reporting "no update" instead. This affected
  async-profiler, mvnd, sbt, scala, shellcheck and shfmt.
- `bunny dev` could write another file's checksum into a manifest when a
  multi-file sums file (`SHA256SUMS`, `checksums.txt`) had no line naming the
  download. It now fails instead of picking the first hash in the file.
- With a configured catalog unreachable and uncached, `bunny install` of one
  of its packages reported "not found in catalog", and `bunny update`
  skipped its packages and could report everything up to date. Install now
  says the catalog is unavailable, and the update check reports the packages
  it could not check and exits non-zero.
- `bunny update <id>` for a package that is not installed said "all packages
  are up to date"; it now says the package is not installed.
- `bunny reshim <typo>` and `bunny dev update <typo>` reported success. Both
  now refuse an unknown target.
- `bunny toolchains` claimed to regenerate config when no Gradle or Maven
  package was installed.
- Ctrl+C during an install or update now cancels it and rolls back, instead
  of killing bunny between moving a package into place and recording it. A
  second Ctrl+C still exits at once.
- A partial download that cannot be resumed (a `.part` left by an earlier,
  larger version under the same name) no longer fails with "HTTP 416" on
  every run; it is discarded and fetched again. A download that stops
  sending is abandoned after 60 seconds and resumed, instead of hanging for
  up to 30 minutes.
- A manifest whose sources download to the same filename is refused; the
  second used to overwrite the first.
- `bunny self-update` replaces the binary the shims actually run, not
  `~/.local/bin/bunny`, so `go install` and symlinked installs update.
- A failed uninstall puts back the shims it had already removed.
- A catalog entry whose manifest names another package is refused, rather
  than installed under one id and run as the other.
- `bunny dev update` refuses to commit a manifest that no longer validates
  (a Debian `1:2.3-1~jammy` version, say). Debian versions are ordered the
  way dpkg orders them, and a JSON API's numeric version such as `3.10` is
  no longer read as `3.1`.
- `bunny update` could see a new version in the refreshed index and still
  load the old manifest through the CDN, or have a slow background refresh
  replace the index it had just fetched. Both are fixed.
- A scoped reshim (`bunny use`, `bunny reshim node`) no longer takes over
  global commands owned by another capability.
- `bunny doctor` checks only the shims bunny manages. Another tool's dangling
  link in `~/.local/bin` is no longer a failure, a deleted shim is reported,
  and a command shadowed by an earlier PATH entry is flagged.
- Shell init and the rc line `bunny setup` writes quote their paths, so a
  home or `BUNNY_HOME` with a space or quote works. zsh init no longer leaves
  exit status 1, and `setup` names an existing init line written for another
  layout.
- `bunny run <pkg> <TAB>` completes files in bash, zsh and fish.
- Pinning in a directory whose `.bunny-version` is a symlink writes through
  it instead of replacing it. A relative `HOME` is refused. Newlines in
  desktop entry categories, keywords and MIME types are refused. A reinstall
  keeps the recorded catalog source.

## [0.7.1] - 2026-09-15

### Fixed

- Install-time `prepare:` steps failed on ostree distros (Fedora Silverblue,
  Kinoite) with `bwrap: Can't mount on symlink destination /home`: the
  prepare sandbox mounted its scratch `$HOME` directly onto `/home`, which
  those distros symlink to `/var/home`, and bwrap refuses to mount onto a
  symlink destination. The scratch home now lives under `/var/tmp` instead —
  real and non-symlink everywhere, and unlike `/tmp`, not a path some
  prepare-step tooling (e.g. `codex`'s installer) refuses to place itself
  under as looking like a temp dir.

## [0.7.0] - 2026-09-14

### Added

- `bunny self-update` checks GitHub for a newer bunny release and replaces the
  running binary in place. It is independent of the catalog/install
  machinery every other package goes through — bunny is not a catalog
  package, has no `state.json` entry, and is the one file every shim already
  resolves to — so plain `bunny update` never mentions it.
- A `man:` manifest field installs man pages into a shared XDG man root
  (`~/.local/share/man` under XDG, `$BUNNY_HOME/share/man` under a single
  root), sectioned by each page's own filename. `bunny init`/`bunny setup`
  now export `MANPATH` for it in bash, zsh, and fish, unconditionally: unlike
  desktop entries and icons, `man` implementations do not search the XDG
  data dirs on their own. An entry can name one file or a directory of them
  — the latter for a tool like `gh` that ships one page per subcommand,
  where listing each individually would not be reasonable; directory entries
  are symlinked rather than copied so a reinstall can find exactly what the
  previous version installed without re-reading a source tree that, by the
  time the old integration is torn down, may already hold the new version's
  files.
- `bunny run --no-sandbox <id>` skips the policy a `sandbox.packages` entry
  applies, for one launch, so "is the sandbox what broke this?" costs one
  command rather than an edit to `config.yaml` and an edit back. It removes
  Bunny's own layer only: a launch inside an enclosing sandbox stays inside
  it. Combining it with `--sandbox` or `--sandbox-profile` is refused rather
  than resolved by precedence.
- Sandbox `env:` policy over the host environment the payload inherits.
  `hide` drops the variables it names and appends across layers; `keep`
  admits only what it names and replaces an inherited list. Entries are exact
  names or one trailing `*` as a prefix. The filesystem boundary never
  covered this channel: a hardened package that cannot read `~/.aws` was
  still handed `AWS_SECRET_ACCESS_KEY`, because the launch environment starts
  from the host's own. What Bunny sets for the launch — a manifest `env:`, a
  dependency's `JAVA_HOME`, the redirected `HOME` — is not host state and
  always applies, and `PATH` crosses every `keep` list so the package can
  still exec a child.
- `bunny doctor` resolves every armed sandbox policy and checks the host
  paths it names, so a deleted `hide` path or a moved grant surfaces on
  demand instead of at the next launch of that package, which may be weeks
  away. An armed package that is no longer installed is reported too: the
  entry arms nothing.
- `bunny doctor` names command shims occupied by a file it cannot prove it
  created — another tool's install script having overwritten one is the
  common case — which `bunny update` and `bunny reshim` otherwise keep
  failing on with no guided fix.

### Changed

- **Breaking:** `bunny sandbox check <id>` is gone; `bunny run --explain`
  now reports it in a "Host readiness" section after the enforcement plan —
  whether the exact helpers and kernel facilities that policy needs are
  actually available, not just what the policy asks for. `--explain` exits
  non-zero if a required one is missing. `sandbox check`'s `--profile` flag
  is `--sandbox-profile`, already accepted by `run`.

### Fixed

- A policy a nested launch cannot apply is reported where the user sees it.
  Inside an existing sandbox the child adds no layer, and the report naming
  what was dropped went only to the logger, which the CLI silences unless
  `--log-level` asks for it.
- A hardened launch from the host home, or another protected root, keeps the
  default `fs.cwd: read` but cannot bind the directory back without undoing
  the emptied home. It now says so, instead of leaving the package with no
  view of where it was launched and its own error as the only signal.
- `fs.cwd: write` counts as an effective writable root. A package launched
  inside such a sandbox is no longer refused a redirected home under the very
  project the enclosing policy made writable.
- `bunny run --explain` no longer reports a hardened boundary as excluding
  X11 while the launch shares the host network namespace. X clients reach the
  server through the abstract socket `@/tmp/.X11-unix/X0`, which lives in the
  network namespace rather than the filesystem, so no mask and no baseline
  reaches it; `net: private` or `net: none` is the only lever.

## [0.6.0] - 2026-09-08

### Added

- `bunny pin --exact` records an installed release as `package@version`.
  Direct shims, dependent tools, and global commands reject release drift.
  Exact pins guard launches; they do not fetch historical releases or add
  side-by-side patch installations.
- Dedicated sandbox acceptance CI and `make test-sandbox`, with mandatory
  helpers, a private session bus, and real namespace/network/overlay tests.
- Hardened sandbox boundary (`boundary: hardened`): a deny-by-default,
  kernel-enforced allowlist — read-only host root, hidden host home, private
  `/run`/`/tmp`/`/var/tmp`, explicit `fs.read`/`fs.write` grants, and
  mandatory PID/IPC/UTS/session isolation with a full capability drop.
  Integrations default off and grant targets fail closed.
- Three-state network policy (`net: host | private | none`, or a `net:` block
  with `inbound`/`egress`). `private` gives the package its own stack via
  pasta, with nftables egress allowlists and a pinned DNS forwarder.
  Hostnames are rejected: name-based filtering cannot be enforced.
- Filtered D-Bus for hardened packages: `features.dbus: true` starts a
  portal-only `xdg-dbus-proxy`; the raw bus is never bound.
- `home: ephemeral` discards HOME writes on exit, except paths under
  `persist:`. `home: clean` gives a blank HOME every run with no seed at all.
- Built-in `ephemeral` and `clean` profiles, so `bunny run --sandbox-profile
  ephemeral <id>` makes any installed package throwaway with no config.
- Built-in `agent` profile, the only hardened built-in: writable working
  directory, read-only host, hidden home, network up, credential agents off.
  An agent's own config persists in the isolated home, so nothing needs
  bind-mounting by hand.
- A sandboxed launch whose HOME is redirected now receives the host's Git
  identity as `GIT_AUTHOR_*`/`GIT_COMMITTER_*`, resolved by asking Git in the
  working directory so `include`/`includeIf` chains and per-repository
  identities come out right. Only name and email cross; `credential.helper`,
  `core.sshCommand`, `url.*.insteadOf`, `http.extraHeader`, and
  `commit.gpgSign` deliberately do not. An identity already in the
  environment wins.
- `bunny run --explain <id>` now leads with a risk summary, then prints the
  detailed enforcement plan without launching.
- `bunny sandbox check <id>` preflights the exact resolved policy and only the
  helper programs and kernel facilities that launch requires.
- The outermost sandbox owns the boundary: a launch inside an existing sandbox
  runs directly under it and never builds a second bubblewrap layer. A child
  home is redirected only when the parent's effective writable roots make it
  available; otherwise the launch fails closed. Anything its policy asked for that only a
  layer could apply — a stricter boundary, a narrower network, an ephemeral
  home, extra masks, filesystem grants — is reported by `--explain` and warned
  about at launch rather than half-applied.
- Sandbox helper artifacts now live in unique per-launch directories. Active
  ownership is tied to PID start time, allowing later launches to remove
  crashed-process state without racing a reused PID or a live sandbox.
- The immutable nested context is versioned and records effective writable
  roots, so child-home decisions use explicit capabilities and incompatible
  Bunny versions fail closed.
- Doctor checks for `pasta`, `nft`, `xdg-dbus-proxy`, and overlayfs, shown
  only when a configured policy needs them.
- Scoped sandbox now masks a disabled feature's documented endpoints with
  kernel-backed mounts, not just its environment variables, so libraries that
  fall back to a default socket are actually cut off.
- New `agents` feature key (SSH agent, GnuPG, keyring) and `tty` feature key
  (new session and PID namespace). All built-in profiles keep `tty` enabled.
- `config.yaml` is bound read-only inside every sandbox, so a package cannot
  rewrite the policy that governs it.
- Hardened sandboxes bind Bunny's own layout read-only — shim directory,
  `state.json`, manifest snapshots, and the install roots — so a shim resolves
  a toolchain inside the boundary while `bunny install` still fails on the
  read-only root. The paths come from the resolved layout, so they follow a
  configured `install:` root and need no `fs.read` entry.
- A `.bunny-version` pin may name a package id instead of a version
  (`jdk corretto-21`), so a project can fix a specific vendor build. A bare
  version still derives `<capability>-<version>`; package ids cannot start
  with a digit, so the forms are unambiguous. A pinned package must actually
  provide the capability.

See [Sandboxing](docs/sandbox.md) for the full model and trust boundary.

### Changed

- `bunny sandbox <id>` is now `bunny run --sandbox <id>`, with
  `--sandbox-profile <name>` and `--explain`. Arguments after the package id
  pass through to the binary, so a tool flag Bunny does not define no longer
  needs a `--` escape.
- Sandbox policy resolves through two layers — the selected profile, then the
  package's inline override — so the effective policy is readable from
  `config.yaml` alone.
- Nested sandbox state moved from `BUNNY_SANDBOX_CONTEXT` to a read-only
  mounted context file, so a sandboxed process cannot forge or unset its
  inherited restrictions.
- A `hide` path that does not exist is now a launch error rather than being
  silently skipped.
- `bunny sandbox check`, `bunny run --explain`, and `bunny dev validate` print
  through the same renderer as every other result: color on a terminal, none
  in a pipe, columns sized to their contents, and the check glyphs
  `bunny doctor` uses.
- **Breaking:** the `online-cli` profile is gone and `offline-cli` is now
  `offline`. The built-ins each cover one axis — `desktop` for device
  integration, `offline` for the network, `ephemeral`/`clean` for the home,
  `agent` for the boundary — where `online-cli` was `desktop` minus
  integrations a CLI never opens, and `offline-cli` named a program shape for
  a policy that has nothing to do with one. Rename `offline-cli` to `offline`;
  replace `online-cli` with `desktop`, or with `agent` for a coding agent.

### Removed

- `sandbox.packages.<id>.activation`: presence under `sandbox.packages` is now
  the whole activation rule.
- `features.network`, in favor of `net` alone.
- `sandbox:` in a package manifest: run-time policy is the user's alone. No
  catalog manifest used it.
- `bunny sandbox` as its own command; see Changed.
- Reading other tools' pin files (`.tool-versions`, `.sdkmanrc`,
  `.java-version`). They encode a vendor and patch level Bunny cannot honor —
  `java=21.0.1-amzn` names Corretto, which was reduced to "JDK 21" and then
  ran Temurin — so a shared pin file silently diverged from what it asked for.
  `.bunny-version` is now the only format read, and `bunny pin` writes it.

### Fixed

- Maven, Gradle, and other packages resolve project pins for their runtime
  dependencies. Incompatible pinned JDKs fail instead of using a global JDK.
- Sandbox acceptance probes use the current CLI and require payload execution
  when testing D-Bus filtering; home-isolation tests use an actual secret marker.
- Maven toolchain XML escapes installation paths, vendor names, and versions.
- Pin creation and doctor diagnostics honor vendor package IDs and exact
  releases. Malformed/unreadable pins fail closed; pin writes are atomic.
- Installer version-selection example passes `BUNNY_VERSION` to the installer
  shell. Java/vendor guidance and competitor comparisons reflect actual behavior.
- `bunny update --apply` with nothing to install now answers that the packages
  are up to date instead of reporting "updated 0 packages", and naming a
  package that is not installed prints only the error.
- Shell completion: `bunny search` completes past its first term in bash and
  zsh, the short flags complete alongside their long forms, and
  `--sandbox-profile` and `--command` complete their values.
- A hardened sandbox with `net: host` could not resolve DNS. The baseline's
  private `/run` masked `/run/systemd/resolve`, and `/etc/resolv.conf` is a
  symlink into it, so every name lookup failed with the network otherwise
  reachable. The resolver configuration is now bound back for host
  networking; restricted modes still mask it.

### Security

- The hardened D-Bus proxy validates its upstream bus address to a plain
  `unix:` socket, so a package cannot inject a `unixexec:` address that would
  run a process outside the sandbox.
- Hardened filesystem grants and the working directory are refused when they
  equal, or are an ancestor of, a protected root, including via a symlink.
- A `persist:` entry is symlink-resolved and refused if it lands outside the
  isolated home, so an earlier run cannot plant a symlink out of the sandbox.

## [0.5.0] - 2026-08-24

### Added

- `catalogs:` in `config.yaml`: named catalogs in priority order, so an
  organization can add its own packages beside the public ones without
  maintaining a fork. The first catalog listed that carries a package serves it,
  so none can take over a package id held by one above it. An unreachable
  catalog is skipped rather than failing the lookup. `bunny doctor` reports one
  row per catalog, `bunny search`/`bunny info` name the catalog a package came
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

- `bunny search` ranks results by which field matched, so an id or capability
  hit outranks a passing mention in a description; takes several terms, all of
  which must match; and shares one filter set with `bunny list` (`-t/--tag`,
  `--capability`, `--kind`), adding `--installed`/`--available` of its own.
  Filters alone are a valid query, so `bunny search --tag ai` browses that
  slice of the catalog, while a query with nothing to narrow by is an error
  rather than the whole catalog.
- A table's final column of free text is clipped to the room the terminal has
  left, so one long description no longer wraps every row. Piped output keeps
  the whole text.
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
  prints tags in full).
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

- `bunny list --remote` — `bunny search` browses the catalog, with the same
  filters and an optional query: `bunny search --tag ai`,
  `bunny search --kind sdk --available`. `--active` stays on `bunny list`,
  where an active provider is a property of what is installed.
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

[Unreleased]: https://github.com/cristatus/bunny/compare/v0.7.1...HEAD
[0.7.1]: https://github.com/cristatus/bunny/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/cristatus/bunny/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/cristatus/bunny/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/cristatus/bunny/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/cristatus/bunny/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/cristatus/bunny/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/cristatus/bunny/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/cristatus/bunny/releases/tag/v0.1.0
