## Portability model

Bunny installs each package into an install root chosen by its kind (`sdk/`,
`cli/`, `app/`, all configurable). Whether it touches an app's persistent data
follows a single principle:

**Nothing is isolated by default.**

A tool run through bunny writes where it would have written had you installed it
yourself. `mvn` populates `~/.m2/repository`, `gradle` uses `~/.gradle`, `npm`
caches in `~/.npm`, VS Code reads `~/.config/Code`. Bunny installs the binary,
resolves which version to run, and gets out of the way. Nothing is redirected
unless you ask for it in `~/.config/bunny/config.yaml`.

This matches what `mise`, `sdkman`, `pyenv`, and `rustup` do, and it is the
behaviour most build caches are designed for: a Maven artifact or an npm tarball
is content-addressed and version-agnostic, so a private copy per SDK version
buys correctness you already had while costing gigabytes and cold caches.

### What manifests still set

Manifests keep an `env:` block. It carries the wiring a package cannot run
without, not policy:

```yaml
env:
  JAVA_HOME: "{app}"
```

```yaml
env:
  # Point Maven at the toolchains.xml bunny generates from the installed JDKs.
  MAVEN_ARGS: "--toolchains {data}/toolchains.xml"
```

Those values point at the install tree (`{app}`) or at a file bunny itself
generates (`{data}/toolchains.xml`). They are not redirecting your data
anywhere, and a forked catalog can put whatever it likes in `env:`.

### Where global installs land

`npm -g` installs into node's own prefix, which is that version's install tree,
so globals belong to the Node version that installed them and go away when it
does. This is how `nvm` behaves. Everything else (the npm cache, the pnpm store,
corepack, Yarn) uses its native host location and is shared across versions.

### GUI apps

VS Code, Cursor, Zed, and JetBrains Toolbox already namespace their own config
directories per application, and bunny adds nothing on top. They read and write
their normal host paths exactly as if you had installed them yourself.

### Not a sandbox

The model is **not** a security sandbox. Runtime launch is a plain direct exec.
Don't run untrusted software through bunny.

## Opting into isolation

Isolation lives in `~/.config/bunny/config.yaml`, keyed by package id, capability, or
`*` for everything. Values expand the same placeholders manifests use, so
`{data}` resolves per package version:

```yaml
env:
  node:
    NPM_CONFIG_PREFIX: "{data}/npm-global"
  gradle:
    GRADLE_USER_HOME: "{data}/gradle"
  maven:
    MAVEN_ARGS: "-Dmaven.repo.local={data}/repository --toolchains {data}/toolchains.xml"
```

Precedence at launch, lowest to highest: the host environment, each
dependency's manifest `env:`, the package's own manifest `env:`, then your
config. Config always wins, which is what makes it an override rather than a
suggestion.

Note the Maven line: `MAVEN_ARGS` is a single variable doing two jobs, so
overriding it means restating the toolchains flag the manifest set. The
manifest's current value is in `~/.local/share/bunny/data/maven/manifest.yaml`.

See [Configuration](config.md) for the full file reference.

## What survives uninstall

- the install tree (including `npm -g` globals): removed on uninstall, at the
  location recorded when it was installed.
- `data/<id>/manifest.yaml` (cache used by runtime + uninstall): removed.
- `data/<id>/<your configured dirs>`: kept unless `--purge`.

Data at native host paths (`~/.m2`, `~/.gradle`, `~/.npm`, `~/.config/Code`) is
never touched by `bunny uninstall`, with or without `--purge`. Bunny did not
create it and does not claim it.

Bunny's own bookkeeping (`state.json`, the download cache, and cached manifests)
lives under `~/.local/share/bunny/` and `~/.cache/bunny/`, independent of any
single package.
