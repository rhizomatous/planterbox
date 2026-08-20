# AGENTS.md

**planterbox** (`plbx`) is a Go CLI that runs coding agents inside isolated, persistent container sandboxes. Read `README.md` for what it does and what it protects against.

`docs/concessions.md` records where an intended design met reality and lost: what was wanted, what turned out to be true, and what would have to change to get it back. Read it before re-litigating a design that looks wrong. Add to it when a requirement forces a design you wouldn't otherwise have chosen.

## Dev environment

All tooling is provided in Nix dev shell: **work inside it.**

## Commands

- See `Makefile` for the common dev commands.
- `plbx --dry-run`: print the exact container commands without executing them. The best way to inspect behavior without a live runtime.

## Conventions

- **Formatting:** gofumpt & goimports.
- **Linting:** golangci-lint (staticcheck for bugs; revive for style; errcheck, gocritic, errorlint, etc.). Keep it at 0 issues.
- **Doc comments:** standard Go form. Do not write archaeological comments describing past states and changes.
- **Prose:** comments are lowercase and terse.
- **Errors:** lowercase, no trailing punctuation; `errors.New` for static strings, `fmt.Errorf` + `%w` when wrapping.
- **An error never begins with a name the user supplied.** The CLI's renderer upper-cases an error's first letter, so a message leading with a sandbox name reports a name nobody has: `myrepo` comes back as `Myrepo`. Put the sentinel first and the identifier after it, quoted: `fmt.Errorf("%w: %q", ErrNotFound, ref)`. A fixed leading word is fine, and reads correctly capitalised: `docker create: ...`.

## Layout

- `cmd/plbx`: the CLI (cobra + fang). The `main` package.
- `cmd/plbxd`: the daemon. A second `main` package, plain `flag`, no fang.
- `cmd/plbx-relay`: the egress relay that runs inside the runtime. Tiny, static, and deliberately incurious.
- `internal/api`: the `Service` interface and its types.
- `internal/api/direct`: in-process implementation of `Service`.
- `internal/api/rpc`: the same `Service` over gRPC. Client, server, and the generated contract in `plbxv1`; regenerate with `make proto`.
- `internal/daemon`: socket lifecycle, autostart, and connecting to a running daemon.
- `internal/store`: sandbox specs + state, on disk, XDG-respecting.
- `internal/runner`: the `Runner` interface, runtime detection, and the OCI adapter.
- `internal/proxy`: the egress policy engine, the filtering proxy, and the connection log. The engine is pure.
- `internal/api/direct/clone.go`: clone mode. The private clone, and the `plbx-<name>` remote it becomes in your repository.
- `internal/sshd`: the wish-based SSH gateway, on a unix socket. Sessions are execs, not connections.
- `internal/tui`: the bubbletea dashboard, which bare `plbx` opens.
- `internal/ui`: Charm-based terminal output.
- `images/`: one multi-stage Dockerfile, a build target per agent, published to ghcr.

**The invariant:** `cmd/plbx` and `internal/tui` hold an `api.Service` and never reach past it to `internal/runner`, `internal/store`, or a container runtime. `depguard` enforces this; don't work around it.

Which implementation they hold is `open`'s business in `cmd/plbx/root.go`: the daemon normally, in-process for `--dry-run` and `--state-dir`.

## Things that will bite you

