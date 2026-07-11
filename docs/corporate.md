# Corporate environments

Bunny is designed to coexist with corporate developer environments without breaking them. Apps run as normal processes — there is no sandbox at runtime — so host paths and env vars apply directly: corp CA bundles, SSO tokens, ssh-agent sockets, and the rest of `$HOME` are simply there. This doc covers the few interactions that need explicit setup.

## Network access

Bunny-launched apps have full network access — they're ordinary host processes. Maven/Gradle reach your internal Nexus, npm reaches your internal registry, IDEs reach your license server. Nothing extra needed.

## Custom CA bundles

The host's CA store is read at its real path (typically `/etc/ssl/certs/ca-certificates.crt` on Debian/Ubuntu, `/etc/pki/tls/certs/ca-bundle.crt` on RHEL). Tools that read these system paths just work.

For Java, the JDK's own `cacerts` keystore is used. If your org ships an internal Temurin/Corretto build with a pre-populated `cacerts`, vendor it as a custom manifest (see [Team deployment](teams.md#vendoring-an-internal-jdk-build)). For an upstream JDK with extra corp roots, the standard `keytool -import` workflow lands the cert in `{app}/lib/security/cacerts` — survives reinstall only if your team manifest's `prepare:` step does the import; otherwise re-run after each update.

For Node, set `NODE_EXTRA_CA_CERTS` either globally (in `~/.bunny/config.yaml` shell setup) or per-project in `.envrc` / `.env`.

For npm/pnpm, point `~/.npmrc` at your internal registry as you would on any setup. Each Node version's global npm cache is redirected under `~/.bunny/var/app/<id>/` via `env:`, so caches don't collide across versions; the registry config in `~/.npmrc` is read at its normal host path and shared.

## HTTP(S) proxies

`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` env vars set in the parent shell are inherited by every bunny-launched process. Set them in your `~/.zshrc` / `~/.bashrc` after the bunny init line and they apply everywhere.

If your proxy needs a TLS-intercepting cert, include it in your system CA bundle (the OS-level method, e.g. `update-ca-certificates`) and Java/Node both pick it up via the system store + `NODE_EXTRA_CA_CERTS`.

## Maven `~/.m2/settings.xml`

Bunny only redirects Maven's **local repository**, not the whole of `~/.m2`. The manifest sets `MAVEN_ARGS=-Dmaven.repo.local={data}/repository`, so each Maven version downloads its artifacts into its own dir under `~/.bunny/var/app/maven/`. Your `~/.m2/settings.xml` is read at its normal host path — Maven runs as a normal process, so there is nothing special to find or relocate.

Practical setup for an internal Nexus:

1. Install Maven: `bunny install maven`.
2. Drop your `settings.xml` at the usual `~/.m2/settings.xml`.
3. Run `mvn -version`; the `<mirrors>`/`<servers>` config is picked up from the host path, while downloaded artifacts land in the per-version `{data}/repository`.

For a team rollout you can instead ship a custom Maven manifest with `prepare:` that copies a `settings.xml` template into `{app}/conf/settings.xml`, and document the `mvn --settings` flag for users who want overrides.

## Gradle daemon and caches

Gradle's daemon and cache live under `~/.gradle/` (`GRADLE_USER_HOME`). The manifest sets `GRADLE_USER_HOME={data}/gradle` per version, so each Gradle install keeps its own daemon and cache under `~/.bunny/var/app/gradle/`. This is usually the desired behavior: Gradle's daemon caches are a common source of hard-to-diagnose build failures, and per-version isolation keeps a JDK 21 → 25 switch from corrupting a daemon.

## SSH and Git credentials

`~/.ssh/`, `~/.gitconfig`, and ssh-agent socket (`$SSH_AUTH_SOCK`) are visible at host paths. `git`, `gh`, and any tool that shells out to ssh use them transparently. No bunny-specific setup.

## SSO / company credentials

`~/.aws/`, `~/.kube/`, `~/.gcloud/`, `~/.azure/`, browser-stored cookies under `~/.config/<browser>/` — all read at host paths. Bunny doesn't mask host paths; if you don't want a specific app to see one of these, drop access at the OS level (file permissions / parent dir ACL).

Java apps that read `~/.aws/credentials` (e.g. AWS SDK in an integration test) just work — they're ordinary host processes reading ordinary host paths.

## Air-gapped / offline installs

`bunny install` needs network access to fetch source archives. For an air-gapped environment:

1. On a connected machine, run `bunny install <ids>` for everything you want.
2. Tar up `~/.bunny/var/cache/` (the download cache) and the `~/.bunny/catalog/` directory if you want a local catalog.
3. Move the tarball into the air-gapped network.
4. Extract to the same paths on the target machine.
5. `bunny install <id>` will hash-match against the local cache and skip the download.

For a permanent setup, host the catalog and an HTTPS mirror of the source archives on your internal network and point `catalog.remote` at it.

## Backups

`~/.bunny` is self-contained, so backing it up captures everything — but `var/cache/` (downloads) and `var/tmp/` (install work dirs) are regenerable. bunny tags both with a `CACHEDIR.TAG` (the [Cache Directory Tagging](https://bford.info/cachedir/) standard) and a `.nobackup` file, so backup tools that honor them skip those dirs automatically:

- **borg** / **restic**: `--exclude-caches`
- **GNU tar**: `--exclude-caches`
- tools with `--exclude-if-present`: point them at `.nobackup`

Tools without cache-tag support (rsync, `cp`, Time Machine) need an explicit exclude of `~/.bunny/var/cache` and `~/.bunny/var/tmp`.

## Logging and audit trails

`bunny --log-level debug install <id>` logs every download URL, hash check, and prepare command. Pipe to a file for a record of exactly what was installed and from where:

```bash
bunny --log-level debug install jdk-21 2> jdk-21-install.log
```

For an org-wide audit, `~/.bunny/var/state.json` is a JSON file with installed packages + versions, easy to scrape from a fleet-management tool.
