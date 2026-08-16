## Configuration

Bunny reads one optional file, `~/.config/bunny/config.yaml` (or
`$BUNNY_HOME/config.yaml` when bunny is collapsed under a single root). A
missing file is not an error: it means the defaults, which are the stock
catalog, the standard install locations, and no isolation.

Bunny never creates this file. [`config.example.yaml`](../config.example.yaml)
in the repository is a commented copy of everything below:

```bash
mkdir -p ~/.config/bunny
cp config.example.yaml ~/.config/bunny/config.yaml
```

Copying it changes nothing until you uncomment something. `bunny doctor` prints
the path bunny reads and the install root each kind resolves to, which is the
quickest way to confirm a setting is actually in effect:

```
✓ config   ~/.config/bunny/config.yaml
✓ install  app=~/.local/share/bunny/app  cli=~/.local/share/bunny/cli  sdk=~/opt
```

```yaml
catalog:
  remote: https://github.com/acme/bunny-catalog

install:
  sdk: ~/opt

env:
  node:
    NPM_CONFIG_PREFIX: "{data}/npm-global"

dirs:
  node:
    - "{data}/npm-global"
```

Unknown top-level keys are rejected rather than ignored, so a typo fails loudly
on the next command instead of silently doing nothing.

### `catalog`

Where package manifests come from. A local checkout takes precedence over the
remote, package by package, so a local override shadows one package without
forking the rest.

```yaml
catalog:
  remote: https://github.com/acme/bunny-catalog
  local: ~/src/bunny-catalog
```

`remote` defaults to the public
[bunny-catalog](https://github.com/cristatus/bunny-catalog). `local` defaults to
`~/.local/share/bunny/catalog`, and setting it is the usual way to work on a
catalog: point bunny at your checkout and `bunny dev validate` and
`bunny dev update` operate on it directly. A path that does not exist is not an
error, bunny simply uses the remote, so `bunny doctor` reports which one is in
play:

```
✓ catalog  local: ~/src/bunny-catalog
```

See [Team deployment](teams.md).

### `install`

Where each kind of package is installed. Keys are the three install kinds, and a
value is an absolute path or one starting with `~/`:

| Kind  | Contains                                                                       | Default                    |
| ----- | ------------------------------------------------------------------------------ | -------------------------- |
| `sdk` | JDKs, Node, Maven, Gradle, sbt, pnpm: anything another tool may need a path to | `~/.local/share/bunny/sdk` |
| `cli` | plain commands (ripgrep, jq, gh)                                               | `~/.local/share/bunny/cli` |
| `app` | GUI applications (code, cursor, zed, jetbrains-toolbox)                        | `~/.local/share/bunny/app` |

The kind comes from the package's manifest. It is separate from `tags:`, which
describe what a package is for search, and from the catalog's folder layout,
which bunny reads from the index rather than deriving.

The reason to set this is usually `sdk`. Bunny does not isolate SDK data, so an
install tree is a plain, self-contained directory that any tool can consume, and
IDEs ask you to point at exactly these paths: IntelliJ wants a JDK home, a Maven
home, and a Gradle home. Navigating to `~/.local/share/bunny/sdk/jdk-21` in a
file picker is worse than navigating to `~/opt/jdk-21`.

```yaml
install:
  sdk: ~/opt
```

```
~/opt/jdk-21  ~/opt/corretto-21  ~/opt/maven  ~/opt/gradle  ~/opt/node-22
```

Directories are named by package id, so a picker shows `jdk-21`, `corretto-21`,
`semeru-17`: the same names `bunny list` and `bunny install` use.

Bunny records where each package was installed. Changing a root, or a catalog
changing a package's kind, affects the **next** install only. Packages already
on disk keep running from where they are, and `bunny uninstall` removes the
recorded location rather than a recomputed one. To move an existing package,
uninstall and reinstall it.

Roots may be on different filesystems: each one gets its own `.staging`
subdirectory, so the atomic rename that finishes an install never has to cross a
filesystem boundary.

Pointing a root at a directory you also keep things in is safe. Every install
tree carries a `.bunny-package` marker naming the package that owns it, and
bunny refuses to replace or remove a directory without one, so `--force` on a
name collision reports the clash instead of deleting your files.

### `env`

Extra environment variables applied when bunny launches a package. This is the
only place isolation is configured: catalog manifests describe how to install
and wire a package, never whether your data gets redirected.

Keys are matched from least to most specific, so a later match wins:

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

Values expand the same placeholders manifests use:

| Placeholder         | Expands to                                                                 |
| ------------------- | -------------------------------------------------------------------------- |
| `{app}`             | the package's install tree, wherever it was installed                      |
| `{data}`            | the per-package data dir, `~/.local/share/bunny/data/<id>`, yours to clear |
| `{home}`            | your real `$HOME`                                                          |
| `{bin}`             | the shim dir, `~/.local/bin`                                               |
| `{share}`           | `~/.local/share`                                                           |
| `{id}`, `{version}` | the package id and version                                                 |

A manifest's `prepare:` steps see a different set, because they run while the
package is being built rather than after it is installed:

| Placeholder         | Expands to                                            |
| ------------------- | ----------------------------------------------------- |
| `{src}`             | the downloaded sources, and the working directory     |
| `{pkg}`             | the tree being built, which becomes `{app}`           |
| `{work}`            | the staging root holding both, the only writable area |
| `{data}`            | the package's real data dir                           |
| `{id}`, `{version}` | the package id and version                            |

`{data}` expands to the same real path it will have at run time, so a step can
bake it into a config file and seed that directory in one go. The writes are
staged and merged in once the install commits, filling in only what is missing,
so a config edited since the last install survives an upgrade.

There is no `{app}` during `prepare:`; the install tree does not exist yet.
Write to `{pkg}`, which is renamed into place at the end of the install.

Because `{data}` is per package id, a capability-keyed entry still gives each
version its own directory: `node-22` and `node-24` both get `NPM_CONFIG_PREFIX`,
pointing at different paths.

Config env is applied to a package's dependencies too. Setting `jdk` env reaches
every tool that declares `requires: ["jdk"]`, not just a direct `java` launch.

Precedence at launch, lowest to highest:

1. the host environment bunny inherited
2. each dependency's manifest `env:` (then that dependency's config `env:`)
3. the package's own manifest `env:`
4. the package's config `env:`

