package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rhizomatous/planterbox/internal/api"
)

func tempDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	return dir
}

func TestCreateKeyOpensTheForm(t *testing.T) {
	m := loaded(t, api.NewFake())
	if m.create != nil {
		t.Fatal("the form should start closed")
	}
	m = press(t, m, "c")
	if m.create == nil {
		t.Fatal("c should open the form")
	}
	if !strings.Contains(view(m), "workspace") {
		t.Errorf("the open form should be visible:\n%s", view(m))
	}
}

func TestFormOwnsTheKeyboardWhileOpen(t *testing.T) {
	// typing "r" into the workspace field must not remove a sandbox, and "j"
	// must not move the cursor out from under the user.
	fake := api.NewFake(sandbox("a", api.StatusStopped), sandbox("b", api.StatusStopped))
	m := loaded(t, fake, fake.Sandboxes...)
	m = press(t, m, "c")

	for _, key := range []string{"r", "j", "x", "s"} {
		m = press(t, m, key)
	}
	if len(fake.Sandboxes) != 2 {
		t.Error("keys typed into the form acted on the list")
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want it left alone while the form is open", m.cursor)
	}
	if m.Attach() != nil {
		t.Error("typing x into the form should not have asked to attach")
	}
}

// pump keeps running whatever commands the model produces until it goes quiet,
// the way bubbletea's loop does. A form advances between fields by sending
// itself messages, so one command round is not enough to follow it.
func pump(t *testing.T, m *Model, cmd tea.Cmd) *Model {
	t.Helper()
	for range 32 {
		msg, ok := runCmd(cmd)
		if !ok || msg == nil {
			return m
		}
		next, nextCmd := m.Update(msg)
		m = next.(*Model)
		cmd = nextCmd
	}
	return m
}

// enter presses return and follows every message that falls out of it.
func enter(t *testing.T, m *Model) *Model {
	t.Helper()
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*Model)
	return pump(t, m, cmd)
}

func TestFormAdvancesThroughItsFieldsAndSubmits(t *testing.T) {
	// the form moves between fields by sending itself messages, and Init
	// produces one too. Routing only keypresses to it leaves it unstarted and
	// stuck on the first field with enter doing nothing.
	// the form defaults its workspace to the working directory, and huh binds
	// each field's value at init — so the directory has to be right before the
	// form opens, not assigned onto the struct afterwards.
	t.Chdir(tempDir(t, "myrepo"))
	fake := api.NewFake()

	m := loaded(t, fake)
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = pump(t, next.(*Model), cmd)
	if m.create == nil {
		t.Fatal("c should have opened the form")
	}

	// workspace → agent → name → workspace access → submit.
	for i := range 4 {
		if m.create == nil {
			t.Fatalf("the form closed after %d of 4 fields", i)
		}
		m = enter(t, m)
	}

	if m.create != nil {
		t.Fatal("the form should have closed once every field was answered")
	}
	if len(fake.Sandboxes) != 1 {
		t.Fatalf("sandboxes = %+v, want the one the form asked for", fake.Sandboxes)
	}
	if got := fake.Sandboxes[0].Spec.Name; got != "myrepo" {
		t.Errorf("name = %q, want it derived from the workspace", got)
	}
}

func TestEscBacksOutOfTheForm(t *testing.T) {
	// the footer advertises esc, and huh does not bind it itself.
	fake := api.NewFake(sandbox("a", api.StatusStopped))
	m := loaded(t, fake, fake.Sandboxes...)

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = pump(t, next.(*Model), cmd)
	if m.create == nil {
		t.Fatal("c should have opened the form")
	}

	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = pump(t, next.(*Model), cmd)
	if m.create != nil {
		t.Error("esc should have closed the form")
	}
	if len(fake.Sandboxes) != 1 {
		t.Error("backing out should not have created anything")
	}

	// and the list has the keyboard again.
	m = press(t, m, "j")
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want the list to be usable again", m.cursor)
	}
}

func TestFormSpecUsesTheAnswers(t *testing.T) {
	dir := tempDir(t, "myrepo")
	c := newCreateForm(dir)
	c.agent = "codex"

	spec, err := c.spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	if spec.Name != "myrepo" {
		t.Errorf("name = %q, want it derived from the directory", spec.Name)
	}
	if spec.Agent != "codex" {
		t.Errorf("agent = %q, want codex", spec.Agent)
	}
	if len(spec.Workspaces) != 1 || spec.Workspaces[0].Host != dir {
		t.Errorf("workspaces = %+v, want the given directory", spec.Workspaces)
	}
	if spec.Image == "" {
		t.Error("image should come from the chosen agent")
	}
	if err := spec.Validate(); err != nil {
		t.Errorf("the form produced a spec the service would reject: %v", err)
	}
}

func TestFormNameOverridesTheDerivedOne(t *testing.T) {
	c := newCreateForm(tempDir(t, "myrepo"))
	c.name = "custom"
	spec, err := c.spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	if spec.Name != "custom" {
		t.Errorf("name = %q, want the one typed in", spec.Name)
	}
}

func TestFormResolvesARelativeWorkspace(t *testing.T) {
	// the sandbox binds the workspace at its host path, which has to be absolute.
	c := newCreateForm(".")
	spec, err := c.spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	if !filepath.IsAbs(spec.Workspaces[0].Host) {
		t.Errorf("workspace = %q, want an absolute path", spec.Workspaces[0].Host)
	}
}

