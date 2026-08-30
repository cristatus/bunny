# First-class Java

Bunny is built around the Java workstation. That means multi-vendor JDK support,
automated build toolchains, runtime version constraints, and pin file
interoperability.

## Multiple JDKs, multiple vendors

Every JDK package in the catalog declares `provides: jdk`. They share the same
capability slot, so a project pin (`jdk 21`) matches whichever vendor you have
installed:

```bash
bunny install jdk-21        # Eclipse Temurin (default)
bunny install corretto-21   # Amazon Corretto
bunny install zulu-21       # Azul Zulu
bunny install graalvm-21    # GraalVM Community

bunny use corretto-21       # set Corretto as the global default
bunny run zulu-21 -- -version        # one-off execution without changing the default
```

JDK manifests update through the vendor-neutral
[Foojay Disco API](https://api.foojay.io/). Adding a new vendor or major line
is a simple manifest entry (`update: {type: foojay, distribution: <vendor>}`).
All downloads are verified end-to-end with SHA-256 checksums.

## Build toolchains (Gradle and Maven)

The JDK that launches Gradle or Maven is not always the JDK a project compiles
against. You might run builds under JDK 21 while a module targets Java 17. Both
build tools support "toolchains" to select JDKs by version from known
installation paths. Bunny automatically generates and maintains that list.

`bunny toolchains` writes toolchain configuration pointing at every installed
`provides: jdk` package. It runs automatically when you install or uninstall a
JDK or tool declaring toolchain dependencies. You can also run it manually to
force a refresh.

**Gradle**: Bunny writes a managed block into `~/.gradle/gradle.properties` (or
the active `GRADLE_USER_HOME`):

```properties
# >>> bunny managed (jdk toolchains) >>>
org.gradle.java.installations.paths=/home/you/.local/share/bunny/sdk/jdk-17,/home/you/.local/share/bunny/sdk/jdk-21
org.gradle.java.installations.auto-download=false
# <<< bunny managed <<<
```

A build declaring `java { toolchain { languageVersion = JavaLanguageVersion.of(17) } }`
resolves to Bunny's JDK 17 without triggering external downloads or requiring
manual `-Dorg.gradle...` flags.

**Maven**: Bunny generates a `toolchains.xml` file and passes it via `MAVEN_ARGS`
(`--global-toolchains …`), allowing `maven-toolchains-plugin` to match
`<jdk><version>17` against installed JDKs. The installation slot is deliberate:
Maven merges it with your own `~/.m2/toolchains.xml`, where `-t/--toolchains`
would replace that file and hide anything you declared in it.

```xml
<toolchain>
  <type>jdk</type>
  <provides><version>17</version></provides>
  <configuration><jdkHome>/home/you/.local/share/bunny/sdk/jdk-17</jdkHome></configuration>
</toolchain>
```

## Version constraints (`requires`)

Packages can declare minimum JDK version constraints:

```yaml
requires: ["jdk>=17"]
```

Bunny enforces constraints at two points:
- **Install time**: Refuses installation unless a satisfying JDK is present.
- **Run time**: Dynamically sets `JAVA_HOME` to a qualifying JDK (preferring the
  active default, or the newest installed satisfying version).

This is not theoretical: the Micronaut CLI ships class files compiled for Java
25, so its manifest declares `jdk>=25`. With JDK 21 as your default and JDK 25
also installed, `mn --version` still runs correctly because bunny launches it
under 25.

## Pinning a JDK per project

`.bunny-version` pins by capability, and a pin may name a package id, so a
project can select a specific vendor build:

```
jdk 21              # Temurin, the jdk-* line
jdk corretto-21     # Amazon Corretto for this tree
```

See [Per-project pinning](pinning.md).
