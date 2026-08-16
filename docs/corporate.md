# Corporate environments

Bunny apps run as normal processes, with no runtime sandbox, so host paths and env vars apply directly: corp CA bundles, SSO tokens, ssh-agent sockets, and the rest of `$HOME` are simply there. This doc covers the few interactions that need explicit setup.

## Network access

Bunny-launched apps have full network access: they're ordinary host processes. Maven/Gradle reach your internal Nexus, npm reaches your internal registry, IDEs reach your license server. Nothing extra needed.

## Custom CA bundles

The host's CA store is read at its real path (typically `/etc/ssl/certs/ca-certificates.crt` on Debian/Ubuntu, `/etc/pki/tls/certs/ca-bundle.crt` on RHEL). Tools that read these system paths just work.

For Java, the JDK's own `cacerts` keystore is used. Ship a pre-populated one via a vendored manifest (see [Team deployment](teams.md#vendoring-an-internal-jdk-build)), or `keytool -import` extra corp roots into `{app}/lib/security/cacerts`; that survives reinstall only if a `prepare:` step redoes the import, otherwise repeat after each update.

For Node, set `NODE_EXTRA_CA_CERTS` either globally (in your shell setup) or per-project in `.envrc` / `.env`.

For npm/pnpm, point `~/.npmrc` at your internal registry as you would on any setup. Both the registry config and the npm cache are read at their normal host paths, so nothing bunny-specific is involved.

## HTTP(S) proxies

`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` env vars set in the parent shell are inherited by every bunny-launched process. Set them in your `~/.zshrc` / `~/.bashrc` after the bunny init line and they apply everywhere.

If your proxy needs a TLS-intercepting cert, include it in your system CA bundle (the OS-level method, e.g. `update-ca-certificates`) and Java/Node both pick it up via the system store + `NODE_EXTRA_CA_CERTS`.

## Maven `~/.m2/settings.xml`

Bunny does not redirect anything in `~/.m2`. Your `settings.xml` is read at its normal host path and artifacts download into `~/.m2/repository`, exactly as on a hand-installed Maven. The only thing bunny injects is `MAVEN_ARGS=--toolchains {data}/toolchains.xml`, pointing at the JDK toolchains file it generates.

Practical setup for an internal Nexus:

1. Install Maven: `bunny install maven`.
2. Drop your `settings.xml` at the usual `~/.m2/settings.xml`.
3. Run `mvn -version`; the `<mirrors>`/`<servers>` config is picked up from the host path, and artifacts land in `~/.m2/repository`.

For a team rollout you can instead ship a custom Maven manifest with `prepare:` that copies a `settings.xml` template into `{app}/conf/settings.xml`, and document the `mvn --settings` flag for users who want overrides.

## Gradle daemon and caches

Gradle's daemon and cache live under `~/.gradle/`, and bunny leaves them there. Your `~/.gradle/gradle.properties` (credentials, `org.gradle.jvmargs`) keeps working, and Gradle's own namespacing (`~/.gradle/caches/<version>/`, daemons keyed by JVM and JVM args) already keeps versions apart.

Bunny writes its generated JDK toolchain block into `~/.gradle/gradle.properties`, between `# >>> bunny managed` markers. Everything outside those markers is preserved. To keep bunny out of that file, set `GRADLE_USER_HOME` in `~/.config/bunny/config.yaml`; the block then follows it. See [Configuration](config.md).

## SSH and Git credentials

`~/.ssh/`, `~/.gitconfig`, and ssh-agent socket (`$SSH_AUTH_SOCK`) are visible at host paths. `git`, `gh`, and any tool that shells out to ssh use them transparently. No bunny-specific setup.

## SSO / company credentials

`~/.aws/`, `~/.kube/`, `~/.gcloud/`, `~/.azure/`, browser-stored cookies under `~/.config/<browser>/`: all read at host paths. Bunny doesn't mask host paths; if you don't want a specific app to see one of these, drop access at the OS level (file permissions / parent dir ACL).

Java apps that read `~/.aws/credentials` (e.g. AWS SDK in an integration test) just work: they're ordinary host processes reading ordinary host paths.

## Air-gapped / offline installs

`bunny install` needs network access to fetch source archives. For an air-gapped environment:

1. On a connected machine, run `bunny install <ids>` for everything you want.
2. Tar up `~/.cache/bunny/` (the download cache) and `~/.local/share/bunny/catalog/` if you want a local catalog.
3. Move the tarball into the air-gapped network.
4. Extract to the same paths on the target machine.
5. `bunny install <id>` will hash-match against the local cache and skip the download.

For a permanent setup, host the catalog and an HTTPS mirror of the source archives on your internal network and point `catalog.remote` at it.

## Backups

Back up `~/.local/share/bunny/` and `~/.config/bunny/`. The download cache (`~/.cache/bunny/`) and the `.staging/` dirs inside each install root are regenerable. bunny tags both with a `CACHEDIR.TAG` (the [Cache Directory Tagging](https://bford.info/cachedir/) standard) and a `.nobackup` file, so backup tools that honor them skip those dirs automatically:

- **borg** / **restic**: `--exclude-caches`
- **GNU tar**: `--exclude-caches`
- tools with `--exclude-if-present`: point them at `.nobackup`

Tools without cache-tag support (rsync, `cp`, Time Machine) need an explicit exclude of `~/.cache/bunny` and any `.staging` directory.

## Logging and audit trails

`bunny --log-level debug install <id>` logs every download URL, hash check, and prepare command, along with the paths involved: the staging directory, the install target, the download cache, and the manifest snapshot. Pipe to a file for a record of exactly what was installed and from where:

```bash
bunny --log-level debug install jdk-21 2> jdk-21-install.log
```

Enabling a log level replaces bunny's progress output rather than adding to it. There is no spinner, no per-package status line, and no summary: the log is the whole account of what happened, so a captured file is complete and a terminal run is readable. Commands whose output is the data you asked for (`list`, `search`, `info`, `doctor`, `init`, `completion`) still print it, so `eval "$(bunny init zsh)"` works with any log level.

For an org-wide audit, `~/.local/share/bunny/state.json` is a JSON file with installed packages + versions, easy to scrape from a fleet-management tool.
