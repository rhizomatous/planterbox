# 🪴 planterbox

`plbx` gives a coding agent its own long-lived container sandbox. Create one, set it up however you like, and it stays: packages, shell history, and agent state are all still there next time.

```sh
cd ~/work/myrepo
plbx run          # first time: builds a sandbox, starts Claude Code
                  # every time after: reattaches, with your setup intact
```

## prerequisites

An OCI-compatible container runtime: Docker, Podman, OrbStack, or colima. `plbx` ships none of them and autodetects whichever you have.

## install

**Nix (as system package):**

```sh
# run without installing
nix run github:rhizomatous/planterbox

# or install into your profile
nix profile install github:rhizomatous/planterbox
```

**Nix (in your project flake):**

Add planterbox as an input, then apply its overlay to get `pkgs.plbx`:

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    planterbox.url = "github:rhizomatous/planterbox";
    # optional: dedupe nixpkgs so you don't pull a second copy
    planterbox.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = { nixpkgs, planterbox, ... }:
    let
      system = "aarch64-darwin";
      pkgs = import nixpkgs {
        inherit system;
        overlays = [ planterbox.overlays.default ];
      };
    in {
      devShells.${system}.default = pkgs.mkShell {
        packages = [ pkgs.plbx ];
      };
    };
}
```

**Homebrew (macOS):**

```sh
brew install rhizomatous/tap/plbx
```

**Go:**

```sh
go install github.com/rhizomatous/planterbox/cmd/plbx@latest
```

**Binary:**

Grab a prebuilt binary for macOS or Linux from the [latest release](https://github.com/rhizomatous/planterbox/releases/latest). This _miiiiight_ not work on macOS, because codesigning isn't set up yet.

## usage

```sh
plbx                          # the dashboard: everything you have, and its load
plbx run                      # the current directory, with the default agent
plbx run codex                # a different agent
plbx run ~/work/myrepo        # somewhere else
plbx run --name scratch       # reattach by name, from anywhere

