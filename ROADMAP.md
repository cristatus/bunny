# Roadmap

Bunny is a small, opinionated tool. This page states its scope and lists a handful of items likely to land eventually. Everything here is best-effort: bunny is built primarily to serve its maintainers' team workflow rather than to chase a feature backlog.

## What's in scope

We ship packages whose canonical distribution is a **standalone binary or tarball**, downloaded once and run indefinitely. If a tool's canonical install is `npm install -g X` or `pip install X`, it is out of scope — install it within the project that needs it.

- **JVM ecosystem.** JDK distros (Temurin, Corretto, GraalVM, Liberica, OpenJ9), build tools (Maven, Gradle, sbt, Ant, JBang), language compilers (Kotlin, Scala), profilers (JMC, VisualVM, async-profiler).
- **JavaScript runtimes.** Node LTS lines, Bun, and Deno.
- **Node ecosystem tools.** Package managers that ship a standalone binary
  (currently pnpm). npm comes with Node, so we don't ship it separately.
- **Editors / IDEs that target the above.** JetBrains IDEs (via the Toolbox app), Eclipse, VS Code, Cursor, Zed.
- **General-purpose CLI tools that show up in nearly every dev workflow.** ripgrep, fd, bat, fzf, jq, gh, lazygit, delta, eza.

## What's out of scope

- **npm-installed JS tooling.** Prettier, ESLint, TypeScript, Biome, Vite, webpack, etc. These belong in `package.json`; run them via `npx` or a script.
- **Yarn.** Corepack (shipped with Node) handles it. No separate install layer needed.
- **Browsers, chat apps, media tools, productivity software.** Install via Flatpak or your distro.
- **Toolchains outside the JVM/Node families.** mise and asdf already do this well; we will not compete.
- **Operating systems other than Linux.** macOS users are well served by brew and sdkman.
- **Replacing your distro's package manager.** `apt`, `dnf`, `pacman` still own system-level packages.
- **Sandboxing as a security boundary.** Bunny isolates SDK data per-version via launcher `env:`; GUI apps run native. Neither is a security model.

## Anti-roadmap

Things people sometimes ask for that we're explicitly not doing:

- **A bunny-managed shell** (replacing nix-shell / direnv). `.bunny-version` is the boundary; shells stay the user's.
- **Building from source.** We curate prebuilt upstream releases. If you need a custom build, vendor it as a catalog manifest with a `prepare:` step that drives the build.
- **A plugin system.** The catalog format is the extension point. Anything more is premature.
- **A package registry beyond the catalog.** The catalog is just YAML in git. Forks are encouraged.
- **Repackaging npm/pip/cargo packages as bunny manifests.** Those ecosystems have their own package managers — that's not the layer bunny operates at.
- **macOS / Windows.** Linux only. Install-time `prepare:` isolation relies on bwrap (Linux-only); runtime launch is plain exec and the `env:` redirection is portable in principle, but the rest of the codebase has Linux assumptions baked in.
- **Fleet management, telemetry, signed catalogs, snapshot/restore.** Real features, real engineering, no current plan.

## How to influence this

Open an issue at [cristatus/bunny](https://github.com/cristatus/bunny/issues) with the use case, not just the feature. The bar for adding to the catalog is lower than the bar for adding to the binary, so if the answer is "ship a manifest," that's the fastest path.
