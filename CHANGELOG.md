# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Removed

- `plbx agents` is gone. It answered a question you only ask while typing `plbx create` or `plbx run`, from a command you had to already know about — and the two commands that take an `AGENT` never listed one. They do now.

### Changed

- `plbx create --help` and `plbx run --help` name every agent you can pass, and which one you get by default.

### Fixed

- Two flag descriptions rendered their initialisms as words: `--cpus` read `Cpu limit`, and `plbx setup ssh --config` read `Ssh config to edit`. Both are reworded to lead with an ordinary word.

## [0.6.2] - 2026-08-14

### Fixed

- The error formatter mistakenly mangled sandbox names and directories with title case formatting. Instead, errors now quote these identifiers verbatim.

## [0.6.1] - 2026-08-14

### Fixed

- Homebrew installs could not find the `plbx` daemon. The cask now links both `plbx` and `plbxd` as expected.
- `plbx` is better at finding `plbxd`. It now looks for its daemon through a symlink, beside itself, and on `$PATH`.
- Homebrew installs stripped macOS's quarantine flag from `plbx` only, causing Gatekeeper to kill `plbxd` on launch. Quarantine is now cleared from both.

## [0.6.0] - 2026-08-14

### Changed

- Project is renamed to `planterbox`, and the binaries are `plbx` and `plbxd`. Nobody knew what a jardinière was.

## [0.5.0] - 2026-08-12

### Added

- `jard create --clone` gives a sandbox a copy of your repository instead of your repository. The original mounts read-only, the agent works in a clone under its own home, and nothing it does can reach your tree — including a `.git/hooks` script that would otherwise run on your machine the next time you commit.
- A clone-mode sandbox becomes a `jard-<name>` remote in your repository, so `git fetch jard-<name>` brings its work back. The remote goes when the sandbox does. It reaches the clone over jard's ssh gateway, so `jard setup ssh` wants running first.
- The clone keeps your repository's own remotes, minus any that name a local path, and calls the read-only original `host`. So `git push origin` in the sandbox reaches GitHub, and `git fetch host` picks up what you have done since.
- `jard inspect` reports which mode a sandbox is in, and the dashboard's create form asks which one you want — it cannot be changed afterwards.
- Published ports can be bound to a specific host address rather than every interface.

### Fixed

- Agents could not start under the `balanced` preset due to blocked domains. Harness vendors' own domains are now allowed beside their APIs.

## [0.4.0] - 2026-08-12

### Added

- Added a TUI dashboard, which `jard` with no arguments now opens. This displays each sandbox, its status, live CPU and memory for the running sandboxes, and interactive controls.
- A dramatically expanded CLI, including `create`, `ls`, `start`, `stop`, `rm`, `inspect`, `exec`, `cp`, and `agents` commands.
- Sandboxes are now persistent. `jard run` in a directory creates one the first time, then reattaches to it every time after. The packages, shell history, and agent state you left behind will still be in place. You may also reattach to a workspace by name.
- Added a background daemon, `jardd`, which owns sandbox state and lifecycle.
- Network policy has been overhauled, including a `jard policy` CLI command, `balanced` (default) / `open` / `locked-down` presets, logging, and a dashboard.
- SSH support via `jard setup ssh`. Run `ssh <name>.jard` to open a shell in a sandbox. VSCode over SSH is supported by default.
- Support for setting resource limits, environment, and published ports at create time: `--cpus` / `-m/--memory` / `-p/--publish` / `-e/--env`.
- Allow publishing ports from a sandbox with `jard ports`, including editing live sandboxes with `--publish`/`--unpublish`. Only TCP is supported.
- Base images for claude, codex, opencode, and a bare shell.
- Shell completions, via `jard completion`, and a man page, via `jard man`.

### Changed

- Workspaces are now mounted read-write by default, the primary and any others alike. Add a `:ro` suffix to mount one read-only.
- CLI output is now styled, and rewritten to be more informative. Unknown flags produce usage rather than a stack of parser output.

### Removed

- `jard` is no longer tied to Nix. There is no more `flake.nix` requirement, nor auto-entry to a Nix dev shell.
- The `jardiniere.toml` config file is removed.
- ssh-agent forwarding, host git identity injection, and seeding the agent's settings from the host are removed.

### Fixed

- `jard rm` refuses a running sandbox unless `--force` is given.
- `jard rm` now correctly deletes the sandbox's home volume.
- Create-time settings passed to `jard run` for a sandbox that already exists now warn, rather than being silently ignored.

## [0.3.0] - 2026-08-04

### Added

- CLI flags for every `jardiniere.toml` key, so any run can be tuned without a config file.
- Seed the configured agent's user settings from the host. Preferences like editor theme no longer need to be reconfigured each run.
- Support relative mount sources, resolved against the target `--dir` (e.g. `../backend:/work/backend:rw`).

### Fixed

- Set `IS_SANDBOX=1` when injecting Claude Code, so `--dangerously-skip-permissions` works inside the root-owned container.

## [0.2.0] - 2026-07-20

### Added

- `agent` config field: optionally drop Opencode, Claude Code, or Codex into the sandbox's Nix env.
- Nix flake package output, so `jard` can be installed with `nix profile install github:rhizomatous/jardiniere` or run with `nix run github:rhizomatous/jardiniere`.
- Nix flake `overlays.default`, so downstream flakes can add jardinière as an input and get `pkgs.jard`.

## [0.1.4] - 2026-07-19

### Added

- Homebrew cask distribution: `brew install rhizomatous/tap/jard` (macOS).

## [0.1.3] - 2026-07-19

### Fixed

- `go install github.com/rhizomatous/planterbox/cmd/jard@latest` now produces a binary named `jard` rather than `jardiniere`.

## [0.1.1] - 2026-07-19

### Fixed

- Corrected the Go module path from `github.com/vivshaw/jardiniere` to `github.com/rhizomatous/planterbox` so it matches the repository host and `go install github.com/rhizomatous/planterbox@latest` resolves.

## [0.1.0] - 2026-07-19

### Added

- Core sandbox: run coding agents inside a Nix-enabled Linux container, with the target repo bind-mounted at `/work` and a persistent `/nix` store volume.
- Container runtime autodetection across Docker, Podman, OrbStack, and other OCI-compatible runtimes.
- Git identity injection so the agent can author commits as you.
- SSH-agent forwarding on Linux, and on macOS when using Docker or OrbStack.
- Network policy, including an allowlist mode.
- Configurable extra host mounts.
- `jardiniere.toml` config file, supporting a custom `startup` command, `image` override, and network policy.
- Kong-based CLI with `--version` and `--dry-run` flags.

[Unreleased]: https://github.com/rhizomatous/planterbox/compare/v0.6.2...HEAD
[0.6.2]: https://github.com/rhizomatous/planterbox/compare/v0.6.1...v0.6.2
[0.6.1]: https://github.com/rhizomatous/planterbox/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/rhizomatous/planterbox/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/rhizomatous/planterbox/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/rhizomatous/planterbox/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/rhizomatous/planterbox/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/rhizomatous/planterbox/compare/v0.1.4...v0.2.0
[0.1.4]: https://github.com/rhizomatous/planterbox/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/rhizomatous/planterbox/compare/v0.1.1...v0.1.3
[0.1.1]: https://github.com/rhizomatous/planterbox/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/rhizomatous/planterbox/releases/tag/v0.1.0
