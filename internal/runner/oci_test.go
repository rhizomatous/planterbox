package runner

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
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
	inv := testOCI().createInvocation(api.Spec{Name: "demo", Image: "base:1"})

	if inv.Path != "/usr/bin/docker" {
		t.Errorf("path = %q, want the detected runtime binary", inv.Path)
	}
	if inv.Args[0] != "create" {
		t.Errorf("args[0] = %q, want create — a sandbox is built, not run", inv.Args[0])
	}
	if name, _ := argsAfter(inv.Args, "--name"); name != "plbx-demo" {
		t.Errorf("--name = %q, want plbx-demo", name)
	}
	if vol, _ := argsAfter(inv.Args, "--volume"); vol != "plbx-demo-home:/home/agent" {
		t.Errorf("--volume = %q, want the home volume that makes a sandbox persistent", vol)
	}
	// image then the idle command, in that order, at the tail.
	tail := inv.Args[len(inv.Args)-3:]
	if tail[0] != "base:1" || tail[1] != "sleep" || tail[2] != "infinity" {
		t.Errorf("tail = %v, want the image followed by the idle command", tail)
	}
}

func TestCreateInvocationNeverPrivileged(t *testing.T) {
	inv := testOCI().createInvocation(api.Spec{Name: "demo", Image: "base:1"})
	for _, a := range inv.Args {
		if a == "--privileged" {
			t.Fatal("sandboxes must never run privileged")
		}
	}
}

