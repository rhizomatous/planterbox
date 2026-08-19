package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/proxy"
)

// sandbox builds a listing entry.
func sandbox(name string, status api.Status) api.Sandbox {
	return api.Sandbox{
		Spec: api.Spec{
			Name:       name,
			Agent:      "claude",
			Image:      "base:1",
			Workspaces: []api.Workspace{{Host: "/home/viv/" + name}},
		},
		State: api.State{Status: status},
	}
}

// press sends a keypress and returns the updated model.
//
// It also runs whatever command the update produced and feeds the result back,
// the way bubbletea's loop does. Dropping the command would make every test
// pass whether or not the key was wired to anything, since starting, stopping,
// removing, and creating all happen inside one.
func press(t *testing.T, m *Model, key string) *Model {
	t.Helper()
	next, cmd := m.Update(tea.KeyPressMsg{Code: keyCode(key), Text: key})
	got, ok := next.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *Model", next)
	}
	msg, ok := runCmd(cmd)
	if !ok || msg == nil {
		return got
	}
	next, _ = got.Update(msg)
	if got, ok = next.(*Model); !ok {
		t.Fatalf("Update returned %T, want *Model", next)
	}
	return got
}

// runCmd runs a command, giving up on one that does not finish promptly.
//
// Everything worth asserting on resolves immediately against a fake service.
// The commands that don't are timers (the form's cursor blink, the list
// refresh), and waiting out their intervals would cost seconds per keypress
// while telling us nothing.
func runCmd(cmd tea.Cmd) (tea.Msg, bool) {
	if cmd == nil {
		return nil, false
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg, true
	case <-time.After(100 * time.Millisecond):
		return nil, false
	}
}

// keyCode maps the keys these tests use onto their rune, which is what
// KeyPressMsg.String reports for printable keys.
func keyCode(key string) rune {
	switch key {
	case "up":
		return tea.KeyUp
	case "down":
		return tea.KeyDown
	case "enter":
		return tea.KeyEnter
	default:
		return []rune(key)[0]
	}
}

// loaded returns a model already holding a listing.
func loaded(t *testing.T, svc api.Service, sandboxes ...api.Sandbox) *Model {
	t.Helper()
	m := New(context.Background(), svc)
	next, _ := m.Update(sandboxesMsg(sandboxes))
	return next.(*Model)
}

// view renders without styling, for assertions on content.
func view(m *Model) string { return ansi.Strip(m.render()) }

// detail renders the pane alone, so an assertion about it can't be satisfied
// by the list row above it, which carries the name and workspace too.
func detail(m *Model) string { return ansi.Strip(m.renderDetail(m.renderList())) }

func TestEmptyStateSaysHowToMakeOne(t *testing.T) {
	m := loaded(t, api.NewFake())
	if !strings.Contains(view(m), "plbx run") {
		t.Errorf("empty dashboard should say how to make a sandbox:\n%s", view(m))
	}
}

func TestListingRendersEachSandbox(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("alpha", api.StatusRunning), sandbox("beta", api.StatusStopped))
	out := view(m)
	for _, want := range []string{"alpha", "running", "beta", "stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

func TestCursorMovesAndStopsAtTheEnds(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusStopped), sandbox("b", api.StatusStopped))

	if m.cursor != 0 {
		t.Fatalf("cursor starts at %d, want 0", m.cursor)
	}
	m = press(t, m, "up")
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want it held at the top", m.cursor)
	}
	m = press(t, m, "down")
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.cursor)
	}
	m = press(t, m, "down")
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want it held at the bottom", m.cursor)
	}
	// up from the bottom must actually move, or the earlier assertion only
	// proved that an unrecognised key does nothing.
	m = press(t, m, "up")
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want up to have moved it back to 0", m.cursor)
	}
}

func TestVimKeysMatchTheArrows(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusStopped), sandbox("b", api.StatusStopped))
	if m = press(t, m, "j"); m.cursor != 1 {
		t.Errorf("j: cursor = %d, want 1", m.cursor)
	}
	if m = press(t, m, "k"); m.cursor != 0 {
		t.Errorf("k: cursor = %d, want 0", m.cursor)
	}
}

