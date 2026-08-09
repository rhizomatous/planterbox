# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Host-enforced network policy. A sandbox is alone on a private network with no route out; its only way to reach anything is jard's proxy, which checks every request against a policy set on the host with `jard policy`. An agent that ignores `HTTP_PROXY` gets nowhere — there is no route to ignore it with. Three presets to start from: `balanced` (the default), `open`, and `locked-down`.
- `jard policy ls`, `allow`, `deny`, `rm`, `check`, `log`, and `preset`. Wildcards are written `*.example.com`, and cover subdomains but not the apex. A deny beats any allow covering the same host.
- A network panel in the dashboard, on `tab`: everything sandboxes have reached for and what was refused, with `a` to allow the selected host and `d` to deny it. The next request goes through with nothing restarted.
- Private, loopback, and link-local addresses are never reachable, whatever the policy says — including a hostname that resolves into them.
- A background daemon, `jardd`, which owns sandbox state and lifecycle. It starts on its own the first time a command needs it, so there is nothing to set up. `jard daemon start`, `stop`, and `status` are there for when you want to drive it deliberately.
- A dashboard, which `jard` with no arguments now opens: every sandbox, its status, and live CPU and memory for the running ones. `c` creates one, `i` shows its details, `enter` attaches the agent, `x` opens a shell, `s` starts or stops, `r` removes, `?` lists the bindings.
- Persistent sandboxes. `jard run` in a directory creates one the first time and reattaches to it every time after, with the packages, shell history, and agent state you left behind still in place.
- `jard run`, `create`, `ls`, `start`, `stop`, `rm`, `inspect`, `exec`, `cp`, and `agents`. A command that takes a sandbox name defaults to the one for the current directory.
- Reattach by workspace path or by `--name`, so `jard run` finds the right sandbox from inside a repo or from anywhere.
- Multiple workspaces, bind-mounted at their host paths. The first is the primary; later ones take a `:ro` suffix. `jard run ~/work/frontend ~/work/backend:ro`.
- Resource limits, environment, and published ports at create time: `--cpus`, `-m/--memory`, `-p/--publish`, `-e/--env`.
- `jard ports` shows what a sandbox publishes, and `--publish`/`--unpublish` change it. Unlike the settings fixed when a sandbox is created, ports can change at any time: a change applies at once to a running sandbox and on next start otherwise. Published ports are TCP — a sandbox is alone on a private network and cannot publish for itself, so its ports are carried by a forwarder alongside it, rebuilt on every start, and that forwarder speaks TCP only.
- `--` passes everything after it to the agent verbatim, and the agent's exit status becomes jard's.
- Base images for claude, codex, opencode, and a bare shell, published to `ghcr.io/rhizomatous/jard-<agent>`. `--image` starts from something else.
- `jard rm` refuses a running sandbox unless `--force` is given.
- `jard ls`, listing sandboxes and their status. `--json` for scripting, `-q` for names only.
- Sandbox definitions and state now persist on disk under an XDG-respecting directory. `--state-dir` or `JARD_STATE_DIR` overrides where.
- Shell completions and man page generation.

### Changed

- CLI moved from Kong to Cobra, and wrapped in Fang
- A sandbox is defined by the flags given when it is created, and thereafter by its stored spec.
- Help, errors, and `--version` are styled. Unknown flags produce usage rather than a stack of parser output.
- A missing or unreachable container runtime no longer stops jard from starting. It fails on the first command that needs one, and `--dry-run` needs no runtime at all.
- The CLI and TUI now reach the sandbox layer only through a single `api.Service` interface, and normally reach it over a socket to the daemon rather than in-process. `--dry-run` and `--state-dir` stay in-process: the first must work with no runtime and no daemon at all, and the second names a store the running daemon does not own.
- Sessions are now owned by the daemon, which holds the terminal on your behalf. `jard exec` and attaching from the dashboard behave as before.
- Create-time settings passed to `jard run` for a sandbox that already exists now warn, rather than being silently ignored.
- Workspaces are mounted read-write by default.
- Published ports have moved off a sandbox's spec, since they are not part of the container it builds. `jard inspect` reports them as before; sandboxes created by an earlier build of this release lose theirs, and `jard ports --publish` puts them back without recreating anything.

### Removed

- The `flake.nix` requirement, along with the `nix develop` entry and the shared `/nix` store volume.
- Bare `jard` no longer starts a sandbox. It opens the dashboard, or prints the sandbox listing when there is no terminal to draw on.
- The `jardiniere.toml` config file. Network policy is set host-side with `jard policy` in a later release, so that a repo cannot request its own egress permissions.
- The `--dir`, `--startup`, `--mount`, `--network`, and `--allow` flags.
- ssh-agent forwarding, host git identity injection, and seeding the agent's settings from the host. Nothing is seeded from the host now; a sandbox's contents come from its base image and from what you run inside it.
- The tinyproxy allowlist sidecar, ahead of host-enforced network policy in a later release.

### Fixed

- `jard rm` now deletes the sandbox's home volume. It had been naming the volume after the container's runtime id rather than the sandbox, and once a sandbox had been started that id is a hash — so the removal quietly succeeded against a volume that never existed, and every removed sandbox left its whole disk behind. If you have used jard before this release, `docker volume ls | grep '^jard-'` will show the orphans; they are safe to delete.

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

- `go install github.com/rhizomatous/jardiniere/cmd/jard@latest` now produces a binary named `jard` rather than `jardiniere`.

## [0.1.1] - 2026-07-19

### Fixed

- Corrected the Go module path from `github.com/vivshaw/jardiniere` to `github.com/rhizomatous/jardiniere` so it matches the repository host and `go install github.com/rhizomatous/jardiniere@latest` resolves.

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

[Unreleased]: https://github.com/rhizomatous/jardiniere/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/rhizomatous/jardiniere/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/rhizomatous/jardiniere/compare/v0.1.4...v0.2.0
[0.1.4]: https://github.com/rhizomatous/jardiniere/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/rhizomatous/jardiniere/compare/v0.1.1...v0.1.3
[0.1.1]: https://github.com/rhizomatous/jardiniere/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/rhizomatous/jardiniere/releases/tag/v0.1.0
