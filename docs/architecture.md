# Architecture

Bunny is organized around a small set of boundaries. The command package
coordinates workflows; internal packages own the filesystem or domain behavior
for one concern. Keeping those directions one-way avoids putting install logic
in the CLI or command parsing in reusable packages.

```text
cmd/bunny
  ├── catalog + checker
  ├── installer ── desktop, runtime, shim
  ├── reshim
  ├── toolchains
  └── state + paths

shared primitives: manifest, fsutil
```

## Durable state and ownership

`internal/paths` is the source of truth for every path below `BUNNY_HOME`.
`internal/state` is the source of truth for installed packages, active
capability providers, command ownership, and runtime-installed global commands.
State is schema-versioned, validated on load and save, and replaced atomically.

Every state-changing CLI command holds `var/mutation.lock`. State is reloaded
after acquiring the lock, so two Bunny processes cannot commit mutations based
on the same stale snapshot. New mutating commands should use
`App.withMutation`; read-only commands should not take the lock.

Regular files in `bin/` are never treated as Bunny-owned shims. The command
name `bunny` is reserved at manifest validation and shim layers.

## Catalog views

The live catalog is used for discovery and new installs. Runtime and cleanup
use the install-time manifest snapshot in `var/app/{id}/manifest.yaml`, falling
back to the live catalog only when that snapshot is absent. A corrupt snapshot
is an error rather than a reason to silently use a potentially different live
manifest.

Remote index responses and checker metadata are size-bounded and timeout-bound.
The index cache is atomically replaced and uses a six-hour
stale-while-revalidate policy, so an expired cache remains usable offline.
Each index package summary includes `provides` and `requires` when present in
the manifest. List, search, completion, and reverse-dependency discovery can
therefore remain lightweight instead of fetching every remote manifest.

## Mutation transactions

An install stages downloads and prepared files before swapping the app
directory. Shims, desktop integration, the installed-manifest snapshot, and
state are then committed with compensating rollback for failures. State is the
commit record: it is never saved until the runnable files and manifest snapshot
exist.

Uninstall follows the reverse order. It first checks reverse dependencies,
renames the app directory aside, removes owned integration, activates any
fallback provider, and saves state before deleting the staged directory.

Provider switching updates both the state ownership map and physical shims.
Commands unique to the previous provider are removed; failures restore the
previous state and shim set.

## Error policy

- Integrity, state, required dependency, shim, and manifest-snapshot failures
  are hard errors.
- Optional desktop integration may fail without discarding an otherwise usable
  package, but partial files are cleaned up and the failure is reported.
- Batch-like operations join independent failures rather than hiding all but
  the last one.
- Cleanup reports successful removals alongside any paths it could not remove.
- Atomic file helpers never report a directory-sync error after the rename has
  already committed; callers must not roll back related state on a false
  pre-commit signal.

## Adding behavior

Put path construction in `paths`, persisted invariants in `state` or
`manifest`, transport behavior in `catalog`/`checker`/`installer.Downloader`,
and filesystem integration in its owning internal package. The CLI should
mainly establish the mutation boundary, call those operations, and present the
result.

For a mutating workflow, test both the success path and a failure at the state
save boundary. Also verify that filesystem artifacts and the in-memory state
are restored together.
