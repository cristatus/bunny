# Configuration

Bunny reads one optional file, `~/.config/bunny/config.yaml` (or
`$BUNNY_HOME/config.yaml` when Bunny is collapsed under a single root). A
missing file is not an error: it defaults to the stock catalog, the standard
install locations, and native `$HOME` storage without isolation.

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
catalog:
  remote: https://github.com/acme/bunny-catalog
  local: ~/src/bunny-catalog

install:
  sdk: ~/opt

env:
  node:
    NPM_CONFIG_PREFIX: "{data}/npm-global"

dirs:
  node:
    - "{data}/npm-global"
```

## `catalog`

Configures where package manifests come from. A local checkout takes precedence
over the remote on a per-package basis, allowing you to shadow individual
packages without forking the entire catalog.

```yaml
catalog:
  remote: https://github.com/acme/bunny-catalog
  local: ~/src/bunny-catalog
```

`remote` defaults to the public [bunny-catalog](https://github.com/cristatus/bunny-catalog).
`local` defaults to `~/.local/share/bunny/catalog`. Pointing `local` at a git
checkout is the standard way to develop catalog manifests: `bunny dev validate`
and `bunny dev update` operate on it directly. If the path does not exist, Bunny
falls back to `remote`, and `bunny doctor` reports which source is active.

See [Team deployment](teams.md).

## `install`

Configures the root directory for each package kind. Keys are the three install
kinds, accepting absolute paths or paths prefixed with `~/`:

| Kind  | Contains                                                                       | Default                    |
| ----- | ------------------------------------------------------------------------------ | -------------------------- |
| `sdk` | JDKs, Node, Maven, Gradle, sbt, pnpm: anything another tool may need a path to | `~/.local/share/bunny/sdk` |
| `cli` | plain commands (ripgrep, jq, gh)                                               | `~/.local/share/bunny/cli` |
| `app` | GUI applications (code, cursor, zed, jetbrains-toolbox)                        | `~/.local/share/bunny/app` |

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
where per-version data isolation is configured; manifests describe build and
wiring requirements, never user isolation policy.

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

Manifest `prepare:` steps receive build-time placeholders:

| Placeholder         | Expands to                                            |
| ------------------- | ----------------------------------------------------- |
| `{src}`             | downloaded sources and working directory              |
| `{pkg}`             | the tree being constructed (becomes `{app}`)          |
| `{work}`            | the staging root holding both                         |
| `{data}`            | the package runtime data dir                          |
| `{id}`, `{version}` | the package id and version                            |

`{data}` expands to the real runtime path, allowing a preparation step to seed
default configuration files and bake the path into configs simultaneously.
Staged files are merged into `{data}` upon install commit without overwriting
existing user edits.

Because `{data}` resolves per package ID, a capability key gives each version
its own directory: `node-22` and `node-24` each receive distinct `NPM_CONFIG_PREFIX`
paths.

Environment overrides also propagate to package dependencies (for instance,
setting `jdk` environment applies to tools declaring `requires: ["jdk"]`).

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
      "-Dmaven.repo.local={data}/repository --toolchains {data}/toolchains.xml"
dirs:
  maven:
    - "{data}/repository"
```

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
      "-Dmaven.repo.local={data}/repository --toolchains {data}/toolchains.xml"
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

Important considerations:

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
      "-Dmaven.repo.local={home}/.cache/maven/repository --toolchains {data}/toolchains.xml"
```
