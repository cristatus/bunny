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
    └── bunny/config.yaml               # lists your fork under catalogs:
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
catalogs:
  - name: your-org
    remote: https://raw.githubusercontent.com/your-org/bunny-catalog/main
```

The URL needs to serve `index.json` at its root and the manifest path recorded
by each index entry (`packages/<id>/manifest.yaml` by default). Any standard
HTTP or file endpoint works: GitHub raw, GitLab raw, internal static sites,
S3 buckets, or local `file://` URLs.

For a private repository, typical setups include:

- a public-readable mirror bucket populated by internal CI
- a lightweight reverse proxy injecting auth headers
- a distributed catalog checkout, listed under `catalogs:` above your remote so
  its manifests win

## Adding a catalog instead of forking

A fork means carrying every upstream package in your own repository and
rebasing to stay current. If your additions are yours alone — internal tools, a
vendored build, a package upstream has no interest in — list your catalog beside
the public one instead:

```yaml
catalogs:
  - name: acme
    remote: https://raw.githubusercontent.com/your-org/bunny-catalog/main
  - name: bunny
    remote: https://raw.githubusercontent.com/cristatus/bunny-catalog/main
```

Your catalog then holds only the packages you actually maintain, with its own
CI and its own `bunny dev update` schedule. Upstream packages keep coming from
upstream.

Where both carry a package id, the first one listed serves it. That is what
makes the layering safe to adopt: a package your catalog owns cannot be taken
over by upstream publishing a higher version, and pinning a version in your own
catalog holds.

`bunny doctor` prints the catalogs in the order they resolve, and `bunny info`
names the one a package came from. See [Configuration](config.md#catalogs) for
the full resolution rules.

## Local catalog override

A checkout listed above your remote overrides it per package. Point one at a
directory of your own and put the override in
`<checkout>/packages/<id>/manifest.yaml`:

```yaml
catalogs:
  - name: overrides
    local: ~/src/bunny-overrides
  - name: your-org
    remote: https://raw.githubusercontent.com/your-org/bunny-catalog/main
```

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
merging. Both commands take `--catalog <name>` to name the checkout they act
on, which is required when more than one is configured.

For tighter control, run `bunny dev update <id>` manually for the packages you
trust to auto-bump and skip the cron entirely on internal manifests.

## Auditing installations

`~/.local/share/bunny/state.json` provides a flat JSON record of installed
packages and versions on each machine, making it straightforward to parse from
configuration management or MDM tooling. There's no built-in fleet view yet;
see [ROADMAP](../ROADMAP.md).

## Lockfiles and reproducibility

The catalog repository functions as a lockfile. Pinning a catalog's `remote` to a
specific Git commit SHA guarantees reproducible toolchains across the entire team:

```yaml
catalogs:
  - name: your-org
    remote: https://raw.githubusercontent.com/your-org/bunny-catalog/<sha>
```
