// Package tui is plbx's dashboard: the sandbox list, its live resource
// samples, and the keys that act on them.
//
// The model holds an [api.Service] and nothing else. Every action goes through
// it, so the dashboard works against a daemon or an in-process service without
// knowing which.
package tui

import (
	"context"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/proxy"
)

// refreshEvery is how often the sandbox list is re-read. Status changes are the
// only thing it catches, and they are rare, so this is deliberately slower than
// the stats feed.
const refreshEvery = 2 * time.Second

// cursor is a generic position tracker for a list of items.
type cursor[T any] struct {
	items []T
	at    int
}

// clamp keeps the cursor position within valid bounds.
func (c *cursor[T]) clamp() {
	if c.at >= len(c.items) {
		c.at = len(c.items) - 1
	}
	if c.at < 0 {
		c.at = 0
	}
}

// selected returns the item under the cursor, and whether one exists.
func (c *cursor[T]) selected() (T, bool) {
	if c.at < 0 || c.at >= len(c.items) {
		var zero T
		return zero, false
	}
	return c.items[c.at], true
}

// Model is the dashboard's state.
type Model struct {
	svc api.Service
	ctx context.Context // bubbletea gives Model no other way to reach the caller's context

	sandboxes []api.Sandbox
	stats     map[string]api.Stats
	cursor    int

	width, height int
	showHelp      bool
	// showDetail opens the pane under the list, which follows the cursor rather
	// than pinning to the sandbox that was selected when it opened.
	showDetail bool
	// panel is which half of the dashboard has the keyboard.
	panel panel
	// connections is what the proxy has decided, newest last.
	connections []proxy.Entry
	// connCursor is the selected decision, and connSeq the newest one already
	// seen: the feed is polled, so it asks only for what is new.
	connCursor int
	// connFilter narrows the network panel to the selected sandbox. Off by
	// default: the whole point of the panel is noticing a denial you were not
	// looking for, which a filter would hide.
	connFilter bool
	connSeq    uint64
	err        error
	// now measures the detail pane's ages, so tests can pin them.
	now func() time.Time
	// status is a transient line under the list: what just happened, or why it
	// didn't.
	status string
	// pending names the sandbox an action is running against, so the row can
	// say so rather than appearing to have ignored the keypress.
	pending string
	// create is the open new-sandbox form, or nil when the list has focus.
	create *createForm
	// building is the sandbox a create is working on. It has no record yet, so
	// it cannot be marked in the listing the way a running action on an
	// existing sandbox is, and without it the form closes onto a list where
	// nothing happens for as long as the work takes.
	building *api.Spec
	// buildStep is what that work is doing, since fetching an image and
	// making a container are minutes and seconds apart.
	buildStep string
	// focus is a sandbox the next listing should select, rather than keeping
	// whatever was selected before it arrived.
	focus string
	// quitting suppresses a final render of stale state on the way out.
	quitting bool
	// attach, when set, is the sandbox to hand the terminal to on exit.
	attach *AttachRequest
}

// AttachRequest is a session the dashboard wants run in its place: the terminal
// belongs to the agent until it exits.
type AttachRequest struct {
	Sandbox string
	Cmd     []string
	Workdir string
}

// New returns a dashboard over svc.
func New(ctx context.Context, svc api.Service) *Model {
	return &Model{svc: svc, ctx: ctx, stats: map[string]api.Stats{}, now: time.Now}
}

// Attach reports the session the dashboard exited to run, if any.
func (m *Model) Attach() *AttachRequest { return m.attach }

// panel names the two halves of the dashboard.
type panel int

const (
	// sandboxPanel is the list of sandboxes.
	sandboxPanel panel = iota
	// networkPanel is the connection log, where a denial can be allowed.
	networkPanel
)

// connectionsEvery is how often the decision feed is re-read. Faster than the
// sandbox listing: a denial that shows up a beat after the agent stalled is
// the one thing this panel is for.
const connectionsEvery = 700 * time.Millisecond