func TestWorkspaceValidation(t *testing.T) {
	dir := tempDir(t, "myrepo")
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("writing a file: %v", err)
	}

	if err := validWorkspace(dir); err != nil {
		t.Errorf("a real directory should validate: %v", err)
	}
	for _, bad := range []string{"", "/definitely/not/here", file} {
		if err := validWorkspace(bad); err == nil {
			t.Errorf("validWorkspace(%q) should have failed", bad)
		}
	}
}

func TestOptionalNameValidation(t *testing.T) {
	// blank is legal and means "derive one"; anything else must be usable.
	if err := validOptionalName(""); err != nil {
		t.Errorf("a blank name is allowed: %v", err)
	}
	if err := validOptionalName("my-repo"); err != nil {
		t.Errorf("a valid name should pass: %v", err)
	}
	for _, bad := range []string{"has space", "-leading", "has/slash"} {
		if err := validOptionalName(bad); err == nil {
			t.Errorf("validOptionalName(%q) should have failed", bad)
		}
	}
}

func TestSubmitCreateGoesThroughTheService(t *testing.T) {
	dir := tempDir(t, "myrepo")
	fake := api.NewFake()
	m := New(fake)

	c := newCreateForm(dir)
	spec, err := c.spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	got, ok := runCreate(t, m, spec).(actionMsg)
	if !ok || got.err != nil {
		t.Fatalf("action = %+v, want a successful create", got)
	}
	if len(fake.Sandboxes) != 1 || fake.Sandboxes[0].Spec.Name != "myrepo" {
		t.Errorf("sandboxes = %+v, want the new one", fake.Sandboxes)
	}
}

func TestSubmitCreateReportsAFailure(t *testing.T) {
	dir := tempDir(t, "myrepo")
	fake := api.NewFake(api.Sandbox{Spec: api.Spec{Name: "myrepo"}})
	m := New(fake)

	spec, err := newCreateForm(dir).spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	got, ok := runCreate(t, m, spec).(actionMsg)
	if !ok || got.err == nil {
		t.Fatalf("action = %+v, want a duplicate-name failure", got)
	}
}

// runCreate drives a create to its outcome the way the update loop does: the
// pull reports itself a line at a time, and the container is built once it
// closes.
func runCreate(t *testing.T, m *Model, spec api.Spec) tea.Msg {
	t.Helper()
	msg := m.submitCreate(spec)()
	for range 100 {
		pull, ok := msg.(pullMsg)
		if !ok {
			return msg
		}
		if pull.done {
			return m.buildSandbox(pull.spec)()
		}
		msg = m.nextPullLine(pull)()
	}
	t.Fatal("a pull that never ends")
	return nil
}

// TestCreateReportsWhatItIsDoing covers the gap this closed: the form used to
// close onto an unchanged list, with nothing to say the work had started.
func TestCreateReportsWhatItIsDoing(t *testing.T) {
	dir := tempDir(t, "myrepo")
	m := loaded(t, api.NewFake())

	spec, err := newCreateForm(dir).spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	m.building, m.buildStep = &spec, "creating…"

	view := view(m)
	if !strings.Contains(view, "myrepo") || !strings.Contains(view, "building") {
		t.Errorf("a sandbox being made should be on screen:\n%s", view)
	}

	// and it goes when the real record arrives, rather than sitting beside it
	m.sandboxes = []api.Sandbox{sandbox("myrepo", api.StatusCreated)}
	if row := m.buildingRow(); row != "" {
		t.Errorf("the placeholder should give way to the real row: %q", row)
	}
}

// an empty list plus a create in flight is not an empty list.
func TestCreateReplacesTheEmptyMessage(t *testing.T) {
	dir := tempDir(t, "myrepo")
	m := loaded(t, api.NewFake())
	spec, err := newCreateForm(dir).spec()
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	m.building = &spec

	if view := view(m); strings.Contains(view, "no sandboxes yet") {
		t.Errorf("a create in flight should replace the empty state:\n%s", view)
	}
}

func TestCreateKeyIsListedInHelp(t *testing.T) {
	m := loaded(t, api.NewFake())
	m = press(t, m, "?")
	if !strings.Contains(view(m), "create") {
		t.Errorf("help should list the create key:\n%s", view(m))
	}
}

// Clone mode is fixed when a sandbox is made, so the dashboard has to be able
// to choose it — and has to default to the same thing the CLI does.
func TestFormDefaultsToADirectMountAndCanChooseClone(t *testing.T) {
	for _, tc := range []struct {
		name      string
		downFirst bool
		want      bool
	}{
		{name: "left alone, the workspace stays writable", want: false},
		{name: "moving down picks clone mode", downFirst: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(tempDir(t, "myrepo"))
			fake := api.NewFake()

			m := loaded(t, fake)
			next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
			m = pump(t, next.(*Model), cmd)

			// workspace → agent → name, then the access field.
			for range 3 {
				m = enter(t, m)
			}
			if tc.downFirst {
				next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				m = pump(t, next.(*Model), cmd)
			}
			enter(t, m) // submits; what it returns is of no further use

			if len(fake.Sandboxes) != 1 {
				t.Fatalf("sandboxes = %+v, want one", fake.Sandboxes)
			}
			if got := fake.Sandboxes[0].Spec.Clone; got != tc.want {
				t.Errorf("clone = %v, want %v", got, tc.want)
			}
		})
	}
}