plbx run -- --dangerously-skip-permissions   # everything after -- goes to the agent
```

The first `plbx run` in a directory creates a sandbox. Every one after that reattaches to it.

```sh
plbx ls                       # what exists, and what's running
plbx inspect myrepo           # one sandbox in full
plbx stop                     # stop the one for this directory
plbx start myrepo
plbx rm myrepo                # delete it and everything in it
plbx exec myrepo bash -lc 'npm test'
plbx cp myrepo:/home/agent/notes.md ./notes.md
```

`ls` and `inspect` take `--json`. Every command that takes a sandbox name will default to the one for your current directory if you leave it out.

### the daemon

A small background process, `plbxd`, owns your sandboxes. It starts on its own the first time a command needs one, so there's nothing to set up.

```sh
plbx daemon status            # is it running, and where does it listen
plbx daemon start             # start it deliberately
plbx daemon stop              # stop it; your sandboxes keep running
```

It exists because some of what plbx does has to outlive the command that asked for it, and because host-enforced network policy lives there. Stopping it leaves every sandbox alone: they're containers in their own right, and the next command that needs a daemon starts a new one.

Two flags skip it. `--dry-run` prints what plbx would do and works on a machine with no runtime and no daemon at all; `--state-dir` names a store the running daemon doesn't own, so it runs in-process rather than quietly reading the wrong one. To run a daemon against a store of your choosing, start it yourself with `plbxd --state-dir`.

### the dashboard

Run `plbx` with no arguments and you get a dashboard instead: every sandbox, its status, and live CPU and memory for the running ones.

| key | |
| --- | --- |
| `↑` `↓` / `k` `j` | move |
| `tab` | switch between sandboxes and network |
| `i` | show the selected sandbox's details |
| `c` | create a sandbox |
| `enter` | attach the agent |
| `x` | open a shell |
| `s` | start or stop |
| `r` | remove |
| `a` | allow the selected host (network panel) |
| `d` | deny the selected host (network panel) |
| `?` | show every binding |
| `q` | quit |

`i` opens a pane under the list with what `plbx inspect` would print — image, workspaces, limits, ports, env. It follows the cursor, so moving between sandboxes moves the details with it.

Attaching leaves the dashboard and hands the terminal to the agent, the same as `plbx run` would. When the session ends you're back at the dashboard.

`tab` switches to the network panel: everything sandboxes have reached for, and what was refused. Selecting a denied host and pressing `a` allows it. The next request goes through, with nothing restarted.

Piped or run from a script, `plbx` prints the `ls` table rather than trying to draw a dashboard into something that isn't a terminal.

### ssh, and editors

```sh
plbx setup ssh
ssh myrepo.plbx
```

`plbx setup ssh` adds a managed block to `~/.ssh/config`. Everything outside its markers is left alone, and re-running it rewrites the block in place. After that `<name>.plbx` is a sandbox, to ssh and to anything that speaks ssh.

Nothing listens on a port. ssh reaches the sandbox through a `ProxyCommand` onto a socket only you can open, and a session is an exec rather than a connection, which is how it reaches a sandbox that has no route in. There are no keys to manage: the socket's permissions decide, so any key you already have is accepted and none is required.

`ssh -L 8000:127.0.0.1:3000 myrepo.plbx` reaches a service inside the sandbox without publishing it to the host. `ssh -R` is refused: it would put the sandbox on a host port it didn't ask for.

What a session may bring in from your shell is an allowlist: `TERM`, `LANG`, `LC_*`, `COLORTERM`, `TZ`. `PATH`, `NODE_OPTIONS`, `LD_PRELOAD` and anything credential-shaped stay on your machine.

## network policy

A sandbox has no route to the internet. It sits alone on a private network whose only other occupant forwards to plbx's proxy, and the proxy decides every request against a policy you set on the host. An agent that ignores `HTTP_PROXY` entirely does not get out; there is no route to ignore it with.

Policy is set with `plbx policy` and nowhere else, so a repository cannot ask for its own permissions.

```sh
plbx policy ls                    # the preset, and the rules over it
plbx policy allow example.com     # every port on that host
plbx policy allow '*.example.com' # subdomains, but not example.com itself
plbx policy deny tracker.example  # a deny beats any allow covering the same host
plbx policy check example.com     # what would happen, without connecting
plbx policy log --denied          # what has been refused
```

You pick a starting posture the first time plbx needs one:

| preset | |
| --- | --- |
| `balanced` | package registries, source hosts, and model providers. Everything else denied. The default. |
| `open` | everything reachable, nothing filtered |
| `locked-down` | nothing until you allow it, model providers included |

`plbx policy preset NAME` changes it later. Your own rules survive the change: a preset carries its own allowances rather than copying them into your policy.

Private, loopback, and link-local addresses are never reachable, whatever the policy says. No rule can grant a sandbox the host's own network, another sandbox, or a cloud metadata endpoint.

Changes take effect on the next connection. Nothing needs restarting.

### workspaces

The directory you run in is the sandbox's **primary workspace**. It's bind-mounted read-write at the same absolute path it has on your host, so a stack trace pointing at `/home/you/work/myrepo/main.go` means the same thing on both sides.

Mount more than one, with `:ro` for read-only:

```sh
plbx run ~/work/frontend ~/work/backend ~/work/design-docs:ro
```

The first path is the primary: it's the working directory, and the path `plbx run` reattaches by. Workspaces are fixed when the sandbox is created.

### resource limits, environment, and ports

```sh
plbx create --cpus 4 -m 8GiB -p 3000 -p 8080:80 -e NODE_ENV=development
```

`--cpus`, `-m`, and `-e` are create-time settings. Passing them to `plbx run` for a sandbox that already exists warns rather than silently doing nothing. Recreate the sandbox to change them.

Ports are not. They can change at any time:

```sh
plbx ports                          # what this directory's sandbox publishes
plbx ports --publish 3000
plbx ports --publish 8080:80 --publish 5432
plbx ports api --unpublish 3000
```

A change takes effect immediately on a running sandbox, and on next start otherwise.

Ports are different because a sandbox can't publish one for itself: alone on a private network, it accepts `--publish` and silently creates no mapping. So plbx runs a small forwarder alongside each sandbox that holds the host ports and carries them in, which is what you'll find in `docker ps`. The forwarder is rebuilt on every start, so ports aren't fixed the way the rest of a sandbox's definition is. It speaks TCP, so published ports are TCP.

### seeing what it would do

```sh
plbx run --dry-run
```

Prints the exact container commands and runs none of them. Works without a container runtime installed.

## how it works

```
plbx run  →  detect the OCI runtime (docker / podman / orbstack / colima)
          →  find the sandbox for this directory, or create one:
               • from the agent's base image
               • workspaces bind-mounted at their host paths
               • a named volume at /home/agent
          →  start it if it isn't running
          →  exec the agent, with your terminal attached
