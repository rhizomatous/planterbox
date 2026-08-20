package runner

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rhizomatous/planterbox/internal/api"
)

// A sandbox cannot publish its own ports.
//
// It is alone on an internal network, which leaves it with no route out and,
// in the other direction, unreachable from the host. A runtime does not report
// that as a conflict: `--publish` on a container attached to an internal
// network exits zero, starts the container, and creates no mapping.
// Measured against OrbStack, the port simply does not appear.
//
// So ingress takes the shape egress already has. A forwarder container sits on
// both sides: it publishes the host ports for real, because it is on an
// ordinary network, and carries each one to the sandbox over the internal one.
const (
	// portsNet is the ordinary network a forwarder publishes from.
	//
	// Separate from the relay's own: the two carry traffic in opposite
	// directions and nothing belongs on both. Any non-internal network grants
	// its members a route out, which a forwarder has no use for: it dials only
	// the addresses on its command line, and holds no policy that could be
	// talked out of it.
	portsNet = "plbx-ports"
)

// PortsContainer is the forwarder publishing a sandbox's ports.
func PortsContainer(sandbox string) string { return containerPrefix + sandbox + "-ports" }

// relayRef is the image both forwarders run, the published one unless
// overridden.
func (o *OCI) relayRef() string {
	if o.relayImage != "" {
		return o.relayImage
	}
	return DefaultRelayImage
}

// PortsInvocation renders the command running a sandbox's port forwarder. It is
// pure, so the mapping it builds is testable without a runtime.
func (o *OCI) PortsInvocation(sandbox string, ports []api.Port) Invocation {
	args := []string{
		"run", "--detach",
		"--name", PortsContainer(sandbox),
		"--label", "plbx.sandbox=" + sandbox,
		"--label", "plbx.ports=true",
		"--network", portsNet,
	}
	for _, p := range ports {
		args = append(args, "--publish", p.Address())
	}
	args = append(args, o.relayRef())

	// the forwarder listens on the sandbox-side port number, since that is
	// what its own published mapping delivers to, and carries it to the same
	// port on the sandbox.
	for _, p := range ports {
		port := strconv.Itoa(p.Sandbox)
		args = append(args, "-forward", ":"+port+"="+ContainerName(sandbox)+":"+port)
	}
	return o.invoke(args...)
}

// Publish makes a sandbox's ports reachable from the host, replacing whatever
// was published for it before.
//
// The forwarder is replaced rather than adjusted. Its mappings are fixed when
// it starts, the same constraint that stops a sandbox publishing for itself.
// A forwarder holds nothing, so replacing one costs only the connections it
// was carrying.
func (o *OCI) Publish(ctx context.Context, sandbox string, ports []api.Port) error {
	for _, p := range ports {
		if strings.EqualFold(p.Proto, "udp") {
			return fmt.Errorf("cannot publish %d:%d/udp: the forwarder that carries ports "+
				"into a sandbox is TCP-only", p.Host, p.Sandbox)
		}
	}
	if err := o.Unpublish(ctx, sandbox); err != nil {
		return err
	}
	if len(ports) == 0 {
		return nil
	}

	if err := o.ensurePortsNetwork(ctx); err != nil {
		return err
	}
	if _, err := o.exec.Output(ctx, o.PortsInvocation(sandbox, ports)); err != nil {
		// a run that fails partway still leaves the container record behind,
		// holding nothing and cluttering `docker ps -a`.
		_ = o.Unpublish(ctx, sandbox)
		return publishFailure(err, ports)
	}
	// being published is only half of it; reaching the sandbox means being on
	// its network too.
	_, err := o.exec.Output(ctx, o.invoke("network", "connect",
		SandboxNetwork(sandbox), PortsContainer(sandbox)))
	if err != nil && !isAlreadyExists(err) && !isAlreadyConnected(err) {
		return fmt.Errorf("attaching the port forwarder to %s: %w", SandboxNetwork(sandbox), err)
	}
	return nil
}

// Unpublish takes a sandbox's ports back off the host, tolerating one that was
// never published.
//
// It runs on stop as well as on removal. A forwarder left behind would hold
// its host ports bound against a sandbox that is no longer listening, which
// denies them to whatever wants them next and answers on them meanwhile.
func (o *OCI) Unpublish(ctx context.Context, sandbox string) error {
	_, err := o.exec.Output(ctx, o.invoke("rm", "--force", PortsContainer(sandbox)))
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("removing the port forwarder for %s: %w", sandbox, err)
	}
	return nil
}

func (o *OCI) ensurePortsNetwork(ctx context.Context) error {
	_, err := o.exec.Output(ctx, o.invoke("network", "create", portsNet))
	if err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

// bindConflict matches a runtime reporting a host port it could not take.
// Docker and podman word the rest of the failure differently; both say this.
var bindConflict = regexp.MustCompile(`[Bb]ind for (\S+?) failed`)

// publishFailure trims a runtime's answer down to the part worth reading.
//
// A bind conflict arrives as the daemon's message, the endpoint's id, a blank
// line, and a suggestion to run `docker run --help`. None of that belongs in
// front of someone who published a port that was already taken, and the whole
// of it lands in a warning printed under a sandbox that started fine.
func publishFailure(err error, ports []api.Port) error {
	first, _, _ := strings.Cut(err.Error(), "\n")
	first = strings.TrimSpace(first)

	if m := bindConflict.FindStringSubmatch(first); m != nil {
		// the address carries the port, and the host part is whatever the
		// runtime binds on rather than anything the user asked for.
		addr := m[1]
		if i := strings.LastIndex(addr, ":"); i >= 0 {
			addr = addr[i+1:]
		}
		return fmt.Errorf("host port %s is already in use", addr)
	}
	if strings.Contains(first, "already allocated") || strings.Contains(first, "address already in use") {
		return fmt.Errorf("a host port is already in use, of %s", portList(ports))
	}
	return errors.New(first)
}

// portList names the host ports a publish asked for.
func portList(ports []api.Port) string {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		out = append(out, strconv.Itoa(p.Host))
	}
	return strings.Join(out, ", ")
}
