# Sandboxing

Bunny can run an installed package in a lightweight
[bubblewrap](https://github.com/containers/bubblewrap) sandbox. The feature is
opt-in for each package: manifests can recommend a policy, but only the user
can activate it.

The sandbox is primarily for keeping a trusted application's state separate
and disabling integrations it does not need. It is not a hardened boundary
for hostile software: the package retains a read-write view of the host
filesystem except for paths explicitly listed under `hide`.

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

Keep a policy available without changing normal launches:

```yaml
sandbox:
  packages:
    code:
      activation: on-demand
      profile: desktop
```

```bash
code                            # normal direct launch
bunny sandbox code -- .        # sandbox this launch
```

You can also force a sandbox for an installed package with no config entry:

```bash
bunny sandbox code -- .
bunny sandbox code --command code -- --new-window .
```

The first form uses the manifest's first binary. `--command` selects another
binary declared by that package. Arguments after `--` are passed through.

## Activation and policy are separate

The package map controls activation. There is deliberately no global enable
switch.

| Package configuration | Normal shim / `bunny run` | `bunny sandbox <id>` |
| --- | --- | --- |
| No package entry | Direct | Sandboxed |
| `<id>: {}` | Sandboxed | Sandboxed |
| `activation: always` | Sandboxed | Sandboxed |
| `activation: on-demand` | Direct | Sandboxed |

Presence defaults to `activation: always`. This makes an empty package entry
useful: it accepts the preferred manifest policy and built-in defaults without
copying them into user config.

Policy and activation remain independent. An `on-demand` entry can select a
profile and override it, but those settings have no effect until an explicit
`bunny sandbox` launch.

## Effective policy

Policy is merged from least to most authoritative:

1. Built-in defaults
2. Manifest recommendation
3. Selected built-in or user-defined profile
4. Inline package override

The built-in defaults are:

```yaml
home: isolated
features:
  network: true
  x11: true
  wayland: true
  dbus: true
  audio: true
hide: []
```

`home` and individual feature keys use the last explicitly specified value.
`hide` is additive and deduplicated: an override extends inherited masks
instead of replacing them.

Three built-in profiles are always available:

| Profile | Home | Network | Desktop integrations |
| --- | --- | --- | --- |
| `desktop` | Isolated | Enabled | X11, Wayland, D-Bus, and audio enabled |
| `online-cli` | Isolated | Enabled | X11, Wayland, D-Bus, and audio disabled |
| `offline-cli` | Isolated | Disabled | X11, Wayland, D-Bus, and audio disabled |

Their names are reserved so their behavior is stable across configurations.
Custom profiles remain available under other names for policies that do not
fit a built-in. For the common case, select a built-in and override it inline:

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
      activation: on-demand
      profile: online-cli
```

This isolates Code's home, leaves its network and Wayland access enabled,
and removes its audio environment. Codex gets an isolated home and network but
no desktop integrations when explicitly sandboxed. A package selects exactly
one profile; inline policy then layers on top.

A custom profile contains its complete reusable override and is selected the
same way:

```yaml
sandbox:
  profiles:
    private-no-network:
      home: isolated
      features:
        network: false
      hide:
        - ~/.ssh
        - ~/.aws
  packages:
    some-tool:
      profile: private-no-network
```

## Manifest recommendations

A package author can describe a preferred policy:

```yaml
sandbox:
  home: isolated
  features:
    audio: false
```

The recommendation is inert by itself. Manifests have no `enabled` field and
cannot add their package to the user's activation map.

Manifests may name a built-in profile, or a user-defined profile that must
exist when the package is launched. Generic desktop and CLI policy is usually
better selected by the user in `config.yaml`; manifest recommendations should
be reserved for genuinely package-specific requirements. A package entry can
always select a different profile or override individual values.

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

Changing HOME can also move configuration the application expects. Copy or
recreate needed settings inside its isolated home, or redirect individual
caches through top-level `env:` configuration instead.

### Optional integrations

Supported feature keys are:

| Feature | Effect when `false` |
| --- | --- |
| `network` | Creates a private network namespace |
| `x11` | Removes `DISPLAY` |
| `wayland` | Removes `WAYLAND_DISPLAY` |
| `dbus` | Removes `DBUS_SESSION_BUS_ADDRESS` |
| `audio` | Removes `PULSE_SERVER` and `PIPEWIRE_REMOTE` |

Feature keys default to `true`. Unknown keys are rejected, preventing a typo
from silently producing a weaker policy.

Only `network: false` creates an enforcing namespace. The other toggles remove
conventional environment variables; because the host filesystem remains
visible, they are compatibility controls for cooperative applications rather
than complete access blocks.

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
files are covered with `/dev/null`. Missing paths are ignored. `~` continues
to mean the original host home even after HOME has been redirected, including
inside a child package launch.

## Applications, dependencies, and child commands

The top-level application normally owns the sandbox. Dependencies inherit the
environment and restrictions of the process that launched them, but there are
two different launch paths to understand.

For a command typed in Code's integrated terminal, the common cases are:

| Code | Child package | Child HOME | Execution |
| --- | --- | --- | --- |
| Direct | Absent / `on-demand` | Host HOME | Direct |
| Direct | `always` | Child `{data}/home` | Top-level child sandbox |
| Sandboxed | Absent / `on-demand` | Code's isolated HOME | Direct inside Code's sandbox |
| Sandboxed | `always` | Child `{data}/home` | Reuses Code's sandbox unless the child adds mount/network restrictions |

An explicit `bunny sandbox <child>` behaves like `always` for that invocation.
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
`bunny sandbox jdk-21`.

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
real config, state, catalog, and shim directories, so shim resolution keeps
working even though Code changed HOME and every XDG base directory.

Bunny avoids unnecessary bubblewrap nesting. If the child only changes HOME
or removes environment integrations, it is executed directly within the
existing sandbox. Another bubblewrap layer is added only when the child
introduces a new hidden-path mount or disables network.

Restrictions are monotonic down the process tree:

- a child cannot restore network disabled by its parent;
- hidden paths remain hidden;
- X11, Wayland, D-Bus, and audio variables removed by a parent remain removed,
  even if the child's manifest or user environment tries to set them;
- a child can add further restrictions but cannot loosen the outer ones.

Commands that do not pass through Bunny cannot activate another package's
policy. They still inherit the current process environment, mounts, and
network namespace normally.

## Recommended case: sandbox the app, redirect SDK data

It is often unnecessary to sandbox a command-line SDK merely to move its
caches. For example, sandbox Code but keep Node as an ordinary direct package:

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

Running `node` or `npm` in Code's terminal then behaves like this:

```text
Code sandbox ({data}/code/home)
└── Node direct
    ├── HOME = Code's isolated home
    ├── npm prefix = node-22's {data}/npm-global
    ├── npm cache  = node-22's {data}/npm-cache
    └── restrictions = Code's restrictions
```

This separates Node's expensive or reusable npm data without creating a Node
sandbox. Other Node tools that use plain HOME still use Code's isolated home
inside Code and the real home when launched outside it.

Use a Node package entry as well when Node should always have its own complete
HOME or should add restrictions independently:

```yaml
sandbox:
  packages:
    code:
      profile: desktop
    node-22: {}
```

## Global npm tools and shims

With `NPM_CONFIG_PREFIX` configured as above:

```bash
npm install -g prettier
bunny reshim node
```

The Node manifest's `global-bins` declaration lets `bunny reshim` discover
executables under `{data}/npm-global/bin`. Bunny creates a `prettier` shim and
records that the active Node provider owns it. Run plain `bunny reshim` to
refresh every provider, or run it again after removing a global package.

When `prettier` is invoked inside sandboxed Code while Node is not sandboxed:

```text
Code sandbox
└── prettier shim
    └── active Node provider's {data}/npm-global/bin/prettier
```

Prettier runs directly inside Code's existing sandbox. The Node provider's
configured npm prefix/cache remain in Node's `{data}`, while HOME-based state
and all restrictions are inherited from Code. Outside Code, the same shim and
global installation run directly with the normal host HOME.

`npm install -g` does not automatically regenerate Bunny shims. Until
`bunny reshim` runs, the new executable may not be on PATH even though npm has
installed it successfully.

## Common configurations

### Desktop application with isolated state

```yaml
sandbox:
  packages:
    code:
      profile: desktop
      hide:
        - ~/.ssh
        - ~/.aws
```

### Network-free command-line tool on demand

```yaml
sandbox:
  packages:
    some-tool:
      activation: on-demand
      profile: offline-cli
```

```bash
bunny sandbox some-tool -- input.txt
```

### Override a manifest without replacing it

```yaml
sandbox:
  packages:
    code:
      features:
        audio: false
      hide:
        - ~/Documents/private
```

The override changes only `audio` and adds one hidden path. Any manifest
settings for network, Wayland, HOME, and other hidden paths remain in effect.

## Choosing between `env:` and sandboxing

Use top-level `env:` when the goal is simply to relocate a known cache,
repository, or global install prefix. This preserves native execution and can
share data across different enclosing applications.

Use `sandbox.packages` when the package should have a complete private HOME,
masked paths, or disabled host integrations. The two mechanisms compose:
explicit cache variables continue to point to the package's `{data}` even
when the command inherits another application's isolated HOME.

See [Configuration](config.md) for environment precedence and additional SDK
recipes, and [Portability](portability.md) for the default direct-execution
model.

## Trust boundary and limitations

This runtime sandbox uses a read-write host filesystem view. It does not:

- make untrusted or malicious code safe;
- prevent reads or writes outside isolated HOME, except at explicit `hide`
  paths;
- prevent access through every possible desktop or session mechanism merely
  by unsetting an environment variable;
- apply a package policy to a binary launched by absolute path rather than
  through Bunny;
- automatically sandbox packages that are absent or `on-demand` during a
  normal launch.

Use it as state and integration isolation for software you already trust. A
container, VM, or stricter read-only/allow-listed sandbox is more appropriate
for hostile code.

## Diagnosing support

Runtime sandboxing requires Linux and bubblewrap. `bunny doctor` checks both
the executable and unprivileged namespace support:

```text
✓ bwrap    bubblewrap 0.11.2
✓ sandbox  unprivileged user namespaces OK
```

An always-activated or explicitly sandboxed package fails rather than silently
running unsandboxed when bubblewrap cannot start. Packages without active
sandboxing continue to launch directly.

Useful checks when behavior is surprising:

- confirm the exact installed package ID under `sandbox.packages`;
- check whether activation is `always` or `on-demand`;
- built-in profile names cannot be redefined; check custom profile spelling;
- run `bunny reshim` after installing or removing runtime-global tools;
- use `bunny doctor` to verify layout, shims, and bubblewrap support.
