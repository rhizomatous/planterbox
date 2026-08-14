// Package tui is plbx's dashboard: the sandbox list, its live resource
// samples, and the keys that act on them.
//
// The model holds an [api.Service] and nothing else. Every action goes through
// it, so the dashboard works against a daemon or an in-process service without
// knowing which.
package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/proxy"
)

// refreshEvery is how often the sandbox list is re-read. Status changes are the
// only thing it catches, and they are rare, so this is deliberately slower than
// the stats feed.
const refreshEvery = 2 * time.Second

// Model is the dashboard's state.
type Model struct {
	svc api.Service

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
	// seen — the feed is polled, so it asks only for what is new.
	connCursor int
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
func New(svc api.Service) *Model {
	return &Model{svc: svc, stats: map[string]api.Stats{}, now: time.Now}
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
		entries, err := m.svc.Connections(context.Background(), since)
		if err != nil {
			return nil // a daemon without a proxy simply has nothing to say
		}
		return connectionsMsg(entries)
	}
}

// list re-reads the sandboxes.
func (m *Model) list() tea.Cmd {
	return func() tea.Msg {
		sandboxes, err := m.svc.List(context.Background())
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

	case actionMsg:
		m.pending = ""
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
	if m.cursor >= len(m.sandboxes) {
		m.cursor = len(m.sandboxes) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// selected returns the sandbox under the cursor, and whether there is one.
func (m *Model) selected() (api.Sandbox, bool) {
	if m.cursor < 0 || m.cursor >= len(m.sandboxes) {
		return api.Sandbox{}, false
	}
	return m.sandboxes[m.cursor], true
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

// restoreConnCursor keeps the cursor on the same decision as entries arrive
// beneath it, so a denial does not slide away while being read.
func (m *Model) restoreConnCursor(seq uint64) {
	if seq != 0 {
		for i, e := range m.connections {
			if e.Seq == seq {
				m.connCursor = i
				return
			}
		}
	}
	// nothing was selected, or it aged out: follow the newest.
	m.connCursor = len(m.connections) - 1
	m.clampConnCursor()
}

func (m *Model) clampConnCursor() {
	if m.connCursor >= len(m.connections) {
		m.connCursor = len(m.connections) - 1
	}
	if m.connCursor < 0 {
		m.connCursor = 0
	}
}

// selectedConnection returns the sequence under the cursor, or zero.
func (m *Model) selectedConnection() uint64 {
	if m.connCursor < 0 || m.connCursor >= len(m.connections) {
		return 0
	}
	return m.connections[m.connCursor].Seq
}

// selectedEntry returns the decision under the cursor, and whether there is one.
func (m *Model) selectedEntry() (proxy.Entry, bool) {
	if m.connCursor < 0 || m.connCursor >= len(m.connections) {
		return proxy.Entry{}, false
	}
	return m.connections[m.connCursor], true
}
