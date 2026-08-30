# Configuration

Bunny reads one optional file, `~/.config/bunny/config.yaml` (or
`$BUNNY_HOME/config.yaml` when Bunny is collapsed under a single root). A
missing file is not an error: it defaults to the stock catalog, the standard
install locations, native `$HOME` storage, and direct package execution.

Bunny never creates this file automatically.
[`config.example.yaml`](../config.example.yaml) in the repository is a commented
template:

```bash
mkdir -p ~/.config/bunny
cp config.example.yaml ~/.config/bunny/config.yaml
```

Copying it changes nothing until you uncomment a setting. `bunny doctor` prints
the active configuration path and resolved install roots:

```
✓ config   ~/.config/bunny/config.yaml
✓ install  app=~/.local/share/bunny/app  cli=~/.local/share/bunny/cli  sdk=~/opt
```

Unknown top-level keys are rejected during validation so typos fail immediately.

```yaml
catalogs:
  - name: acme
    local: ~/src/acme-catalog
  - name: bunny
    remote: https://raw.githubusercontent.com/cristatus/bunny-catalog/main

install:
  sdk: ~/opt

env:
  node:
    NPM_CONFIG_PREFIX: "{data}/npm-global"

dirs:
  node:
    - "{data}/npm-global"
```

## `catalogs`

Lists the catalogs package manifests come from, highest priority first. Each
entry needs a `name` and exactly one of `local`, a checkout on disk, or
`remote`, an HTTP catalog:

```yaml
catalogs:
  - name: company
    local: ~/src/company-catalog
  - name: company-remote
    remote: https://raw.githubusercontent.com/company/bunny-catalog/main
  - name: bunny
    remote: https://raw.githubusercontent.com/cristatus/bunny-catalog/main
```

The name identifies the catalog in `bunny doctor`, in `state.json`, and in
`--catalog`, so it is held to the same shape as a package id: lowercase
`[a-z0-9-]`.

