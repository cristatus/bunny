# Per-project version pinning

Drop a `.bunny-version` file in a project root and bunny resolves shim invocations from inside that tree to the pinned versions. No shell hooks, no `cd` listeners, no IDE plugin required. Bunny also reads the pin files other tools already leave behind — `.sdkmanrc`, `.tool-versions`, `.java-version` — so existing projects work without conversion (see [Reading other tools' pin files](#reading-other-tools-pin-files)).

## The file

`.bunny-version` is a flat list of `<provides> <version>` lines:

```
jdk 21
maven 3.9
node 22
```

Each line names a `provides:` capability (declared by manifests in the catalog) and the version of that capability you want active in this tree. `jdk 21` means "use any installed package whose `provides: jdk` matches version 21" — bunny picks the matching installed manifest (e.g. `jdk-21`) and dispatches the shim there.

Comments (`#`) and blank lines are allowed.

## Reading other tools' pin files

Bunny does not require you to convert anything. In each directory it also
recognizes the pin files these tools write, mapping their `java`/`jdk` and
`node`/`nodejs` keys onto bunny capabilities and normalizing the version to a
major:

| File | Tool | Format read |
|---|---|---|
| `.bunny-version` | bunny | `<capability> <version>` lines |
| `.tool-versions` | asdf / mise | `<tool> <version>` lines |
| `.sdkmanrc` | SDKMAN | `<key>=<value>` lines |
| `.java-version` | jenv | a single bare version |

When several of these exist **in the same directory**, that's the precedence
order above — `.bunny-version` wins, then `.tool-versions`, `.sdkmanrc`,
`.java-version`. Keys for tools bunny has no capability for (e.g. `ruby` in a
`.tool-versions`) are simply ignored, and a non-numeric value like `latest` is
skipped rather than guessed.

## How resolution works

When a shim like `java`, `mvn`, or `node` is invoked:

1. bunny starts at the current working directory.
2. It walks up the directory tree looking for any recognized pin file.
3. In the nearest directory that has one, it picks the version pinned for the shim's `provides:` capability (consulting the files in the precedence order above).
4. If nothing pins that capability, it falls back to the global default set by `bunny use`.

This means a shim works the same in your terminal, in IntelliJ's embedded terminal, in `make`, and in CI — they all hit the same lookup logic via `argv[0]`.

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

The global default (set by `bunny use jdk-21`) only kicks in outside any pinned tree.

## IDE integration

Because resolution happens in the shim binary (not in a shell hook), IDEs that spawn `java` / `mvn` / `node` directly through `$PATH` get the right version automatically as long as they inherit the project working directory. That's typical for IntelliJ, Eclipse, VS Code, and most JDK auto-detection plugins.

If your IDE shells out from a different working directory, point its tool path at `~/.bunny/bin/<shim>` and either:

- set the IDE's working directory to the project root, or
- have the IDE pass the project root explicitly (e.g. `mvn -f /path/to/project/pom.xml`).

## Inspecting what would resolve

```bash
bunny info <id>     # shows the active version of that package
bunny use           # without args: list current providers
```

There isn't a `bunny which` yet (see [ROADMAP](../ROADMAP.md)) — `bunny info` plus reading `.bunny-version` is the workaround.

## Common gotchas

- **`provides:` not `id:`.** `.bunny-version` pins by capability, not by package id. Write `jdk 21`, not `jdk-21`. The package id `jdk-21` happens to encode the version, but the pin file works at the capability layer so `temurin-21` and `corretto-21` are interchangeable from the project's perspective.
- **Pinned version isn't installed.** Bunny falls back to the global default and logs a warning. `bunny install jdk-17` to make it real.
- **Multiple `.bunny-version` files in the tree.** The closest one wins. Subprojects with their own pin override the parent.
