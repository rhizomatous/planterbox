# the sandbox images, one build target per agent.
#
# a single file with a shared `base` stage, rather than a base image the others
# pull: the agent targets then have no published dependency to bootstrap, they
# share the base layer automatically, and a pull request can build every one of
# them without pushing anything.
#
#   docker build --target claude -t plbx-claude images/
#
# the relay target is the exception: it is not a sandbox image, and it builds
# from the repository root rather than from images/.
#
#   docker build --target relay -t plbx-relay -f images/Dockerfile .
#
# the base contract, matching Docker's sbx so third-party images are portable:
# Ubuntu, a non-root `agent` at UID 1000, passwordless sudo, proxy environment
# preserved across sudo, and the agent's own binary baked in.
#
# nothing here is seeded from your host. a sandbox's contents come from its
# image and from whatever you run inside it.

FROM ubuntu:24.04 AS base

# tools an agent needs to be useful at all. kept deliberately small, because
# persistence means you install the rest once and it stays.
#
# apt and npm versions are deliberately unpinned (hadolint DL3008, DL3016). A
# sandbox base wants current security updates and a current agent; pinning here
# would freeze both, and the image is rebuilt and republished rather than being
# a reproducible artifact anyone depends on byte-for-byte.
# hadolint ignore=DL3008
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      git \
      gnupg \
      jq \
      less \
      openssh-client \
      python3 \
      ripgrep \
      sudo \
      unzip \
      xz-utils \
    && rm -rf /var/lib/apt/lists/*

# node, which claude, codex, and opencode all ship as. The setup script is
# downloaded and then run, rather than piped into a shell: in a pipeline the
# shell's exit status wins, so a failed download would leave an image with no
# node in it and no error to show for it.
ARG NODE_MAJOR=22
# hadolint ignore=DL3008
RUN curl -fsSL -o /tmp/nodesource.sh "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" \
    && bash /tmp/nodesource.sh \
    && rm /tmp/nodesource.sh \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# ubuntu:24.04 ships its own UID 1000 user; drop it so `agent` can have the ID
# the contract requires. matching the host's usual first UID is what keeps
# bind-mounted workspace files writable from inside.
RUN userdel -r ubuntu 2>/dev/null || true \
    && useradd --create-home --shell /bin/bash --uid 1000 agent \
    && echo 'agent ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/agent \
    && chmod 0440 /etc/sudoers.d/agent

# the prompt names the working directory rather than spelling out the path to
# it. A workspace is bind-mounted at its absolute host path, so bash's \w has
# no ~ to shorten and spends most of a line on a prefix every path on the
# machine shares before saying which directory this is. \W is that directory's
# own name, which is the part that differs.
#
# The xterm title on the line below it keeps \w: a window title has room, and
# nothing else is competing for it.
#
# This is /home/agent, which a sandbox's volume takes over on first use — so it
# reaches new sandboxes and leaves existing ones with the prompt they already
# have. It is the agent's own file either way, and editing it is allowed.
RUN sed -i "/PS1='/s/\\\\w/\\\\W/g" /home/agent/.bashrc

# egress policy is enforced by a host-side proxy the sandbox is pointed at, so
# anything run under sudo would bypass it entirely unless the proxy variables
# survive the sudo environment.
RUN printf '%s\n' \
      'Defaults env_keep += "HTTP_PROXY HTTPS_PROXY NO_PROXY"' \
      'Defaults env_keep += "http_proxy https_proxy no_proxy"' \
      > /etc/sudoers.d/proxy \
    && chmod 0440 /etc/sudoers.d/proxy

# npm's global prefix belongs to the agent, so the packages *you* install need
# no sudo and land under $HOME, where the sandbox's volume makes them persist.
#
# The agent's own binary deliberately does not go here. /home/agent is where the
# sandbox mounts its volume, and a volume is populated from the image only the
# first time it is used — anything under it is frozen at that moment. An agent
# installed here would never be updated by a newer image; it would take deleting
# the sandbox. So agents install to /usr/local below, outside the volume, where
# pulling a new image is enough.
ENV NPM_CONFIG_PREFIX=/home/agent/.npm-global
ENV PATH=/home/agent/.npm-global/bin:/home/agent/.local/bin:/usr/local/bin:$PATH

# each target ends as `agent`; the stages below install as root first.


# the bare sandbox: no agent, just the contract. Useful for trying plbx out, for
# exercising the lifecycle without an agent in the way, and as a starting point
# when you want to install your own.
FROM base AS shell
USER agent
WORKDIR /home/agent


FROM base AS claude
# hadolint ignore=DL3016
RUN npm install -g --prefix /usr/local @anthropic-ai/claude-code && claude --version
# Claude Code refuses --dangerously-skip-permissions unless the environment
# declares itself a sandbox. It is one, and that is the entire point of it.
ENV IS_SANDBOX=1
USER agent
WORKDIR /home/agent


FROM base AS codex
# hadolint ignore=DL3016
RUN npm install -g --prefix /usr/local @openai/codex && codex --version
USER agent
WORKDIR /home/agent


FROM base AS opencode
# hadolint ignore=DL3016
RUN npm install -g --prefix /usr/local opencode-ai && opencode --version
USER agent
WORKDIR /home/agent


# the relay is not a sandbox image. It carries a sandbox's egress to the proxy
# on the host, because a sandbox on an internal network has no route there
# itself. Static, scratch-based, and holding nothing but the forwarder: it is
# on the network path, so it should have the smallest surface we can give it.
# pinned to the build machine's own architecture, with the target passed in:
# Go cross-compiles in a second, where building arm64 under emulation takes
# minutes for the same result.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS relay-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/plbx-relay ./cmd/plbx-relay

FROM scratch AS relay
COPY --from=relay-build /out/plbx-relay /plbx-relay
ENTRYPOINT ["/plbx-relay"]