func TestRunningSandboxShowsItsLoad(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusRunning))
	next, _ := m.Update(statsMsg{name: "a", sample: api.Stats{
		CPUPercent: 42, MemoryBytes: 2 << 30, MemoryLimit: 8 << 30,
	}})
	out := view(next.(*Model))

	for _, want := range []string{"42% cpu", "2GiB", "8GiB"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

func TestStatsAreDroppedWhenASandboxStops(t *testing.T) {
	// otherwise a stopped sandbox keeps displaying the load it had when it
	// stopped, which reads as though it were still working.
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusRunning))
	next, _ := m.Update(statsMsg{name: "a", sample: api.Stats{CPUPercent: 42, MemoryBytes: 1 << 30, MemoryLimit: 8 << 30}})
	m = next.(*Model)
	if !strings.Contains(view(m), "42% cpu") {
		t.Fatal("expected the sample to render while running")
	}

	next, _ = m.Update(sandboxesMsg([]api.Sandbox{sandbox("a", api.StatusStopped)}))
	m = next.(*Model)
	if strings.Contains(view(m), "42% cpu") {
		t.Errorf("a stopped sandbox should not still show its old load:\n%s", view(m))
	}
}

func TestGaugeSaturatesAboveOneHundredPercent(t *testing.T) {
	// a multi-core sandbox reports well over 100%; the bar must not overflow
	// its column and wreck the layout.
	wide := gauge(450)
	if got := lipglossWidth(wide); got != gaugeWidth {
		t.Errorf("gauge(450) is %d cells wide, want %d", got, gaugeWidth)
	}
	if got := lipglossWidth(gauge(-5)); got != gaugeWidth {
		t.Errorf("gauge(-5) is %d cells wide, want %d", got, gaugeWidth)
	}
}

func TestCursorStaysOnTheSameSandboxAcrossRefreshes(t *testing.T) {
	// the list re-reads every couple of seconds; the cursor jumping because a
	// sandbox above it disappeared would make the dashboard unusable.
	m := loaded(t, api.NewFake(),
		sandbox("a", api.StatusStopped),
		sandbox("b", api.StatusStopped),
		sandbox("c", api.StatusStopped))
	m = press(t, m, "down")
	m = press(t, m, "down")
	if m.selectedName() != "c" {
		t.Fatalf("selected %q, want c", m.selectedName())
	}

	// "a" goes away; the cursor should follow "c", not the index.
	next, _ := m.Update(sandboxesMsg([]api.Sandbox{
		sandbox("b", api.StatusStopped),
		sandbox("c", api.StatusStopped),
	}))
	m = next.(*Model)
	if m.selectedName() != "c" {
		t.Errorf("selected %q after refresh, want c", m.selectedName())
	}
}

func TestCursorClampsWhenTheSelectedSandboxDisappears(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusStopped), sandbox("b", api.StatusStopped))
	m = press(t, m, "down")

	next, _ := m.Update(sandboxesMsg([]api.Sandbox{sandbox("a", api.StatusStopped)}))
	m = next.(*Model)
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want it clamped into range", m.cursor)
	}
	if _, ok := m.selected(); !ok {
		t.Error("a non-empty listing should always have a selection")
	}
}

func TestSelectionOnAnEmptyListingIsSafe(t *testing.T) {
	m := loaded(t, api.NewFake())
	if _, ok := m.selected(); ok {
		t.Error("an empty listing has nothing selected")
	}
	// none of these should panic with nothing to act on.
	for _, key := range []string{"up", "down", "enter", "x", "s", "r"} {
		m = press(t, m, key)
	}
}

func TestEnterAsksToAttachTheSandboxsOwnAgent(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusRunning), sandbox("b", api.StatusStopped))
	m = press(t, m, "down")
	m = press(t, m, "enter")

	got := m.Attach()
	if got == nil {
		t.Fatal("enter should have asked for a session")
	}
	if got.Sandbox != "b" {
		t.Errorf("sandbox = %q, want the selected one", got.Sandbox)
	}
	if len(got.Cmd) == 0 || got.Cmd[0] != "claude" {
		t.Errorf("cmd = %v, want the sandbox's own agent", got.Cmd)
	}
	if got.Workdir != "/home/viv/b" {
		t.Errorf("workdir = %q, want the primary workspace", got.Workdir)
	}
}

func TestEnterOnAStoppedSandboxStillAttaches(t *testing.T) {
	// starting it is the caller's job; refusing here would make Enter work on
	// some rows and not others for no reason the user can see.
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusStopped))
	m = press(t, m, "enter")
	if m.Attach() == nil {
		t.Error("enter should attach a stopped sandbox too")
	}
}

func TestShellKeyAsksForAShell(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusRunning))
	m = press(t, m, "x")
	got := m.Attach()
	if got == nil || got.Cmd[0] != "bash" {
		t.Errorf("attach = %+v, want a shell", got)
	}
}

