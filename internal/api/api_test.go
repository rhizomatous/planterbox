package api

import (
	"context"
	"errors"
	"testing"
)

func TestRef(t *testing.T) {
	if got := ByName("demo").String(); got != "demo" {
		t.Errorf("String = %q, want demo", got)
	}
	if got := ByPath("/a").String(); got != "/a" {
		t.Errorf("String = %q, want /a", got)
	}
	// name wins when both are set, matching how the store resolves.
	if got := (Ref{Name: "demo", Path: "/a"}).String(); got != "demo" {
		t.Errorf("String = %q, want the name to win", got)
	}
	if got := (Ref{}).String(); got == "" {
		t.Error("an empty ref should still render something in an error message")
	}
}

func TestSpecPrimary(t *testing.T) {
	if got := (Spec{}).Primary(); got.Host != "" {
		t.Errorf("Primary of a workspace-less spec = %+v, want the zero value", got)
	}
	spec := Spec{Workspaces: []Workspace{{Host: "/first"}, {Host: "/second", ReadOnly: true}}}
	if got := spec.Primary(); got.Host != "/first" {
		t.Errorf("Primary = %q, want the first workspace", got.Host)
	}
}

func TestPath(t *testing.T) {
	host := Path{Path: "/tmp/a"}
	if host.InSandbox() {
		t.Error("a bare path is on the host")
	}
	if got := host.String(); got != "/tmp/a" {
		t.Errorf("String = %q, want /tmp/a", got)
	}

	inside := Path{Sandbox: "demo", Path: "/home/agent/a"}
	if !inside.InSandbox() {
		t.Error("a path with a sandbox is inside it")
	}
	if got := inside.String(); got != "demo:/home/agent/a" {
		t.Errorf("String = %q, want demo:/home/agent/a", got)
	}
}

func TestCopyRefTakesTheSandboxFromWhicheverSideHasOne(t *testing.T) {
	inside := Path{Sandbox: "demo", Path: "/home/agent/a"}
	host := Path{Path: "/tmp/a"}

	out, err := CopyRef(inside, host)
	if err != nil {
		t.Fatalf("sandbox→host: %v", err)
	}
	if out.Name != "demo" {
		t.Errorf("sandbox→host ref = %q, want demo", out.Name)
	}

	in, err := CopyRef(host, inside)
	if err != nil {
		t.Fatalf("host→sandbox: %v", err)
	}
	if in.Name != "demo" {
		t.Errorf("host→sandbox ref = %q, want demo", in.Name)
	}
}

func TestCopyRefRejectsAmbiguousAndPointlessCopies(t *testing.T) {
	// two sandbox paths have no host leg to route through.
	_, err := CopyRef(Path{Sandbox: "a", Path: "/x"}, Path{Sandbox: "b", Path: "/y"})
	if err == nil {
		t.Error("a copy naming two sandboxes should be refused")
	}
	// neither side names a sandbox: that is just cp.
	if _, err := CopyRef(Path{Path: "/x"}, Path{Path: "/y"}); err == nil {
		t.Error("a copy naming no sandbox should be refused")
	}
}

func TestFakeCopyResolvesFromThePathNotAnArgument(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	if _, err := f.Create(ctx, Spec{Name: "demo"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := f.Copy(ctx, Path{Sandbox: "demo", Path: "/a"}, Path{Path: "/tmp/a"}); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	// the name in the path is load-bearing: an unknown one must not silently
	// fall through to some other sandbox.
	if err := f.Copy(ctx, Path{Sandbox: "nope", Path: "/a"}, Path{Path: "/tmp/a"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Copy from an unknown sandbox = %v, want ErrNotFound", err)
	}
}

func TestFakeLifecycle(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	if _, err := f.Create(ctx, Spec{Name: "demo", Workspaces: []Workspace{{Host: "/a"}}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.Create(ctx, Spec{Name: "demo"}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate Create = %v, want ErrExists", err)
	}

	if err := f.Start(ctx, ByName("demo")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := f.Remove(ctx, ByName("demo"), false); !errors.Is(err, ErrRunning) {
		t.Errorf("Remove of a running sandbox = %v, want ErrRunning", err)
	}
	if err := f.Remove(ctx, ByName("demo"), true); err != nil {
		t.Fatalf("Remove --force: %v", err)
	}

	all, err := f.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("List = %v, want empty", all)
	}
}

func TestFakeResolvesByPath(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	if _, err := f.Create(ctx, Spec{Name: "demo", Workspaces: []Workspace{{Host: "/home/viv/demo"}}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	sb, err := f.Inspect(ctx, ByPath("/home/viv/demo"))
	if err != nil {
		t.Fatalf("Inspect by path: %v", err)
	}
	if sb.Spec.Name != "demo" {
		t.Errorf("Inspect by path found %q, want demo", sb.Spec.Name)
	}
	if _, err := f.Inspect(ctx, ByName("nope")); !errors.Is(err, ErrNotFound) {
		t.Errorf("Inspect of an unknown ref = %v, want ErrNotFound", err)
	}
}

func TestFakeErrOverridesEverything(t *testing.T) {
	sentinel := errors.New("boom")
	f := NewFake()
	f.Err = sentinel
	ctx := context.Background()

	if _, err := f.List(ctx); !errors.Is(err, sentinel) {
		t.Errorf("List = %v, want the injected error", err)
	}
	if err := f.Stop(ctx, ByName("demo")); !errors.Is(err, sentinel) {
		t.Errorf("Stop = %v, want the injected error", err)
	}
}

func TestFakeListIsACopy(t *testing.T) {
	// a caller mutating the listing must not corrupt the fake's world.
	f := NewFake(Sandbox{Spec: Spec{Name: "demo"}})
	got, err := f.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got[0].Spec.Name = "clobbered"
	if f.Sandboxes[0].Spec.Name != "demo" {
		t.Error("List returned the fake's own slice")
	}
}

func TestPortAddress(t *testing.T) {
	for _, tc := range []struct {
		name string
		port Port
		want string
	}{
		{"the ordinary case", Port{Host: 8080, Sandbox: 80}, "8080:80"},
		// anything unauthenticated has to stay on this machine. Without the
		// bind address a runtime publishes on every interface, which offers it
		// to the whole network the host is on.
		{"bound to loopback", Port{Host: 9418, Sandbox: 9418, Bind: "127.0.0.1"}, "127.0.0.1:9418:9418"},
		{"a protocol that is not the default", Port{Host: 53, Sandbox: 53, Proto: "udp"}, "53:53/udp"},
		{"tcp is left implicit", Port{Host: 80, Sandbox: 80, Proto: "tcp"}, "80:80"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.port.Address(); got != tc.want {
				t.Errorf("Address() = %q, want %q", got, tc.want)
			}
		})
	}
}
