# Per-project version pinning

Drop a `.bunny-version` file in a project root and Bunny resolves shim
invocations from inside that tree to the pinned versions. No shell hooks, no
`cd` listeners, and no IDE plugins required. Bunny also reads the pin files
other tools leave behind (`.sdkmanrc`, `.tool-versions`, `.java-version`), so
existing projects work without conversion.

## The file

`.bunny-version` is a list of `<capability> <version>` lines:

```
jdk 21
maven 3.9
node 22
```

Each line names a capability declared by manifests in the catalog and the version
you want active in this tree. `jdk 21` means "use any installed package providing
`jdk` at version 21": Bunny selects the matching installed package (such as
`jdk-21` or `corretto-21`) and dispatches the shim there.

Comments (`#`) and blank lines are allowed.

### CLI commands

```bash
# Pin capabilities in the current directory's ./.bunny-version
bunny pin jdk 21
bunny pin node 22

# Remove a pin
bunny unpin node
```

## Reading other tools' pin files

Bunny recognizes pin files written by other tools, mapping `java`/`jdk` and
`node`/`nodejs` keys to Bunny capabilities:

| File             | Tool        | Format read                    |
| ---------------- | ----------- | ------------------------------ |
| `.bunny-version` | bunny       | `<capability> <version>` lines |
| `.tool-versions` | asdf / mise | `<tool> <version>` lines       |
| `.sdkmanrc`      | SDKMAN      | `<key>=<value>` lines          |
| `.java-version`  | jenv        | a single bare version          |

When several of these exist in the same directory, precedence follows the table
order above: `.bunny-version` wins, followed by `.tool-versions`, `.sdkmanrc`,
and `.java-version`. Keys for tools Bunny does not manage are ignored, and
non-numeric values like `latest` are skipped.

## How resolution works

When a shim like `java`, `mvn`, or `node` is invoked:

1. Bunny starts at the current working directory.
2. It walks up the directory tree looking for any recognized pin file.
3. In the nearest directory containing one, it selects the version pinned for the
   shim's capability (consulting files in the precedence order above).
4. If nothing pins that capability, it falls back to the global default set by
   `bunny use`.

Because resolution happens in the shim binary via `argv[0]`, it behaves
identically in your shell, inside IntelliJ's embedded terminal, under `make`,
and in CI pipelines.

## Examples

```bash
# Project A: Java 17 + Maven 3.9
$ cat ~/work/legacy-app/.bunny-version
jdk 17
maven 3.9

# Project B: Java 21 + Gradle 8
$ cat ~/work/new-app/.bunny-version
jdk 21
gradle 8

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

## Inspecting what would resolve

```bash
bunny info <id>  # shows details for a package
```

## Common gotchas

- **Capabilities versus Package IDs**: `.bunny-version` pins by capability (`jdk 21`),
  not package ID (`jdk-21`). This ensures `temurin-21` and `corretto-21` remain
  interchangeable from the project's perspective.
- **Pinned version not installed**: Bunny falls back to the global default and
  emits a warning. Run `bunny install jdk-17` to install it.
- **Multiple pin files in a tree**: The closest directory wins. Subprojects with
  their own pin override parent directories.
