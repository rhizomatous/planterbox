package direct

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/runner"
	"github.com/rhizomatous/planterbox/internal/store"
)

var epoch = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

func testService(t *testing.T) (*Service, *runner.Fake) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sandboxes"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	rn := runner.NewFake()
	return New(st, rn, WithClock(func() time.Time { return epoch })), rn
}

func spec(name string) api.Spec {
	return api.Spec{
		Name:       name,
		Image:      "base:1",
		Workspaces: []api.Workspace{{Host: "/home/viv/" + name}},
	}
}

func TestListEmpty(t *testing.T) {
	svc, _ := testService(t)
	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List = %v, want empty", got)
	}
}

func TestCreateStampsAndPersists(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()

	sb, err := svc.Create(ctx, spec("demo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !sb.Spec.CreatedAt.Equal(epoch) {
		t.Errorf("created_at = %v, want the injected clock's time", sb.Spec.CreatedAt)
	}
	if sb.State.Status != api.StatusCreated {
		t.Errorf("status = %q, want created", sb.State.Status)
	}
	if _, ok := rn.States["plbx-demo"]; !ok {
		t.Error("Create should have built the container through the runner")
	}

	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].Spec.Name != "demo" {
		t.Errorf("List = %v, want the created sandbox", all)
	}
}

func TestCreateRejectsDuplicateName(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Create(ctx, spec("demo")); !errors.Is(err, api.ErrExists) {
		t.Errorf("err = %v, want ErrExists", err)
	}
}

func TestCreateRejectsBadNameBeforeTouchingRuntime(t *testing.T) {
	svc, rn := testService(t)
	if _, err := svc.Create(context.Background(), spec("../escape")); err == nil {
		t.Fatal("Create with an unsafe name should fail")
	}
	if len(rn.Calls) != 0 {
		t.Errorf("runner was called %v, want validation to reject first", rn.Calls)
	}
}

func TestListReportsLiveStatusNotStoredStatus(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// something outside plbx started the container.
	rn.States["plbx-demo"] = api.State{Status: api.StatusRunning, ContainerID: "plbx-demo"}

	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if all[0].State.Status != api.StatusRunning {
		t.Errorf("status = %q, want the runtime's answer to win over the stored one", all[0].State.Status)
	}
}

func TestListFallsBackToStoredStateWhenRuntimeCannotAnswer(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// a stale status beats an error on a command that only wanted to list.
	rn.Err = errors.New("daemon unreachable")
	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List should survive an unreachable runtime: %v", err)
	}
	if len(all) != 1 || all[0].State.Status != api.StatusCreated {
		t.Errorf("List = %v, want the stored status", all)
	}
}

func TestListReportsMissingContainer(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	delete(rn.States, "plbx-demo")

	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if all[0].State.Status != api.StatusMissing {
		t.Errorf("status = %q, want missing when the record outlives the container", all[0].State.Status)
	}
}

func TestStartStopPersistStatus(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Start(ctx, api.ByName("demo")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sb, err := svc.Inspect(ctx, api.ByName("demo"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if sb.State.Status != api.StatusRunning {
		t.Errorf("status after Start = %q, want running", sb.State.Status)
	}

	if err := svc.Stop(ctx, api.ByName("demo")); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if sb, _ = svc.Inspect(ctx, api.ByName("demo")); sb.State.Status != api.StatusStopped {
		t.Errorf("status after Stop = %q, want stopped", sb.State.Status)
	}
}

func TestInspectByPath(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	sb, err := svc.Inspect(ctx, api.ByPath("/home/viv/demo"))
	if err != nil {
		t.Fatalf("Inspect by path: %v", err)
	}
	if sb.Spec.Name != "demo" {
		t.Errorf("Inspect by path found %q, want demo", sb.Spec.Name)
	}
}

func TestCopyRoutesToTheSandboxNamedInThePath(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()
	for _, name := range []string{"alpha", "beta"} {
		if _, err := svc.Create(ctx, spec(name)); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	// drop beta's container so the two sandboxes are distinguishable: a copy
	// that reaches beta fails, one that reaches alpha succeeds.
	delete(rn.States, "plbx-beta")
	host := api.Path{Path: "/tmp/a"}

	if err := svc.Copy(ctx, api.Path{Sandbox: "alpha", Path: "/home/agent/a"}, host); err != nil {
		t.Errorf("copy naming alpha should reach alpha's container: %v", err)
	}
	if err := svc.Copy(ctx, api.Path{Sandbox: "beta", Path: "/home/agent/a"}, host); err == nil {
		t.Error("copy naming beta reached a container that beta does not have")
	}
}

func TestCopyRejectsAMalformedPair(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	rn.Calls = nil

	if err := svc.Copy(ctx, api.Path{Path: "/a"}, api.Path{Path: "/b"}); err == nil {
		t.Error("a copy naming no sandbox should be refused")
	}
	if len(rn.Calls) != 0 {
		t.Errorf("runner was called %v, want validation to reject first", rn.Calls)
	}
}

func TestCopyToAnUnknownSandboxIsErrNotFound(t *testing.T) {
	svc, _ := testService(t)
	err := svc.Copy(context.Background(), api.Path{Path: "/tmp/a"}, api.Path{Sandbox: "nope", Path: "/a"})
	if !errors.Is(err, api.ErrNotFound) {
		t.Errorf("err = %v, want api.ErrNotFound", err)
	}
}

func TestStatsResolvesTheRefBeforeStreaming(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	rn.Calls = nil

	ch, err := svc.Stats(ctx, api.ByPath("/home/viv/demo"))
	if err != nil {
		t.Fatalf("Stats by path: %v", err)
	}
	for range ch { //nolint:revive // draining is the point
	}
	if len(rn.Calls) != 1 || rn.Calls[0] != "Stats" {
		t.Errorf("runner calls = %v, want a single Stats", rn.Calls)
	}

	if _, err := svc.Stats(ctx, api.ByName("nope")); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("Stats of an unknown sandbox = %v, want api.ErrNotFound", err)
	}
}

func TestUnknownRefIsErrNotFound(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	if _, err := svc.Inspect(ctx, api.ByName("nope")); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("Inspect err = %v, want api.ErrNotFound", err)
	}
	if err := svc.Start(ctx, api.ByName("nope")); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("Start err = %v, want api.ErrNotFound", err)
	}
	if err := svc.Remove(ctx, api.ByName("nope"), false); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("Remove err = %v, want api.ErrNotFound", err)
	}
}