// message types the dashboard sends itself.
type (
	// sandboxesMsg is a fresh listing.
	sandboxesMsg []api.Sandbox
	// statsMsg is one sample for one sandbox.
	statsMsg struct {
		name   string
		sample api.Stats
	}
	// actionMsg is the outcome of a key that changed something.
	actionMsg struct {
		verb string
		name string
		err  error
		// created marks the one action that brings a sandbox into being.
		created bool
	}
	// pullMsg carries an image fetch in progress: the open channel, so the
	// next line can be read, and the spec to build once it closes. A create
	// is the one action long enough to need reporting, because a first run
	// for an agent fetches an image before it makes anything.
	pullMsg struct {
		spec  api.Spec
		lines <-chan string
		line  string
		done  bool
	}
	// tickMsg asks for another listing.
	tickMsg time.Time
	// connectionsMsg is a batch of decisions newer than what we had.
	connectionsMsg []proxy.Entry
	// connTickMsg asks for another batch.
	connTickMsg time.Time
	// ruleMsg is the outcome of allowing or denying from the panel.
	ruleMsg struct {
		pattern string
		allow   bool
		err     error
	}
	// errMsg is a failure with nowhere better to go.
	errMsg struct{ err error }
)

// Init starts the first listing and the refresh loop.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.list(), tick(), m.readConnections(), connTick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func connTick() tea.Cmd {
	return tea.Tick(connectionsEvery, func(t time.Time) tea.Msg { return connTickMsg(t) })
}

// readConnections asks for decisions newer than the last one seen.
func (m *Model) readConnections() tea.Cmd {
	since := m.connSeq
	return func() tea.Msg {
		entries, err := m.svc.Connections(m.ctx, since)
		if err != nil {
			return nil // a daemon without a proxy simply has nothing to say
		}
		return connectionsMsg(entries)
	}
}

// list re-reads the sandboxes.
func (m *Model) list() tea.Cmd {
	return func() tea.Msg {
		sandboxes, err := m.svc.List(m.ctx)
		if err != nil {
			return errMsg{err}
		}
		return sandboxesMsg(sandboxes)
	}
}

// Update advances the dashboard.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// the dashboard's own messages are handled whatever has focus, so the list
	// keeps refreshing behind an open form.
	switch msg := msg.(type) {
	case sandboxesMsg:
		return m, m.applyListing(msg)

	case statsMsg:
		m.stats[msg.name] = msg.sample
		return m, nil

	case pullMsg:
		if msg.done {
			m.buildStep = "creating…"
			return m, m.buildSandbox(msg.spec)
		}
		if msg.line != "" {
			m.buildStep = msg.line
		}
		return m, m.nextPullLine(msg)

	case actionMsg:
		m.pending = ""
		// a create is the one action that produces something new, so it is
		// the one where the cursor should move: you have just decided this
		// sandbox should exist, and the next thing you do is to it. After a
		// stop or a remove, moving would take the selection off whatever you
		// were working through.
		if msg.created && msg.err == nil {
			m.focus = msg.name
		}
		m.building, m.buildStep = nil, ""
		if msg.err != nil {
			m.status = msg.verb + " " + msg.name + ": " + msg.err.Error()
		} else {
			m.status = msg.verb + " " + msg.name
		}
		return m, m.list()

	case tickMsg:
		return m, tea.Batch(m.list(), tick())

	case connTickMsg:
		return m, tea.Batch(m.readConnections(), connTick())

	case connectionsMsg:
		m.appendConnections(msg)
		return m, nil

	case ruleMsg:
		m.pending = ""
		verb := "denied"
		if msg.allow {
			verb = "allowed"
		}
		if msg.err != nil {
			m.status = verb + " " + msg.pattern + ": " + msg.err.Error()
		} else {
			m.status = verb + " " + msg.pattern
		}
		return m, nil

	case errMsg:
		m.err = msg.err
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.create == nil {
			return m, nil
		}
	}

	// an open form takes everything else. Not just keypresses: it advances
	// between fields by sending itself messages, and its own Init produces one,
	// so filtering to keys leaves it unstarted and stuck on the first field.
	if m.create != nil {
		return m.updateCreate(msg)
	}

	if key, ok := msg.(tea.KeyPressMsg); ok {
		return m.handleKey(key)
	}
	return m, nil
}

// applyListing replaces the sandbox list, keeping the cursor on the same
// sandbox where it can, and starts a stats feed for anything newly running.
func (m *Model) applyListing(sandboxes []api.Sandbox) tea.Cmd {
	selected := m.selectedName()
	// a focus survives listings that do not have it yet. The periodic refresh
	// races a create, and a listing taken before the sandbox existed would
	// otherwise spend the focus on a name it could not find.
	if m.focus != "" {
		if slices.ContainsFunc(sandboxes, func(sb api.Sandbox) bool { return sb.Spec.Name == m.focus }) {
			selected, m.focus = m.focus, ""
		}
	}
	running := make(map[string]bool, len(sandboxes))
	for _, sb := range sandboxes {
		if sb.State.Status == api.StatusRunning {
			running[sb.Spec.Name] = true
		}
	}

	var cmds []tea.Cmd
	for _, sb := range sandboxes {
		// a sandbox that just started needs a feed; one already sampled has one.
		if running[sb.Spec.Name] {
			if _, sampled := m.stats[sb.Spec.Name]; !sampled {
				cmds = append(cmds, m.sample(sb.Spec.Name))
			}
		}
	}
	// drop samples for anything no longer running, so a stopped sandbox does
	// not keep showing the load it had when it stopped.
	for name := range m.stats {
		if !running[name] {
			delete(m.stats, name)
		}
	}

	m.sandboxes = sandboxes
	m.err = nil
	m.restoreCursor(selected)
	return tea.Batch(cmds...)
}

