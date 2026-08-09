package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rhizomatous/jardiniere/internal/api"
)

func testOCI(opts ...Option) *OCI {
	return NewOCI(Runtime{Name: "docker", Path: "/usr/bin/docker"}, opts...)
}

// argsAfter returns the value following flag, and whether it was present.
func argsAfter(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// allAfter returns every value following each occurrence of flag.
func allAfter(args []string, flag string) []string {
	var out []string
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			out = append(out, args[i+1])
		}
	}
	return out
}

func TestCreateInvocationCoreShape(t *testing.T) {
	inv := testOCI().CreateInvocation(api.Spec{Name: "demo", Image: "base:1"})

	if inv.Path != "/usr/bin/docker" {
		t.Errorf("path = %q, want the detected runtime binary", inv.Path)
	}
	if inv.Args[0] != "create" {
		t.Errorf("args[0] = %q, want create — a sandbox is built, not run", inv.Args[0])
	}
	if name, _ := argsAfter(inv.Args, "--name"); name != "jard-demo" {
		t.Errorf("--name = %q, want jard-demo", name)
	}
	if vol, _ := argsAfter(inv.Args, "--volume"); vol != "jard-demo-home:/home/agent" {
		t.Errorf("--volume = %q, want the home volume that makes a sandbox persistent", vol)
	}
	// image then the idle command, in that order, at the tail.
	tail := inv.Args[len(inv.Args)-3:]
	if tail[0] != "base:1" || tail[1] != "sleep" || tail[2] != "infinity" {
		t.Errorf("tail = %v, want the image followed by the idle command", tail)
	}
}

func TestCreateInvocationNeverPrivileged(t *testing.T) {
	inv := testOCI().CreateInvocation(api.Spec{Name: "demo", Image: "base:1"})
	for _, a := range inv.Args {
		if a == "--privileged" {
			t.Fatal("sandboxes must never run privileged")
		}
	}
}

func TestCreateInvocationWorkspacesBindAtHostPaths(t *testing.T) {
	inv := testOCI().CreateInvocation(api.Spec{
		Name:  "demo",
		Image: "base:1",
		Workspaces: []api.Workspace{
			{Host: "/home/viv/project"},
			{Host: "/home/viv/shared", ReadOnly: true},
		},
	})

	vols := allAfter(inv.Args, "--volume")
	want := []string{
		"jard-demo-home:/home/agent",
		"/home/viv/project:/home/viv/project",
		"/home/viv/shared:/home/viv/shared:ro",
	}
	if len(vols) != len(want) {
		t.Fatalf("volumes = %v, want %v", vols, want)
	}
	for i := range want {
		if vols[i] != want[i] {
			t.Errorf("volume %d = %q, want %q", i, vols[i], want[i])
		}
	}
	if wd, _ := argsAfter(inv.Args, "--workdir"); wd != "/home/viv/project" {
		t.Errorf("--workdir = %q, want the primary workspace", wd)
	}
}

func TestCreateInvocationResources(t *testing.T) {
	inv := testOCI().CreateInvocation(api.Spec{
		Name:      "demo",
		Image:     "base:1",
		Resources: api.Resources{CPUs: 2.5, Memory: 8 << 30},
	})

	if cpus, _ := argsAfter(inv.Args, "--cpus"); cpus != "2.5" {
		t.Errorf("--cpus = %q, want 2.5", cpus)
	}
	if mem, _ := argsAfter(inv.Args, "--memory"); mem != "8589934592" {
		t.Errorf("--memory = %q, want the byte count", mem)
	}
	// ports are never on the container: it cannot publish for itself.
	if got := allAfter(inv.Args, "--publish"); len(got) != 0 {
		t.Errorf("--publish = %v, want none — ports live in a forwarder", got)
	}
}

func TestCreateInvocationOmitsUnsetLimits(t *testing.T) {
	inv := testOCI().CreateInvocation(api.Spec{Name: "demo", Image: "base:1"})
	for _, flag := range []string{"--cpus", "--memory", "--publish", "--env", "--workdir"} {
		if _, ok := argsAfter(inv.Args, flag); ok {
			t.Errorf("%s should be omitted when unset, so the runtime's own default applies", flag)
		}
	}
}

func TestCreateInvocationEnvIsSorted(t *testing.T) {
	// map iteration is random; a rendered command has to be stable to be useful
	// under --dry-run and in tests.
	spec := api.Spec{
		Name:  "demo",
		Image: "base:1",
		Env:   map[string]string{"ZED": "3", "ALPHA": "1", "MID": "2"},
	}
	want := []string{"ALPHA=1", "MID=2", "ZED=3"}
	for range 20 {
		got := allAfter(testOCI().CreateInvocation(spec).Args, "--env")
		if len(got) != len(want) {
			t.Fatalf("env = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("env = %v, want %v", got, want)
			}
		}
	}
}