func TestRemoveRefusesRunningSandboxWithoutForce(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Start(ctx, api.ByName("demo")); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := svc.Remove(ctx, api.ByName("demo"), false); !errors.Is(err, api.ErrRunning) {
		t.Fatalf("Remove err = %v, want api.ErrRunning", err)
	}
	if _, err := svc.Inspect(ctx, api.ByName("demo")); err != nil {
		t.Errorf("the refused sandbox should still exist: %v", err)
	}

	if err := svc.Remove(ctx, api.ByName("demo"), true); err != nil {
		t.Fatalf("Remove --force: %v", err)
	}
	if _, err := svc.Inspect(ctx, api.ByName("demo")); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("Inspect after force-remove = %v, want api.ErrNotFound", err)
	}
}

func TestRemoveDropsRecordAndContainerTogether(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Remove(ctx, api.ByName("demo"), false); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, ok := rn.States["plbx-demo"]; ok {
		t.Error("Remove left the container behind")
	}
	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("List = %v, want empty after Remove", all)
	}
}

func TestOpenWithoutRuntimeStillLists(t *testing.T) {
	// a machine with no docker installed can still read its own records.
	dir := filepath.Join(t.TempDir(), "sandboxes")
	t.Setenv("PLBX_RUNTIME", "definitely-not-a-runtime")

	svc, err := Open(context.Background(), Options{StateDir: dir})
	if err != nil {
		t.Fatalf("Open should tolerate a missing runtime: %v", err)
	}
	defer func() { _ = svc.Close() }()

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List = %v, want empty", got)
	}

	// but anything needing the runtime says so.
	if _, err := svc.Create(context.Background(), spec("demo")); err == nil {
		t.Error("Create without a runtime should fail")
	}
}

func TestPublishRecordsWithoutStartingAForwarder(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	ports := []api.Port{{Host: 8080, Sandbox: 80}}
	if err := svc.Publish(ctx, api.ByName("demo"), ports); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// recorded, and it survives a reread.
	sb, err := svc.Inspect(ctx, api.ByName("demo"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(sb.Ports) != 1 || sb.Ports[0] != ports[0] {
		t.Errorf("ports = %+v, want %+v", sb.Ports, ports)
	}
	// but nothing was published: a forwarder standing by for a sandbox that is
	// not running would hold the host port against it.
	if _, ok := rn.Published["demo"]; ok {
		t.Error("a stopped sandbox had its ports bound on the host")
	}
}

func TestPublishOnARunningSandboxTakesEffectAtOnce(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Start(ctx, api.ByName("demo")); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ports := []api.Port{{Host: 8080, Sandbox: 80}}
	if err := svc.Publish(ctx, api.ByName("demo"), ports); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := rn.Published["demo"]; len(got) != 1 || got[0] != ports[0] {
		t.Errorf("published = %+v, want %+v", got, ports)
	}
}

// Ports are recorded on the sandbox, so a start has to put them back.
func TestStartRepublishesAndStopTakesThemDown(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Publish(ctx, api.ByName("demo"), []api.Port{{Host: 8080, Sandbox: 80}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if err := svc.Start(ctx, api.ByName("demo")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(rn.Published["demo"]) != 1 {
		t.Errorf("published = %+v, want the recorded ports back on the host", rn.Published["demo"])
	}

	if err := svc.Stop(ctx, api.ByName("demo")); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, ok := rn.Published["demo"]; ok {
		t.Error("a stopped sandbox still holds its host ports")
	}
	// the record keeps them, so the next start puts them back.
	sb, err := svc.Inspect(ctx, api.ByName("demo"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(sb.Ports) != 1 {
		t.Errorf("ports = %+v, want them still recorded after a stop", sb.Ports)
	}
}

// A sandbox whose ports could not be published has still started, and the
// record has to say so, or the next command acts on a stale status.
func TestStartRecordsRunningEvenWhenPublishingFails(t *testing.T) {
	svc, rn := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Publish(ctx, api.ByName("demo"), []api.Port{{Host: 8080, Sandbox: 80}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	rn.PublishErr = errors.New("port is already allocated")
	err := svc.Start(ctx, api.ByName("demo"))
	if !errors.Is(err, api.ErrPortsUnavailable) {
		t.Fatalf("Start err = %v, want ErrPortsUnavailable", err)
	}

	rn.PublishErr = nil
	sb, err := svc.Inspect(ctx, api.ByName("demo"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if sb.State.Status != api.StatusRunning {
		t.Errorf("status = %q, want running: the sandbox started, only its ports did not", sb.State.Status)
	}
}

func TestPublishValidates(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, spec("demo")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Publish(ctx, api.ByName("demo"), []api.Port{{Host: 0, Sandbox: 80}}); err == nil {
		t.Error("Publish accepted an out-of-range host port")
	}
}
