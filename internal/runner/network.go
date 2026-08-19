package runner

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
)

// Sandboxes reach the outside through one narrow path and no other.
//
// Each sandbox gets its own network, and with egress control on it is an
// internal one: no route off the host, and no route to another sandbox. The
// only other thing on it is that sandbox's own relay, which is attached to
// this network and to one ordinary network it reaches the host from, and which
// forwards to the proxy and nowhere else.
//
// The relay exists because an internal network cannot reach the host at all on
// macOS, where containers live inside the runtime's own VM. See
// docs/concessions.md.
const (
	// relayEgressNet is the ordinary network the relay reaches the host from.
	relayEgressNet = "plbx-egress"
	// relayPort is where the relay accepts a sandbox's connections.
	relayPort = 8080
	// defaultRelayImage is the published relay. It holds a single static
	// binary on an empty base.
	defaultRelayImage = "ghcr.io/rhizomatous/plbx-relay:latest"
)

// sandboxNetwork is the network a sandbox is alone on.
func sandboxNetwork(sandbox string) string { return containerPrefix + sandbox + "-net" }

// relayName is a sandbox's own relay. One per sandbox, so its lifetime is the
// sandbox's: up on start, down on stop.
func relayName(sandbox string) string { return containerPrefix + sandbox + "-relay" }

// proxyEnv is what a sandbox is told about its way out.
//
// The address is the relay's name on the sandbox's own network, which the
// runtime's embedded DNS resolves without any egress of its own. NO_PROXY keeps
// loopback direct, so a service the agent runs and then curls does not take a
// trip through the proxy to reach itself.
//
// The sandbox's name rides along as a proxy credential, which is how the
// connection log can say who asked. It is a label and not a secret: the policy
// is host-side and identical for every sandbox, so the name grants nothing and
// proves nothing.
func proxyEnv(sandbox string) map[string]string {
	addr := "http://" + url.UserPassword(sandbox, "x").String() + "@" + relayName(sandbox) + ":" + strconv.Itoa(relayPort)
	return map[string]string{
		"HTTP_PROXY":  addr,
		"HTTPS_PROXY": addr,
		"http_proxy":  addr,
		"https_proxy": addr,
		"NO_PROXY":    "localhost,127.0.0.1,::1",
		"no_proxy":    "localhost,127.0.0.1,::1",
	}
}

// createNetworkInvocation renders the command creating a sandbox's network.
//
// Every sandbox gets one, whether or not egress control is on. Only the
// --internal flag turns on the isolation; the network itself is what lets the
// port forwarder find the sandbox by name, which the runtime's embedded DNS
// does on a user-defined network and not on the default bridge.
func (o *OCI) createNetworkInvocation(sandbox string) Invocation {
	args := []string{"network", "create"}
	if o.egressUpstream != "" {
		args = append(args, "--internal")
	}
	return o.invoke(append(args, "--label", "plbx.sandbox="+sandbox, sandboxNetwork(sandbox))...)
}

// removeNetworkInvocation renders the command removing it.
func (o *OCI) removeNetworkInvocation(sandbox string) Invocation {
	return o.invoke("network", "rm", sandboxNetwork(sandbox))
}

// ensureNetwork creates a sandbox's network, tolerating one that already
// exists: a sandbox being recreated keeps the same name.
func (o *OCI) ensureNetwork(ctx context.Context, sandbox string) error {
	return o.createNetwork(ctx, o.createNetworkInvocation(sandbox))
}

