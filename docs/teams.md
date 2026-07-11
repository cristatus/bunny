# Team deployment

Bunny is built to be forked. The catalog is just a directory of YAML manifests in a git repo, and the bunny CLI reads its catalog URL from `~/.bunny/config.yaml`. Together, that's enough to give a team a single source of truth for "the official toolchain".

This doc walks through the practical setup.

## The shape of a team deployment

```
your-org/
├── bunny-catalog/          fork of cristatus/bunny-catalog (or built from scratch)
│   ├── index.json
│   ├── sdk/
│   │   ├── jdk-21/manifest.yaml      # internal vendored Temurin build with corp certs
│   │   └── node-22/manifest.yaml
│   └── java-tool/
│       ├── maven/manifest.yaml        # pre-configured to your internal Nexus
│       └── gradle/manifest.yaml
└── dotfiles/
    └── bunny/config.yaml               # points catalog.remote at your fork
```

Team members:

```bash
curl -fsSL https://raw.githubusercontent.com/cristatus/bunny/main/install.sh | sh
~/.bunny/bin/bunny init >> ~/.zshrc && exec $SHELL
cp /path/to/dotfiles/bunny/config.yaml ~/.bunny/config.yaml
bunny install jdk-21 maven gradle node-22
```

That is the entire onboarding: a new hire goes from zero to a matching toolchain in a few minutes.

## Pointing at your fork

`~/.bunny/config.yaml`:

```yaml
catalog:
  remote: https://raw.githubusercontent.com/your-org/bunny-catalog/main
```

The URL needs to serve `index.json` at its root and `<category>/<id>/manifest.yaml` for each package. Anything that does that works — GitHub raw, GitLab raw, an internal pages site, an S3 bucket with directory listing, even a file:// path.

For a private GitHub/GitLab repo, the simplest options are:

- a public mirror via internal CI (push to a public-readable bucket)
- a self-hosted reverse proxy that injects an auth token
- distribute a pre-populated `~/.bunny/catalog/` directory (a local catalog under that path always wins over the remote)

## Local catalog override

Per-package overrides go into `~/.bunny/catalog/<category>/<id>/manifest.yaml`. If a package id exists in both local and remote, local wins. Use this for:

- pinning a package to a specific version while the team catalog moves
- testing a manifest change before opening a PR upstream
- patching a `prepare:` script for a one-off platform issue

## Vendoring an internal JDK build

If your org distributes its own JDK build (custom CA bundles, security policies, vendored `lib/security/cacerts`), publish it like any other manifest:

```yaml
id: jdk-21-corp
name: "Corporate JDK 21"
description: "Internal Temurin 21 build with org CA bundle"
version: "21.0.5+1-corp.3"
category: sdk
provides: jdk

sources:
  - url: "https://artifacts.your-org.internal/jdk/jdk-{version}-linux-x64.tar.gz"
    file: "jdk.tar.gz"
    sha256: "..."

prepare:
  - "tar xf jdk.tar.gz -C {pkg} --strip-components=1"

bin:
  - { name: java,    path: "{app}/bin/java" }
  - { name: javac,   path: "{app}/bin/javac" }
  - { name: jshell,  path: "{app}/bin/jshell" }
  - { name: jar,     path: "{app}/bin/jar" }
  - { name: keytool, path: "{app}/bin/keytool" }
```

Because it `provides: jdk`, it slots into the same capability slot as upstream Temurin, so a project's `.bunny-version` pinning `jdk 21` will pick it up automatically.

## Pre-configured tools (Maven, Gradle)

You can ship a Maven manifest that points Maven at your internal Nexus. Drop a `settings.xml` into the package data dir with a `prepare:` step, then reference it from the manifest's `env:` via `MAVEN_ARGS` (e.g. `--settings {data}/settings.xml`) so every `mvn` invocation picks it up. For details on settings.xml and corp CA bundles, see [Corporate environments](corporate.md).

## Updating the team catalog

Same flow as the upstream catalog: a daily GitHub Actions cron runs `bunny dev update` and opens a PR with version bumps. Reviewers approve, merge, and team members get the new versions on their next `bunny update --apply`.

For tighter control, run `bunny dev update <id>` manually for the packages you trust to auto-bump and skip the cron entirely on internal manifests.

## Auditing what a team member has installed

`~/.bunny/var/state.json` is a flat JSON file listing installed packages and versions. A short script gathered across machines (or surfaced via your existing MDM) gives you the picture. There's no built-in fleet view yet — see [ROADMAP](../ROADMAP.md).

## Lockfiles

The catalog itself is the lockfile. Pin `catalog.remote` to a specific commit instead of `main` and the team is locked to that revision until you update it:

```yaml
catalog:
  remote: https://raw.githubusercontent.com/your-org/bunny-catalog/<sha>
```

This is the simplest way to make a release of the team toolchain reproducible.
