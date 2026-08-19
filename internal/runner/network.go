package runner

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Sandboxes reach the outside through one narrow path and no other.
//
// Each sandbox gets its own network, and with egress control on it is an
// internal one: no route off the host, and no route to another sandbox. The
// only other thing on it is the relay, which is attached to every sandbox
// network and to one ordinary network of its own, and which forwards to the
// proxy on the host and nowhere else.
//
// The relay exists because an internal network cannot reach the host at all on
// macOS, where containers live inside the runtime's own VM. See
// docs/concessions.md.
const (
	// relayName is the shared relay container. One serves every sandbox: it
	// forwards to a single fixed address, so it grants nothing that being on
	// its network does not already grant.
	relayName = "plbx-relay"
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
	addr := "http://" + url.UserPassword(sandbox, "x").String() + "@" + relayName + ":" + strconv.Itoa(relayPort)
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
// The relay is detached first. It is attached to every sandbox's network, and
// a runtime refuses to remove a network that still has endpoints on it. Without
// this, removing any sandbox fails once egress control is on, and fails after
// the container is already gone.
func (o *OCI) removeNetwork(ctx context.Context, sandbox string) error {
	_, err := o.exec.Output(ctx, o.invoke("network", "disconnect", "--force",
		sandboxNetwork(sandbox), relayName))
	if err != nil && !isNotFound(err) && !isNotConnected(err) {
		return err
	}
	if _, err := o.exec.Output(ctx, o.removeNetworkInvocation(sandbox)); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// ensureRelay brings the relay up if it is not already, and attaches it to a
// sandbox's network.
//
// Called on every start rather than once: the relay is shared, and a sandbox
// starting after the relay was removed by hand would otherwise have no way out
// with nothing to say about why.
func (o *OCI) ensureRelay(ctx context.Context, sandbox, image, upstream string) error {
	if err := o.ensureEgressNetwork(ctx); err != nil {
		return err
	}

	running, err := o.relayRunning(ctx)
	if err != nil {
		return err
	}
	if !running {
		if err := o.startRelay(ctx, image, upstream); err != nil {
			return err
		}
	}

	if err := o.connectNetwork(ctx, sandboxNetwork(sandbox), relayName); err != nil {
		return fmt.Errorf("attaching the relay to %s: %w", sandboxNetwork(sandbox), err)
	}
	return nil
}

func (o *OCI) ensureEgressNetwork(ctx context.Context) error {
	return o.createNetwork(ctx, o.sharedNetworkInvocation(relayEgressNet))
}

// relayRunning reports whether the relay container is up.
func (o *OCI) relayRunning(ctx context.Context) (bool, error) {
	out, err := o.exec.Output(ctx, o.invoke("inspect", "--type", "container",
		"--format", "{{.State.Running}}", relayName))
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// startRelay removes any stopped relay and runs a fresh one.
//
// Replacing rather than restarting keeps the upstream address current: it is
// baked into the container's arguments, and a daemon that moved would
// otherwise be talking to a relay still pointed at where it used to be.
func (o *OCI) startRelay(ctx context.Context, image, upstream string) error {
	if _, err := o.exec.Output(ctx, o.invoke("rm", "--force", relayName)); err != nil && !isNotFound(err) {
		return err
	}
	_, err := o.exec.Output(ctx, o.relayInvocation(image, upstream))
	return err
}

// relayInvocation renders the command that runs the relay.
func (o *OCI) relayInvocation(image, upstream string) Invocation {
	return o.invoke(
		"run", "--detach",
		"--name", relayName,
		"--label", "plbx.relay=true",
		"--restart", "unless-stopped",
		"--network", relayEgressNet,
		image,
		"-listen", ":"+strconv.Itoa(relayPort),
		"-upstream", upstream,
	)
}

// isAlreadyExists reports whether err is a runtime refusing to create
// something twice. docker says "already exists", podman "already used".
func isAlreadyExists(err error) bool {
	return runtimeSays(err, "already exists", "already used")
}

// isNotConnected reports whether err is a runtime declining to detach a
// container from a network it was never on.
func isNotConnected(err error) bool {
	return runtimeSays(err, "is not connected", "not connected to network")
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
