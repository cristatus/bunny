## Portability model

Bunny installs each package into `~/.bunny/app/<id>/`. Whether bunny touches an app's persistent data at all depends on a single principle:

**Isolate only where the upstream tool does not already isolate itself.**

Most tools fall cleanly into one of two buckets.

### SDKs — isolated by bunny

SDKs (node, jdk, gradle, maven, deno, bun, graalvm) do **not** self-namespace by version. Every Node wants `~/.npm`, every Gradle wants `~/.gradle`, every Maven wants `~/.m2` — install two versions and they collide on one global directory.

So bunny redirects them per-manifest via `env:` (and, where needed, a `prepare:` step), pointing each version at its own dir under `~/.bunny/var/app/<id>/`. The binary is exec'd directly with that environment. Any tool spawned from a bunny-launched process sees the host's normal `$HOME` layout — only the specific env var bunny sets steers the SDK's global dir.

### GUI apps — run native

GUI apps (VS Code, Cursor, Zed, JetBrains Toolbox) **already** namespace their own config directories: VS Code → `~/.config/Code`, Cursor → `~/.config/Cursor`, Zed → `~/.config/zed`, JetBrains → per-version dirs. Bunny adds nothing here, so it runs them **natively** — they read and write their normal host paths, exactly as if you had installed them yourself. There is no redirection, and settings and extensions remain where each application normally stores them.

### Not a sandbox

The model is **not** a security sandbox. Runtime launch is a plain direct exec. Don't run untrusted software through bunny.

## Examples

SDK isolation is expressed in the manifest. VS Code, Zed, and the other GUI apps carry none of this — their `bin:` entries are just `name` + `path`.

Maven and Gradle redirect their global dirs with env vars:

```yaml
env:
  GRADLE_USER_HOME: "{data}/gradle"
```

```yaml
env:
  MAVEN_ARGS: "-Dmaven.repo.local={data}/repository"
```

Eclipse points its OSGi config area via `eclipse.ini`, written at install time:

```yaml
prepare:
  - |
    cat >> {pkg}/eclipse.ini <<EOF
    -Dosgi.configuration.area={data}/configuration
    EOF
```

In each case bunny invokes the binary with the right env, the SDK routes its global state to `{data}/...`, and **child processes see the host's normal `$HOME` view**.

## What survives uninstall

- `app/<id>/` (the install tree) — removed on uninstall.
- `var/app/<id>/manifest.yaml` (cache used by runtime + uninstall) — removed.
- `var/app/<id>/<bunny-data-paths>` (an SDK's per-version global dir, e.g. the redirected `.m2`/`.gradle`) — kept unless `--purge`.

GUI apps run native, so their settings live at their normal host paths (`~/.config/Code`, etc.) and are untouched by `bunny uninstall` either way.

Bunny's own bookkeeping — `state.json`, the download cache, and cached manifests — lives under `~/.bunny/var/` and is independent of any single package.

The split is deliberate: `bunny uninstall` is reversible for SDK data (reinstall and the per-version directory is still present), while `bunny uninstall --purge` removes that data as well.