func TestToggleStartsAStoppedSandbox(t *testing.T) {
	fake := api.NewFake(sandbox("a", api.StatusStopped))
	m := loaded(t, fake, fake.Sandboxes...)

	m2, cmd := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	m = m2.(*Model)
	if m.pending != "a" {
		t.Errorf("pending = %q, want the row to show work in flight", m.pending)
	}
	if cmd == nil {
		t.Fatal("pressing s should have produced a command")
	}
	msg := cmd()
	if got, ok := msg.(actionMsg); !ok || got.err != nil || got.verb != "started" {
		t.Fatalf("action = %+v, want a successful start", msg)
	}
	if fake.Sandboxes[0].State.Status != api.StatusRunning {
		t.Errorf("status = %q, want running", fake.Sandboxes[0].State.Status)
	}
}

func TestToggleStopsARunningSandbox(t *testing.T) {
	fake := api.NewFake(sandbox("a", api.StatusRunning))
	m := loaded(t, fake, fake.Sandboxes...)

	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if got, ok := cmd().(actionMsg); !ok || got.verb != "stopped" {
		t.Fatalf("action = %+v, want a stop", got)
	}
	if fake.Sandboxes[0].State.Status != api.StatusStopped {
		t.Errorf("status = %q, want stopped", fake.Sandboxes[0].State.Status)
	}
}

func TestRemoveRefusesARunningSandboxRatherThanForcing(t *testing.T) {
	// the service's guard exists so one keystroke cannot destroy a live
	// session. Passing force from the dashboard would defeat it.
	fake := api.NewFake(sandbox("a", api.StatusRunning))
	m := loaded(t, fake, fake.Sandboxes...)

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	got, ok := cmd().(actionMsg)
	if !ok || got.err == nil {
		t.Fatalf("action = %+v, want a refusal", got)
	}
	if !strings.Contains(got.err.Error(), "stop it first") {
		t.Errorf("err = %v, want it to say what to do instead", got.err)
	}
	if len(fake.Sandboxes) != 1 {
		t.Error("the sandbox should still exist")
	}
}

func TestRemoveDeletesAStoppedSandbox(t *testing.T) {
	fake := api.NewFake(sandbox("a", api.StatusStopped))
	m := loaded(t, fake, fake.Sandboxes...)

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if got, ok := cmd().(actionMsg); !ok || got.err != nil {
		t.Fatalf("action = %+v, want a successful removal", got)
	}
	if len(fake.Sandboxes) != 0 {
		t.Error("the sandbox should be gone")
	}
}

func TestKeysAreIgnoredWhileAnActionIsInFlight(t *testing.T) {
	// a second keypress must not race the first against the same sandbox.
	fake := api.NewFake(sandbox("a", api.StatusStopped), sandbox("b", api.StatusStopped))
	m := loaded(t, fake, fake.Sandboxes...)

	m2, _ := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	m = m2.(*Model)

	before := m.cursor
	m = press(t, m, "down")
	if m.cursor != before {
		t.Error("the cursor should not move while an action is pending")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"}); cmd != nil {
		t.Error("a second action should not start while one is pending")
	}
}

func TestQuitAndHelpWorkEvenWhilePending(t *testing.T) {
	fake := api.NewFake(sandbox("a", api.StatusStopped))
	m := loaded(t, fake, fake.Sandboxes...)
	m2, _ := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	m = m2.(*Model)

	m = press(t, m, "?")
	if !m.showHelp {
		t.Error("help should toggle even with an action pending")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); cmd == nil {
		t.Error("q should quit even with an action pending")
	}
}

func TestActionFailureIsShownRatherThanSwallowed(t *testing.T) {
	fake := api.NewFake(sandbox("a", api.StatusStopped))
	fake.Err = errors.New("daemon unreachable")
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusStopped))
	m.svc = fake

	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	next, _ := m.Update(cmd())
	m = next.(*Model)

	if !strings.Contains(view(m), "daemon unreachable") {
		t.Errorf("a failed action should be visible:\n%s", view(m))
	}
	if m.pending != "" {
		t.Error("a failed action should clear the pending marker")
	}
}

func TestListingErrorIsShown(t *testing.T) {
	m := New(context.Background(), api.NewFake())
	next, _ := m.Update(errMsg{errors.New("no runtime")})
	if !strings.Contains(view(next.(*Model)), "no runtime") {
		t.Errorf("a listing failure should be visible:\n%s", view(next.(*Model)))
	}
}

func TestDetailPaneIsClosedUntilAskedFor(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusStopped))
	if strings.Contains(view(m), "base:1") {
		t.Errorf("the list should not show the image until details are open:\n%s", view(m))
	}
}