// sample reads a single stats reading for one sandbox.
//
// One reading per command, rather than a long-lived subscription: the feed is
// re-established on the next refresh, which keeps the dashboard's state a plain
// map rather than a set of goroutines to reconcile against the listing.
func (m *Model) sample(name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), refreshEvery)
		defer cancel()

		ch, err := m.svc.Stats(ctx, api.ByName(name))
		if err != nil {
			return nil // a sandbox that stopped mid-refresh is not an error
		}
		sample, ok := <-ch
		if !ok {
			return nil
		}
		return statsMsg{name: name, sample: sample}
	}
}

// restoreCursor puts the cursor back on the named sandbox, or clamps it into
// range when that sandbox is gone.
func (m *Model) restoreCursor(name string) {
	if name != "" {
		for i, sb := range m.sandboxes {
			if sb.Spec.Name == name {
				m.cursor = i
				return
			}
		}
	}
	m.clampCursor()
}

func (m *Model) clampCursor() {
	c := cursor[api.Sandbox]{items: m.sandboxes, at: m.cursor}
	c.clamp()
	m.cursor = c.at
}

// selected returns the sandbox under the cursor, and whether there is one.
func (m *Model) selected() (api.Sandbox, bool) {
	c := cursor[api.Sandbox]{items: m.sandboxes, at: m.cursor}
	return c.selected()
}

func (m *Model) selectedName() string {
	if sb, ok := m.selected(); ok {
		return sb.Spec.Name
	}
	return ""
}

// appendConnections adds a batch, keeping the cursor on what it was looking at
// and the buffer bounded.
func (m *Model) appendConnections(batch []proxy.Entry) {
	if len(batch) == 0 {
		return
	}
	selected := m.selectedConnection()
	m.connections = append(m.connections, batch...)
	m.connSeq = m.connections[len(m.connections)-1].Seq

	if excess := len(m.connections) - connectionLimit; excess > 0 {
		m.connections = append(m.connections[:0], m.connections[excess:]...)
	}
	m.restoreConnCursor(selected)
}

// connectionLimit is how many decisions the dashboard holds. The daemon keeps
// more; this is only what can be scrolled.
const connectionLimit = 200

// visibleConnections is the decisions the network panel is showing, which is
// all of them unless the filter is on.
//
// Every cursor operation goes through this rather than the held list, so the
// selection means the same thing whichever view is up.
func (m *Model) visibleConnections() []proxy.Entry {
	if !m.connFilter {
		return m.connections
	}
	name := m.selectedName()
	if name == "" {
		return m.connections
	}
	out := make([]proxy.Entry, 0, len(m.connections))
	for _, e := range m.connections {
		if e.Sandbox == name {
			out = append(out, e)
		}
	}
	return out
}

// restoreConnCursor keeps the cursor on the same decision as entries arrive
// beneath it, so a denial does not slide away while being read.
func (m *Model) restoreConnCursor(seq uint64) {
	visible := m.visibleConnections()
	if seq != 0 {
		for i, e := range visible {
			if e.Seq == seq {
				m.connCursor = i
				return
			}
		}
	}
	// nothing was selected, or it aged out: follow the newest.
	m.connCursor = len(visible) - 1
	m.clampConnCursor()
}

func (m *Model) clampConnCursor() {
	c := cursor[proxy.Entry]{items: m.visibleConnections(), at: m.connCursor}
	c.clamp()
	m.connCursor = c.at
}

// selectedConnection returns the sequence under the cursor, or zero.
func (m *Model) selectedConnection() uint64 {
	if entry, ok := m.selectedEntry(); ok {
		return entry.Seq
	}
	return 0
}

// selectedEntry returns the decision under the cursor, and whether there is one.
func (m *Model) selectedEntry() (proxy.Entry, bool) {
	c := cursor[proxy.Entry]{items: m.visibleConnections(), at: m.connCursor}
	return c.selected()
}
