package runner

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/rhizomatous/jardiniere/internal/api"
)

// withEgress is the configuration every sandbox actually runs under: a private
// network, and therefore no ability to publish its own ports.
func portsOCI(exec Executor) *OCI {
	return testOCI(WithExecutor(exec), WithEgress("host.docker.internal:47821", ""))
}

func TestPortsInvocationPublishesAndForwardsEachPort(t *testing.T) {
	inv := portsOCI(&scriptedExecutor{}).PortsInvocation("demo", []api.Port{
		{Host: 3000, Sandbox: 3000},
		{Host: 8080, Sandbox: 80},
	})

	if inv.Args[0] != "run" {
		t.Errorf("args[0] = %q, want run", inv.Args[0])
	}
	if name, _ := argsAfter(inv.Args, "--name"); name != "jard-demo-ports" {
		t.Errorf("--name = %q, want jard-demo-ports", name)
	}
	if got := allAfter(inv.Args, "--publish"); !slices.Equal(got, []string{"3000:3000", "8080:80"}) {
		t.Errorf("--publish = %v, want the host mappings", got)
	}
	// the forwarder listens on the sandbox-side port, which is where its own
	// published mapping delivers, and carries it to the sandbox by name.
	want := []string{":3000=jard-demo:3000", ":80=jard-demo:80"}
	if got := allAfter(inv.Args, "-forward"); !slices.Equal(got, want) {
		t.Errorf("-forward = %v, want %v", got, want)
	}
	if net, _ := argsAfter(inv.Args, "--network"); net != portsNet {
		t.Errorf("--network = %q, want the ordinary network %q — an internal one cannot publish", net, portsNet)
	}
}

// The image is the relay's: one binary serves both directions.
func TestPortsInvocationRunsTheRelayImage(t *testing.T) {
	inv := portsOCI(&scriptedExecutor{}).PortsInvocation("demo", []api.Port{{Host: 1, Sandbox: 1}})
	if !slices.Contains(inv.Args, DefaultRelayImage) {
		t.Errorf("args = %v, want the relay image %s", inv.Args, DefaultRelayImage)
	}
}

// A sandbox on an internal network must not carry --publish itself: the
// runtime accepts it, creates no mapping, and says nothing.
func TestCreateDoesNotPublishOnAnInternalNetwork(t *testing.T) {
	spec := api.Spec{Name: "demo", Image: "base:1", Ports: []api.Port{{Host: 3000, Sandbox: 3000}}}

	withEgress := portsOCI(&scriptedExecutor{}).CreateInvocation(spec)
	if got := allAfter(withEgress.Args, "--publish"); len(got) != 0 {
		t.Errorf("--publish = %v, want none: an internal network drops it silently", got)
	}
	if net, ok := argsAfter(withEgress.Args, "--network"); !ok || net != SandboxNetwork("demo") {
		t.Errorf("--network = %q, want the sandbox's private network", net)
	}

	// with egress off there is no internal network, so the sandbox publishes
	// for itself. This is the --dry-run and in-process path.
	direct := testOCI().CreateInvocation(spec)
	if got := allAfter(direct.Args, "--publish"); !slices.Equal(got, []string{"3000:3000"}) {
		t.Errorf("--publish = %v, want the sandbox publishing directly", got)
	}
}

func TestPublishIsInertWithoutEgress(t *testing.T) {
	exec := &scriptedExecutor{}
	o := testOCI(WithExecutor(exec))

	if err := o.Publish(context.Background(), "demo", []api.Port{{Host: 1, Sandbox: 1}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := o.Unpublish(context.Background(), "demo"); err != nil {
		t.Fatalf("Unpublish: %v", err)
	}
	if len(exec.ran) != 0 {
		t.Errorf("ran %v, want nothing: the sandbox published for itself at create", exec.ran)
	}
}

func TestPublishRefusesUDP(t *testing.T) {
	err := portsOCI(&scriptedExecutor{}).Publish(context.Background(), "demo",
		[]api.Port{{Host: 53, Sandbox: 53, Proto: "udp"}})
	if err == nil {
		t.Fatal("Publish accepted a udp port; the forwarder carrying it is TCP-only")
	}
	if !strings.Contains(err.Error(), "udp") {
		t.Errorf("error = %q, want it to name the protocol it cannot carry", err)
	}
}

// Publishing replaces: the forwarder's mappings are fixed when it starts.
func TestPublishReplacesTheForwarder(t *testing.T) {
	exec := &scriptedExecutor{}
	o := portsOCI(exec)

	if err := o.Publish(context.Background(), "demo", []api.Port{{Host: 3000, Sandbox: 3000}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var removed, ran bool
	for _, inv := range exec.ran {
		line := strings.Join(inv.Args, " ")
		switch {
		case strings.HasPrefix(line, "rm --force jard-demo-ports"):
			removed = true
		case strings.HasPrefix(line, "run --detach --name jard-demo-ports"):
			if !removed {
				t.Error("the forwarder was started before the old one was removed")
			}
			ran = true
		}
	}
	if !removed || !ran {
		t.Errorf("ran %v, want the old forwarder removed and a new one started", exec.ran)
	}
}

// Publishing nothing still clears what was published before.
func TestPublishNoPortsOnlyClears(t *testing.T) {
	exec := &scriptedExecutor{}
	if err := portsOCI(exec).Publish(context.Background(), "demo", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(exec.ran) != 1 || !strings.HasPrefix(strings.Join(exec.ran[0].Args, " "), "rm --force") {
		t.Errorf("ran %v, want only the removal", exec.ran)
	}
}

// The forwarder sits on the sandbox's network, so it has to come off before
// that network can be removed — the same ordering the relay needs.
func TestRemoveDropsTheForwarderBeforeTheNetwork(t *testing.T) {
	exec := &scriptedExecutor{}
	if err := portsOCI(exec).Remove(context.Background(), "jard-demo", "demo", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	forwarder, network := -1, -1
	for i, inv := range exec.ran {
		line := strings.Join(inv.Args, " ")
		if strings.HasPrefix(line, "rm --force jard-demo-ports") {
			forwarder = i
		}
		if strings.HasPrefix(line, "network rm "+SandboxNetwork("demo")) {
			network = i
		}
	}
	switch {
	case forwarder < 0:
		t.Errorf("ran %v, want the port forwarder removed", exec.ran)
	case network < 0:
		t.Errorf("ran %v, want the sandbox network removed", exec.ran)
	case forwarder > network:
		t.Error("the network was removed before the forwarder attached to it")
	}
}

// A forwarder that was never started is not an error to remove.
func TestUnpublishToleratesNothingPublished(t *testing.T) {
	exec := &scriptedExecutor{err: errors.New("Error: No such container: jard-demo-ports")}
	if err := portsOCI(exec).Unpublish(context.Background(), "demo"); err != nil {
		t.Errorf("Unpublish: %v, want a missing forwarder to be tolerated", err)
	}
}