func TestDetailPaneShowsTheSelectedSandboxsDefinition(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusRunning))
	m = press(t, m, "i")

	out := detail(m)
	for _, want := range []string{"status", "running", "claude", "base:1", "/home/viv/a"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail pane missing %q:\n%s", want, out)
		}
	}
}

func TestDetailPaneFollowsTheCursor(t *testing.T) {
	// the pane reads the selection on every render, rather than pinning to
	// whatever was selected when it opened.
	m := loaded(t, api.NewFake(), sandbox("alpha", api.StatusStopped), sandbox("beta", api.StatusStopped))
	m = press(t, m, "i")
	if !strings.Contains(detail(m), "/home/viv/alpha") {
		t.Fatalf("detail pane should open on the selected sandbox:\n%s", detail(m))
	}

	m = press(t, m, "down")
	out := detail(m)
	if !strings.Contains(out, "/home/viv/beta") {
		t.Errorf("detail pane should follow the cursor:\n%s", out)
	}
	if strings.Contains(out, "/home/viv/alpha") {
		t.Errorf("detail pane should have left the previous sandbox behind:\n%s", out)
	}
}

func TestDetailPaneClosesOnASecondPress(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusStopped))
	m = press(t, m, "i")
	m = press(t, m, "i")
	if strings.Contains(view(m), "base:1") {
		t.Errorf("a second press should close the pane:\n%s", view(m))
	}
}

func TestDetailPaneAgesAgainstTheModelsClock(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusStopped))
	m.sandboxes[0].Spec.CreatedAt = time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC) }

	if out := view(press(t, m, "i")); !strings.Contains(out, "3 hours ago") {
		t.Errorf("detail pane should date the sandbox:\n%s", out)
	}
}

func TestDetailPaneOpensEvenWhilePending(t *testing.T) {
	// it changes nothing about a sandbox, so it is not what the pending guard
	// is there to stop.
	fake := api.NewFake(sandbox("a", api.StatusStopped))
	m := loaded(t, fake, fake.Sandboxes...)
	m2, _ := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	m = m2.(*Model)

	if m = press(t, m, "i"); !m.showDetail {
		t.Error("details should toggle even with an action pending")
	}
}

func TestDetailPaneOnAnEmptyListingIsSafe(t *testing.T) {
	m := loaded(t, api.NewFake())
	m = press(t, m, "i")
	if !strings.Contains(view(m), "plbx run") {
		t.Errorf("an empty dashboard should still say how to make a sandbox:\n%s", view(m))
	}
}

func TestHelpListsEveryBinding(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusStopped))
	m = press(t, m, "?")
	out := view(m)
	for _, k := range Keys {
		if !strings.Contains(out, k.Help) {
			t.Errorf("help missing %q:\n%s", k.Help, out)
		}
	}
}

// lipglossWidth measures a styled string in terminal cells.
func lipglossWidth(s string) int { return lipgloss.Width(s) }

// decision builds a proxy decision for the network panel.
func decision(seq uint64, host string, allowed bool) proxy.Entry {
	reason := "denied by default"
	if allowed {
		reason = "allowed by rule " + host
	}
	return proxy.Entry{
		Seq:     seq,
		At:      time.Now(),
		Target:  proxy.Target{Host: host, Port: 443},
		Allowed: allowed,
		Reason:  reason,
		Sandbox: "web",
	}
}

// networked returns a model showing the network panel, loaded with decisions.
func networked(t *testing.T, fake *api.Fake) *Model {
	t.Helper()
	m := loaded(t, fake, fake.Sandboxes...)
	next, _ := m.Update(connectionsMsg(fake.Decisions))
	m = next.(*Model)
	return press(t, m, "tab")
}

func TestTabSwitchesPanels(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("a", api.StatusRunning))
	if m.panel != sandboxPanel {
		t.Fatal("the dashboard should open on the sandbox list")
	}
	m = press(t, m, "tab")
	if m.panel != networkPanel {
		t.Error("tab should reach the network panel")
	}
	m = press(t, m, "tab")
	if m.panel != sandboxPanel {
		t.Error("tab should come back")
	}
}

func TestNetworkPanelShowsDecisions(t *testing.T) {
	fake := api.NewFake()
	fake.Decisions = []proxy.Entry{decision(1, "ok.test", true), decision(2, "no.test", false)}

	out := view(networked(t, fake))
	for _, want := range []string{"ok.test:443", "no.test:443", "web", "denied by default"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel missing %q:\n%s", want, out)
		}
	}
}