func TestExecInvocation(t *testing.T) {
	inv := testOCI().ExecInvocation("jard-demo", api.ExecRequest{
		Cmd:         []string{"bash", "-lc", "echo hi"},
		Workdir:     "/home/viv/project",
		User:        "agent",
		Interactive: true,
		TTY:         true,
	})

	joined := strings.Join(inv.Args, " ")
	for _, want := range []string{"exec", "--interactive", "--tty", "--workdir /home/viv/project", "--user agent"} {
		if !strings.Contains(joined, want) {
			t.Errorf("exec args %q missing %q", joined, want)
		}
	}
	// the container must come before the command, or the runtime reads the
	// command as the container.
	tail := inv.Args[len(inv.Args)-4:]
	if tail[0] != "jard-demo" || tail[1] != "bash" {
		t.Errorf("tail = %v, want the container followed by the command", tail)
	}
}

func TestExecInvocationOmitsUnrequestedTTY(t *testing.T) {
	inv := testOCI().ExecInvocation("jard-demo", api.ExecRequest{Cmd: []string{"ls"}})
	for _, a := range inv.Args {
		if a == "--tty" || a == "--interactive" {
			t.Errorf("%s should not be set for a non-interactive exec", a)
		}
	}
}

func TestCopyInvocationRewritesSandboxSide(t *testing.T) {
	o := testOCI()

	in := o.CopyInvocation("jard-demo", api.Path{Path: "/tmp/a"}, api.Path{Sandbox: "demo", Path: "/home/agent/a"})
	if got := in.Args[1:]; got[0] != "/tmp/a" || got[1] != "jard-demo:/home/agent/a" {
		t.Errorf("host→sandbox = %v, want the sandbox side prefixed with the container", got)
	}

	out := o.CopyInvocation("jard-demo", api.Path{Sandbox: "demo", Path: "/home/agent/a"}, api.Path{Path: "/tmp/a"})
	if got := out.Args[1:]; got[0] != "jard-demo:/home/agent/a" || got[1] != "/tmp/a" {
		t.Errorf("sandbox→host = %v, want the sandbox side prefixed with the container", got)
	}
}

// scriptedExecutor answers invocations from canned output, and records what it
// was asked to run.
type scriptedExecutor struct {
	out  []byte
	err  error
	code int
	ran  []Invocation
}

func (s *scriptedExecutor) Output(_ context.Context, inv Invocation) ([]byte, error) {
	s.ran = append(s.ran, inv)
	return s.out, s.err
}

func (s *scriptedExecutor) Session(_ context.Context, inv Invocation, _ api.Streams, _ bool) (int, error) {
	s.ran = append(s.ran, inv)
	return s.code, s.err
}

// Stream yields nothing. Tests that care about streaming use streamExecutor.
func (s *scriptedExecutor) Stream(_ context.Context, inv Invocation) (<-chan string, error) {
	s.ran = append(s.ran, inv)
	if s.err != nil {
		return nil, s.err
	}
	out := make(chan string)
	close(out)
	return out, nil
}

func TestDryRunRendersWithoutExecuting(t *testing.T) {
	var out strings.Builder
	o := testOCI(WithDryRun(&out))

	id, err := o.Create(context.Background(), api.Spec{Name: "demo", Image: "base:1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "jard-demo" {
		t.Errorf("id = %q, want jard-demo", id)
	}
	// the network comes first: a create naming one that does not exist fails.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "/usr/bin/docker network create") {
		t.Fatalf("rendered %q, want the network created before the container", out.String())
	}
	if !strings.HasPrefix(lines[1], "/usr/bin/docker create --name jard-demo") {
		t.Errorf("rendered %q, want the create command line", lines[1])
	}
	if !strings.HasSuffix(lines[1], "base:1 sleep infinity") {
		t.Errorf("rendered %q, want it to end with the image and idle command", lines[1])
	}
}