With nothing configured, the chain is the public
[bunny-catalog](https://github.com/cristatus/bunny-catalog) alone:

```yaml
catalogs:
  - name: bunny
    remote: https://raw.githubusercontent.com/cristatus/bunny-catalog/main
```

The list is exhaustive, and there is nothing implicit in it. Writing it replaces
that default rather than adding to it, so every catalog Bunny reads is one you
named — including a checkout, which is a catalog like any other and has no
special path Bunny looks in. Pointing an entry at a git checkout is how catalog
manifests are developed: `bunny dev validate` and `bunny dev update` operate on
it directly, and refuse to guess when no checkout is listed.

Listing your own catalog above the public one adds packages beside it without a
fork; see [Team deployment](teams.md#adding-a-catalog-instead-of-forking).

### Which catalog serves a package

The order is the answer. When several catalogs carry the same package id, the
first one listed serves it, whatever versions the others publish. That makes the
list genuinely priority-ordered, and means no catalog can take over a package id
held by one above it — pinning a version in your own catalog holds, and a
checkout listed first is an override with nothing special about it beyond its
position.

A catalog that cannot be reached is skipped, and the next one serves the package
rather than the whole lookup failing. A catalog that carries the package and then
cannot produce its manifest hands off to the next catalog too, rather than
failing an install over a transient error — but that is a substitution the
ordering did not ask for, so `state.json` records which catalog actually served
and `bunny info` reports it.

A checkout that is not on disk is skipped rather than answering emptily, which
would mask a remote below it. `bunny doctor` lists every configured catalog and
says which are usable.

### Seeing which catalog answered

`bunny search` gains a `Catalog` column and `bunny info` a `Catalog` row
whenever more than one catalog is usable. Both stay hidden for a single one,
where the answer carries no information — and a checkout that is not on disk
does not count as a second catalog.

Bunny records the catalog each package was installed from in `state.json`, and
reports a package that changes hands:

```
yq now comes from catalog "company" (was "upstream")
```

Removing or renaming a catalog is reported in its own words — `"company" is no
longer configured` — since dropping the catalog that owned a package moves it to
whoever is left. Renaming also orphans that catalog's index cache, which costs
one re-fetch.

`bunny dev validate` and `bunny dev update` rewrite a checkout, and take
`--catalog <name>` to say which. With one checkout configured the flag is
optional; with several it is required, since rewriting the wrong catalog is not
something a default should decide.

See [Team deployment](teams.md).

## `install`

Configures the root directory for each package kind. Keys are the three install
kinds, accepting absolute paths or paths prefixed with `~/`:

| Kind  | Contains                                                                       | Default                    |
| ----- | ------------------------------------------------------------------------------ | -------------------------- |
| `sdk` | JDKs, Node, Maven, Gradle, sbt, pnpm: anything another tool may need a path to | `~/.local/share/bunny/sdk` |
| `cli` | plain commands (ripgrep, jq, gh)                                               | `~/.local/share/bunny/cli` |
| `app` | GUI applications (code, cursor, zed, idea)                                     | `~/.local/share/bunny/app` |

The package kind is defined in its manifest. Setting `install.sdk: ~/opt` places
JDKs and build tools where IDE file pickers can easily reach them:

```
~/opt/jdk-21  ~/opt/corretto-21  ~/opt/maven  ~/opt/gradle  ~/opt/node-22
```

Directories are named by package ID (`jdk-21`, `corretto-21`, `semeru-17`),
matching the names used by `bunny list` and `bunny install`.

Bunny records where each package was installed. Changing an install root affects
new installs only. Packages already on disk remain at their recorded locations,
and `bunny uninstall` removes the recorded path rather than a recomputed one.
To move an existing package, uninstall and reinstall it.

Roots may reside on different filesystems: each install root maintains its own
`.staging` subdirectory, ensuring the atomic directory rename at the end of an
install never crosses a filesystem boundary.

Pointing a root at an existing directory is safe. Every install tree carries a
`.bunny-package` marker identifying the owning package, and Bunny refuses to
replace or remove a directory without one.

## `env`

Extra environment variables applied when Bunny launches a package. This is
where per-version data redirection is configured. Manifest `env` values describe
runtime wiring; user config controls optional cache and data relocation.
Sandbox policy is separate from `env:` entirely, and manifests carry none of
it: see [Sandboxing](sandbox.md).

Keys match from least to most specific (most specific wins):

| Key                                    | Matches                        |
| -------------------------------------- | ------------------------------ |
| `*`                                    | every package                  |
| a capability (`node`, `jdk`, `gradle`) | every package that provides it |
| a package id (`node-22`, `jdk-21`)     | that package only              |

```yaml
env:
  "*":
    LANG: C.UTF-8
  node:
    NPM_CONFIG_PREFIX: "{data}/npm-global" # all Node versions
  node-22:
    NODE_OPTIONS: "--max-old-space-size=8192" # this version only
```

Values expand template placeholders:

| Placeholder         | Expands to                                                                 |
| ------------------- | -------------------------------------------------------------------------- |
| `{app}`             | the package install tree                                                   |
| `{data}`            | the per-package data dir, `~/.local/share/bunny/data/<id>`, yours to clear |
| `{home}`            | your real `$HOME`                                                          |
| `{bin}`             | the shim dir, `~/.local/bin`                                               |
| `{share}`           | `~/.local/share`                                                           |
| `{id}`, `{version}` | the package id and version                                                 |

Manifest `prepare:` steps receive build-time placeholders instead (manifest
authoring lives in the [catalog](https://github.com/cristatus/bunny-catalog)):

| Placeholder         | Expands to                                            |
| ------------------- | ----------------------------------------------------- |
| `{src}`             | downloaded sources and working directory              |
| `{pkg}`             | the tree being constructed (becomes `{app}`)          |
| `{work}`            | the staging root holding both                         |
| `{data}`            | the package runtime data dir                          |
| `{id}`, `{version}` | the package id and version                            |

A `prepare:` step writes `{data}` through staging; those files merge into the
real data dir on commit without overwriting existing user edits.

Because `{data}` resolves per package ID, a capability key gives each version
its own directory. Overrides also propagate to dependencies, so setting `jdk`
env applies to tools declaring `requires: ["jdk"]`.

Precedence at launch (lowest to highest):

1. Host environment inherited by the shell
2. Dependency manifest `env:` (followed by dependency config `env:`)
3. Target package manifest `env:`
4. Target package config `env:`

## `dirs`

Directories to create before launch, keyed identically to `env`. Useful when a
tool expects target directories to exist beforehand or when paths are embedded
in complex command flags:

```yaml
env:
  maven:
    MAVEN_ARGS:
      "-Dmaven.repo.local={data}/repository --global-toolchains {data}/toolchains.xml"
dirs:
  maven:
    - "{data}/repository"
```

## `sandbox`

`profiles:` names a policy, `packages:` activates one. Presence under
`packages:` is what applies the sandbox to every normal launch — there is no
activation field.

A profile no package selects is inert until named at launch with
`bunny run --sandbox-profile <name> <id>`, which is how a policy stays
available without being automatic. It also saves repeating a policy across
several packages, though that is the smaller reason to have it.

A package entry selects at most one profile (built-in or custom) and overrides
individual keys; `features` and `hide` merge additively rather than replacing.

```yaml
sandbox:
  profiles:
    private-no-network:       # a custom profile is a complete policy
      home: isolated
      net: none
      hide: [~/.ssh, ~/.aws]
  packages:
    code:
      profile: desktop        # built-in: desktop, agent, offline,
      features:               # ephemeral, clean (names are reserved)
        audio: false
      hide: [~/Documents/private]
    some-tool:
      profile: private-no-network
```

The full field set (`home`, `persist`, `features`, `hide`, `boundary`, `fs`,
`net`), what each built-in sets, and the nesting and trust model are in
[Sandboxing](sandbox.md).

## Recipes

Per-version isolation recipes:

```yaml
env:
  node:
    NPM_CONFIG_PREFIX: "{data}/npm-global"
    NPM_CONFIG_CACHE: "{data}/npm-cache"
    COREPACK_HOME: "{data}/corepack"
    PNPM_HOME: "{data}/pnpm-global"
    PNPM_STORE_DIR: "{data}/pnpm-cache"
    YARN_CACHE_FOLDER: "{data}/yarn/cache"
  gradle:
    GRADLE_USER_HOME: "{data}/gradle"
  maven:
    MAVEN_ARGS:
      "-Dmaven.repo.local={data}/repository --global-toolchains {data}/toolchains.xml"
  sbt:
    SBT_OPTS:
      "-Dsbt.global.base={data}/sbt -Dsbt.boot.directory={data}/boot -Dsbt.ivy.home={data}/ivy"
    COURSIER_CACHE: "{data}/coursier"
  deno:
    DENO_DIR: "{data}/deno"
  bun:
    BUN_INSTALL: "{data}/bun"

dirs:
  node:
    - "{data}/npm-global"
  maven:
    - "{data}/repository"
```

With this Node recipe, `npm install -g prettier` writes under the active Node
package's `{data}/npm-global`; run `bunny reshim` afterwards to expose it. See
[Sandboxing](sandbox.md#sandbox-the-app-redirect-sdk-data) for how this
composes with a sandboxed editor.

Two caveats:

- **`GRADLE_USER_HOME` moves user configuration**: `~/.gradle/gradle.properties`
  (credentials, `org.gradle.jvmargs`) is no longer read from `$HOME`. Copy it
  to the new location if needed. Gradle already namespaces its caches by
  version under `~/.gradle/caches/<version>/`. When `GRADLE_USER_HOME` is set,
  Bunny writes its generated JDK toolchain block to `gradle.properties` in that
  directory.
- **`PNPM_STORE_DIR` bypasses pnpm hardlink pooling**: Isolating the pnpm store
  per Node version disables pnpm's global content-addressable cache, increasing
  disk usage.

## Isolating from `$HOME` without splitting per version

`{data}` is scoped to each package ID. To relocate caches out of `$HOME` while
sharing a single cache across multiple versions of a tool, specify a static path:

```yaml
env:
  maven:
    MAVEN_ARGS:
      "-Dmaven.repo.local={home}/.cache/maven/repository --global-toolchains {data}/toolchains.xml"
```
