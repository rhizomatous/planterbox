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

// Every sandbox is alone on its own network, whether or not egress control is
// on. Only the isolation is conditional — the network itself is what lets the
// forwarder find the sandbox by name.
func TestEverySandboxGetsItsOwnNetwork(t *testing.T) {
	spec := api.Spec{Name: "demo", Image: "base:1"}

	for _, tc := range []struct {
		name     string
		o        *OCI
		internal bool
	}{
		{"with egress control", portsOCI(&scriptedExecutor{}), true},
		{"without", testOCI(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if net, ok := argsAfter(tc.o.CreateInvocation(spec).Args, "--network"); !ok || net != SandboxNetwork("demo") {
				t.Errorf("--network = %q, want the sandbox's own network", net)
			}
			create := tc.o.CreateNetworkInvocation("demo")
			if got := slices.Contains(create.Args, "--internal"); got != tc.internal {
				t.Errorf("--internal = %v, want %v", got, tc.internal)
			}
		})
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

// A bind conflict comes back as several lines of runtime chatter around one
// useful fact, and it lands in a warning under a sandbox that started fine.
func TestPublishFailureIsReadable(t *testing.T) {
	docker := errors.New("docker run: docker: Error response from daemon: failed to set up " +
		"container networking: driver failed programming external connectivity on endpoint " +
		"jard-demo-ports (b557c7307104): Bind for 0.0.0.0:19090 failed: port is already allocated\n" +
		"\nRun 'docker run --help' for more information (exit status 125)")

	got := publishFailure(docker, []api.Port{{Host: 19090, Sandbox: 9090}}).Error()
	if got != "host port 19090 is already in use" {
		t.Errorf("publishFailure = %q, want the port and the reason and nothing else", got)
	}
}

func TestPublishFailurePassesThroughWhatItCannotRead(t *testing.T) {
	got := publishFailure(errors.New("docker run: no such image\nsecond line"), nil).Error()
	if got != "docker run: no such image" {
		t.Errorf("publishFailure = %q, want the first line intact", got)
	}
}

// A run that fails leaves a container record behind, holding nothing.
func TestPublishClearsUpAfterAFailedForwarder(t *testing.T) {
	exec := &failOnceExecutor{err: errors.New("Bind for 0.0.0.0:19090 failed: port is already allocated")}
	err := portsOCI(exec).Publish(context.Background(), "demo", []api.Port{{Host: 19090, Sandbox: 9090}})
	if err == nil {
		t.Fatal("Publish succeeded against a taken port")
	}

	var removals int
	for _, inv := range exec.ran {
		if strings.HasPrefix(strings.Join(inv.Args, " "), "rm --force jard-demo-ports") {
			removals++
		}
	}
	// once before the run, and once after it failed.
	if removals != 2 {
		t.Errorf("removed the forwarder %d times, want the failed one cleared up too", removals)
	}
}

// failOnceExecutor fails only the `run`, as a bind conflict does.
type failOnceExecutor struct {
	scriptedExecutor
	err error
}

func (f *failOnceExecutor) Output(ctx context.Context, inv Invocation) ([]byte, error) {
	out, err := f.scriptedExecutor.Output(ctx, inv)
	if len(inv.Args) > 0 && inv.Args[0] == "run" {
		return nil, f.err
	}
	return out, err
}