- **Most of a sandbox's definition is fixed at create time.** `Spec` is what the container was built from: written once, reread on every reattach. Changing any of it means building a different container, which costs everything the old one held outside its home volume. Published ports are the exception, and live on `Sandbox` rather than `Spec`, because they aren't on the container at all: a sandbox cannot publish for itself, so its ports are carried by a forwarder beside it that is rebuilt on every start regardless. Anything else that should be changeable needs the same kind of story.
- **`plbx` and `plbxd` ship together, and every packaging route has to keep them together.** `plbx` autostarts the daemon from beside itself: the Makefile builds both, goreleaser builds both, the Homebrew cask links both. It also has to find them together. `os.Executable` does not resolve a symlink, so a `plbx` reached through one on `$PATH` looks for its daemon in a directory that holds only the link; `daemonNear` checks the resolved directory first for exactly that reason.
- **ssh has nowhere to put the sandbox's name, so `plbx ssh-proxy` writes it first.** The protocol never sends the hostname the client typed, and a single `Host *.plbx` block has no token to interpolate a username from. The name and a newline go ahead of the ssh bytes, and `ConnCallback` reads exactly that much, a byte at a time: a buffered read would swallow the handshake with no way to give it back.
- **`ssh -L` dials from inside the sandbox, not from the daemon.** The daemon has no route to a sandbox's network, the same wall the egress proxy met. `internal/sshd/forward.go` runs the dial as an exec, with bash's `/dev/tcp` opening the socket. It's a builtin, so it needs nothing installed that the image contract does not already promise.
- **A clone-mode sandbox's working directory exists before its clone does.** The runtime creates the container's `--workdir` on first start, as root, inside the home volume, so `git clone` hits a directory it cannot write. The clone script `rmdir`s it first, which is safe because rmdir refuses a directory with anything in it.
- **A sandbox cannot publish its own ports.** `--publish` on a container attached to an internal network is not an error: the runtime exits zero and creates no mapping. `internal/runner/ports.go` carries them instead, and that is also why `Spec` has no ports in it.
- **`/home/agent` is a volume mount point.** A volume takes its contents from the image only on first use, so anything the image puts under `/home/agent` is frozen the moment a sandbox is first started. Agent binaries go in `/usr/local`.
- **`rm --volumes` does not remove named volumes,** only anonymous ones. The home volume needs its own `volume rm`, or every removed sandbox leaks its disk.
- **Workspace paths cannot contain `:`.** A mount spec is colon-delimited, so such a path silently binds somewhere else. `Spec.Validate` rejects it; keep it that way.
- **A session's stdio is a parameter, not the process's.** `api.Service.Exec` takes `api.Streams` because the process running the command is not always the one holding the terminal. In-process they are the CLI's own stdio and the runtime inherits a real tty; through the daemon they are the far end of a socket, and the executor allocates a pty because the runtime refuses `-t` on a pipe.
- **A pty opens at 0x0, and a full-screen program reads its size once.** Size it before the child starts, not when the first resize arrives, or the agent lays itself out against nothing.
- **A pty has no EOF to deliver.** Its slave stays open after whatever feeds the master is spent, so a command reading to EOF never finishes. This is why the CLI asks for a tty only when stdin actually is one.
- **A sandbox reaches the proxy through a relay, not directly.** An internal network cannot reach the host on macOS, where containers live in the runtime's own VM. `docs/concessions.md` has the measurements and what would remove the need for it.
- **Egress control is off when no proxy address is configured.** That is what keeps `--dry-run` and the in-process path rendering plain container commands; neither has a daemon holding a proxy to point at.
- **A preset carries its own allowances; `Policy.Rules` is only what a person added.** Copying a preset's hosts into Rules makes switching preset a no-op, because the old preset's list comes along with it.
- **Every api sentinel needs its own gRPC code.** Decoding takes the first entry whose code matches, so two sharing one silently hands callers the wrong error with the right message, which is what makes it hard to see.
- **A character-device check is not a terminal check.** `/dev/null` is a character device. Use `term.IsTerminal`, or a redirected command will claim a TTY it doesn't have.
- **Don't drive the TUI by feeding keys to `tea.Run` in tests.** It races the program's startup and produces tests that sometimes wait for a deadline. Call `Model.Update` directly; `run_test.go` covers only that the loop starts and stops.

## Testing

Unit tests are **pure**, with no container runtime required. They cover arg-building, parsing, validation, and rendering. Keep them that way: inject dependencies like `goos` rather than reading globals.

There are two fakes, at opposite ends of the stack. `runner.Fake` replaces only the container runtime, so `direct` and `store` run for real against a temp dir. `api.NewFake` replaces the whole sandbox layer, for driving the CLI; it's required, not merely convenient, because `depguard` forbids `cmd/plbx` from importing `internal/store`.

To verify real container behavior, use `plbx --dry-run` (which needs no runtime), or `make e2e-test`, which stands an isolated daemon up against docker/OrbStack and checks egress end to end. That is the half the pure tests structurally cannot reach: whether the relay resolves the host, and whether one sandbox's lifecycle leaves another's alone. Run it on Linux as well as macOS, since the two resolve the host differently.

## Committing, versioning, releasing

- Use [Conventional Commits](https://www.conventionalcommits.org/). Always include the scope: `feat(sandbox): ...`, `chore(docs): ...`. View the git log for the scopes this project uses.
- Keep `CHANGELOG.md` up to date, per [Keep a Changelog](https://keepachangelog.com/).
- Use [Semantic Versioning](https://semver.org). Keep the version in `flake.nix` in sync with the release tags.
