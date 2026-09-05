# Sandboxing

Bunny can run an installed package in a lightweight
[bubblewrap](https://github.com/containers/bubblewrap) sandbox. The feature is
opt-in for each package, and policy is the user's alone: a catalog manifest
carries no sandbox policy at all, so nothing outside your own config
influences what a launch actually does.

There are two boundary modes, selected per policy with `boundary:`.

| Boundary | Filesystem baseline | Security claim |
| --- | --- | --- |
| `scoped` (default) | read-write host view | state scoping plus the individually reported masks and namespaces |
| `hardened` | read-only host root, hidden host home, explicit writable grants | kernel-enforced filesystem and namespace boundary |

The scoped mode keeps an application's state separate and disables
integrations it does not need. Disabled integrations are enforced at their
documented endpoints by kernel-backed mounts, not just by removing
environment variables, but the package retains a read-write view of the host
filesystem except for paths explicitly listed under `hide`. It is a
compatibility environment for trusted packages, not a boundary for hostile
software.

The hardened mode is deny-by-default: the host root is read-only, the real
home, `/run`, `/tmp`, `/var/tmp`, `/mnt`, and `/media` are hidden or private,
and only the package's own state plus explicitly granted paths are reachable.
PID, IPC, and UTS isolation, a new session, a fresh `/proc`, a minimal
`/dev`, and a full capability drop are part of the boundary, not options. It
materially limits unexpected or malicious user-space behaviour; a VM remains
the answer when the host kernel itself is outside the trust boundary.

`bunny run --explain <id>` prints a risk summary followed by the effective
policy and each control's enforcement level, without launching anything.
`bunny sandbox check <id>` additionally probes the helpers and kernel
facilities required by that package's exact policy.

## Quick start

Enable the preferred policy for every normal launch of one package:

```yaml
sandbox:
  packages:
    code:
      profile: desktop
```

Now `code`, `bunny run code`, and desktop entries that invoke Code's Bunny
shim all use the sandbox.

To keep a policy available *without* changing normal launches, name a profile
at launch and leave the package out of `packages:` — that is the whole
mechanism, so there is no activation field to set. A built-in works as-is:

```bash
code                                             # normal direct launch
bunny run --sandbox-profile desktop code -- .    # sandbox this launch
```

Or define a complete profile of your own. A profile is a standalone policy: it
cannot select another profile, so it spells out what it wants.

```yaml
sandbox:
  profiles:
    code-locked:
      home: isolated
      net: host
      hide: [~/.ssh]
      features:
        audio: false
```

```bash
bunny run --sandbox-profile code-locked code -- .
```

You can also force a sandbox for an installed package with no config entry:

```bash
bunny run --sandbox code -- .
bunny run --sandbox jdk-21 --command javac -- -version
```

The first form uses the manifest's first binary. `--command` selects another
binary declared by that package — `javac` instead of `jdk-21`'s default
`java`. Arguments after `--` are passed through untouched, even ones that
would otherwise collide with a bunny flag name.

## `profiles:` defines, `packages:` activates

That one sentence is the whole activation model. There is no global enable
switch and no activation field: presence under `packages:` is what makes the
sandbox apply to ordinary launches.

| Package configuration | Normal shim / `bunny run` | `bunny run --sandbox <id>` |
| --- | --- | --- |
| No `packages:` entry | Direct | Sandboxed |
| `<id>: {}` | Sandboxed | Sandboxed |
| `<id>: {profile: ...}` | Sandboxed | Sandboxed |

An empty package entry is useful on its own: it accepts the built-in defaults
without copying them into your config.

`profiles:` exists for the policy you do *not* want automatic. A named profile
that no package selects is inert until you ask for it:

```bash
bunny run --sandbox-profile locked-down some-tool
```

That is how you run something once under a tight policy — the case an
`on-demand` activation flag would otherwise be for. It reuses cleanly across
packages too, but that is the lesser benefit: the point is naming a policy
without arming it.

## Effective policy

Two layers merge over the built-in defaults: the selected profile, then the
package's inline override. Nothing else contributes.

```yaml
home: isolated
net: host          # hardened defaults to none instead
features: {x11: true, wayland: true, dbus: true, audio: true, agents: true, tty: true}
hide: []
persist: []
```

`home` and individual feature keys take the last value set; `hide` and
`persist` are additive and deduplicated. `persist` is meaningful only under
`home: ephemeral`, and an entry cannot be in both `hide` and `persist`.

Five reserved built-in profiles are always available, one per axis: `desktop`
for device integration, `offline` for the network, `ephemeral` and `clean` for
the home, and `agent` for the boundary.

| Profile | Boundary | Home | Network | Agents | Desktop integrations |
| --- | --- | --- | --- | --- | --- |
| `desktop` | Scoped | Isolated | Enabled | Enabled | enabled |
| `agent` | **Hardened** | Isolated | Enabled | Disabled | disabled |
| `offline` | Scoped | Isolated | Disabled | Disabled | disabled |
| `ephemeral` | Scoped | Ephemeral | Enabled | Enabled | enabled |
| `clean` | Scoped | Clean | Enabled | Enabled | enabled |

The scoped profiles keep `tty` enabled, so interactive programs keep their
controlling terminal. `agent` cannot: its boundary mandates a new session and
PID namespace. A full-screen TUI still renders over inherited stdio.

`ephemeral` and `clean` differ from `desktop` only in `home`, and work on any
installed package with no config:

```bash
bunny run --sandbox-profile ephemeral codex   # try it once, keep nothing
bunny run --sandbox-profile clean codex       # same, but always from blank
```

`agent` is the only built-in that uses the hardened boundary, and the only one
that grants `fs.cwd: write`. It is the shape agent vendors converge on: the
working directory is writable, the rest of the host is read-only, the host home
is hidden, and the network stays up because the model is behind it. Credential
agents are off, so a tool acting on a prompt cannot push, publish, or sign as
you.

```bash
cd ~/Projects/thing
bunny run --sandbox-profile agent claude   # can edit this project, nothing else
```

Because `home` is `isolated`, the tool gets a durable private home under
`{data}/home`, which is where its own config and credentials accumulate —
`~/.claude`, `~/.claude.json`, `~/.codex` — persisting across runs without the
host home being readable. See [Agent config and
credentials](#agent-config-and-credentials) for what that means the first time
you run one.

Two limits are worth knowing before relying on it. Commits work, because Bunny
passes your Git identity through (see [Git
identity](#git-identity-in-a-sandbox)), but `git push` over SSH does not — see
[Trust boundary and limitations](#trust-boundary-and-limitations). And `fs.cwd`
is the directory you launch from, so launch from the project you mean to grant.

Select a built-in and override it inline for the common case:

```yaml
sandbox:
  packages:
    code:
      profile: desktop
      features:
        audio: false
      hide:
        - ~/Documents/private
    codex:
      profile: agent
```

A custom profile is a complete standalone policy — it cannot select another
profile — and is chosen the same way:

```yaml
sandbox:
  profiles:
    private-no-network:
      home: isolated
      net: none
      hide: [~/.ssh, ~/.aws]
  packages:
    some-tool:
      profile: private-no-network
```

Restricting a package's *reach* as well as its state needs
`boundary: hardened` on top; a network-free variant of `ephemeral`/`clean` is
a short custom profile rather than a built-in for every combination.

### Selecting a profile at launch

A package's `sandbox.packages` entry holds exactly one `profile`, used by
every normal launch. To give the same package a second, throwaway
configuration — an ephemeral variant of an otherwise-persistent tool, say —
define both as profiles and pick one at launch with `--sandbox-profile`:

```yaml
sandbox:
  profiles:
    claude-persist:            # normal: everything survives
      home: isolated
    claude-scratch:            # throwaway: seeds from the same {data}/home
      home: ephemeral
      persist:
        - .claude/memory
  packages:
    claude:
      profile: claude-persist  # default for a plain launch
```

```bash
claude                                       # → claude-persist (isolated)
bunny run claude                             # → claude-persist
bunny run --sandbox-profile claude-scratch claude    # → claude-scratch (ephemeral)
```

`--sandbox-profile <name>` overrides the package's configured `profile` for that one
invocation, resolving through the same merge a configured profile does; both
a built-in and a custom profile are valid names. It implies `--sandbox` —
naming a profile only makes sense if the policy is actually applied — so it
works on an unconfigured package too, the same way `--sandbox` alone already
does. There is deliberately no narrower `--ephemeral`/`--persist` shorthand:
the two configurations can differ in anything — network, hidden paths, home —
not only in persistence, so naming a whole profile is the general mechanism.

## What the sandbox changes

### Isolated home and package state

With the default `home: isolated`, Bunny sets:

```text
HOME={data}/home
XDG_CONFIG_HOME={data}/home/.config
XDG_CACHE_HOME={data}/home/.cache
XDG_DATA_HOME={data}/home/.local/share
```

`{data}` belongs to the exact package ID, so `node-22`, `node-24`, and
`code` receive different trees. The data survives uninstall unless the
package is removed with `--purge`.

Use `home: shared` when the application should use the real host home:

```yaml
sandbox:
  packages:
    code:
      home: shared
```

For a child launched inside an existing sandbox, `shared` means “keep the
inherited HOME.” It does not escape back to the real host home; a child cannot
loosen its parent's isolation.

### Agent config and credentials

The isolated home starts empty. On a first run, expect this:

- You sign in from scratch.
- Your host settings, skills, hooks, and MCP definitions do not come with it. They stay under the host `~/.claude` or `~/.codex`.
- After you sign in, the credentials persist. `{data}/home` is durable.

Starting empty is the recommended posture, not a shortcoming. Anthropic's dev
container guide mounts a config volume scoped to the container rather than
binding the host directory. Its reference configuration goes further and scopes
that volume per project. The reason is blunt: a sandbox does not stop a hostile
prompt from exfiltrating whatever the agent can reach, credentials included.
A smaller reachable set is the whole point.

Bunny also avoids a problem those setups work around. Claude Code keeps state
in two places: the `~/.claude` directory, and the `~/.claude.json` file beside
it. A container volume can mount a directory but not a file, so the file gets
lost and onboarding returns on every start. Bunny's unit is the whole home, so
both are already inside it.

**Seeding what you want.** Files placed in the durable home appear at `$HOME`
inside the boundary:

```bash
mkdir -p ~/.local/share/bunny/data/claude/home/.claude
cp -r ~/.claude/settings.json ~/.claude/skills \
  ~/.local/share/bunny/data/claude/home/.claude/
```

Copy deliberately, not wholesale. Hooks and MCP definitions name absolute paths
and commands. One that points outside the boundary will fail inside it. Under
`home: ephemeral`, these are the directories `persist:` exists to keep.

**One limit.** The OAuth token has to live in the home, because the tool reads
it from there. Some sandbox platforms inject credentials at the boundary so raw
values never enter; Bunny cannot do that for a token the tool manages itself.
What the boundary does buy is that the token is this package's alone. Your SSH
keys, other tools' credentials, and the rest of your home stay unreachable.

### Matching the vendor's container guidance

Anthropic's dev container guide is the closest published equivalent to the
`agent` profile. Most of its checklist is policy Bunny already applies:

| Their guidance | Bunny |
| --- | --- |
| Config in a container-scoped volume, not the host directory | `home: isolated`, the profile default |
| Don't mount host secrets (`~/.ssh`, cloud credential files) | the hardened boundary hides the host home; `agents: false` drops the SSH and GnuPG sockets |
| Cloud provider credentials as environment variables, not mounted files | per-package `env:` in `config.yaml` |
| MCP servers at project scope in a checked-in `.mcp.json` | works as-is: the repository is the granted working directory |
| Restrict outbound traffic to what the agent needs | `net: private` with an `egress` allowlist |
| Run without permission prompts, because execution is confined | see below |

**Running without permission prompts.** This is the practical reason to use the
profile. Their guidance is that `--dangerously-skip-permissions` becomes
reasonable once execution is confined and the process is not root. Both hold
here: the boundary is kernel-enforced, and the process keeps your own uid.

```bash
cd ~/Projects/thing
bunny run --sandbox-profile agent claude -- --dangerously-skip-permissions
```

Their warning carries over unchanged. Confinement is not immunity. The working
directory is writable and the network is open, so a prompt-free session can
still rewrite the project and send its contents anywhere. Keep it for
repositories you trust. Add `net: private` with an `egress` allowlist if
outbound traffic matters.

**Outbound filtering.** `egress` takes addresses and CIDRs, not hostnames.
Name-based filtering cannot be enforced at that layer. Their reference script
makes the same trade in a different place: it resolves the domains it wants and
allowlists the resulting addresses. See [Network modes](#network-modes).

**Two things differ on purpose.**

Their reference scopes the config volume per project, using `${devcontainerId}`.
An isolated home is scoped per package, so one `claude` home serves every
repository. A dev container is already a project, and a rebuild re-authenticates
anyway. Per-project homes here would mean a fresh login per repository, for a
token that is the same account either way. Session and project state stays
namespaced inside the home regardless.

Bunny does not set `CLAUDE_CONFIG_DIR`. That variable exists so a volume
mounted at `~/.claude` also captures `~/.claude.json` beside it. Here the whole
home is the unit, so both are already inside it and the native layout survives.

### Using your host agent config instead of the isolated home

Sharing the host config is a step away from the guidance above. You take it for
convenience: one login, one set of settings, your MCP servers. There are three
ways. Pick by what you are willing to give up:

| Option | Boundary | Gives up | Use when |
| --- | --- | --- | --- |
| Share the whole home | `scoped` only | all home isolation | you trust the tool and want zero setup |
| Share config, read-only | `hardened` | tool cannot write history or a refreshed token back | **recommended** |
| Share config, read-write | `hardened` | hooks become a deferred escape | you would have run the agent unsandboxed anyway |

First, a note on `CLAUDE_CONFIG_DIR`, because it looks like the obvious tool and
is not. It is documented, and it exists because Claude Code keeps the OAuth
account, personal MCP servers, and per-project trust in `~/.claude.json`,
*outside* `~/.claude`. Setting the variable makes Claude Code write that file
inside the config directory instead.

That makes it right for a self-contained config directory and wrong for sharing
an existing host layout. Your `~/.claude.json` already sits at the home root,
and the variable only changes where Claude Code looks, not where the file is.
Point it at `~/.claude` and you get the credentials and settings but not that
file. The recipes below address `~/.claude.json` where it actually lives.

**Share the whole home.** Simplest, and `scoped` only, since `home: shared`
contradicts the hardened boundary:

```yaml
sandbox:
  profiles:
    agent-shared:
      home: shared
      net: host
      features: {x11: false, wayland: false, dbus: false, audio: false, agents: false, tty: true}
```

HOME is the real host home, so the tool finds everything exactly as it does
unsandboxed. You keep integration masking and `hide:` paths. You give up home
isolation entirely.

**Share config, read-only.** Under `hardened`, symlink what you want into the
isolated home and grant it read-only. Session state still lands in
`{data}/home`, so it stays out of your host config:

```bash
D=~/.local/share/bunny/data/claude/home
mkdir -p "$D/.claude"
ln -sfn ~/.claude/.credentials.json "$D/.claude/.credentials.json"
ln -sfn ~/.claude/settings.json     "$D/.claude/settings.json"
ln -sfn ~/.claude/skills            "$D/.claude/skills"
ln -sfn ~/.claude.json              "$D/.claude.json"
```

```yaml
sandbox:
  profiles:
    agent-hostconfig:
      boundary: hardened
      net: host
      features: {agents: false}
      fs:
        cwd: write
        read:
          - ~/.claude/.credentials.json
          - ~/.claude/settings.json
          - ~/.claude/skills
          - ~/.claude.json
```

Both halves are required. The symlink lets the redirected HOME reach the host
file. The grant makes the target readable. Neither works alone.

**Share config, read-write.** Move those entries from `read:` to `write:`, or
symlink `~/.claude` wholesale. The tool can then record history and refresh
credentials into your host config as it would unsandboxed.

Know the cost first. `~/.claude/settings.json` defines hooks, which are shell
commands. Your *next unsandboxed* launch runs them. So a write grant there lets
a sandboxed agent acting on a hostile prompt plant a command that later
executes outside the boundary with your full privileges. It is a deferred
escape, and nothing else in the sandbox prevents it.

Trying to grant write to the directory but read-only to `settings.json` does not
work. Grants bind read before write, so the directory bind covers the file and
the file ends up writable. Only `hide` overrides a write grant, because masks
are applied last, and it makes the path unreadable too:

```yaml
hide: [~/.claude/settings.json, ~/.claude/hooks]   # writes there fail; reads too
fs:
  write: [~/.claude]
```

### Git identity in a sandbox

Redirecting HOME costs a package its Git identity: Git looks for `.gitconfig`
under HOME, which now points into `{data}/home`, so `git commit` fails with
“Please tell me who you are.” Granting the host file under `fs.read` does not
fix it, because HOME still points elsewhere.

Bunny asks Git instead. Every sandboxed launch whose HOME is redirected —
`isolated`, `ephemeral`, or `clean`, under either boundary — resolves
`user.name` and `user.email` in the working directory and passes them in:

```text
GIT_AUTHOR_NAME     GIT_COMMITTER_NAME
GIT_AUTHOR_EMAIL    GIT_COMMITTER_EMAIL
```

Asking Git is what makes an `include` or `includeIf` chain resolve, and it is
resolved per launch in the working directory because those conditions can key
on the repository: an identity selected by remote URL differs between two
checkouts, and the sandbox gets the one the host would have used.

Only the name and email cross the boundary. Everything that would hand over
credentials stays behind — `credential.helper`, `core.sshCommand`,
`url.*.insteadOf`, `http.extraHeader` — as does `commit.gpgSign`, which would
only fail inside a sandbox whose GnuPG socket is masked. An identity already in
the environment was set deliberately and wins; a missing identity or a missing
Git is not an error, and the launch proceeds without one.

`home: shared` needs none of this: it reads the host config through the real
HOME already.

### Ephemeral home: seeded and discarded, with selective persistence

`home: ephemeral` runs a package against its usual configuration and login,
but throws away whatever it writes to HOME during the run — except paths
listed under `persist`, which survive:

```yaml
sandbox:
  packages:
    claude:
      home: ephemeral
      persist:
        - .claude/memory
```

The seed is the same `{data}/home` tree `home: isolated` persists to: reads
inside the sandbox see it exactly as it stood at launch, and writes land in a
private, discardable layer that disappears when the process exits, however it
exits — no cleanup step, no leftover temp directory. `persist` entries are
home-relative paths bound from `{data}/home/<path>` back over the discard
layer, so reads and writes there go straight to the seed and outlive the run.
An entry cannot appear in both `hide` and `persist`, and `persist` is only
meaningful with `home: ephemeral` — either is a launch error, not a silent
no-op.

Like a `hide` path, a `persist` entry must already exist — as a file or a
directory, whichever it is meant to be — before the sandbox launches. Bunny
cannot mount over a nonexistent target without mutating the host, and cannot
guess whether `.claude/memory` names a file or a directory, so a missing
entry is a launch error rather than something Bunny creates. Whatever type is
already there is preserved: a persisted memory file stays a file.

Because ephemeral overlays the very tree `isolated` persists to, there is
nothing separate to provision: launch the package normally (`home: isolated`,
its everyday configuration) to establish or update the seed — including
creating each `persist` entry — then switch to `home: ephemeral` for
throwaway runs. The common shape is two profiles for one package, selected
per launch — see [Selecting a profile at
launch](#selecting-a-profile-at-launch) below.

Project files are unaffected by any of this: they live in the working
directory, not HOME, and the scoped boundary's read-write host view (or a
hardened `fs.cwd: write` grant) already carries edits there back to the host.
Only HOME-resident state — a memory file, a named session — needs `persist`.

Ephemeral is a property of the home of the package it is set on. Under a
scoped parent, a child package can use its own `{data}/home`. Under a hardened
parent, however, the child runs directly and its data directory must already
be within one of the parent's effective writable roots. Bunny rejects a
redirected child home otherwise. Use `home: shared` to inherit the enclosing
HOME, grant the child data directory through the parent's `fs.write`, or
launch the child outside that sandbox.

Unprivileged overlayfs in a user namespace needs roughly Linux 5.11+; it is
not universal on hardened or older kernels. An ephemeral launch fails closed
with an install/kernel hint when the kernel cannot build the overlay, rather
than silently falling back to `isolated` — that would keep exactly what the
user asked to discard. `bunny doctor` reports this capability up front when
any configured policy needs it (see [Diagnosing support](#diagnosing-support)).

Changing HOME can also move configuration the application expects. Copy or
recreate needed settings inside its isolated home, or redirect individual
caches through top-level `env:` configuration instead.

### Clean home: never seeded

`home: clean` also discards every write on exit, but has no seed at all: HOME
is a bare empty directory every run.

```yaml
sandbox:
  packages:
    some-tool:
      home: clean
```

The two differ only when the same package is *also* used persistently
(`home: isolated`) and a run needs to ignore what that built up — testing
against a fresh install, say. `ephemeral` reads that seed and discards only
the run's own writes; `clean` never reads it. For a package only ever run
this way the two are equivalent, since an unwritten `ephemeral` seed stays
empty.

`persist` has no meaning here and is rejected: there is no seed to bind back
to. `clean` needs no overlayfs support when it creates a layer. Like every
redirected child home, it still requires its destination to be available
inside an enclosing hardened boundary; otherwise the nested launch is
rejected.

### Optional integrations

Supported feature keys are:

| Feature | Enforcement when `false` | Effect |
| --- | --- | --- |
| `network` | namespace | Private network namespace; also masks both D-Bus endpoints |
| `x11` | env + mount | Removes `DISPLAY`; masks `/tmp/.X11-unix` and `~/.Xauthority` |
| `wayland` | env + mount | Removes `WAYLAND_DISPLAY`; masks the runtime `wayland-*` sockets and locks |
| `dbus` | env + mount | Removes `DBUS_SESSION_BUS_ADDRESS`; masks the user bus and `/run/dbus/system_bus_socket` |
| `audio` | env + mount | Removes `PULSE_SERVER` and `PIPEWIRE_REMOTE`; masks the runtime `pulse` and `pipewire-*` endpoints |
| `agents` | env + mount | Removes `SSH_AUTH_SOCK` and `GPG_AGENT_INFO`; masks the SSH agent, GnuPG, and keyring sockets |
| `tty` | namespace | Adds a new session and PID namespace with a fresh `/proc`, detaching the controlling terminal |

Feature keys default to `true`. Unknown keys are rejected, preventing a typo
from silently producing a weaker policy.

A disabled feature is enforced at these documented endpoints: libraries that
fall back to a default socket when their variable is unset (libdbus, GDBus,
libwayland, PulseAudio, PipeWire) find the socket masked by the kernel, not
merely their variable removed. The masks cover exactly the listed paths — not
arbitrary socket locations, abstract Unix addresses, or alternate transports
such as X11 over TCP. In particular, `x11: false` blocks an explicitly set
`DISPLAY` only together with `network: false`, because X clients prefer the
abstract socket, which lives in the network namespace rather than the
filesystem. Endpoints are resolved at launch; one created afterwards is not
retroactively masked.

`network: false` also masks both D-Bus endpoints even when `dbus` is left
enabled: the session bus can start processes outside the network namespace
(via the user service manager), so exposing it would make network isolation
cosmetic. Masking the user bus removes portal access — a package with an
unreachable bus loses the file-chooser portal, notifications, and `xdg-open`.
That is the point, and it is why the `desktop` profile keeps both network and
D-Bus on.

`agents: false` is for packages that should not use your SSH, GnuPG, or
keyring credentials even while `hide: [~/.ssh]` conceals the key files
themselves. `offline` and `agent` disable it.

`tty: false` opts into process and terminal isolation (`TIOCSTI` injection and
tracing of the launching shell). It breaks programs that expect a controlling
terminal, which is why every built-in profile leaves it enabled. In hardened
mode process/session isolation is part of the boundary: `tty` is forced off,
and an inherited or explicit `tty: true` is overridden rather than rejected,
with `--explain` reporting the forced restriction.

### Network modes

`net` is the single, three-state way to express network policy. The common
on/off cases take a bare mode name (`net: none`, `net: host`); `private` uses
the mapping form to carry its lists. There is deliberately no
`features.network` boolean beside it — one setting with two spellings needs a
precedence rule for every way the two can disagree, and got one wrong often
enough to be worth deleting.

| Mode | Namespace | Egress | Ingress |
| --- | --- | --- | --- |
| `host` | host | unrestricted | unrestricted |
| `private` | own stack via [pasta](https://passt.top) | via host stack, filtered by `egress` | denied except `inbound` |
| `none` | own, no stack | none | none |

Scoped mode defaults to `host`; hardened mode defaults to `none`. `private`
gives the package its own network stack and loopback: it is unreachable from
outside unless a port is listed under `inbound` (`port[-port][/tcp|/udp]`),
and it cannot reach the host's loopback services or listening daemons.

```yaml
sandbox:
  packages:
    some-service:
      net:
        mode: private
        inbound:
          - 8080/tcp
        egress:
          - 10.0.0.0/8:443
          - 192.168.1.10:5432
```

`egress` is presence-sensitive: an absent list means unrestricted outbound
access, an explicit empty list means default-drop with no exceptions, and a
non-empty list means default-drop plus those destinations
(`CIDR[:port[-port]][/tcp|/udp]`). The ruleset is installed with nftables
inside the namespace before the package starts and before capabilities are
dropped, so the package cannot rewrite it. DNS is pinned: the host resolver's
IPC is masked, a generated `resolv.conf` names pasta's forwarder, and the
forwarder is a documented exception in the ruleset. That exception is also a
residual channel — anything that can emit DNS queries can tunnel data through
it, which is inherent to permitting DNS at all. An explicit empty `egress`
list gets no resolver and no exception. Hostnames are rejected in `egress`:
name-based filtering cannot be enforced (CDN rotation defeats resolved IP
sets; an SNI proxy requires the program's cooperation), so it is not offered.

Two `private`-mode behaviours to know about: the program sees itself as
uid 0 (mapped to your real user on the host — files it creates are yours),
and a program killed by a signal reports exit status 0 rather than 128+N.
Both non-host modes also force the D-Bus endpoint masks even when `dbus` is
enabled, because the session bus can start processes outside the network
namespace; a package that needs portals must use `host` mode.

`private` requires `pasta` (the `passt` package) and, with `egress`, `nft`.

Inside a `private` sandbox the payload runs as uid 0 in pasta's user
namespace, and no further bubblewrap layer can be created there. A nested
launch therefore only works when it can run directly under the inherited
boundary, which is what the mounted context lets it do. A nested launch that
genuinely needs its own layer — one that adds masks, or narrows the home —
fails at namespace creation instead.
A policy that needs them fails with an install hint when they are absent; it
never silently falls back to host networking.

A launch inside an existing sandbox inherits its network mode and never
changes it, so network policy belongs on the top-level application. See
[Applications, dependencies, and child
commands](#applications-dependencies-and-child-commands).

### Hidden paths

`hide` entries can be absolute, start with `~/`, or be relative to the real
host home:

```yaml
sandbox:
  packages:
    code:
      hide:
        - ~/.ssh
        - ~/.aws
        - Documents/private
```

Existing directories are covered with an empty temporary filesystem. Existing
files are covered with `/dev/null`. A `hide` path that does not exist is a
launch error: Bunny cannot mask a target without knowing whether it is a file
or a directory, and refuses to guess or to create anything on the host —
create the intended path first or remove the entry. `~` continues to mean the
original host home even after HOME has been redirected, including inside a
child package launch.

The sandbox also protects its own policy: `config.yaml` is bound read-only
wherever it exists, so a sandboxed package cannot rewrite the rules that
govern it. In scoped mode Bunny's `bin/` and `state.json` stay writable so
the documented `npm install -g` + `bunny reshim` flow keeps working inside a
sandboxed editor; `bunny install` from inside a sandboxed terminal is the one
casualty and is intentional.

Under hardened, Bunny's control state is read-only rather than writable. The
baseline masks the host home it normally sits under, and Bunny binds the parts
a shim reads — the shim directory, `state.json`, the manifest snapshots, and
the install roots — back on top, read-only. A shim therefore still resolves a
toolchain inside the boundary, while no mutation command works: `bunny install`
fails on the read-only install root. Only the package's own `{data}` is
writable.

These paths come from the resolved layout, not from the policy, so they follow
a configured `install:` root and a config file shared across machines stays
correct. You never list them under `fs.read`. The tradeoff is that a hardened
package can read every toolchain you have installed; what stays hidden is the
rest of the host home, which is where credentials and documents live.

### The hardened boundary

`boundary: hardened` switches the filesystem model from a blacklist to an
allowlist:

```yaml
sandbox:
  profiles:
    locked-down:
      boundary: hardened
      features:
        agents: false
      fs:
        cwd: read
        read:
          - ~/Projects/example
        write: []
      net:
        mode: none
  packages:
    some-tool:
      profile: locked-down
```

The host root is read-only; the real home, `/run`, `/tmp`, and `/var/tmp`
are private; `/mnt` and `/media` are hidden. Bound back are: the package's
install tree and resolved dependencies (read-only), Bunny's own layout
(read-only, described above), its own `{data}` and isolated home
(read-write), `fs.read` grants (read-only), and `fs.write`
grants (read-write, implying read). The working directory defaults to
read-only and can be set to `write` or `hidden` via `fs.cwd`. Grant targets
must already exist and resolve against the real host home; a missing path is
a launch error rather than something Bunny creates on the host. Present grant
lists replace inherited ones instead of appending — appending is correct for
restrictions and wrong for permissions.

Hardened integrations default off and must be enabled explicitly; enabled
display and audio sockets are bound back into the private `/run` and `/tmp`.
With `net: host` the resolver configuration is bound back too — on a
systemd-resolved host `/etc/resolv.conf` is a symlink into `/run`, so the
private `/run` would otherwise leave it dangling and break every name lookup.
A restricted mode keeps it masked, and `private` installs its own instead.
`home: shared` is invalid — sharing the host home contradicts the boundary.
`home: ephemeral` and `home: clean` are both valid and need no exception:
each is strictly more isolated than `isolated`, and simply replaces the
isolated-home bind for that one subtree with an overlay or a bare tmpfs,
respectively (see [Ephemeral
home](#ephemeral-home-seeded-and-discarded-with-selective-persistence) and
[Clean home](#clean-home-never-seeded)). Descendant user namespaces stay
permitted so Chromium-based applications can create their own internal
sandbox.

`features.dbus: true` in hardened mode never binds the raw bus: it starts a
portal-only filtered bus via `xdg-dbus-proxy` (required, with an install hint
when absent — there is no fallback to the raw bus). The sandbox sees only the
proxy socket; portal calls work, `systemd-run --user` does not. This launch
path runs under a small supervisor that owns the proxy lifecycle and
preserves the package's exit status. A non-host network mode excludes the
proxy too, because portals execute on the host side with host network access.

A hardened child inside a hardened parent can only remove access: revoked
grants are masked, revoked write access is demoted to read-only, and a child
policy can never add a grant absent from its parent. A scoped child of a
hardened parent remains hardened.

### Explaining a launch

`--explain` reports what `bunny run` would actually do, matching normal
activation unless `--sandbox` forces it. A package with no active policy
says so plainly rather than showing a hypothetical plan:

```text
$ bunny run --explain some-tool
some-tool runs directly: no sandbox policy is active for this launch (add it to sandbox.packages, or pass --sandbox for this launch only)
```

Add `--sandbox` (or give the package a `packages:` entry) to see the full
plan instead:

```text
$ bunny run --sandbox --explain some-tool
Risk summary
project       read-only
host home     hidden
package home  private and persistent
network       none
credentials   SSH/GnuPG agent sockets unavailable
nesting       new sandbox boundary

Enforcement
boundary  mount+ns   hardened: read-only root, hidden host home, private /run and /tmp
home      mount      isolated: .../data/some-tool/home
fs        mount      1 read, 0 write grants; cwd read: ~/Projects/example
layout    mount      read-only: shims, state, manifests, install roots
network   namespace  no network stack
dbus      mount      session and system endpoints masked
...
context   mount      immutable: /run/user/1000/bunny/sandbox-context.json
```

Every control is listed with its enforcement level (`env`, `mount`,
`namespace`, `filter`) and what it produces, including restrictions forced by
the network mode and anything inherited from an enclosing sandbox. This is the
difference between trusting the sandbox and guessing.

## Applications, dependencies, and child commands

The top-level application normally owns the sandbox. Dependencies inherit the
environment and restrictions of the process that launched them, but there are
two different launch paths to understand.

For a command typed in Code's integrated terminal, the common cases are:

| Code | Child package | Child HOME | Execution |
| --- | --- | --- | --- |
| Direct | No entry | Host HOME | Direct |
| Direct | `always` | Child `{data}/home` | Top-level child sandbox |
| Sandboxed | No entry | Code's isolated HOME | Direct inside Code's sandbox |
| Sandboxed | `always` | Child `{data}/home` | Reuses Code's sandbox unless the child adds mount/network restrictions |

An explicit `bunny run --sandbox <child>` behaves like `always` for that invocation.
Configured `env:` values still apply in every row; activation only determines
whether the child's sandbox policy is applied.

### Declared runtime dependency: no nested sandbox

Suppose VisualVM declares `requires: jdk`. Bunny resolves the selected JDK and
injects values such as `JAVA_HOME` when preparing VisualVM. If VisualVM is
sandboxed, it invokes `$JAVA_HOME/bin/java` directly inside VisualVM's current
sandbox:

```text
VisualVM sandbox
└── JDK binary via JAVA_HOME
```

The JDK package's own sandbox activation is not consulted because Bunny is not
dispatching a second package. The JDK simply inherits VisualVM's home,
filesystem view, network namespace, and disabled integrations. This avoids a
nested sandbox for ordinary application dependencies.

The JDK's policy is consulted only when Bunny launches that package itself,
for example through the `java` shim, `bunny run jdk-21`, or
`bunny run --sandbox jdk-21`.

### Command through a Bunny shim: child policy can apply

A terminal opened inside a sandboxed editor inherits that editor's sandbox.
If a command resolves to a Bunny shim, Bunny re-enters and can identify the
child package.

When both Code and Node are always sandboxed under a scoped outer policy:

```text
Code sandbox ({data}/code/home)
└── terminal
    └── node shim → Node ({data}/node-22/home)
```

Node receives its own HOME/XDG tree. Bunny preserves private anchors to its
real config, state, and shim directories, so shim resolution keeps
working even though Code changed HOME and every XDG base directory.

**The outermost sandbox owns the boundary.** A launch inside an existing
sandbox never builds a second bubblewrap layer. It runs directly under the one
already in force, whatever its own policy asks for.

That keeps the model to one rule. A child could never loosen the enclosing
restrictions anyway — the kernel sees to that, since a new layer can only add
mounts and namespaces, never remove its parent's — and a second layer would
only ever matter for two separately sandboxed packages launching one another,
which is also the one case that cannot work at all inside a private network
namespace.

What a child gets depends on capabilities already exposed by the parent. A
scoped parent exposes the host view, so a child can use its own redirected
home. A hardened parent records its effective writable roots in the immutable
context; a redirected child home must fall beneath one of them or Bunny fails
closed. `home: shared` always keeps the enclosing HOME.

Restrictions therefore run down the process tree unchanged: the network mode,
the hidden paths, the removed X11, Wayland, D-Bus, and audio variables, and
the boundary itself all stay as the outer layer set them.

Anything the nested policy asked for that only a new layer could apply — a
stricter boundary, a narrower network, an ephemeral home, extra masks,
filesystem grants — is reported rather than half-applied. `--explain` shows it
inside a sandbox:

```text
nested    none       runs directly inside code: the outermost sandbox owns the boundary
boundary  inherited  scoped
home      env        isolated: .../data/node-22/home
network   inherited  host
ignored   none       hide: 1 path(s)
```

The launch itself warns at log level `warn` or lower.

Each layer records what it enforced, including a schema version and effective
writable roots, in a context file mounted read-only at
`/run/user/<uid>/bunny/sandbox-context.json`, over a private temporary
filesystem the sandboxed process cannot replace. That file is how a launch
knows it is nested at all; environment variables have no say, so a process
cannot pretend otherwise by unsetting or forging one. The mount point is read
back from the kernel rather than derived from the current uid, because a
private-network sandbox runs the payload as uid 0 inside pasta's namespace.
When the file is absent — very old bubblewrap without `--ro-bind-data` — a
launch cannot tell it is nested and builds its own layer.
An unknown context version is rejected so two incompatible Bunny versions do
not silently disagree about inherited security capabilities.

Commands that do not pass through Bunny cannot activate another package's
policy. They still inherit the current process environment, mounts, and
network namespace normally.

## Sandbox the app, redirect SDK data

Use `env:` to relocate a known cache or install prefix; use `sandbox.packages`
when a package needs a private HOME, masked paths, or fewer integrations. They
compose, so it is usually unnecessary to sandbox a command-line SDK just to
move its caches — sandbox Code and leave Node direct:

```yaml
env:
  node:
    NPM_CONFIG_PREFIX: "{data}/npm-global"
    NPM_CONFIG_CACHE: "{data}/npm-cache"

dirs:
  node:
    - "{data}/npm-global"

sandbox:
  packages:
    code:
      profile: desktop
```

`node` or `npm` in Code's terminal then runs directly inside Code's sandbox,
inheriting Code's HOME and restrictions, while npm's prefix and cache stay in
Node's own `{data}`:

```text
Code sandbox ({data}/code/home)
└── Node direct
    ├── HOME = Code's isolated home
    ├── npm prefix/cache = node-22's {data}
    └── restrictions = Code's restrictions
```

Add a `node-22: {}` entry as well when Node should have its own complete HOME
or add restrictions of its own.

Global tools follow the same path: `npm install -g prettier` lands under
`{data}/npm-global/bin`, and `bunny reshim` exposes it as a shim owned by the
active Node provider. `npm install -g` does not regenerate shims on its own,
so a new executable may not be on PATH until `bunny reshim` runs.

See [Configuration](config.md) for environment precedence and
[Portability](portability.md) for the default direct-execution model.

## Trust boundary and limitations

The scoped boundary uses a read-write host filesystem view and enforces
disabled integrations at their documented endpoints. It does not:

- make untrusted or malicious code safe;
- prevent reads or writes outside isolated HOME, except at explicit `hide`
  paths and masked endpoints;
- discover arbitrary socket paths, abstract Unix addresses, or alternate
  transports beyond the documented endpoints each feature masks;
- apply a package policy to a binary launched by absolute path rather than
  through Bunny;
- automatically sandbox a package with no `sandbox.packages` entry during a
  normal launch.

Use it as state and integration isolation for software you already trust.

The hardened boundary is a kernel-enforced allowlist and materially limits
unexpected or malicious user-space behaviour. It still does not claim
VM-equivalent isolation: when the host kernel itself is inside the threat
model, a VM remains the answer. Neither boundary applies policy to binaries
launched outside Bunny. Nesting is not a defence against a hostile parent
either: a parent can always run a child's code directly with its own
privileges, which is why the outermost sandbox simply owns the boundary
instead of negotiating with the launches inside it.

### SSH does not work inside either boundary

An unprivileged user namespace maps only your own UID; every other UID,
including root, appears as the overflow UID (`65534`, `nobody`). OpenSSH
requires its configuration files be owned by root or by you, so on any host
with drop-ins under `/etc/ssh/ssh_config.d/` — which systemd now ships — ssh
refuses to start:

```text
Bad owner or permissions on /etc/ssh/ssh_config.d/20-systemd-ssh-proxy.conf
```

This affects `scoped` as well as `hardened`, and applies whatever `features:
{agents: ...}` says: with agents enabled the SSH agent socket *is* reachable
and `ssh-add -l` lists your keys, but ssh itself will not run. The practical
consequence is that `git push` over SSH fails inside a sandbox. Bubblewrap
cannot fix it — mapping additional UIDs needs `newuidmap` and a subuid range,
which bubblewrap does not use.

Push from outside the sandbox, or use an HTTPS remote with a credential the
policy grants deliberately.

## Diagnosing support

Runtime sandboxing requires Linux and bubblewrap. `bunny doctor` checks both
the executable and unprivileged namespace support:

```text
✓ bwrap    bubblewrap 0.11.2
✓ sandbox  unprivileged user namespaces OK
```

When configured policies need them, doctor also checks the optional helpers:
`pasta` (any `net: private` package), `nft` (any egress allowlist),
`xdg-dbus-proxy` (any hardened package enabling D-Bus), and `overlay` (any
`home: ephemeral` package) — the last actually builds a probe overlay in a
user namespace, since a present `bwrap` binary is not proof the running
kernel supports it. `home: clean` needs none of this — a plain tmpfs mount
has no comparable kernel dependency to check. A launch that needs a missing
helper or capability fails closed with the same install/kernel hint.

A configured or explicitly sandboxed package fails rather than silently
running unsandboxed when bubblewrap cannot start. Packages without active
sandboxing continue to launch directly.

For one package, preflight the exact effective policy before launching it:

```bash
bunny sandbox check some-tool
bunny sandbox check some-tool --profile agent
```

The result distinguishes required, optional, and unused components and checks
bubblewrap, ephemeral overlays, pasta, nftables, the filtered D-Bus proxy, and
immutable nested-context support only when the resolved launch needs them. A
faint row is one this launch never reaches, so a missing helper there costs
nothing:

```text
$ bunny sandbox check some-tool

Sandbox preflight
✓ policy          resolved and enforceable in the current context
✓ bwrap           /usr/bin/bwrap
· overlay         not required by this launch
· pasta           not required by this launch
· nft             not required by this launch
· dbus-proxy      not required by this launch
✓ nested context  immutable context propagation available

ready to launch
```

A policy that resolves but cannot be planned here reports what was asked
under a `Requested policy` heading, then the failing check; the command exits
non-zero either way.

Useful checks when behavior is surprising:

- confirm the exact installed package ID under `sandbox.packages`;
- check whether the package is under `sandbox.packages` at all — that
  presence is what activates ordinary launches;
- built-in profile names cannot be redefined; check custom profile spelling;
- run `bunny reshim` after installing or removing runtime-global tools;
- use `bunny doctor` to verify layout, shims, and bubblewrap support.
