# concessions

things jardinière wanted to do, couldn't, and does differently instead.

each entry records what was intended, what turned out to be true, what we do
about it, and what would have to change for the original plan to become
possible again. the point is that a later revisit starts from evidence rather
than from re-running the same experiments.

this is not a list of bugs or of work not yet done — `docs/next/plan.md` covers
those. it is a list of places where a deliberate, defensible choice cost us
something real.

## host-side egress proxy, under a bring-your-own runtime

**status:** live, since phase 3.
**forced by:** "bring your own runtime" in `docs/next/plan.md`.

### what we wanted

The plan's phase 3 says:

> sandboxes join an internal network with no route out; the proxy is the only
> egress

with the proxy daemon-resident, alongside the policy engine, the connection
log, and — at the time — an OS keychain the proxy would inject credentials
from. The plan claims this gets us network parity with `sbx`:

> **network** — parity with `sbx` is achievable, because policy enforcement
> lives in the host proxy rather than the isolation layer

### what is actually true

The first half works. The second half does not, on macOS.

Measured against OrbStack (Docker 29.4.0) on macOS, with a listener bound on
the host and a container attached to a `docker network create --internal`
network:

| from an `--internal` network | result |
| --- | --- |
| resolve and fetch `example.com` | fails — DNS itself does not resolve |
| connect to the bridge gateway (`192.168.117.1`) | `Connection refused` |
| resolve `host.docker.internal` | no answer |
| connect to `host.docker.internal`'s address (`0.250.250.254`) | `Network unreachable` |

The same address is reachable from a non-internal network, so the route exists
and `--internal` is what removes it.

Two things follow. The isolation half is genuine: an internal network really
does leave a sandbox with no way out, DNS included. But a host-resident proxy
is not reachable from inside one, so it cannot be the sandbox's egress.

`Connection refused` on the gateway is the tell. The packet arrived somewhere
and was actively refused, which means the gateway is not the macOS host at all
— it is a bridge interface inside the Linux VM that OrbStack runs containers
in. Our daemon is a macOS process. There is a VM boundary between the two that
`--internal` cuts.

### why `sbx` does not have this problem

`sbx` is genuinely host-side-proxy, per `docs/next/research-docker-sandboxes.md`:

> `sandboxd` — host daemon. owns sandbox state, lifecycle, the egress proxy [...]

It gets away with it because each of its sandboxes is a **microVM it creates
itself**. It owns the hypervisor and therefore the virtual NIC, so it can hand
a guest a network whose only route is a host-side listener. Nothing sits in
between.

We are a guest in someone else's VM. Docker Desktop and OrbStack own the Linux
VM our containers live in, and we do not get to configure its routing. This is
the direct cost of not shipping a runtime — which is a requirement, not an
oversight. `sbx` requires its own hypervisor and is Apple-silicon-only on macOS
and KVM-only on Linux; we deliberately run on whatever is already installed.

### what we do instead

The proxy, the policy engine, and the connection log all stay in the daemon,
where the plan wants them. A small dual-homed relay container bridges the gap:

```
  sandbox              relay                    host
  (internal only)      (internal + bridge)      (jardd)

    agent ─────────────► forwards only ────────► proxy ────► internet
                         to the host proxy         │
                                                   └─ policy + connection log
```

The sandbox reaches nothing but the relay. The relay reaches nothing but the
host proxy. Egress enforcement still lives in one host-side place.

