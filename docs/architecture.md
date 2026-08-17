# Architecture

Bunny is organized around a small set of explicit boundaries. The command
package coordinates workflows; internal packages own filesystem or domain
behavior for a single concern. Keeping these dependencies strictly one-way
avoids embedding installation logic in the CLI or command parsing in reusable
libraries.

```text
cmd/bunny
  ├── catalog + checker
  ├── installer ── desktop, runtime, shim
  ├── reshim
  ├── toolchains
  └── state + paths + config

shared primitives: manifest, fsutil
```

## Durable state and ownership

`internal/paths` is the source of truth for every path Bunny reads or writes. It
resolves one logical layout under two possible prefix modes: standard XDG base
directories by default, and a single consolidated root when `$BUNNY_HOME` is
set. No other package branches on which layout is active. Install locations come
from two sources: user-configured install roots determine where new installs go,
while paths recorded in `state.json` track where existing packages reside.
Reading the recorded path is what makes install roots safe to change without
stranding installed tools.

`internal/config` is the source of truth for user policy: it manages
`config.yaml` and is the only place deciding whether a tool's global data is
redirected. Manifests describe how to install and wire a package; they never
express user isolation policy. `runtime.Launcher` layers environment variables
in a fixed precedence order (host, dependency env, manifest env, config env) so
user overrides cannot be silently outranked by catalog changes.

`internal/state` is the source of truth for installed packages, active capability
providers, command ownership, and runtime-installed global commands. State is
schema-versioned, validated on load and save, and replaced atomically.

Every state-changing CLI command holds `mutation.lock` alongside `state.json`.
State is reloaded from disk immediately after acquiring the lock, preventing two
concurrent Bunny processes from committing mutations based on stale snapshots.
Mutating commands must use `App.withMutation`; read-only commands do not acquire
the lock.

Regular files in `bin/` are never treated as Bunny-owned shims. The command name
`bunny` is reserved at manifest validation and shim layers.

## Catalog views

The live catalog is used for package discovery and fresh installs. Runtime
execution and cleanup use the install-time manifest snapshot in
`manifests/{id}.yaml`, falling back to the live catalog only if that snapshot is
absent. A corrupt snapshot produces a hard error rather than silently falling
back to a potentially different live manifest.

Snapshots sit beside `state.json` in the same durability class as Bunny's internal
bookkeeping. `{data}` belongs to the package once configuration redirects a
tool's native paths there, so its contents belong to the user to clear, and
Bunny keeps nothing there that it cannot regenerate.

Remote index responses and checker metadata are bounded by size and timeout.
The index cache is atomically replaced and uses a six-hour
stale-while-revalidate policy, ensuring an expired cache remains usable offline.
Each index package summary includes `provides` and `requires` metadata when
present in the manifest, keeping listing, search, completion, and
reverse-dependency discovery fast without fetching remote manifests.

## Mutation transactions

An install stages downloads and extracted files in a `.staging` directory on the
destination filesystem before swapping the app directory into place. Shims,
desktop entries, manifest snapshots, and state are then committed with
compensating rollback for failures. State acts as the commit record: it is never
persisted until runnable binaries and manifest snapshots are verified.

Uninstall follows the reverse order: it checks reverse dependencies, moves the
app directory aside, removes owned integrations, activates any fallback
provider, and persists state before deleting the staged directory.

Provider switching updates both the state ownership map and physical symlink
shims. Commands unique to the previous provider are removed; failures restore
the previous state and shim set.

## Error policy

- Integrity, state, dependency constraint, shim, and manifest snapshot failures
  are hard errors.
- Optional desktop integration failures do not discard an otherwise usable
  package; partial files are cleaned up and the warning is reported.
- Batch operations aggregate independent failures rather than hiding all but the
  last one.
- Cleanup reports successful removals alongside paths it could not remove.
- Atomic file helpers never report directory-sync errors after a rename has
  committed; callers must not roll back state on false pre-commit signals.

## Adding behavior

Place path construction in `paths`, persisted invariants in `state` or
`manifest`, transport behavior in `catalog`/`checker`/`installer.Downloader`,
and filesystem integration in its owning internal package. The CLI layer should
establish mutation boundaries, call internal operations, and format results.

For mutating workflows, test both the success path and failure at the state-save
boundary, verifying that filesystem artifacts and in-memory state are restored
together.
