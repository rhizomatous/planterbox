# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.10.0] - 2026-08-20

### Changed

- Each sandbox gets its own egress relay rather than sharing one with every other sandbox. A shared relay had to be attached to every sandbox's network, which put its lifetime outside any one of them: rebuilding it cut off every other running sandbox, a relay pointed at a moved daemon was never replaced, and nothing removed it when the last sandbox went. Nothing about network policy changes; the relay decides nothing and all egress still meets at the one proxy. **Upgrading leaves the old shared relay behind: remove it with `docker rm --force plbx-relay`.**
- `--state-dir` and `PLBX_STATE_DIR` name a directory plbx keeps everything under, rather than the records directory alone. **If you pass either, move your existing records into a `sandboxes/` subdirectory of it.** The default layout is unchanged. Previously the policy and the ssh host key landed in that directory's *parent*, so `--state-dir ~/mine` wrote into your home directory, and two daemons under one parent silently shared a policy and an ssh identity.
- `PLBX_SOCKET`, `PLBX_RUNTIME_DIR` and `PLBX_STATE_DIR` must be absolute paths. A relative one resolved against the working directory, so a daemon and a client started from different places looked for each other in different sockets.
- `plbx policy preset --help` lists what each preset allows, instead of just naming the three.
- `plbx policy ls`, `plbx policy rm`, `plbx daemon start` and `plbx daemon status` now have real help text. They previously fell back to their one-line summary.
- Shorter, plainer help throughout, and `-f/--force` on `plbx rm` now says it skips the confirmation as well as the running check.

### Removed

- `PLBX_SSH_SOCKET`. The client honoured it and the daemon could not, so setting it pointed `plbx ssh` at a socket nothing was listening on. The pidfile and the daemon log now sit beside the socket too, wherever `PLBX_SOCKET` puts it.

### Security

- `plbx policy deny` on an IPv6 address reported success and let the traffic through. A rule and the request it covered were read by different parsers, and only one understood an IPv6 literal, so no spelling of one matched anything. An allow failing to match merely puzzles; a deny failing to match is a control that does nothing.

### Fixed

- Sandboxes had no egress at all on stock Docker Engine for Linux. The relay reaches the proxy at `host.docker.internal`, which is a Docker Desktop convenience that Engine does not publish, and it was never given the mapping it needs.
- Creating a clone-mode sandbox whose git remote could not be written reported `sandbox not found` afterwards, having created the sandbox. Only over the daemon, which is the default.
- Creating the first sandbox from the dashboard skipped the network policy question, so it ran under a posture nobody chose. Creating a sandbox now settles the policy whichever way it was made.
- Attaching from the dashboard to a sandbox whose ports could not be bound dropped you out of the dashboard, where `plbx run` warned and carried on. Both tolerate it now.
- `plbx policy allow example.com:notaport` was accepted, stored, and matched nothing. Validation reads a pattern the same way matching does, so a rule it accepts is one the engine can act on.
- Text trimmed to fit is measured in terminal cells rather than characters, and cut between characters rather than through them. A workspace path in Japanese or Chinese was twice as wide as it was counted, so it overran its column in `plbx ls` and wrapped the dashboard; an accented letter written as a combining mark could lose its accent, and an emoji could be cut in half.

## [0.9.0] - 2026-08-18

### Added

- `plbx doctor` checks the health of your installation and tells you how to fix what it finds.
- The dashboard's create form refers to agents by company and harness: `Claude Code · Anthropic`, rather than just `claude`.
- The create form states what name a blank field will produce.
- A running sandbox's row shows its uptime, when the terminal has room for it.
- `f` in the dashboard's network tab filters the log to the selected sandbox.

### Changed

- The dashboard's key hints show only the actions valid for the cursor, rather than every action at all times.
- Sandbox shells have briefer names: `agent@myrepo:myrepo$`, rather than `agent@myrepo:/Users/you/src/myrepo$`.

### Fixed

- Creating a sandbox from the dashboard shows a loading state, instead of a non-interactive empty state.
- Creating a sandbox from the dashboard moves the cursor to it.

## [0.8.0] - 2026-08-16

### Added

- `plbx create` and `plbx run` now report that they're pulling an image, instead of sitting silently until it finishes.
- `plbx create` now echoes the definition before it builds anything.
- `plbx rm --all`
- `plbx policy log SANDBOX` narrows the log to one sandbox, and `--json` emits it as JSON.
- `plbx daemon restart` and `plbx daemon status` commands.
- Every command warns when the daemon answering it is a different build from the CLI.
- `plbx inspect` reports how long a running sandbox has been up.

### Changed

- `plbx rm`, `plbx stop` and `plbx start` now take multiple sandboxes.
- `plbx inspect` now always states the resource limits and ports, even when there are none to report.
- `plbx policy log` folds repeated entries into one line with a count. `--limit` now counts distinct entries rather than repetitions of one.
- `plbx inspect` and `plbx ls --json` now both redact a sandbox's environment variables.

