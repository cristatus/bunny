# Corporate environments

Bunny apps run as normal processes unless their package ID is explicitly
enabled under `sandbox.packages`. By default, host paths and environment
variables apply directly: corporate CA bundles, SSO tokens, SSH agent sockets,
and the rest of `$HOME` are inherited transparently. This
document covers interactions that require explicit configuration.

## Network access

Unsandboxed Bunny-launched apps have full network access as ordinary host
processes. Maven and Gradle reach your internal Nexus, npm reaches your internal
registry, and IDEs reach your license server without extra configuration.

A sandboxed package keeps network access with the `desktop` and `agent`
profiles. The `offline` profile, or `net: none`, creates a
private network namespace and cannot reach proxies, registries, or license
servers.

## Custom CA bundles

The host CA store is read at its standard OS path (typically
`/etc/ssl/certs/ca-certificates.crt` on Debian/Ubuntu or
`/etc/pki/tls/certs/ca-bundle.crt` on RHEL). Tools that read system stores work
out of the box.

For Java, the JDK's internal `lib/security/cacerts` keystore is used. You can
distribute a pre-populated keystore via a vendored manifest (see
[Team deployment](teams.md#vendoring-an-internal-jdk-build)) or import certificates
using `keytool`:

```bash
keytool -importcert -keystore ~/.local/share/bunny/sdk/jdk-21/lib/security/cacerts \
  -storepass changeit -file corp-root.crt -alias "CorpRootCA"
```

Because package upgrades replace the install tree, manual imports must be
repeated after an update unless automated in a custom manifest `prepare:` step.

For Node, set `NODE_EXTRA_CA_CERTS` globally in your shell profile or per-project
in `.envrc` or `.env`:

```bash
export NODE_EXTRA_CA_CERTS="/etc/ssl/certs/corp-ca-bundle.pem"
```

For npm and pnpm, point `~/.npmrc` at your internal registry as usual:

```ini
registry=https://nexus.corp.internal/repository/npm-group/
```

## HTTP(S) proxies

`HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` environment variables set in the
parent shell are inherited by Bunny-launched processes, including sandboxed
ones that retain network access.

```bash
export HTTP_PROXY="http://proxy.corp.internal:8080"
export HTTPS_PROXY="http://proxy.corp.internal:8080"
export NO_PROXY="localhost,127.0.0.1,.corp.internal"
```

If your proxy uses TLS interception, add its certificate to the system CA
bundle (e.g. via `update-ca-certificates`). Java and Node pick it up via the
system trust store and `NODE_EXTRA_CA_CERTS`.

## Maven `~/.m2/settings.xml`

Bunny does not redirect `~/.m2`. Your `settings.xml` is read from its normal
host path, and artifacts download into `~/.m2/repository`. The only variable
Bunny injects is `MAVEN_ARGS="--global-toolchains {data}/toolchains.xml"`,
pointing at the generated JDK toolchains file.

That is the global slot on purpose. Maven reads a global and a user toolchains
file and merges them, so your own `~/.m2/toolchains.xml` still applies. Using
`--toolchains` instead would put Bunny's file in the user slot and replace
yours. See [Java](java.md#build-toolchains-gradle-and-maven) for what the generated file
contains.

Standard setup for an internal Nexus or Artifactory:

1. Install Maven: `bunny install maven`
2. Place your repository and mirror configuration in `~/.m2/settings.xml`
3. Run `mvn -version` to verify

For a team rollout, you can ship a custom Maven manifest whose `prepare:` step
copies a `settings.xml` template into `{data}`, referencing it via
`MAVEN_ARGS: "--settings {data}/settings.xml"`. Because `{data}` is seeded only
when files are missing, local edits survive upgrades.

## Gradle daemon and caches

Gradle daemons and caches live under `~/.gradle/`. Bunny writes its generated
JDK toolchain configuration into `~/.gradle/gradle.properties` between
`# >>> bunny managed` markers, preserving all existing credentials, properties,
and JVM arguments outside those markers.

To redirect Gradle to another directory, set `GRADLE_USER_HOME` in
`~/.config/bunny/config.yaml`. The toolchain block will follow it. See
[Configuration](config.md).

## Sandboxed packages

With the default sandbox setting `home: isolated`, `$HOME` and the XDG user
directories point into the package's `{data}/home`. Tools therefore do not find
host files such as `~/.m2/settings.xml`, `~/.npmrc`, cloud configuration, or IDE
settings through their normal home-relative paths. Copy the required settings
into the isolated home, redirect them with the tool's own environment variables,
or override the package with `home: shared` when sharing the host home is the
intended policy.

System CA stores and inherited proxy variables remain available. The `agent`
profile disables X11, Wayland, D-Bus, and audio environment integration but
retains the network; `desktop` retains those integrations as well. Explicit
`hide` entries can mask credential paths or agent sockets.

The default `scoped` boundary is state separation, not protection from
hostile code: the host filesystem stays read-write unless explicitly hidden,
so an isolated HOME alone does not put host credentials out of reach by
absolute path. `boundary: hardened` is the deny-by-default option, where only
explicitly granted paths are reachable. See
[Sandboxing](sandbox.md#trust-boundary-and-limitations).

## Authentication and credentials

During ordinary direct launches, host credentials operate transparently:

- **SSH & Git**: `~/.ssh/`, `~/.gitconfig`, and `$SSH_AUTH_SOCK` are inherited directly.
- **Cloud credentials**: `~/.aws/`, `~/.kube/`, `~/.gcloud/`, and `~/.azure/` remain accessible to build scripts and test suites.

For sandboxed packages, home-relative credential discovery follows the
isolated or shared HOME policy above. `SSH_AUTH_SOCK` and `GPG_AGENT_INFO`
are inherited by default; `features: {agents: false}` unsets them and masks
the agent, GnuPG, and keyring sockets, which is the control to reach for when
a package should not use your credentials at all. `offline` and `agent` set it.

Note that ssh does not run inside either boundary — an unprivileged user
namespace makes root-owned files under `/etc/ssh/ssh_config.d/` appear owned by
`nobody`, which OpenSSH refuses. Git over SSH therefore fails inside a sandbox
regardless of the `agents` setting. See
[Sandboxing](sandbox.md#ssh-does-not-work-inside-either-boundary).

A sandboxed package with a redirected HOME does get your Git identity: Bunny
resolves `user.name` and `user.email` in the working directory and passes them
as `GIT_AUTHOR_*`/`GIT_COMMITTER_*`, so commits are attributed correctly even
though `~/.gitconfig` is out of reach. Credential-bearing settings such as
`credential.helper` and `core.sshCommand` are deliberately not passed through.

## Air-gapped and offline installs

`bunny install` fetches source archives from upstream URLs. For air-gapped networks:

1. On a connected machine, install the required packages:
   ```bash
   bunny install jdk-21 maven gradle node-22
   ```
2. Archive the download cache:
   ```bash
   tar -czf bunny-cache.tar.gz -C ~/.cache/bunny .
   ```
3. Transfer the archive to the air-gapped host and extract it into `~/.cache/bunny/`.
4. Subsequent `bunny install <id>` commands match archive checksums against the
   local cache and install without network calls.

For permanent offline environments, host an internal catalog mirror pointing to
internal artifact mirrors.

## Backups

Back up `~/.local/share/bunny/` and `~/.config/bunny/`. The download cache
(`~/.cache/bunny/`) and staging directories (`.staging/`) are ephemeral and
tagged with `CACHEDIR.TAG` and `.nobackup` markers.

Backup tools with cache-tag support (Borg, Restic, GNU `tar --exclude-caches`)
automatically skip these directories; tools with `--exclude-if-present` can
point it at `.nobackup`.

Tools without cache-tag support (rsync, `cp`, Time Machine) need an explicit
exclude of `~/.cache/bunny` and any `.staging` directory.

## Logging and audit trails

For auditing or debugging installations:

```bash
bunny -l debug install jdk-21 2> jdk-21-install.log
```

The log records the resolved layout, source URLs, hash verifications, staging
directories, install targets, and shims created.

Enabling a log level replaces bunny's progress output rather than adding to it.
There is no spinner, no per-package status line, and no summary: the log is the
whole account of what happened, so a captured file is complete and a terminal
run is readable.

Commands whose output is the data you asked for (`list`, `search`, `info`,
`doctor`, `init`, `completion`) still print it, so `eval "$(bunny init zsh)"`
works with any log level.

`~/.local/share/bunny/state.json` provides a machine-readable JSON inventory of
installed packages and versions suitable for fleet compliance tracking.