Keeping the proxy on the host is not merely tidiness, even though the thing
that most needed it has since been deferred. Stored credentials want to be
injected at the proxy so that raw values never enter the sandbox, and a
container cannot read the macOS keychain — so a proxy pushed into a container
would have to be dragged back out to build them. See
[credentials live inside the sandbox](#credentials-live-inside-the-sandbox).

### what it costs

- **an extra moving part.** A relay container and a small image to publish and
  keep current, neither of which the plan accounted for.
- **a hop.** Every request crosses the VM boundary twice rather than once.
  Immaterial next to the network round trip, but it is not nothing.
- **a second thing that can be down.** "The daemon is running" is no longer
  sufficient for a sandbox to have egress.
- **it is not the isolation layer doing the work.** The guarantee rests on
  docker's network configuration being what we asked for. A runtime that
  implements `--internal` loosely weakens it, and we would not notice.
- **the wall has a second side, and a third.** Nothing host-side can dial a
  sandbox either, so two other things that would ordinarily be a `net.Dial`
  are not. A published port is carried by a forwarder container attached to
  both networks (`internal/runner/ports.go`), because a runtime accepts
  `--publish` on an internal network and silently creates no mapping. And
  `ssh -L` is served by running the dial *inside* the sandbox as an exec, with
  bash's `/dev/tcp` opening the socket (`internal/sshd/forward.go`). Both work,
  and both would collapse into an ordinary dial if the daemon ever had a route.

### what would let us revisit

The **microVM backend**, already in the plan's deferred list:

> per-sandbox VM with its own kernel, as `sbx` has. via Lima, or natively on
> vz + Firecracker

Owning the hypervisor is exactly what removes this concession. A sandbox in a
VM we created can be given a NIC routed straight at the host proxy, the relay
disappears, and the topology becomes the one phase 3 originally described.

Worth checking at that point, and not before: whether Linux can already skip
the relay today. There the bridge gateway is a real host interface rather than
something inside a VM, so an internal network can very likely reach a host
proxy directly. We use one topology on both platforms because two is twice the
surface to get wrong, and bugs collect in whichever half is used less — but if
the Linux path ever needs to be native, it is probably a short change.

## credentials live inside the sandbox

**status:** deferred, at phase 4.
**forced by:** no stable macOS signing identity, and the cost of TLS
interception weighed against what storage alone would buy.

### what we wanted

The plan's phase 4 was credentials:

> OS keychain storage (`jard secret set|ls|rm|import`) and header injection at
> the proxy, so raw values never enter the sandbox

with a done-when that named the point of the exercise:

> a sandbox can reach the Anthropic API with no `ANTHROPIC_API_KEY` anywhere
> inside it

The threat is a compromised agent — a prompt injection, a malicious dependency
— reading your API key and using it or exfiltrating it. A key the sandbox never
holds cannot be taken from it.

### what is actually true

Four things, which together turn one phase into a much larger one.

**Signing is a hard prerequisite, and it is not ours to satisfy.** The plan
already knew this and put it first: keychain ACLs bind to a binary's Designated
Requirement, and an ad-hoc signature has no stable one, so every jard upgrade
invalidates every ACL and re-prompts for every stored secret. Getting a stable
DR means an Apple Developer Program membership and a Developer ID Application
certificate. That is a purchase and an identity, not a piece of work.

**cgo is off the table, so the keychain is awkward to reach at all.**
`.goreleaser.yaml` sets `CGO_ENABLED=0` and cross-compiles darwin binaries on
an ubuntu runner, so the Security framework is not linkable. What is left is
shelling out to `/usr/bin/security`, which takes the secret on argv where other
processes running as you can see it, or hand-rolled FFI that CI — also ubuntu —
could never exercise.

**The daemon needs unattended reads, which undoes most of what the keychain is
for.** `jardd` injects without a human present, so any ACL that prompts is
fatal to it. Unattended means a permissive ACL, and a permissive ACL means any
process running as you can read the item without being asked. What survives is
encryption at rest and unavailability while the machine is locked. Real, but a
good deal less than "the OS protects your keys" suggests.

**Injection into HTTPS means intercepting it.** The headline case,
`api.anthropic.com`, is CONNECT-tunnelled — the proxy copies bytes it cannot
read. Adding a header means terminating TLS: a jard CA, leaf certificates
minted per host, and then getting the sandbox to trust it. That last part is
the expensive one. It needs a bind-mounted root, `update-ca-certificates` as
root on every start, and `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`,
`CURL_CA_BUNDLE` and `GIT_SSL_CAINFO` besides, because node and python read
their own bundles rather than the system store. It has to work against whatever
`--image` was given. Almost none of it can be tested without a live runtime.

And the fifth thing, which is why the phase was deferred rather than trimmed:
**storage without injection does not address the threat.** A key resolved from
the keychain and handed to the agent's environment is worth about what
`-e ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY` is worth against an agent that has
already been compromised. It is an ergonomics feature. Shipping it under the
heading "credentials" would have implied a protection that was not there.

### what we do instead

Nothing. `-e NAME=VALUE` at create time remains the way to give a sandbox a
key, and the README says plainly that the sandbox holds it.

Egress policy is the mitigation that is actually in place: a stolen key still
has to leave through the proxy, and it can only reach hosts the policy allows.
That bounds exfiltration. It does not prevent the key being used from inside
the sandbox against a host you have already allowed.

### what it costs

- **the sandbox holds live credentials.** Anything running in it can read them
  out of its own environment. This is the gap the phase existed to close, and
  it is open.
- **a key cannot be rotated.** `-e` lands in `Spec.Env`, and a spec is fixed at
  create time. Changing a key means `jard rm` and recreating the sandbox,
  discarding everything it persists — which is the entire reason the sandbox is
  persistent.
- **it is plaintext on disk, and stays there.** The store writes the spec as
  JSON under the state directory, and the runtime bakes it into the container.
  Revoking the key upstream removes it from neither.
- **one copy per sandbox.** Five sandboxes are five places to update.
- **no registry credentials at all.** Pulling from a private registry has no
  mechanism, which is unrelated to any of the above — those are used host-side
  by the runner and never enter a sandbox.

### what would let us revisit

A **Developer ID certificate**, which unblocks the keychain, and with it the
`macOS signing & notarization` entry in the plan's deferred list. Everything
else follows from having somewhere trustworthy to put a secret.

Two things worth knowing before starting again, so the next attempt does not
re-derive them:

**A useful subset lands well before interception does.** Resolving a secret at
attach time and passing it in `ExecRequest.Env` — rather than freezing it into
the spec at create — fixes rotation, the plaintext on disk, and the one-copy-
per-sandbox problem, with no CA and no changes to the images. It makes no
security claim, and should not be sold as one, but it removes three of the five
costs above. Registry pull credentials are entirely unblocked by it, since they
never reach a sandbox in the first place.

**Base-URL redirection is much cheaper than interception, for the model APIs
specifically.** Handing the sandbox `ANTHROPIC_BASE_URL` pointed at the relay
means the request arrives at the proxy as plain HTTP, in the absolute-URI form
`serveHTTP` already requires; the proxy adds the header and originates TLS
itself. No CA, no cert trust, no image changes. It reaches the phase's stated
done-when for Anthropic at a fraction of the cost. Its limits are real, and are
why it is not a general answer: it covers only services with a configurable
base URL, so `git push` and `npm publish` still need a token in the sandbox,
and an agent that cannot read the key can still spend it by using the endpoint.
