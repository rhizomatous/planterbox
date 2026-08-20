package rpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/proxy"
)

// dial stands a server up over an in-memory transport and returns a client
// against it. Nothing touches a socket on disk, so the tests stay pure.
func dial(t *testing.T, svc api.Service) *Client {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	NewServer(svc).Register(srv)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}))
	if err != nil {
		t.Fatalf("dialing the test server: %v", err)
	}

	client := NewClient(conn)
	t.Cleanup(func() {
		_ = client.Close()
		srv.Stop()
	})
	return client
}

// detailed is a spec with every field populated, so a conversion that drops one
// is caught rather than passing on a sparse fixture.
func detailed() api.Spec {
	return api.Spec{
		Name:  "myrepo",
		Agent: "claude",
		Image: "ghcr.io/acme/base:1",
		Workspaces: []api.Workspace{
			{Host: "/home/viv/myrepo"},
			{Host: "/home/viv/shared", ReadOnly: true},
		},
		Resources: api.Resources{CPUs: 4, Memory: 2 << 30},
		Env:       map[string]string{"FOO": "bar"},
		Clone:     true,
		CreatedAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	}
}

func TestSpecSurvivesTheRoundTrip(t *testing.T) {
	client := dial(t, api.NewFake())

	sb, err := client.Create(context.Background(), detailed())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, want := sb.Spec, detailed()
	if got.Name != want.Name || got.Agent != want.Agent || got.Image != want.Image {
		t.Errorf("identity changed: %+v", got)
	}
	if len(got.Workspaces) != 2 || got.Workspaces[0] != want.Workspaces[0] || got.Workspaces[1] != want.Workspaces[1] {
		t.Errorf("workspaces = %+v, want %+v", got.Workspaces, want.Workspaces)
	}
	if got.Resources != want.Resources {
		t.Errorf("resources = %+v, want %+v", got.Resources, want.Resources)
	}
	if !got.Clone {
		t.Error("clone mode did not survive the trip; a sandbox would quietly get a writable workspace")
	}
	if got.Env["FOO"] != "bar" {
		t.Errorf("env = %v, want FOO=bar", got.Env)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("createdAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
}

func TestZeroTimeDoesNotBecomeTheEpoch(t *testing.T) {
	// a sandbox that never started has no StartedAt, and rendering one as
	// 1970 would put a wrong date in front of the user.
	fake := api.NewFake(api.Sandbox{
		Spec:  api.Spec{Name: "a"},
		State: api.State{Status: api.StatusCreated},
	})
	client := dial(t, fake)

	sb, err := client.Inspect(context.Background(), api.ByName("a"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !sb.State.StartedAt.IsZero() {
		t.Errorf("StartedAt = %v, want the zero time", sb.State.StartedAt)
	}
	if !sb.Spec.CreatedAt.IsZero() {
		t.Errorf("CreatedAt = %v, want the zero time", sb.Spec.CreatedAt)
	}
}

func TestListCarriesEverySandbox(t *testing.T) {
	fake := api.NewFake(
		api.Sandbox{Spec: api.Spec{Name: "a"}, State: api.State{Status: api.StatusRunning}},
		api.Sandbox{Spec: api.Spec{Name: "b"}, State: api.State{Status: api.StatusStopped}},
	)
	client := dial(t, fake)

	sandboxes, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sandboxes) != 2 {
		t.Fatalf("got %d sandboxes, want 2", len(sandboxes))
	}
	if sandboxes[0].Spec.Name != "a" || sandboxes[0].State.Status != api.StatusRunning {
		t.Errorf("first sandbox = %+v", sandboxes[0])
	}
	if sandboxes[1].State.Status != api.StatusStopped {
		t.Errorf("second sandbox = %+v", sandboxes[1])
	}
}

func TestEmptyListIsNotAnError(t *testing.T) {
	sandboxes, err := dial(t, api.NewFake()).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sandboxes) != 0 {
		t.Errorf("got %d sandboxes, want none", len(sandboxes))
	}
}

func TestSentinelErrorsSurviveTheWire(t *testing.T) {
	// the CLI and TUI branch on these with errors.Is. If they arrive as plain
	// strings, the guards they drive silently stop working.
	cases := []struct {
		name string
		call func(*Client) error
		want error
	}{
		{
			name: "not found",
			call: func(c *Client) error {
				_, err := c.Inspect(context.Background(), api.ByName("nope"))
				return err
			},
			want: api.ErrNotFound,
		},
		{
			name: "already exists",
			call: func(c *Client) error {
				_, err := c.Create(context.Background(), api.Spec{Name: "taken"})
				return err
			},
			want: api.ErrExists,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := api.NewFake(api.Sandbox{Spec: api.Spec{Name: "taken"}})
			if err := tc.call(dial(t, fake)); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want it to match %v", err, tc.want)
			}
		})
	}
}