### `dirs`

Directories to create before launch, keyed exactly like `env`. Most tools create
their own directories, so this is rarely needed. Reach for it when a tool
expects its target to exist, or when the path is buried inside a compound
variable that bunny cannot parse:

```yaml
env:
  maven:
    MAVEN_ARGS:
      "-Dmaven.repo.local={data}/repository --toolchains {data}/toolchains.xml"
dirs:
  maven:
    - "{data}/repository"
```

### Recipes

Per-version isolation for the tools that had it built in before bunny 0.5. Take
only the entries you want; each one costs disk and a cold cache on first use,
which is exactly why none of them are the default.

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
      "-Dsbt.global.base={data}/sbt -Dsbt.boot.directory={data}/boot
      -Dsbt.ivy.home={data}/ivy"
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

Two caveats worth knowing before you copy the whole block:

- **`GRADLE_USER_HOME` moves more than caches.** Your
  `~/.gradle/gradle.properties` (credentials, `org.gradle.jvmargs`) stops being
  read. Copy it into the new location, or leave Gradle alone: it already
  namespaces its caches by version under `~/.gradle/caches/<version>/`, and
  daemons are keyed by JVM and JVM args, so versions do not actually collide.
- **`PNPM_STORE_DIR` defeats pnpm's design.** The store is one global
  content-addressable pool hardlinked into every project. Splitting it per Node
  version gives pnpm npm's disk profile.

If you set `GRADLE_USER_HOME`, bunny writes its generated JDK toolchain block to
the `gradle.properties` in that directory rather than the one in `~/.gradle`.

### Isolating from `$HOME` without splitting per version

`{data}` is per package id, so it always splits per version. To keep a tool out
of `$HOME` while sharing one cache across versions, point at a fixed path:

```yaml
env:
  maven:
    MAVEN_ARGS:
      "-Dmaven.repo.local={home}/.cache/maven/repository --toolchains
      {data}/toolchains.xml"
```
