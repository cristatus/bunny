# Roadmap

Bunny is a small, opinionated tool. This page states its scope, lists supported
boundaries, and documents deliberate non-goals.

## What's in scope

We ship packages whose canonical distribution is a **standalone binary or
tarball**, downloaded once and run directly:

- **JVM ecosystem**: JDK distros (Temurin, Corretto, Zulu, GraalVM, Liberica,
  OpenJ9/Semeru, JetBrains Runtime), build tools (Maven, Maven Daemon, Gradle,
  sbt, Ant, JBang), language compilers (Kotlin, Scala), framework CLIs
  (Micronaut, Quarkus, Spring Boot), servlet containers (Tomcat), and
  diagnostics/profiling tools (VisualVM, async-profiler, JMC, Arthas).
- **JavaScript runtimes**: Node.js LTS lines, Bun, and Deno.
- **Node ecosystem tools**: Package managers distributed as standalone binaries
  (currently pnpm). npm comes with Node and is managed through it.
- **Editors / IDEs targeting the above**: IntelliJ IDEA, Eclipse, NetBeans,
  VS Code, Cursor, Zed, and Neovim.
- **General-purpose CLI tools**: Ubiquitous single-binary utilities — search and
  file navigation (ripgrep, fd, fzf, bat, eza, broot), Git and forge clients
  (lazygit, delta, difftastic, gh, glab), text and data wrangling (jq, yq, sd),
  system inspection (bottom, procs, duf, dust, hyperfine), shell linters
  (shellcheck, shfmt), command runners and docs (just, tealdeer), and terminal
  environment tools (starship, zoxide, atuin, zellij). A prompt or a
  multiplexer is a binary bunny installs, not a shell bunny manages.
- **AI coding agents**: Terminal agents (Claude Code, Codex CLI, opencode, pi,
  Antigravity) and the desktop apps that drive them (Claude Desktop, ChatGPT,
  GitHub Copilot, Antigravity) — tools pointed at a project, the same grounds
  an editor earns a slot on, not general chat clients. See
  [Sandboxing](docs/sandbox.md) for opting an installed package into isolated
  state and reduced integrations.

## What's out of scope

- **npm-installed JS tooling**: Prettier, ESLint, TypeScript, Biome, Vite,
  webpack. These belong in `package.json` and run via `npx` or package scripts.
- **Yarn standalone**: Yarn publishes no standalone binary; its releases are
  JavaScript bundles that need a Node runtime. Yarn also versions itself per
  project — `yarn set version` writes `.yarn/releases` and pins it in
  `.yarnrc.yml` — so a bunny-managed Yarn would compete with the project's own.
- **Desktop applications**: Browsers, media players, and chat apps that aren't
  a coding assistant belong in Flatpak or distribution packages. AI coding
  agents (terminal and desktop) are the exception — see above.
- **Toolchains outside JVM/Node**: Polyglot ecosystems outside JVM and Node are
  already well-served by mise, asdf, and language-specific tools.
- **Operating systems other than Linux**: macOS and Windows ports are not
  planned. Linux releases are built for `x86_64`; another Linux architecture
  is a build target rather than a change of scope.
- **Replacing system package managers**: `apt`, `dnf`, and `pacman` continue to
  manage system packages and libraries.
- **VM-equivalent isolation**: the opt-in [per-package sandbox](docs/sandbox.md)
  offers a kernel-enforced `hardened` boundary, but a VM remains the answer
  when the host kernel itself is inside the threat model.

## Anti-roadmap

Explicit architectural non-goals:

- **A bunny-managed shell**: Bunny does not replace `direnv` or `nix-shell`.
  `.bunny-version` is the boundary; shells remain standard.
- **Building from source**: We curate prebuilt upstream releases. Custom builds
  can be vendored as catalog manifests with custom `prepare:` steps.
- **Plugin systems**: The catalog format is the extension point.
- **Centralized package registries**: The catalog is YAML in Git, designed for
  forks.
- **Repackaging npm/pip/cargo packages**: Language package managers manage
  their own modules.
- **Fleet management, telemetry, and signed catalog servers**: Out of scope for
  a workstation tool.
- **Reading other tools' pin files**: `.tool-versions`, `.sdkmanrc`, and
  `.java-version` encode a vendor and patch level bunny cannot honor, so a
  shared pin file silently diverges from what it asked for. `.bunny-version`
  is the only format read, and `bunny pin` writes it.
- **Unofficial upstream sources**: update checks follow the project's own
  releases, checksums, and metadata endpoints. Community redistributions such
  as the AUR are not consulted, however convenient their version feeds are.

## How to influence this

Open an issue at [cristatus/bunny](https://github.com/cristatus/bunny/issues)
describing the use case.
Proposals for new packages should be opened against
[cristatus/bunny-catalog](https://github.com/cristatus/bunny-catalog).
