# Roadmap

Bunny is a small, opinionated tool. This page states its scope, lists supported
boundaries, and documents deliberate non-goals.

## What's in scope

We ship packages whose canonical distribution is a **standalone binary or
tarball**, downloaded once and run directly:

- **JVM ecosystem**: JDK distros (Temurin, Corretto, Zulu, GraalVM, Liberica,
  OpenJ9/Semeru), build tools (Maven, Gradle, sbt, Ant, JBang), language
  compilers (Kotlin, Scala), and profilers (VisualVM, async-profiler).
- **JavaScript runtimes**: Node.js LTS lines, Bun, and Deno.
- **Node ecosystem tools**: Package managers distributed as standalone binaries
  (currently pnpm). npm comes with Node and is managed through it.
- **Editors / IDEs targeting the above**: IntelliJ IDEA, VS Code, Cursor, Zed,
  and Eclipse.
- **General-purpose CLI tools**: Ubiquitous utilities including ripgrep, fd, bat,
  fzf, jq, gh, lazygit, delta, and eza.
- **AI coding agents**: Terminal agents (Claude Code, Codex CLI, opencode, pi)
  and the desktop apps that drive them (Claude Desktop, ChatGPT, GitHub
  Copilot) — tools pointed at a project, the same grounds an editor earns a
  slot on, not general chat clients. See [Sandboxing](docs/sandbox.md) for
  opting an installed package into isolated state and reduced integrations.

## What's out of scope

- **npm-installed JS tooling**: Prettier, ESLint, TypeScript, Biome, Vite,
  webpack. These belong in `package.json` and run via `npx` or package scripts.
- **Yarn standalone**: Corepack (shipped with Node) handles Yarn versioning.
- **Desktop applications**: Browsers, media players, and chat apps that aren't
  a coding assistant belong in Flatpak or distribution packages. AI coding
  agents (terminal and desktop) are the exception — see above.
- **Toolchains outside JVM/Node**: Polyglot ecosystems outside JVM and Node are
  already well-served by mise, asdf, and language-specific tools.
- **Operating systems other than Linux**: Bunny is built for Linux (`x86_64`).
- **Replacing system package managers**: `apt`, `dnf`, and `pacman` continue to
  manage system packages and libraries.
- **VM-equivalent isolation**: the opt-in
  [per-package sandbox](docs/sandbox.md) offers a `hardened` boundary that is
  deny-by-default and kernel-enforced, which materially limits unexpected or
  malicious user-space behaviour — but a VM remains the answer when the host
  kernel itself is inside the threat model. The default `scoped` boundary is
  state separation only, and packages not explicitly enabled remain direct
  `execve`.

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
- **macOS / Windows ports**: Linux only.
- **Fleet management, telemetry, and signed catalog servers**: Out of scope for
  a workstation tool.

## How to influence this

Open an issue at [cristatus/bunny](https://github.com/cristatus/bunny/issues)
describing the use case.
Proposals for new packages should be opened against
[cristatus/bunny-catalog](https://github.com/cristatus/bunny-catalog).