func TestCreateInvocationWorkspacesBindAtHostPaths(t *testing.T) {
	inv := testOCI().createInvocation(api.Spec{
		Name:  "demo",
		Image: "base:1",
		Workspaces: []api.Workspace{
			{Host: "/home/viv/project"},
			{Host: "/home/viv/shared", ReadOnly: true},
		},
	})

	vols := allAfter(inv.Args, "--volume")
	want := []string{
		"plbx-demo-home:/home/agent",
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
	inv := testOCI().createInvocation(api.Spec{
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
	inv := testOCI().createInvocation(api.Spec{Name: "demo", Image: "base:1"})
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
		got := allAfter(testOCI().createInvocation(spec).Args, "--env")
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
	inv := testOCI().execInvocation("plbx-demo", api.ExecRequest{
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
	if tail[0] != "plbx-demo" || tail[1] != "bash" {
		t.Errorf("tail = %v, want the container followed by the command", tail)
	}
}

func TestExecInvocationOmitsUnrequestedTTY(t *testing.T) {
	inv := testOCI().execInvocation("plbx-demo", api.ExecRequest{Cmd: []string{"ls"}})
	for _, a := range inv.Args {
		if a == "--tty" || a == "--interactive" {
			t.Errorf("%s should not be set for a non-interactive exec", a)
		}
	}
}

func TestCopyInvocationRewritesSandboxSide(t *testing.T) {
	o := testOCI()

	in := o.copyInvocation("plbx-demo", api.Path{Path: "/tmp/a"}, api.Path{Sandbox: "demo", Path: "/home/agent/a"})
	if got := in.Args[1:]; got[0] != "/tmp/a" || got[1] != "plbx-demo:/home/agent/a" {
		t.Errorf("host→sandbox = %v, want the sandbox side prefixed with the container", got)
	}

	out := o.copyInvocation("plbx-demo", api.Path{Sandbox: "demo", Path: "/home/agent/a"}, api.Path{Path: "/tmp/a"})
	if got := out.Args[1:]; got[0] != "plbx-demo:/home/agent/a" || got[1] != "/tmp/a" {
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
	if id != "plbx-demo" {
		t.Errorf("id = %q, want plbx-demo", id)
	}
	// the network comes first: a create naming one that does not exist fails.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "/usr/bin/docker network create") {
		t.Fatalf("rendered %q, want the network created before the container", out.String())
	}
	if !strings.HasPrefix(lines[1], "/usr/bin/docker create --name plbx-demo") {
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

	if err := o.Start(ctx, "plbx-demo", "demo"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := o.Stop(ctx, "plbx-demo", "demo"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := o.Remove(ctx, "plbx-demo", "demo", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	want := []string{
		"start plbx-demo",
		"stop plbx-demo",
		// a stop takes the sidecars with it: they serve a running sandbox, and
		// the forwarder holds host ports while it lives.
		"rm --force plbx-demo-ports",
		"rm --force plbx-demo-relay",
		"rm --volumes --force plbx-demo",
		"volume rm plbx-demo-home",
		// and a remove repeats it, because it does not require a stop first
		"rm --force plbx-demo-ports",
		"rm --force plbx-demo-relay",
		"network rm plbx-demo-net",
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
	if err := testOCI(WithExecutor(e)).Remove(context.Background(), "plbx-demo", "demo", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := strings.Join(e.ran[1].Args, " "); got != "volume rm plbx-demo-home" {
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
	if got := strings.Join(e.ran[1].Args, " "); got != "volume rm plbx-demo-home" {
		t.Errorf("second invocation = %q, want the volume named after the sandbox", got)
	}
}

func TestRemoveTolerantOfAnAlreadyGoneContainer(t *testing.T) {
	e := &scriptedExecutor{err: errors.New("Error: No such container: plbx-demo")}
	if err := testOCI(WithExecutor(e)).Remove(context.Background(), "plbx-demo", "demo", false); err != nil {
		t.Errorf("removing an already-gone sandbox should succeed: %v", err)
	}
}

func TestRemoveReportsRealFailures(t *testing.T) {
	e := &scriptedExecutor{err: errors.New("permission denied")}
	if err := testOCI(WithExecutor(e)).Remove(context.Background(), "plbx-demo", "demo", false); err == nil {
		t.Error("a genuine removal failure must not be swallowed")
	}
}

func TestExecPropagatesTheExitCode(t *testing.T) {
	// an agent exiting 3 is the agent's answer, not a plbx failure.
	e := &scriptedExecutor{code: 3}
	res, err := testOCI(WithExecutor(e)).Exec(context.Background(), "plbx-demo",
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
	st, err := testOCI(WithExecutor(e)).Inspect(context.Background(), "plbx-demo")
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
	// the record outliving the container is a state plbx shows, not a failure.
	e := &scriptedExecutor{err: errors.New("Error: No such object: plbx-demo")}
	st, err := testOCI(WithExecutor(e)).Inspect(context.Background(), "plbx-demo")
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
	inv := testOCI().inspectInvocation("plbx-demo")
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
	if err := r.Start(ctx, "plbx-demo", "demo"); !errors.Is(err, sentinel) {
		t.Errorf("Start err = %v, want the detection error", err)
	}
	if _, err := r.Inspect(ctx, "plbx-demo"); !errors.Is(err, sentinel) {
		t.Errorf("Inspect err = %v, want the detection error", err)
	}
}

// testEgressOCI is a runner with egress control turned on.
func testEgressOCI(opts ...Option) *OCI {
	return testOCI(append([]Option{WithEgress("host.docker.internal:47821", "plbx-relay:test")}, opts...)...)
}

func TestCreateInvocationPutsTheSandboxOnItsOwnNetwork(t *testing.T) {
	inv := testEgressOCI().createInvocation(api.Spec{Name: "demo", Image: "base:1"})

	net, ok := argsAfter(inv.Args, "--network")
	if !ok || net != "plbx-demo-net" {
		t.Errorf("--network = %q, want the sandbox's own network", net)
	}
}

func TestCreateInvocationTellsTheSandboxItsWayOut(t *testing.T) {
	inv := testEgressOCI().createInvocation(api.Spec{Name: "demo", Image: "base:1"})

	env := strings.Join(allAfter(inv.Args, "--env"), " ")
	for _, want := range []string{"HTTP_PROXY=", "HTTPS_PROXY=", "plbx-demo-relay:8080", "NO_PROXY="} {
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
	inv := testEgressOCI().createInvocation(api.Spec{
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
	// sandbox still gets a network of its own, which is what its port
	// forwarder resolves it on, but nothing about it is restricted and there
	// is no proxy to point the sandbox at.
	o := testOCI()
	inv := o.createInvocation(api.Spec{Name: "demo", Image: "base:1"})

	if env := strings.Join(allAfter(inv.Args, "--env"), " "); strings.Contains(env, "PROXY") {
		t.Errorf("no proxy environment should be set: %q", env)
	}
	if joined := strings.Join(o.createNetworkInvocation("demo").Args, " "); strings.Contains(joined, "--internal") {
		t.Errorf("network create %q must not be internal: there is no proxy to be the way out", joined)
	}
}

func TestSandboxNetworkIsInternal(t *testing.T) {
	// "internal" is the whole guarantee: without it the sandbox has a route
	// out that never passes the proxy.
	inv := testEgressOCI().createNetworkInvocation("demo")
	joined := strings.Join(inv.Args, " ")
	if !strings.Contains(joined, "--internal") {
		t.Errorf("network create %q must be internal", joined)
	}
	if !strings.HasSuffix(joined, "plbx-demo-net") {
		t.Errorf("network create %q should name the sandbox's network", joined)
	}
}

func TestRelayIsGivenOneAddressAndNoMore(t *testing.T) {
	inv := testEgressOCI().relayInvocation("demo", "plbx-relay:test", "host.docker.internal:47821")
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
	if err := testEgressOCI(WithExecutor(e)).Remove(context.Background(), "plbx-demo", "demo", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	var sawNetworkRm bool
	for _, inv := range e.ran {
		if strings.HasPrefix(strings.Join(inv.Args, " "), "network rm plbx-demo-net") {
			sawNetworkRm = true
		}
	}
	if !sawNetworkRm {
		t.Errorf("no network removal among %d invocations", len(e.ran))
	}
}

func TestRemoveDropsTheRelayBeforeTheNetwork(t *testing.T) {
	// a runtime refuses to remove a network anything is still attached to, and
	// the sandbox's relay is attached to it.
	e := &scriptedExecutor{}
	if err := testEgressOCI(WithExecutor(e)).Remove(context.Background(), "plbx-demo", "demo", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	relay, network := -1, -1
	for i, inv := range e.ran {
		switch joined := strings.Join(inv.Args, " "); {
		case strings.HasPrefix(joined, "rm --force plbx-demo-relay"):
			relay = i
		case strings.HasPrefix(joined, "network rm"):
			network = i
		}
	}
	if relay < 0 {
		t.Fatal("the sandbox's relay was never removed")
	}
	if network < 0 {
		t.Fatal("the network was never removed")
	}
	if relay > network {
		t.Error("the relay must go before the network, or the removal fails")
	}
}

// A sandbox's network is named after the sandbox, but its container id is a
// name only until the first start and a hash forever after. Deriving one from
// the other attaches the relay to a network that does not exist, and every
// start after the first fails.
func TestStartAttachesTheRelayBySandboxName(t *testing.T) {
	for _, tc := range []struct{ name, id string }{
		{name: "before the first start, when the id is the container's name", id: "plbx-demo"},
		{name: "after it, when the runtime has replaced that with a hash", id: "9f8c1b2a3d4e5f60718293a4b5c6d7e8f9012345678990abcdef0123456789ab"},
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
			if !strings.Contains(connect, " "+sandboxNetwork("demo")+" ") {
				t.Errorf("relay attached via %q, want the sandbox's own network %s",
					connect, sandboxNetwork("demo"))
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

// Clone mode's whole promise is that nothing the agent does reaches your
// files, so every workspace mounts read-only, not just the one it clones.
func TestCloneModeMountsEveryWorkspaceReadOnly(t *testing.T) {
	spec := api.Spec{
		Name:  "demo",
		Image: "base:1",
		Clone: true,
		Workspaces: []api.Workspace{
			{Host: "/home/viv/myrepo"},
			{Host: "/home/viv/other"},
		},
	}
	inv := testOCI().createInvocation(spec)

	for _, mount := range allAfter(inv.Args, "--volume") {
		if strings.HasPrefix(mount, "/home/viv/") && !strings.HasSuffix(mount, ":ro") {
			t.Errorf("mount %q is writable in clone mode", mount)
		}
	}
	if dir, _ := argsAfter(inv.Args, "--workdir"); dir != spec.CloneDir() {
		t.Errorf("--workdir = %q, want the clone at %q", dir, spec.CloneDir())
	}
}

// Without it, the workspace is writable and sits at its own path.
func TestWithoutCloneTheWorkspaceIsWritable(t *testing.T) {
	spec := api.Spec{
		Name:       "demo",
		Image:      "base:1",
		Workspaces: []api.Workspace{{Host: "/home/viv/myrepo"}},
	}
	inv := testOCI().createInvocation(spec)
	if got := allAfter(inv.Args, "--volume"); !slices.Contains(got, "/home/viv/myrepo:/home/viv/myrepo") {
		t.Errorf("--volume = %v, want the workspace mounted read-write at its own path", got)
	}
	if dir, _ := argsAfter(inv.Args, "--workdir"); dir != "/home/viv/myrepo" {
		t.Errorf("--workdir = %q, want the workspace itself", dir)
	}
}

// host.docker.internal is a Docker Desktop convenience. Docker Engine on Linux
// does not publish it, so the relay is told the mapping explicitly there and
// nowhere else: on macOS the gateway is a bridge inside the runtime's own VM,
// not the host, so the same flag would point the relay at nothing.
//
// A live runtime is the only thing that can prove the Linux half end to end.
// This pins the arguments; the behaviour behind them needs a hand check.
func TestRelayIsToldHowToReachTheHostOnLinuxOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		goos     string
		upstream string
		want     bool
	}{
		{name: "linux needs the mapping", goos: "linux", upstream: "host.docker.internal:47821", want: true},
		{name: "macOS already resolves it", goos: "darwin", upstream: "host.docker.internal:47821", want: false},
		{name: "an address needs no mapping", goos: "linux", upstream: "192.168.1.5:47821", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := testEgressOCI()
			o.goos = tc.goos
			joined := strings.Join(o.relayInvocation("demo", "plbx-relay:1", tc.upstream).Args, " ")
			if got := strings.Contains(joined, "--add-host host.docker.internal:host-gateway"); got != tc.want {
				t.Errorf("relay args = %q, --add-host present = %v, want %v", joined, got, tc.want)
			}
			if !strings.Contains(joined, "-upstream "+tc.upstream) {
				t.Errorf("relay args = %q, want it still dialling %s", joined, tc.upstream)
			}
		})
	}
}

// A relay belongs to one sandbox, and no other sandbox's lifecycle reaches it.
// Anything that serves several has to survive all of them, and a sandbox that
// took one down would cut off the rest without saying so.
func TestEachSandboxGetsItsOwnRelay(t *testing.T) {
	e := &scriptedExecutor{}
	o := testEgressOCI(WithExecutor(e))
	ctx := context.Background()

	for _, name := range []string{"alpha", "beta"} {
		if err := o.Start(ctx, ID("plbx-"+name), name); err != nil {
			t.Fatalf("Start %s: %v", name, err)
		}
	}
	for _, want := range []string{"plbx-alpha-relay", "plbx-beta-relay"} {
		if !strings.Contains(strings.Join(flatten(e.ran), "\n"), want) {
			t.Errorf("no relay named %q was started", want)
		}
	}

	// stopping one must not reach for the other's
	e.ran = nil
	if err := o.Stop(ctx, "plbx-alpha", "alpha"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	joined := strings.Join(flatten(e.ran), "\n")
	if !strings.Contains(joined, "rm --force plbx-alpha-relay") {
		t.Errorf("stopping alpha should take its own relay: %s", joined)
	}
	if strings.Contains(joined, "plbx-beta-relay") {
		t.Errorf("stopping alpha touched beta's relay: %s", joined)
	}
}

func flatten(invs []Invocation) []string {
	out := make([]string, 0, len(invs))
	for _, inv := range invs {
		out = append(out, strings.Join(inv.Args, " "))
	}
	return out
}