## [0.7.0] - 2026-08-16

### Added

- `plbx ls` now shows the ports each sandbox publishes.
- `plbx create` ends by saying how to attach to what it just made.
- `ls`, `exec`, `inspect`, `start`, `setup` and `policy log` have real help text, instead of repeating their one-line summary.
- Examples in the help for `create`, `run`, `exec`, `cp`, `ls` and `policy log`.

### Changed

- `plbx rm` now asks permission before it deletes. You can bypass this with `plbx rm --force`.
- `plbx ls` drops the IMAGE column and moves WORKSPACE to the end.
- `plbx agents` is removed, as it was not useful.

### Fixed

- `plbx exec` against a stopped sandbox printed the container runtime's own error. It now says the sandbox is not running, and how to start it.
- `plbx run` and `plbx exec`, when the thing they ran exited non-zero, now only pass through the status, so both are usable in a script that reads stderr.
- `plbx exec --no-tty` hung after the command exited when run from a terminal. The status now comes back as it should.
- Paths and prose trimmed to fit are ellipsized, and cut from whichever end matters least: paths lose their front, prose loses its tail.
- The error message when invoking a sandbox that does not exist now points you at `plbx ls`.
- Flag descriptions no longer mangle initialisms into title case like `Cpu`.
- `--dry-run` wrote to your real sandbox records on `create` or `rm`.

## [0.6.2] - 2026-08-14

### Fixed

- The error formatter mangled sandbox names and directories with title case. Errors now quote them verbatim.

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

- `jard create --clone` gives a sandbox an isolated copy of your repository instead of your repository.
- A clone-mode sandbox becomes a `jard-<name>` remote in your repository, so `git fetch jard-<name>` pulls its work back. Requires `jard setup ssh`.
- Published ports can be bound to a specific host address rather than every interface.

### Fixed

- Agents could not start under the `balanced` preset, because their vendors' own domains were blocked. Those are now allowed beside the APIs.

## [0.4.0] - 2026-08-12

### Added

- A TUI dashboard, which `jard` with no arguments opens: each sandbox, its status, live CPU and memory for the running ones, and interactive controls.
- `create`, `ls`, `start`, `stop`, `rm`, `inspect`, `exec`, `cp`, and `agents` commands.
- Persistent sandboxes. `jard run` in a directory creates one the first time and reattaches every time after, with the packages, shell history, and agent state you left behind. Reattaching by name works too.
- A background daemon, `jardd`, which owns sandbox state and lifecycle.
- Overhauled network policy: a `jard policy` command, `balanced` (default) / `open` / `locked-down` presets, logging, and a dashboard.
- SSH support via `jard setup ssh`. Run `ssh <name>.jard` to open a shell in a sandbox. VSCode over SSH is supported by default.
- Support for setting resource limits, environment, and published ports at create time: `--cpus` / `-m/--memory` / `-p/--publish` / `-e/--env`.
- Publish ports from a sandbox with `jard ports`, including on live sandboxes via `--publish`/`--unpublish`. TCP only.
- Base images for claude, codex, opencode, and a bare shell.
- Shell completions, via `jard completion`, and a man page, via `jard man`.

### Changed

- Workspaces mount read-write by default, the primary and any others alike. Add a `:ro` suffix to mount one read-only.
- CLI output is styled and rewritten to say more. Unknown flags produce usage rather than a stack of parser output.

### Removed

- `jard` is no longer tied to Nix: no `flake.nix` requirement, no auto-entry to a Nix dev shell.
- The `jardiniere.toml` config file is removed.
- ssh-agent forwarding, host git identity injection, and seeding the agent's settings from the host are removed.

### Fixed

- `jard rm` refuses a running sandbox unless `--force` is given.
- `jard rm` now correctly deletes the sandbox's home volume.
- Create-time settings passed to `jard run` for a sandbox that already exists warn, rather than being silently ignored.

## [0.3.0] - 2026-08-04

### Added

- CLI flags for every `jardiniere.toml` key, so any run can be tuned without a config file.
- Seed the configured agent's user settings from the host, so preferences like editor theme survive between runs.
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

- Corrected the Go module path from `github.com/vivshaw/jardiniere` to `github.com/rhizomatous/planterbox`, so `go install github.com/rhizomatous/planterbox@latest` resolves.

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

[Unreleased]: https://github.com/rhizomatous/planterbox/compare/v0.10.0...HEAD
[0.10.0]: https://github.com/rhizomatous/planterbox/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/rhizomatous/planterbox/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/rhizomatous/planterbox/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/rhizomatous/planterbox/compare/v0.6.2...v0.7.0
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