// removeNetwork drops a sandbox's network, tolerating one already gone.
//
// Everything on it belongs to this sandbox and has already been removed by the
// time this runs; a runtime refuses to remove a network that still has
// endpoints.
func (o *OCI) removeNetwork(ctx context.Context, sandbox string) error {
	if _, err := o.exec.Output(ctx, o.removeNetworkInvocation(sandbox)); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// ensureRelay gives a sandbox a fresh relay on its own network.
//
// Replaced rather than reused on every start: the upstream address is baked
// into its arguments, so a daemon that moved would otherwise leave a relay
// dialling where it used to be.
func (o *OCI) ensureRelay(ctx context.Context, sandbox, image, upstream string) error {
	if err := o.ensureEgressNetwork(ctx); err != nil {
		return err
	}
	if err := o.removeRelay(ctx, sandbox); err != nil {
		return err
	}
	if _, err := o.exec.Output(ctx, o.relayInvocation(sandbox, image, upstream)); err != nil {
		return err
	}
	// the relay reaches the host across its own network and the sandbox across
	// this one; a run can only join the first.
	if err := o.connectNetwork(ctx, sandboxNetwork(sandbox), relayName(sandbox)); err != nil {
		return fmt.Errorf("attaching the relay to %s: %w", sandboxNetwork(sandbox), err)
	}
	return nil
}

// removeRelay takes a sandbox's relay away, tolerating one already gone.
func (o *OCI) removeRelay(ctx context.Context, sandbox string) error {
	if _, err := o.exec.Output(ctx, o.invoke("rm", "--force", relayName(sandbox))); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func (o *OCI) ensureEgressNetwork(ctx context.Context) error {
	return o.createNetwork(ctx, o.sharedNetworkInvocation(relayEgressNet))
}

// relayInvocation renders the command that runs the relay.
func (o *OCI) relayInvocation(sandbox, image, upstream string) Invocation {
	args := []string{
		"run", "--detach",
		"--name", relayName(sandbox),
		"--label", "plbx.relay=true",
		"--restart", "unless-stopped",
		"--network", relayEgressNet,
	}
	args = append(args, hostGatewayArgs(o.goos, upstream)...)
	return o.invoke(append(args,
		image,
		"-listen", ":"+strconv.Itoa(relayPort),
		"-upstream", upstream,
	)...)
}

// hostGatewayArgs maps the upstream's hostname onto the machine the runtime
// runs on, which only Linux needs.
//
// host.docker.internal is a Docker Desktop convenience. Docker Engine on Linux
// does not publish it, so without this the relay cannot resolve the proxy and
// every sandbox loses egress. host-gateway is the machine itself there, because
// nothing sits between a container and the host.
//
// Deliberately not done elsewhere: on macOS the container is inside the
// runtime's own VM, where docs/concessions.md measured the gateway to be a
// bridge inside that VM rather than the host. Writing this into /etc/hosts
// would take precedence over the address the runtime already publishes and
// point the relay at something measured not to answer.
func hostGatewayArgs(goos, upstream string) []string {
	if goos != "linux" {
		return nil
	}
	host, _, err := net.SplitHostPort(upstream)
	if err != nil {
		return nil
	}
	// an address needs no mapping, and mapping one would be nonsense
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	return []string{"--add-host", host + ":host-gateway"}
}

// isAlreadyExists reports whether err is a runtime refusing to create
// something twice. docker says "already exists", podman "already used".
func isAlreadyExists(err error) bool {
	return runtimeSays(err, "already exists", "already used")
}

// isAlreadyConnected reports whether err is a runtime refusing to attach a
// container to a network it is already on.
func isAlreadyConnected(err error) bool {
	return runtimeSays(err, "already exists in network")
}

// createNetwork creates a network, tolerating one that already exists.
func (o *OCI) createNetwork(ctx context.Context, inv Invocation) error {
	if _, err := o.exec.Output(ctx, inv); err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

// sharedNetworkInvocation renders the command creating one of the two networks
// that are not a sandbox's own. Neither is internal: the relay and the port
// forwarder reach the host across them.
func (o *OCI) sharedNetworkInvocation(name string) Invocation {
	return o.invoke("network", "create", name)
}

// connectNetwork attaches a container to a network, tolerating one that is
// already attached.
func (o *OCI) connectNetwork(ctx context.Context, network, container string) error {
	_, err := o.exec.Output(ctx, o.invoke("network", "connect", network, container))
	if err != nil && !isAlreadyExists(err) && !isAlreadyConnected(err) {
		return err
	}
	return nil
}