func TestDryRunCoversEveryMutation(t *testing.T) {
	var out strings.Builder
	o := testOCI(WithDryRun(&out))
	ctx := context.Background()

	if err := o.Start(ctx, "jard-demo", "demo"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := o.Stop(ctx, "jard-demo"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := o.Remove(ctx, "jard-demo", "demo", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	want := []string{
		"start jard-demo",
		"stop jard-demo",
		"rm --volumes --force jard-demo",
		"volume rm jard-demo-home",
		// a sandbox's own belongings go with it: the forwarder holding its
		// ports, and the network both of them were on.
		"rm --force jard-demo-ports",
		"network disconnect --force jard-demo-net jard-relay",
		"network rm jard-demo-net",
	}
	if len(lines) != len(want) {
		t.Fatalf("rendered %d lines, want %d:\n%s", len(lines), len(want), out.String())
	}
	for i := range want {
		if !strings.HasSuffix(lines[i], want[i]) {
			t.Errorf("line %d = %q, want it to end with %q", i, lines[i], want[i])
		}
	}
}

func TestRemoveDeletesTheHomeVolumeSeparately(t *testing.T) {
	// `rm --volumes` reclaims only anonymous volumes. The home volume is named,
	// so without its own call it outlives every sandbox that ever used it.
	e := &scriptedExecutor{}
	if err := testOCI(WithExecutor(e)).Remove(context.Background(), "jard-demo", "demo", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := strings.Join(e.ran[1].Args, " "); got != "volume rm jard-demo-home" {
		t.Errorf("second invocation = %q, want the home volume removed by name", got)
	}
}

func TestRemoveNamesTheVolumeAfterTheSandboxNotTheContainer(t *testing.T) {
	// once a sandbox has been started, the id on record is the runtime's own
	// hash rather than the container's name. A volume name derived from that
	// names nothing at all: the removal then succeeds against a volume that
	// does not exist, and the real one is left behind holding the disk.
	const hash = "5a758d7ed81935e052128081013eef5e14f9f80a5b91bedc3884bf8763602eee"

	e := &scriptedExecutor{}
	if err := testOCI(WithExecutor(e)).Remove(context.Background(), hash, "demo", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := strings.Join(e.ran[1].Args, " "); got != "volume rm jard-demo-home" {
		t.Errorf("second invocation = %q, want the volume named after the sandbox", got)
	}
}

func TestRemoveTolerantOfAnAlreadyGoneContainer(t *testing.T) {
	e := &scriptedExecutor{err: errors.New("Error: No such container: jard-demo")}
	if err := testOCI(WithExecutor(e)).Remove(context.Background(), "jard-demo", "demo", false); err != nil {
		t.Errorf("removing an already-gone sandbox should succeed: %v", err)
	}
}

func TestRemoveReportsRealFailures(t *testing.T) {
	e := &scriptedExecutor{err: errors.New("permission denied")}
	if err := testOCI(WithExecutor(e)).Remove(context.Background(), "jard-demo", "demo", false); err == nil {
		t.Error("a genuine removal failure must not be swallowed")
	}
}

func TestExecPropagatesTheExitCode(t *testing.T) {
	// an agent exiting 3 is the agent's answer, not a jard failure.
	e := &scriptedExecutor{code: 3}
	res, err := testOCI(WithExecutor(e)).Exec(context.Background(), "jard-demo",
		api.ExecRequest{Cmd: []string{"false"}}, api.Streams{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
}

func TestInspectParsesState(t *testing.T) {
	e := &scriptedExecutor{out: []byte("running\tabc123\t2026-08-05T10:00:00.5Z\t0\n")}
	st, err := testOCI(WithExecutor(e)).Inspect(context.Background(), "jard-demo")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if st.Status != api.StatusRunning {
		t.Errorf("Status = %q, want running", st.Status)
	}
	if st.ContainerID != "abc123" {
		t.Errorf("ContainerID = %q, want abc123", st.ContainerID)
	}
	if st.StartedAt.IsZero() {
		t.Error("StartedAt should have parsed")
	}
}

func TestInspectOfAMissingContainerIsNotAnError(t *testing.T) {
	// the record outliving the container is a state jard shows, not a failure.
	e := &scriptedExecutor{err: errors.New("Error: No such object: jard-demo")}
	st, err := testOCI(WithExecutor(e)).Inspect(context.Background(), "jard-demo")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if st.Status != api.StatusMissing {
		t.Errorf("Status = %q, want missing", st.Status)
	}
}

func TestParseStatus(t *testing.T) {
	cases := map[string]api.Status{
		"created":    api.StatusCreated,
		"running":    api.StatusRunning,
		"restarting": api.StatusRunning,
		"exited":     api.StatusStopped,
		"dead":       api.StatusStopped,
		"paused":     api.StatusStopped,
		"removing":   api.StatusStopped,
		"":           api.StatusUnknown,
		"weird":      api.StatusUnknown,
	}
	for in, want := range cases {
		if got := parseStatus(in); got != want {
			t.Errorf("parseStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseInspectSurvivesGarbage(t *testing.T) {
	// a listing is more useful with a missing timestamp than with no row.
	for _, in := range []string{"", "running", "running\tabc", "running\tabc\tnot-a-time\tnot-a-number"} {
		st := parseInspect(in)
		if in != "" && st.Status != api.StatusRunning {
			t.Errorf("parseInspect(%q) lost the status", in)
		}
	}
}

func TestInspectUsesAnUnambiguousFormat(t *testing.T) {
	// tab-separated, so a value containing a space cannot shift the others.
	inv := testOCI().InspectInvocation("jard-demo")
	format, ok := argsAfter(inv.Args, "--format")
	if !ok {
		t.Fatal("inspect should ask for a format")
	}
	if !strings.Contains(format, "\t") {
		t.Errorf("format = %q, want tab-separated fields", format)
	}
}

func TestUnavailableFailsEveryOperation(t *testing.T) {
	sentinel := errors.New("no container runtime found")
	r := Unavailable(sentinel)
	ctx := context.Background()

	if _, err := r.Create(ctx, api.Spec{Name: "demo"}); !errors.Is(err, sentinel) {
		t.Errorf("Create err = %v, want the detection error", err)
	}
	if err := r.Start(ctx, "jard-demo", "demo"); !errors.Is(err, sentinel) {
		t.Errorf("Start err = %v, want the detection error", err)
	}
	if _, err := r.Inspect(ctx, "jard-demo"); !errors.Is(err, sentinel) {
		t.Errorf("Inspect err = %v, want the detection error", err)
	}
}

// testEgressOCI is a runner with egress control turned on.
func testEgressOCI(opts ...Option) *OCI {
	return testOCI(append([]Option{WithEgress("host.docker.internal:47821", "jard-relay:test")}, opts...)...)
}

func TestCreateInvocationPutsTheSandboxOnItsOwnNetwork(t *testing.T) {
	inv := testEgressOCI().CreateInvocation(api.Spec{Name: "demo", Image: "base:1"})

	net, ok := argsAfter(inv.Args, "--network")
	if !ok || net != "jard-demo-net" {
		t.Errorf("--network = %q, want the sandbox's own network", net)
	}
}

func TestCreateInvocationTellsTheSandboxItsWayOut(t *testing.T) {
	inv := testEgressOCI().CreateInvocation(api.Spec{Name: "demo", Image: "base:1"})

	env := strings.Join(allAfter(inv.Args, "--env"), " ")
	for _, want := range []string{"HTTP_PROXY=", "HTTPS_PROXY=", "jard-relay:8080", "NO_PROXY="} {
		if !strings.Contains(env, want) {
			t.Errorf("env %q missing %q", env, want)
		}
	}
	// the sandbox's name rides along, so the connection log can say who asked.
	if !strings.Contains(env, "demo:x@") {
		t.Errorf("env %q should carry the sandbox name as a proxy credential", env)
	}
}

func TestEgressEnvOverridesWhateverTheSpecAsksFor(t *testing.T) {
	// the point of the proxy is that a sandbox cannot choose its own way out,
	// and a spec that could set HTTP_PROXY could choose one.
	inv := testEgressOCI().CreateInvocation(api.Spec{
		Name:  "demo",
		Image: "base:1",
		Env:   map[string]string{"HTTP_PROXY": "http://somewhere.else:3128"},
	})

	env := strings.Join(allAfter(inv.Args, "--env"), " ")
	if strings.Contains(env, "somewhere.else") {
		t.Errorf("a spec talked its way past the proxy: %q", env)
	}
}

func TestWithoutEgressNothingIsRestricted(t *testing.T) {
	// --dry-run and the in-process path have no daemon holding a proxy. The
	// sandbox still gets a network of its own — that is what its port
	// forwarder resolves it on — but nothing about it is restricted, and
	// there is no proxy to point the sandbox at.
	o := testOCI()
	inv := o.CreateInvocation(api.Spec{Name: "demo", Image: "base:1"})

	if env := strings.Join(allAfter(inv.Args, "--env"), " "); strings.Contains(env, "PROXY") {
		t.Errorf("no proxy environment should be set: %q", env)
	}
	if joined := strings.Join(o.CreateNetworkInvocation("demo").Args, " "); strings.Contains(joined, "--internal") {
		t.Errorf("network create %q must not be internal: there is no proxy to be the way out", joined)
	}
}

func TestSandboxNetworkIsInternal(t *testing.T) {
	// "internal" is the whole guarantee: without it the sandbox has a route
	// out that never passes the proxy.
	inv := testEgressOCI().CreateNetworkInvocation("demo")
	joined := strings.Join(inv.Args, " ")
	if !strings.Contains(joined, "--internal") {
		t.Errorf("network create %q must be internal", joined)
	}
	if !strings.HasSuffix(joined, "jard-demo-net") {
		t.Errorf("network create %q should name the sandbox's network", joined)
	}
}

func TestRelayIsGivenOneAddressAndNoMore(t *testing.T) {
	inv := testEgressOCI().RelayInvocation("jard-relay:test", "host.docker.internal:47821")
	joined := strings.Join(inv.Args, " ")

	if !strings.Contains(joined, "-upstream host.docker.internal:47821") {
		t.Errorf("relay %q should forward to the host proxy", joined)
	}
	if strings.Contains(joined, "--publish") {
		t.Errorf("relay %q must publish no ports: it is reached over the sandbox network", joined)
	}
	if strings.Contains(joined, "--privileged") {
		t.Errorf("relay %q must not be privileged", joined)
	}
}

func TestRemoveDropsTheNetworkToo(t *testing.T) {
	// a network per sandbox leaks one per sandbox otherwise.
	e := &scriptedExecutor{}
	if err := testEgressOCI(WithExecutor(e)).Remove(context.Background(), "jard-demo", "demo", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	var sawNetworkRm bool
	for _, inv := range e.ran {
		if strings.HasPrefix(strings.Join(inv.Args, " "), "network rm jard-demo-net") {
			sawNetworkRm = true
		}
	}
	if !sawNetworkRm {
		t.Errorf("no network removal among %d invocations", len(e.ran))
	}
}

func TestRemoveDetachesTheRelayBeforeDroppingTheNetwork(t *testing.T) {
	// the relay is attached to every sandbox's network, and a runtime refuses
	// to remove a network that still has endpoints. Without the detach, every
	// removal fails — and fails after the container is already gone, leaving a
	// record for a sandbox that no longer exists.
	e := &scriptedExecutor{}
	if err := testEgressOCI(WithExecutor(e)).Remove(context.Background(), "jard-demo", "demo", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	disconnect, remove := -1, -1
	for i, inv := range e.ran {
		joined := strings.Join(inv.Args, " ")
		if strings.HasPrefix(joined, "network disconnect") {
			disconnect = i
		}
		if strings.HasPrefix(joined, "network rm") {
			remove = i
		}
	}
	if disconnect < 0 {
		t.Fatal("the relay was never detached")
	}
	if remove < 0 {
		t.Fatal("the network was never removed")
	}
	if disconnect > remove {
		t.Error("the detach must come before the removal, or the removal fails")
	}
}

// A sandbox's network is named after the sandbox, but its container id is a
// name only until the first start and a hash forever after. Deriving one from
// the other left the relay attaching to a network that did not exist, so every
// start after the first one failed.
func TestStartAttachesTheRelayBySandboxName(t *testing.T) {
	for _, tc := range []struct{ name, id string }{
		{"before the first start, when the id is the container's name", "jard-demo"},
		{"after it, when the runtime has replaced that with a hash", "9f8c1b2a3d4e5f60718293a4b5c6d7e8f9012345678990abcdef0123456789ab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := &scriptedExecutor{out: []byte("true\n")}
			o := testOCI(WithExecutor(exec), WithEgress("host.docker.internal:47821", ""))

			if err := o.Start(context.Background(), ID(tc.id), "demo"); err != nil {
				t.Fatalf("Start: %v", err)
			}

			var connect string
			for _, inv := range exec.ran {
				if len(inv.Args) >= 2 && inv.Args[0] == "network" && inv.Args[1] == "connect" {
					connect = strings.Join(inv.Args, " ")
				}
			}
			if !strings.Contains(connect, " "+SandboxNetwork("demo")+" ") {
				t.Errorf("relay attached via %q, want the sandbox's own network %s",
					connect, SandboxNetwork("demo"))
			}
		})
	}
}

// The container itself is still addressed by whatever handle it was given.
func TestStartAddressesTheContainerByItsRuntimeHandle(t *testing.T) {
	exec := &scriptedExecutor{out: []byte("true\n")}
	o := testOCI(WithExecutor(exec))

	if err := o.Start(context.Background(), ID("deadbeef"), "demo"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	last := exec.ran[len(exec.ran)-1]
	if last.Args[0] != "start" || last.Args[len(last.Args)-1] != "deadbeef" {
		t.Errorf("ran %v, want start against the container handle", last.Args)
	}
}
