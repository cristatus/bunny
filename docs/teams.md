# Team deployment

Bunny is built to be forked. The catalog is a directory of YAML manifests in a
git repo, and the CLI reads its catalog URL from `~/.config/bunny/config.yaml`,
allowing a team to maintain a single source of truth for its official toolchain.

## The shape of a team deployment

```
your-org/
├── bunny-catalog/                      # fork of cristatus/bunny-catalog (or built from scratch)
│   ├── index.json
│   ├── tags.yaml                       # tag vocabulary manifests may use
│   └── packages/
│       ├── jdk-21/manifest.yaml        # internal Temurin build with corp certs
│       ├── node-22/manifest.yaml
│       ├── maven/manifest.yaml         # pre-configured to internal Nexus
│       └── gradle/manifest.yaml
└── dotfiles/
    └── bunny/config.yaml               # points catalog.remote at your fork
```

Team onboarding:

```bash
curl -fsSL https://raw.githubusercontent.com/cristatus/bunny/main/install.sh | sh
~/.local/bin/bunny setup && exec $SHELL
cp /path/to/dotfiles/bunny/config.yaml ~/.config/bunny/config.yaml
bunny install jdk-21 maven gradle node-22
```

A new developer goes from a blank machine to a matching workstation in a few
minutes.

## Pointing at your fork

`~/.config/bunny/config.yaml`:

```yaml
catalog:
  remote: https://raw.githubusercontent.com/your-org/bunny-catalog/main
```

The URL needs to serve `index.json` at its root and the manifest path recorded
by each index entry (`packages/<id>/manifest.yaml` by default). Any standard
HTTP or file endpoint works: GitHub raw, GitLab raw, internal static sites,
S3 buckets, or local `file://` URLs.

For a private repository, typical setups include:

- a public-readable mirror bucket populated by internal CI
- a lightweight reverse proxy injecting auth headers
- a distributed catalog checkout placed at `~/.local/share/bunny/catalog/` or
  configured via `catalog.local` (local manifests always override remote entries)

## Local catalog override

Per-package overrides live in
`~/.local/share/bunny/catalog/packages/<id>/manifest.yaml`. If a package ID
exists in both local and remote catalogs, the local manifest takes precedence.
Use this to:

- pin a specific package version locally while the team catalog evolves
- test manifest modifications before submitting an upstream pull request
- patch a `prepare:` build step for local platform experiments

## Vendoring an internal JDK build

If your organization distributes a customized JDK build (including corporate
root certificates or custom `cacerts` keystores), publish it like any standard
manifest:

```yaml
id: jdk-21-corp
name: "Corporate JDK 21"
description: "Internal Temurin 21 build with org CA bundle"
version: "21.0.5+1-corp.3"
kind: sdk
tags: [java, jdk]
provides: jdk

sources:
  - url: "https://artifacts.your-org.internal/jdk/jdk-{version}-linux-x64.tar.gz"
    file: "jdk.tar.gz"
    sha256: "..."

prepare:
  - "tar xf jdk.tar.gz -C {pkg} --strip-components=1"

bin:
  - { name: java, path: "{app}/bin/java" }
  - { name: javac, path: "{app}/bin/javac" }
  - { name: jshell, path: "{app}/bin/jshell" }
  - { name: jar, path: "{app}/bin/jar" }
  - { name: keytool, path: "{app}/bin/keytool" }
```

Because it declares `provides: jdk`, it occupies the standard Java capability
slot, so any project `.bunny-version` pinning `jdk 21` resolves to it
automatically.

## Pre-configured tools (Maven, Gradle)

You can ship a Maven manifest pre-configured for an internal repository manager.
A `prepare:` step copies a template `settings.xml` into `{data}`, and the
manifest's `env:` block references it via `MAVEN_ARGS` (such as
`--settings {data}/settings.xml`). Because `{data}` is seeded only when a file is
missing, user modifications survive subsequent package upgrades. See
[Corporate environments](corporate.md).

## Updating the team catalog

Team catalogs follow the same workflow as the upstream catalog: a scheduled
CI job runs `bunny dev update` and opens pull requests for version bumps.
Team members receive the new versions on their next `bunny update --apply`.

Run `bunny dev validate` in CI to ensure manifests match `index.json` before
merging.

For tighter control, run `bunny dev update <id>` manually for the packages you
trust to auto-bump and skip the cron entirely on internal manifests.

## Auditing installations

`~/.local/share/bunny/state.json` provides a flat JSON record of installed
packages and versions on each machine, making it straightforward to parse from
configuration management or MDM tooling. There's no built-in fleet view yet;
see [ROADMAP](../ROADMAP.md).

## Lockfiles and reproducibility

The catalog repository functions as a lockfile. Pinning `catalog.remote` to a
specific Git commit SHA guarantees reproducible toolchains across the entire team:

```yaml
catalog:
  remote: https://raw.githubusercontent.com/your-org/bunny-catalog/<sha>
```
