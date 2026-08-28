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

`bunny run --explain <id>` prints what that launch would actually do —
either that it runs directly, or the effective policy with each control's
enforcement level — without launching anything.

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

Five reserved built-in profiles are always available:

| Profile | Home | Network | Agents | Desktop integrations |
| --- | --- | --- | --- | --- |
| `desktop` | Isolated | Enabled | Enabled | enabled |
| `online-cli` | Isolated | Enabled | Enabled | disabled |
| `offline-cli` | Isolated | Disabled | Disabled | disabled |
| `ephemeral` | Ephemeral | Enabled | Enabled | enabled |
| `clean` | Clean | Enabled | Enabled | enabled |

All keep `tty` enabled, so interactive programs keep their controlling
terminal. `ephemeral` and `clean` differ from `desktop` only in `home`, and
work on any installed package with no config:

```bash
bunny run --sandbox-profile ephemeral codex   # try it once, keep nothing
bunny run --sandbox-profile clean codex       # same, but always from blank
```

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
      profile: online-cli
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

Ephemeral is a property of the home of the package it is set on. A child
package launched inside an ephemeral parent (say, `node` opened in an
ephemeral Claude's terminal) that requests its own `isolated` home gets its
own, separate, persistent `{data}/home` — a parent's ephemeral home discards
only its own writes, never a child's, and a child cannot un-discard its
parent's home either.

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
to. That also means `clean` needs no overlayfs support and no writable host
access, so unlike `ephemeral` it works identically nested or standalone, on
any kernel bubblewrap runs on.

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
themselves. `offline-cli` disables it.

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
A policy that needs them fails with an install hint when they are absent; it
never silently falls back to host networking.

Nested launches clamp monotonically (`host` < `private` < `none`): a child
cannot widen its parent's mode, an absent child list inherits the parent's, a
broader list is clamped, and a narrower list is an error — narrowing an
existing namespace cannot be enforced once capabilities are dropped, so
network policy belongs on the top-level application.

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
the network mode and clamps inherited from an enclosing sandbox. This is the
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

When both Code and Node are always sandboxed:

```text
Code sandbox ({data}/code/home)
└── terminal
    └── node shim → Node ({data}/node-22/home)
```

Node receives its own HOME/XDG tree. Bunny preserves private anchors to its
real config, state, and shim directories, so shim resolution keeps
working even though Code changed HOME and every XDG base directory.

Bunny avoids unnecessary bubblewrap nesting. If the child adds no kernel
restriction beyond its parent's, it is executed directly within the existing
sandbox. Another bubblewrap layer is added only when the child introduces a
new mask, disables the network, or opts into process/TTY isolation.

Each sandbox layer records the restrictions it enforced in a context file
mounted read-only at `/run/user/<uid>/bunny/sandbox-context.json`, over a
private temporary filesystem the sandboxed process cannot replace. A nested
Bunny invocation clamps its policy against that file; environment variables
have no effect on this decision, so a process cannot loosen its inheritance by
unsetting or forging one. When the file is absent, a child builds its complete
sandbox layer rather than assuming anything about its parent. (Very old
bubblewrap without `--ro-bind-data` cannot mount the context; nested launches
then always pay for a full layer, which is slower but never weaker.)

Restrictions are monotonic down the process tree:

- a child cannot restore network disabled by its parent;
- hidden paths remain hidden;
- X11, Wayland, D-Bus, and audio variables removed by a parent remain removed,
  even if the child's manifest or user environment tries to set them;
- a child can add further restrictions but cannot loosen the outer ones.

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
launched outside Bunny, and nested clamping is a correctness mechanism for
cooperative nesting, not a defence against a hostile parent process — a
parent can always run a child's code directly with its own privileges.

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

Useful checks when behavior is surprising:

- confirm the exact installed package ID under `sandbox.packages`;
- check whether the package is under `sandbox.packages` at all — that
  presence is what activates ordinary launches;
- built-in profile names cannot be redefined; check custom profile spelling;
- run `bunny reshim` after installing or removing runtime-global tools;
- use `bunny doctor` to verify layout, shims, and bubblewrap support.