```

The named volume at `/home/agent` is what makes persistence work. Anything installed under your home directory (apt packages, npm globals, rustup toolchains, shell history, the agent's own state) survives `plbx stop`, and lives until `plbx rm`.

Two things deliberately don't. The agent binary lives in `/usr/local`, outside the volume, so pulling a newer image updates it. And nothing is seeded from your host: a sandbox's contents come from its image and what you run inside it.

### images

Each agent has a base image published at `ghcr.io/rhizomatous/plbx-<agent>`. They follow the same contract Docker's `sbx` uses, so third-party images are portable: Ubuntu, a non-root `agent` user at UID 1000, passwordless sudo, and proxy environment preserved across sudo.

`--image` starts from something else:

```sh
plbx create --image ghcr.io/acme/our-toolchain:latest
```

`plbx` never builds images and never commits them. Build your own starting point and point at it; [`images/`](./images/Dockerfile) is a place to start.

## what the sandbox actually protects

**Containers, not microVMs.**

- **macOS** — the container runs inside your runtime's Linux VM (Docker Desktop, OrbStack, colima). An escape lands the agent in that VM, not on your Mac.
- **Linux** — a shared-kernel boundary. A kernel privilege-escalation bug is a host compromise. Rootless Podman with user namespaces is the configuration to prefer.
- **Nested Docker** — not supported. No dind, no sysbox, no `--privileged`. Sandboxes never run privileged, and `docker build` inside one is out of scope.

Kernel-level isolation is the standing gap. If you need it today, this isn't the tool.

### your working tree is not protected

The workspace mount is read-write by design: the agent has to edit your code. It can also write files that execute on **your** machine later:

`.git/hooks/`, `.github/workflows/`, `Makefile`, `package.json` scripts, `.vscode/tasks.json`, `.claude/settings.json`.

Committing, pushing, building, or just opening the project is enough to run them. Treat a sandbox's changes the way you'd treat a pull request from a contributor you don't know.

`.git/hooks/` doesn't show up in `git diff`, so reviewing only the diff won't catch it.

`--clone` removes that exposure:

```sh
plbx create --clone ~/work/myrepo
```

Your repository mounts **read-only** and the agent works in a private clone under its own home. Nothing it does reaches your tree: not a stray edit, not a hook that would run on your machine later.

Getting the work back is a fetch. plbx adds the sandbox to your repository as a remote when it creates one, and removes it again on `plbx rm`:

```sh
git fetch plbx-myrepo
git log plbx-myrepo/some-branch
```

The clone keeps your own remotes too, minus any pointing at local paths, so `git push origin` from inside still reaches GitHub. The read-only original is there as `host`, so `git fetch host` picks up whatever you've done since.

The remote reaches the clone over plbx's ssh gateway, so run `plbx setup ssh` first. And the clone lives at `/home/agent/<repo>` rather than your repository's own path: matching paths on both sides is the cost of making the original unreachable.

### network

Everything a sandbox reaches goes through the proxy, checked against [the policy](#network-policy) you set on the host.

The limits: the guarantee rests on your runtime implementing `--internal` the way it documents, and plbx doesn't verify that it does. Egress control is off entirely under `--dry-run` and `--state-dir`, neither of which has a daemon holding a proxy.

### your credentials are inside the sandbox

An API key you pass with `-e` lives in the sandbox, and anything running there can read it. There is no secret store yet; [concessions](./docs/concessions.md) has why, and what it would take to build one.

The value is written to plbx's state directory as plaintext and baked into the container, so revoking it upstream removes it from neither. And a spec is fixed at create time, so changing a key means removing the sandbox and recreating it.

Network policy is what bounds the damage: a stolen key still has to leave through the proxy, and only to a host you've allowed.

## development

A Nix dev shell is provided: `nix develop`, or `use flake` with direnv. See [the Makefile](./Makefile) for the usual commands, and [AGENTS.md](./AGENTS.md) for conventions.