func TestErrorMessageIsNotSwallowedByItsSentinel(t *testing.T) {
	// the daemon's message is what tells the user which sandbox and why, so
	// mapping onto a sentinel must not replace it.
	fake := api.NewFake()
	fake.Err = errors.New(`sandbox is running: "web" (use --force)`)
	client := dial(t, fake)

	err := client.Stop(context.Background(), api.ByName("web"))
	if err == nil {
		t.Fatal("Stop should have failed")
	}
	if !strings.Contains(err.Error(), "use --force") {
		t.Errorf("err = %q, want the daemon's own message", err)
	}
}

func TestForceReachesTheService(t *testing.T) {
	fake := api.NewFake(api.Sandbox{Spec: api.Spec{Name: "a"}})
	client := dial(t, fake)

	if err := client.Remove(context.Background(), api.ByName("a"), true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(fake.Sandboxes) != 0 {
		t.Error("a forced remove should have deleted the sandbox")
	}
}

func TestCopyCarriesBothSides(t *testing.T) {
	fake := api.NewFake(api.Sandbox{Spec: api.Spec{Name: "a"}})
	client := dial(t, fake)

	err := client.Copy(context.Background(),
		api.Path{Path: "/host/file"}, api.Path{Sandbox: "a", Path: "/in/sandbox"})
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
}

func TestStatsStreams(t *testing.T) {
	fake := api.NewFake(api.Sandbox{Spec: api.Spec{Name: "a"}, State: api.State{Status: api.StatusRunning}})
	fake.Samples = []api.Stats{
		{CPUPercent: 12.5, MemoryBytes: 1 << 20, MemoryLimit: 4 << 20},
		{CPUPercent: 30, MemoryBytes: 2 << 20, MemoryLimit: 4 << 20},
	}
	client := dial(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	samples, err := client.Stats(ctx, api.ByName("a"))
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	var got []api.Stats
	for s := range samples {
		got = append(got, s)
	}
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2: %+v", len(got), got)
	}
	if got[0].CPUPercent != 12.5 || got[1].MemoryBytes != 2<<20 {
		t.Errorf("samples = %+v", got)
	}
}

func TestStatsStopsWhenTheCallerCancels(t *testing.T) {
	fake := api.NewFake(api.Sandbox{Spec: api.Spec{Name: "a"}, State: api.State{Status: api.StatusRunning}})
	fake.Samples = []api.Stats{{CPUPercent: 1}}
	client := dial(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	samples, err := client.Stats(ctx, api.ByName("a"))
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	cancel()

	// draining must finish rather than hang: a cancelled feed closes.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range samples {
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sample channel never closed after the context was cancelled")
	}
}

func TestExecCarriesStdioAndExitCode(t *testing.T) {
	fake := api.NewFake(api.Sandbox{Spec: api.Spec{Name: "a"}, State: api.State{Status: api.StatusRunning}})
	fake.OnExec = func(_ context.Context, _ api.Ref, _ api.ExecRequest, s api.Streams) (api.ExecResult, error) {
		in, err := io.ReadAll(s.Stdin)
		if err != nil {
			return api.ExecResult{}, err
		}
		if _, err := s.Stdout.Write([]byte("saw: " + string(in))); err != nil {
			return api.ExecResult{}, err
		}
		if _, err := s.Stderr.Write([]byte("a warning")); err != nil {
			return api.ExecResult{}, err
		}
		return api.ExecResult{ExitCode: 3}, nil
	}
	client := dial(t, fake)

	var stdout, stderr bytes.Buffer
	res, err := client.Exec(context.Background(), api.ByName("a"),
		api.ExecRequest{Cmd: []string{"cat"}, Interactive: true},
		api.Streams{
			Stdin:  strings.NewReader("hello"),
			Stdout: &stdout,
			Stderr: &stderr,
		})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3 — a command's own status is not an error", res.ExitCode)
	}
	if stdout.String() != "saw: hello" {
		t.Errorf("stdout = %q, want the input echoed back", stdout.String())
	}
	if stderr.String() != "a warning" {
		t.Errorf("stderr = %q, want it kept separate from stdout", stderr.String())
	}
}

func TestExecCarriesTheRequestAndItsOpeningSize(t *testing.T) {
	fake := api.NewFake(api.Sandbox{Spec: api.Spec{Name: "a"}, State: api.State{Status: api.StatusRunning}})

	var gotReq api.ExecRequest
	gotSize := make(chan api.Size, 1)
	fake.OnExec = func(_ context.Context, _ api.Ref, req api.ExecRequest, s api.Streams) (api.ExecResult, error) {
		gotReq = req
		select {
		case size := <-s.Resize:
			gotSize <- size
		case <-time.After(2 * time.Second):
		}
		return api.ExecResult{}, nil
	}
	client := dial(t, fake)

	sizes := make(chan api.Size, 1)
	sizes <- api.Size{Rows: 40, Cols: 120}

	_, err := client.Exec(context.Background(), api.ByName("a"),
		api.ExecRequest{
			Cmd:     []string{"bash", "-l"},
			Env:     map[string]string{"TERM": "xterm"},
			Workdir: "/work",
			User:    "agent",
			TTY:     true,
		},
		api.Streams{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, Resize: sizes})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if len(gotReq.Cmd) != 2 || gotReq.Cmd[0] != "bash" {
		t.Errorf("cmd = %v", gotReq.Cmd)
	}
	if gotReq.Workdir != "/work" || gotReq.User != "agent" || !gotReq.TTY {
		t.Errorf("request = %+v", gotReq)
	}
	if gotReq.Env["TERM"] != "xterm" {
		t.Errorf("env = %v", gotReq.Env)
	}
	select {
	case size := <-gotSize:
		if size.Rows != 40 || size.Cols != 120 {
			t.Errorf("size = %+v, want 40x120", size)
		}
	default:
		t.Error("the opening size never reached the session")
	}
}

