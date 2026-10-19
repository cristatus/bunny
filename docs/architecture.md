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

`internal/paths` is the source of truth for every path Bunny reads or writes.
Everything lives under one root, `$BUNNY_HOME` or `~/.bunny`; the variable only
moves the root. A package installs under the root for its kind (`sdk/`, `cli/`,
`app/`), and `state.json` records the kind it was installed as, so a package
whose kind later changes in the catalog is still found where it is.

A package's path is a relative link into `store/<id>/<version>`, one tree per
installed version. An install builds in `staging/<id>/`, moves the tree into
the store, then switches the link with a rename; the install journal records
the old link, so an interrupted install restores it. Updates remove no version:
a process started from one may still be running, and a path a tool resolved
through the link stays valid until `bunny clean` removes the versions no link
points at. `current/<capability>` links to each capability's active provider and
is rebuilt from state after every install, uninstall, and `bunny use`. Paths
the user lists under `links:` in the config are rebuilt the same way, and bunny
replaces only a link that already points at that package's install directory.

`internal/config` is the source of truth for activation and user policy: it
decides whether tool data is redirected and which exact package IDs run in the
optional sandbox. Manifests carry no sandbox policy at all: it is the user's
alone.

## Runtime execution

`runtime.Launcher` resolves project pins for required capabilities and checks
minimum versions before launch. An explicit incompatible pin is an error; no
other installed provider silently replaces it. Pins select package IDs and
continue to apply after an update switches a package to its new version. The launcher
layers environment variables in a fixed precedence order
(host, dependency env, manifest env, config env). Normal execution ends in a
direct `execve` unless the exact package is listed under sandbox.packages or
the user invoked `bunny run --sandbox`.

The sandbox resolver merges exactly two layers over the built-in defaults:
the selected built-in or user profile, then the package override. Sandbox launches
also export `$BUNNY_HOME` for the resolved root. A shim re-entering Bunny from
an isolated HOME therefore reads the same config and state. The versioned inherited sandbox context makes disabled integrations,
hidden paths, and effective writable roots explicit and monotonic. The
outermost sandbox owns the boundary; nested Bunny launches run directly and
fail closed when a requested child home is not already writable. Declared SDK
dependencies are launched directly inside the application's existing sandbox,
so enabling a JDK's own package sandbox does not wrap it again when an IDE uses
that JDK through `requires`.

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

An install stages downloads and extracted files in `~/.bunny/staging`, on the
same filesystem as the store, before moving the tree into the store and
switching the package's link to it. Shims,
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

## Project SDK provisioning

`install --project` uses the shared pin walker to collect the nearest selection
for each capability. It checks SDK kinds, capabilities, command conflicts,
minimum dependency versions, and cycles before installing in dependency order.
Existing packages are skipped unless forced; global provider selections stay
in place. `pin --init` captures inherited selections and active SDK providers
in a new file, without overwriting an existing file.
