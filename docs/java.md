# First-class Java

Bunny is built around the Java workstation. That means four things beyond "it
installs JDKs": you can pick a JDK *vendor*, your build tools compile against
the *right* JDK regardless of which one launched them, a tool can *require* a
minimum JDK, and bunny reads the pin files you already have.

## Multiple JDKs, multiple vendors

Every JDK package declares `provides: jdk`, so they all compete for the same
capability slot and a project pin (`jdk 21`) matches whichever vendor you
installed.

```bash
bunny install jdk-21        # Eclipse Temurin (the default line)
bunny install corretto-21   # Amazon Corretto
bunny install zulu-21       # Azul Zulu
bunny install graalvm-21    # GraalVM Community

bunny use corretto-21       # make Corretto the global default
bunny run zulu-21 -- java -version   # one-off, without switching the default
```

JDK manifests update through the vendor-neutral [Foojay Disco
API](https://api.foojay.io/), so adding a new vendor or major line is a
one-line manifest (`update: {type: foojay, distribution: <vendor>}`) — no
per-vendor scraping. Installs are checksum-verified end to end.

## Build toolchains (Gradle & Maven)

The JDK that *launches* Gradle or Maven is not always the JDK a project should
*compile* with. You might run everything under JDK 21 while a legacy module
must target 17. Both build tools solve this with "toolchains" — they select a
JDK by version from a list of known installations. Bunny generates that list.

`bunny toolchains` writes toolchain config pointing at every installed
`provides: jdk` package. It runs automatically whenever you install or
uninstall a JDK (or a tool that declares `toolchains:`), so the list stays in
sync; run it by hand only to force a refresh.

**Gradle** — a managed block is merged into the Gradle user home's
`gradle.properties` (the rest of the file is preserved):

```properties
# >>> bunny managed (jdk toolchains) >>>
org.gradle.java.installations.paths=/home/you/.bunny/app/jdk-17,/home/you/.bunny/app/jdk-21
org.gradle.java.installations.auto-download=false
# <<< bunny managed <<<
```

A build declaring `java { toolchain { languageVersion = JavaLanguageVersion.of(17) } }`
now resolves to bunny's JDK 17 — no `auto-download`, no manual `-Dorg.gradle...`
flags.

**Maven** — a `toolchains.xml` is generated and passed via `MAVEN_ARGS`
(`--toolchains …`), so `maven-toolchains-plugin` matches `<jdk><version>17`
against bunny's installs:

```xml
<toolchain>
  <type>jdk</type>
  <provides><version>17</version></provides>
  <configuration><jdkHome>/home/you/.bunny/app/jdk-17</jdkHome></configuration>
</toolchain>
```

## Version constraints (`requires`)

A package can require a minimum JDK rather than any JDK:

```yaml
requires: ["jdk>=17"]
```

Bunny enforces this both ways. At **install** time it refuses unless a JDK that
satisfies the constraint is installed. At **run** time it sets `JAVA_HOME` to a
*satisfying* JDK — preferring the active one, otherwise the newest installed
JDK that qualifies — even when your global default is older.

This is not theoretical: the Micronaut CLI ships class files compiled for Java
25, so its manifest declares `jdk>=25`. With JDK 21 as your default and JDK 25
also installed, `mn --version` still runs correctly because bunny launches it
under 25.

## Reading your existing pin files

You don't have to convert anything. Alongside its own `.bunny-version`, bunny
reads `.sdkmanrc` (SDKMAN), `.tool-versions` (asdf/mise), and `.java-version`
(jenv) — see [Per-project pinning](pinning.md).