func TestExecReportsAMissingSandbox(t *testing.T) {
	client := dial(t, api.NewFake())

	_, err := client.Exec(context.Background(), api.ByName("nope"), api.ExecRequest{},
		api.Streams{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
	if !errors.Is(err, api.ErrNotFound) {
		t.Errorf("err = %v, want it to match ErrNotFound", err)
	}
}

func TestEverySentinelHasItsOwnCode(t *testing.T) {
	// decoding takes the first entry whose code matches, so a duplicate would
	// hand callers a different error than the daemon raised, and it would look
	// right, because the message survives either way.
	seen := map[codes.Code]error{}
	for _, s := range sentinels {
		if other, dup := seen[s.code]; dup {
			t.Errorf("%v and %v share code %v", other, s.err, s.code)
		}
		seen[s.code] = s.err
	}
}

func TestNoPolicyRoundTripsAsItself(t *testing.T) {
	// the first-run wizard branches on this, and ErrNotFound would send it
	// down the wrong path entirely.
	client := dial(t, api.NewFake())

	_, err := client.Policy(context.Background())
	if !errors.Is(err, api.ErrNoPolicy) {
		t.Errorf("err = %v, want it to match ErrNoPolicy", err)
	}
	if errors.Is(err, api.ErrNotFound) {
		t.Error("ErrNoPolicy must not arrive as ErrNotFound")
	}
}

func TestPolicySurvivesTheRoundTrip(t *testing.T) {
	client := dial(t, api.NewFake())

	want := proxy.Policy{Preset: proxy.PresetBalanced, Rules: []proxy.Rule{
		{Pattern: "*.example.com", Allow: true},
		{Pattern: "blocked.test:443"},
	}}
	if err := client.SetPolicy(context.Background(), want); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	got, err := client.Policy(context.Background())
	if err != nil {
		t.Fatalf("Policy: %v", err)
	}
	if got.Preset != want.Preset || len(got.Rules) != 2 {
		t.Fatalf("policy = %+v, want %+v", got, want)
	}
	if got.Rules[0] != want.Rules[0] || got.Rules[1] != want.Rules[1] {
		t.Errorf("rules = %+v, want %+v", got.Rules, want.Rules)
	}
}

func TestConnectionsCarryTheirDecisions(t *testing.T) {
	fake := api.NewFake()
	fake.Decisions = []proxy.Entry{
		{Seq: 1, At: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC), Target: proxy.Target{Host: "a.test", Port: 443}, Allowed: true, Reason: "allowed by rule a.test"},
		{Seq: 2, Target: proxy.Target{Host: "b.test", Port: 80}, Reason: "denied by default", Sandbox: "web"},
	}
	client := dial(t, fake)

	got, err := client.Connections(context.Background(), 0)
	if err != nil {
		t.Fatalf("Connections: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d decisions, want 2", len(got))
	}
	if got[0].Target.Host != "a.test" || !got[0].Allowed || got[0].Reason == "" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Sandbox != "web" || got[1].Allowed {
		t.Errorf("second = %+v", got[1])
	}

	since, err := client.Connections(context.Background(), 1)
	if err != nil {
		t.Fatalf("Connections(since): %v", err)
	}
	if len(since) != 1 || since[0].Seq != 2 {
		t.Errorf("Connections(1) = %+v, want only the newer one", since)
	}
}

// Ports ride on the sandbox rather than the spec, so they need their own trip.
func TestPortsSurviveTheRoundTrip(t *testing.T) {
	client := dial(t, api.NewFake())
	ctx := context.Background()

	if _, err := client.Create(ctx, detailed()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := []api.Port{{Host: 3000, Sandbox: 3000}, {Host: 8080, Sandbox: 80, Proto: "tcp"}}
	if err := client.Publish(ctx, api.ByName("myrepo"), want); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	sb, err := client.Inspect(ctx, api.ByName("myrepo"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(sb.Ports) != len(want) || sb.Ports[0] != want[0] || sb.Ports[1] != want[1] {
		t.Errorf("ports = %+v, want %+v", sb.Ports, want)
	}
}
