# Portability model

Bunny installs each package into an install root chosen by its kind (`sdk/`,
`cli/`, `app/`, all configurable). Whether it touches an app's persistent data
follows a single default:

**Nothing is isolated by default.**

A tool run through Bunny writes where it would have written had you installed it
yourself. `mvn` populates `~/.m2/repository`, `gradle` uses `~/.gradle`, `npm`
caches in `~/.npm`, VS Code reads `~/.config/Code`. Bunny installs the binary,
resolves which version to run, and gets out of the way. Nothing is redirected,
and no runtime sandbox is activated, unless you explicitly configure it in
`~/.config/bunny/config.yaml`.

This matches what `mise`, `sdkman`, `pyenv`, and `rustup` do, and it is the
behavior most build caches are designed for: a Maven artifact or an npm tarball
is content-addressed and version-agnostic, so a private copy per SDK version
buys correctness you already had while costing gigabytes and cold caches.

## What manifests set

Manifests define an `env:` block for essential runtime wiring, not isolation policy:

```yaml
env:
  JAVA_HOME: "{app}"
  MAVEN_ARGS: "--toolchains {data}/toolchains.xml"
```

These values point at the install tree (`{app}`) or at files Bunny generates
(`{data}/toolchains.xml`). They do not redirect user data directories.

A manifest may also recommend a `sandbox:` policy describing the environment
in which its package normally works. That recommendation is inert unless the
user names the package under `sandbox.packages`; manifests cannot activate
sandboxing themselves.

## Global package installs

`npm -g` installs into Node's own prefix (that version's install tree), so globals
belong to the Node version that installed them and are removed when that version
is uninstalled, matching `nvm` behavior. Shared caches (the npm cache, pnpm store,
Corepack, Yarn) remain at native host paths across versions.

## GUI applications

VS Code, Cursor, Zed, and JetBrains Toolbox namespace their configuration
directories per application natively. Bunny runs them against their normal host
paths unless their exact package IDs are configured for data redirection or the
optional runtime sandbox.

## Default execution and optional sandbox

Bunny executes a package directly via `execve` unless that exact package ID is
listed under `sandbox.packages`. The optional [per-package sandbox](sandbox.md)
scopes a trusted package's state and integrations; it is not a hardened
security boundary for untrusted code.

## Opting into data redirection

Data paths can be redirected with `env` in
`~/.config/bunny/config.yaml`, keyed by package ID, capability, or `*` for all
packages:

```yaml
env:
  node:
    NPM_CONFIG_PREFIX: "{data}/npm-global"
  gradle:
    GRADLE_USER_HOME: "{data}/gradle"
  maven:
    MAVEN_ARGS:
      "-Dmaven.repo.local={data}/repository --toolchains {data}/toolchains.xml"
```

Precedence at launch (lowest to highest): the host environment, each dependency's
manifest `env:`, each dependency's config `env:`, the package's own manifest
`env:`, and finally your package config `env:`. Config always wins.

Note the Maven configuration: `MAVEN_ARGS` is a single variable doing two jobs,
so overriding it requires restating the toolchains flag the manifest sets
(`--toolchains {data}/toolchains.xml`). The manifest's current value is saved in
`~/.local/share/bunny/manifests/maven.yaml`.

See [Configuration](config.md) for full details.

This is independent of the runtime sandbox. `env` redirects the paths a tool
uses even during direct execution; `sandbox.packages` controls whether the
process receives an isolated HOME, path masks, or reduced integrations. They
can be used separately or together.

## What survives uninstall

- **Install tree (`{app}`)**: Removed on uninstall, including `npm -g` globals.
- **Manifest snapshot (`manifests/<id>.yaml`)**: Removed.
- **Data directory (`data/<id>/`)**: Retained unless `--purge` is passed.
- **Native host data (`~/.m2`, `~/.gradle`, `~/.npm`, `~/.config/Code`)**: Never
  touched by Bunny, with or without `--purge`.