func TestDeniedCountIsOnTheTab(t *testing.T) {
	// the count is what makes the panel worth switching to without looking.
	fake := api.NewFake()
	fake.Decisions = []proxy.Entry{decision(1, "ok.test", true), decision(2, "no.test", false)}

	m := loaded(t, fake, sandbox("a", api.StatusRunning))
	next, _ := m.Update(connectionsMsg(fake.Decisions))
	if out := view(next.(*Model)); !strings.Contains(out, "1 denied") {
		t.Errorf("the sandbox view should carry the denial count:\n%s", out)
	}
}

func TestAllowFromThePanelWritesARule(t *testing.T) {
	// the loop this panel exists for: a denial on screen, one key, fixed.
	fake := api.NewFake()
	fake.NetworkPolicy = &proxy.Policy{Preset: proxy.PresetBalanced}
	fake.Decisions = []proxy.Entry{decision(1, "blocked.test", false)}

	m := networked(t, fake)
	m = press(t, m, "a")

	if fake.NetworkPolicy == nil || len(fake.NetworkPolicy.Rules) != 1 {
		t.Fatalf("policy = %+v, want one rule", fake.NetworkPolicy)
	}
	rule := fake.NetworkPolicy.Rules[0]
	if !rule.Allow || rule.Pattern != "blocked.test" {
		t.Errorf("rule = %+v, want an allow for the host", rule)
	}
	// pinning the port would leave the same host blocked on 80, which reads
	// as the allow having silently not worked.
	if strings.Contains(rule.Pattern, ":") {
		t.Errorf("rule %q should name the host without its port", rule.Pattern)
	}
	if !strings.Contains(view(m), "allowed blocked.test") {
		t.Errorf("the outcome should be shown:\n%s", view(m))
	}
}

func TestDenyFromThePanelWritesARule(t *testing.T) {
	fake := api.NewFake()
	fake.NetworkPolicy = &proxy.Policy{Preset: proxy.PresetOpen}
	fake.Decisions = []proxy.Entry{decision(1, "sketchy.test", true)}

	m := networked(t, fake)
	_ = press(t, m, "d")

	if len(fake.NetworkPolicy.Rules) != 1 || fake.NetworkPolicy.Rules[0].Allow {
		t.Errorf("rules = %+v, want a deny", fake.NetworkPolicy.Rules)
	}
}

func TestAllowingSomethingPreviouslyDeniedReplacesTheRule(t *testing.T) {
	// the deny would otherwise keep winning and the keystroke would appear to
	// have done nothing.
	fake := api.NewFake()
	fake.NetworkPolicy = &proxy.Policy{
		Preset: proxy.PresetBalanced,
		Rules:  []proxy.Rule{{Pattern: "x.test"}},
	}
	fake.Decisions = []proxy.Entry{decision(1, "x.test", false)}

	m := networked(t, fake)
	_ = press(t, m, "a")

	if len(fake.NetworkPolicy.Rules) != 1 {
		t.Fatalf("rules = %+v, want the deny replaced", fake.NetworkPolicy.Rules)
	}
	if v := fake.NetworkPolicy.Check(proxy.Target{Host: "x.test", Port: 443}); !v.Allowed {
		t.Errorf("x.test still denied after allowing it: %s", v.Reason)
	}
}

func TestPanelKeysDoNotLeakIntoTheSandboxList(t *testing.T) {
	// `a` and `d` belong to the network panel; on the list they must not act.
	fake := api.NewFake(sandbox("a", api.StatusStopped))
	fake.NetworkPolicy = &proxy.Policy{Preset: proxy.PresetBalanced}
	m := loaded(t, fake, fake.Sandboxes...)

	_ = press(t, m, "a")
	_ = press(t, m, "d")
	if len(fake.NetworkPolicy.Rules) != 0 {
		t.Errorf("the sandbox list wrote a policy rule: %+v", fake.NetworkPolicy.Rules)
	}
}

func TestConnectionCursorStaysPutAsEntriesArrive(t *testing.T) {
	// a denial should not slide away while it is being read.
	fake := api.NewFake()
	fake.Decisions = []proxy.Entry{decision(1, "a.test", false), decision(2, "b.test", false)}

	m := networked(t, fake)
	m = press(t, m, "up")
	selected := m.selectedConnection()

	next, _ := m.Update(connectionsMsg([]proxy.Entry{decision(3, "c.test", false)}))
	m = next.(*Model)

	if m.selectedConnection() != selected {
		t.Errorf("the cursor moved to %d, want it left on %d", m.selectedConnection(), selected)
	}
}

func TestEmptyNetworkPanelSaysSo(t *testing.T) {
	m := press(t, loaded(t, api.NewFake(), sandbox("a", api.StatusRunning)), "tab")
	if !strings.Contains(view(m), "nothing has tried") {
		t.Errorf("an empty panel should say so:\n%s", view(m))
	}
}
