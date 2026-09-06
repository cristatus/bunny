# Per-project version pinning

Drop a `.bunny-version` file in a project root and Bunny resolves shim
invocations from inside that tree to the pinned packages. No shell hooks, no
`cd` listeners, and no IDE plugins required.

`.bunny-version` is the only pin format Bunny reads. Foreign files
(`.tool-versions`, `.sdkmanrc`, `.java-version`) use version/vendor naming that
Bunny does not translate into catalog package IDs. They are ignored rather
than reduced to a potentially different vendor or release. `bunny pin` writes
`.bunny-version` in one command.

## The file

`.bunny-version` is a list of `<capability> <value>` lines:

```
jdk 21
node 22
```

Only a capability some package declares with `provides:` can be pinned —
`bunny complete-capabilities` lists them. Maven and Gradle, for instance,
declare none, so there is no `maven 3.9` to pin.

Comments (`#`) and blank lines are allowed.

### CLI commands

```bash
# Pin capabilities in the current directory's ./.bunny-version
bunny pin jdk 21
bunny pin node 22

# Remove a pin
bunny unpin node
```

## Version or package id

A pin's value is either a bare version or a package id:

```
jdk 21              # -> jdk-21
jdk corretto-21     # that exact package
node 24             # -> node-24
```

A value starting with a digit is a version and is joined to the capability; a
value starting with a letter is a package id, used as written. Package ids
cannot begin with a digit, so the two forms are never ambiguous.

Naming a package id is how a project selects a specific provider — every JDK
vendor declares `provides: jdk`, so `jdk 21` alone can only ever mean `jdk-21`.
A pinned package must actually provide the capability, or the launch fails.

Keys are taken literally: `java 21` pins a `java` capability that nothing
provides, not `jdk`.

## Exact release pins

```bash
# Capture the currently installed vendor and release
bunny pin --exact jdk corretto-21
# Example result: jdk corretto-21@21.0.4+7

# Or write an explicit release guard
bunny pin jdk corretto-21@21.0.4+7
```

`--exact` requires the selected package to be installed. An explicit release
must match if that package is already installed. Exact pins compare the entire
installed manifest version, including build suffixes. An update to a different
release makes subsequent launches fail until you restore that release or
explicitly change the pin. The same check applies to dependent tools and
runtime-installed global commands.

Bare pins still track a package ID: `jdk 21` means whichever release is currently
installed as `jdk-21`. Exact pins do not lock artifact checksums, fetch historical
releases, or add side-by-side patch installations. To provision the same release,
use a catalog pinned to a Git commit and keep its artifacts available. Restore
with `bunny install --force <id>`. Only one release per package ID is installed.

## How resolution works

When a shim like `java`, `mvn`, or `node` is invoked:

1. Bunny starts at the current working directory and walks up.
2. In each directory it reads `.bunny-version`, looking for the shim's own
   capability or a capability required by the launched package.
3. The first directory that pins *that capability* wins. A nearer pin file
   that names other capabilities does not stop the walk, so a subproject
   overrides only what it names and inherits the rest from ancestors.
4. If nothing pins it, the global default from `bunny use` applies.
5. A missing package, wrong capability, incompatible minimum version, or exact
   release mismatch fails with an explanation rather than falling back.
6. Unreadable or malformed pin files also fail. Duplicate keys are rejected;
   comments after a pin are allowed.

Because resolution happens in the shim binary via `argv[0]`, it behaves
identically in your shell, inside IntelliJ's embedded terminal, under `make`,
and in CI pipelines.

## Examples

```bash
$ cat ~/work/legacy-app/.bunny-version
jdk 17
$ cat ~/work/new-app/.bunny-version
jdk 21

$ cd ~/work/legacy-app && java -version
openjdk version "17.x"
$ cd ~/work/new-app && java -version
openjdk version "21.x"
```

The global default (set by `bunny use jdk-21`) applies outside any pinned tree.

## IDE integration

IDEs that spawn `java`, `mvn`, or `node` directly through `$PATH` get the right
version automatically as long as they inherit the project working directory.
This is standard for IntelliJ, Eclipse, VS Code, and JDK detection plugins.

If your IDE executes processes from a different working directory, point its
tool path at `~/.local/bin/<shim>` and ensure the IDE sets the working directory
to the project root (or passes project files explicitly).

## Inspecting what resolves

`bunny doctor` reports the pin file in effect and what each pin resolves to,
including a pin whose package is missing:

```text
✓ .bunny-version  /path/to/project/.bunny-version
✓ Pin (jdk)       21 → jdk-21
✗ Pin (maven)     maven 3.9 pinned in /path/to/project/.bunny-version, but maven-3.9 is not installed
  fix: run 'bunny install maven-3.9'
```

It reports the pins from the nearest pin file only, while a shim keeps walking
up per capability, so a capability inherited from an ancestor directory can be
in effect without appearing here.

## Common gotchas

- **A bare version is not a provider search**: `jdk 21` means the package
  `jdk-21`. Name the package (`jdk corretto-21`) to pin another vendor.
- **Pinned package not installed**: the command fails with an install hint
  rather than falling back to the global default.
- **Multiple pin files in a tree**: the nearest directory that pins *that
  capability* wins. A subproject pin overrides only the capabilities it names.
- **Keys are literal**: `java 21` pins a `java` capability that nothing
  provides. Use `jdk`.
- **Other tools' pin files are ignored**: `.tool-versions`, `.sdkmanrc`, and
  `.java-version` are not read.
