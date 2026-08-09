package runner

import (
	"context"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rhizomatous/jardiniere/internal/api"
)

// containerPrefix namespaces every container and volume jard owns, so a stray
// `docker ps` makes it obvious who created what.
const containerPrefix = "jard-"

// agentHome is the base image contract's home directory. It gets its own named
// volume, which is what makes a sandbox persistent: packages, shell history,
// and agent state all live under it.
const agentHome = "/home/agent"

// idle keeps a created sandbox alive with no agent attached. Sessions arrive
// later over exec.
var idle = []string{"sleep", "infinity"}

// OCI drives a docker-compatible CLI: docker, podman, OrbStack, or colima.
type OCI struct {
	rt   Runtime
	exec Executor
	// egressUpstream is the host proxy a sandbox's traffic ends up at. Empty
	// means egress control is off: no private network, no relay, and no proxy
	// environment. That is the --dry-run and in-process case, where there is
	// no daemon holding a proxy to point at.
	egressUpstream string
	relayImage     string
}

// WithEgress routes sandboxes through the proxy at upstream, over a private
// network and the relay. relayImage may be empty for the published default.
func WithEgress(upstream, relayImage string) Option {
	return func(o *OCI) {
		o.egressUpstream = upstream
		o.relayImage = relayImage
	}
}

var _ Runner = (*OCI)(nil)

// Option configures an [OCI] runner.
type Option func(*OCI)

// WithExecutor replaces how invocations are run.
func WithExecutor(e Executor) Option {
	return func(o *OCI) { o.exec = e }
}

// WithDryRun renders every invocation to w and executes nothing.
func WithDryRun(w io.Writer) Option {
	return WithExecutor(dryRunExecutor{w: w})
}

// NewOCI returns a runner driving rt.
func NewOCI(rt Runtime, opts ...Option) *OCI {
	o := &OCI{rt: rt, exec: hostExecutor{}}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Runtime reports which container CLI this runner drives.
func (o *OCI) Runtime() Runtime { return o.rt }

// ContainerName is the runtime-side name for a sandbox.
func ContainerName(sandbox string) string { return containerPrefix + sandbox }

// HomeVolume is the named volume backing a sandbox's /home/agent.
func HomeVolume(sandbox string) string { return containerPrefix + sandbox + "-home" }

// Create builds the container without starting it.
func (o *OCI) Create(ctx context.Context, spec api.Spec) (ID, error) {
	// the network comes first: the container is created onto it, and a create
	// naming a network that does not exist fails outright.
	if err := o.EnsureNetwork(ctx, spec.Name); err != nil {
		return "", err
	}
	if _, err := o.exec.Output(ctx, o.CreateInvocation(spec)); err != nil {
		return "", err
	}
	// the runtime echoes the new container's ID, but the name is what every
	// later invocation uses and what a human reads in `docker ps`.
	return ID(ContainerName(spec.Name)), nil
}

// Start boots a created or stopped container.
//
// The relay is ensured on every start, not once: it is shared between
// sandboxes, and one removed by hand would otherwise leave the next sandbox
// with no way out and nothing said about why.
//
// The sandbox's name is a parameter rather than something read back out of id.
// id is whatever the runtime last called the container, which is the name
// before its first start and a hash after it — while the network, like the
// home volume, is named after the sandbox and stays that way.
func (o *OCI) Start(ctx context.Context, id ID, sandbox string) error {
	if o.egressUpstream != "" {
		if err := o.EnsureRelay(ctx, sandbox, o.relayImage, o.egressUpstream); err != nil {
			return err
		}
	}
	_, err := o.exec.Output(ctx, o.invoke("start", string(id)))
	return err
}

// Stop halts a running container.
func (o *OCI) Stop(ctx context.Context, id ID) error {
	_, err := o.exec.Output(ctx, o.invoke("stop", string(id)))
	if isNotFound(err) {
		return nil // already gone; the caller wanted it stopped, and it is
	}
	return err
}

// Remove deletes a container and the home volume behind it.
//
// The two are separate calls on purpose: `rm --volumes` reclaims only the
// anonymous volumes a container was given, never a named one, so the home
// volume would otherwise outlive every sandbox that ever used it.
//
// The volume's name comes from the sandbox's, never from id. Once a sandbox
// has been started, the id on record is the runtime's own hash, and a volume
// name derived from that names nothing — the removal then succeeds against
// something that does not exist and the real volume is left behind.
func (o *OCI) Remove(ctx context.Context, id ID, sandbox string, force bool) error {
	args := []string{"rm", "--volumes"}
	if force {
		args = append(args, "--force")
	}
	if _, err := o.exec.Output(ctx, o.invoke(append(args, string(id))...)); err != nil && !isNotFound(err) {
		return err
	}

	if _, err := o.exec.Output(ctx, o.invoke("volume", "rm", HomeVolume(sandbox))); err != nil && !isNotFound(err) {
		return err
	}
	// the network goes last: it cannot be removed while anything is still
	// attached to it, and the port forwarder is one of those things.
	if err := o.Unpublish(ctx, sandbox); err != nil {
		return err
	}
	if err := o.RemoveNetwork(ctx, sandbox); err != nil {
		return err
	}
	return nil
}

// Exec runs a command inside a running container, with the terminal wired
// through so the user can interact with it.
func (o *OCI) Exec(ctx context.Context, id ID, req api.ExecRequest, streams api.Streams) (api.ExecResult, error) {
	code, err := o.exec.Session(ctx, o.ExecInvocation(id, req), streams, req.TTY)
	if err != nil {
		return api.ExecResult{}, err
	}
	return api.ExecResult{ExitCode: code}, nil
}

// Copy moves files between the host and a container.
func (o *OCI) Copy(ctx context.Context, id ID, src, dst api.Path) error {
	_, err := o.exec.Output(ctx, o.CopyInvocation(id, src, dst))
	return err
}

// Stats streams resource samples for a running container until it exits or ctx
// is cancelled.
func (o *OCI) Stats(ctx context.Context, id ID) (<-chan api.Stats, error) {
	lines, err := o.exec.Stream(ctx, o.StatsInvocation(id))
	if err != nil {
		return nil, err
	}

	out := make(chan api.Stats)
	go func() {
		defer close(out)
		for line := range lines {
			s, ok := parseStats(line)
			if !ok {
				continue // a header, a blank redraw, or a line we can't read
			}
			select {
			case out <- s:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// statsFormat asks for the two fields the dashboard shows, tab-separated.
const statsFormat = "{{.CPUPerc}}\t{{.MemUsage}}"

// StatsInvocation renders the streaming `stats` command line.
func (o *OCI) StatsInvocation(id ID) Invocation {
	return o.invoke("stats", "--format", statsFormat, string(id))
}

// parseStats reads one sample line, "12.34%\t1.5GiB / 8GiB".
//
// Streaming stats repaints the terminal, so lines arrive wrapped in cursor
// escapes and the occasional blank; ok is false for anything unreadable rather
// than an error, since one bad sample shouldn't end the stream.
func parseStats(line string) (api.Stats, bool) {
	fields := strings.Split(strings.TrimSpace(ansi.Strip(line)), "\t")
	if len(fields) < 2 {
		return api.Stats{}, false
	}

	cpu, err := parsePercent(fields[0])
	if err != nil {
		return api.Stats{}, false
	}
	used, limit, ok := parseMemUsage(fields[1])
	if !ok {
		return api.Stats{}, false
	}
	return api.Stats{CPUPercent: cpu, MemoryBytes: used, MemoryLimit: limit}, true
}

// parsePercent reads "12.34%".
func parsePercent(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
}

// parseMemUsage reads "1.5GiB / 8GiB" into used and limit.
func parseMemUsage(s string) (used, limit int64, ok bool) {
	left, right, found := strings.Cut(s, "/")
	if !found {
		return 0, 0, false
	}
	used, err := api.ParseBytes(strings.TrimSpace(left))
	if err != nil {
		return 0, 0, false
	}
	limit, err = api.ParseBytes(strings.TrimSpace(right))
	if err != nil {
		return 0, 0, false
	}
	return used, limit, true
}

// Inspect reports a container's observed state. A container the runtime has
// never heard of is [api.StatusMissing] rather than an error: the record
// outliving the container is a state jard displays, not a failure.
func (o *OCI) Inspect(ctx context.Context, id ID) (api.State, error) {
	out, err := o.exec.Output(ctx, o.InspectInvocation(id))
	if isNotFound(err) {
		return api.State{Status: api.StatusMissing}, nil
	}
	if err != nil {
		return api.State{}, err
	}
	return parseInspect(string(out)), nil
}

// CreateInvocation renders the `create` command line for spec. It is pure, so
// arg-building is testable without a runtime.
func (o *OCI) CreateInvocation(spec api.Spec) Invocation {
	args := []string{
		"create",
		"--name", ContainerName(spec.Name),
		"--hostname", spec.Name,
		"--label", "jard.sandbox=" + spec.Name,
		// persistence lives here: everything the user installs is under $HOME.
		"--volume", HomeVolume(spec.Name) + ":" + agentHome,
	}

	// workspaces bind in at their host paths, so paths resolve on both sides.
	for _, ws := range spec.Workspaces {
		mount := ws.Host + ":" + ws.Host
		if ws.ReadOnly {
			mount += ":ro"
		}
		args = append(args, "--volume", mount)
	}
	if primary := spec.Primary(); primary.Host != "" {
		args = append(args, "--workdir", primary.Host)
	}

	if spec.Resources.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(spec.Resources.CPUs, 'g', -1, 64))
	}
	if spec.Resources.Memory > 0 {
		args = append(args, "--memory", strconv.FormatInt(spec.Resources.Memory, 10))
	}

	// a sandbox is always alone on its own network. With egress control on
	// that network has no route out, and the proxy environment points at the
	// relay, which is the only other thing on it.
	args = append(args, "--network", SandboxNetwork(spec.Name))
	env := spec.Env
	if o.egressUpstream != "" {
		env = withProxyEnv(env, spec.Name)
	}

	// map order is random; sort so the rendered command is stable.
	for _, k := range sortedKeys(env) {
		args = append(args, "--env", k+"="+env[k])
	}

	args = append(args, spec.Image)
	args = append(args, idle...)
	return o.invoke(args...)
}

// ExecInvocation renders the `exec` command line for req.
func (o *OCI) ExecInvocation(id ID, req api.ExecRequest) Invocation {
	args := []string{"exec"}
	if req.Interactive {
		args = append(args, "--interactive")
	}
	if req.TTY {
		args = append(args, "--tty")
	}
	if req.Workdir != "" {
		args = append(args, "--workdir", req.Workdir)
	}
	if req.User != "" {
		args = append(args, "--user", req.User)
	}
	for _, k := range sortedKeys(req.Env) {
		args = append(args, "--env", k+"="+req.Env[k])
	}
	args = append(args, string(id))
	args = append(args, req.Cmd...)
	return o.invoke(args...)
}

// CopyInvocation renders the `cp` command line, rewriting sandbox-side paths to
// the runtime's "<container>:<path>" form.
func (o *OCI) CopyInvocation(id ID, src, dst api.Path) Invocation {
	return o.invoke("cp", copyEndpoint(id, src), copyEndpoint(id, dst))
}

// inspectFormat asks for just the state fields jard reads back, tab-separated
// so a value containing a space cannot shift the others.
const inspectFormat = "{{.State.Status}}\t{{.Id}}\t{{.State.StartedAt}}\t{{.State.ExitCode}}"

// InspectInvocation renders the `inspect` command line.
func (o *OCI) InspectInvocation(id ID) Invocation {
	return o.invoke("inspect", "--type", "container", "--format", inspectFormat, string(id))
}

// parseInspect reads back what inspectFormat asked for. Anything it cannot make
// sense of degrades to a zero field rather than an error, since a listing is
// more useful with a missing timestamp than with no row.
func parseInspect(out string) api.State {
	fields := strings.Split(strings.TrimSpace(out), "\t")
	state := api.State{Status: api.StatusUnknown}
	if len(fields) > 0 {
		state.Status = parseStatus(fields[0])
	}
	if len(fields) > 1 {
		state.ContainerID = fields[1]
	}
	if len(fields) > 2 {
		if t, err := time.Parse(time.RFC3339Nano, fields[2]); err == nil && !t.IsZero() {
			state.StartedAt = t.UTC()
		}
	}
	if len(fields) > 3 {
		if code, err := strconv.Atoi(fields[3]); err == nil {
			state.ExitCode = code
		}
	}
	return state
}

// parseStatus maps a runtime's container status onto jard's smaller set.
func parseStatus(s string) api.Status {
	switch strings.TrimSpace(s) {
	case "created":
		return api.StatusCreated
	case "running", "restarting":
		return api.StatusRunning
	case "paused", "exited", "dead", "removing":
		return api.StatusStopped
	default:
		return api.StatusUnknown
	}
}

// invoke pairs args with the runtime binary.
func (o *OCI) invoke(args ...string) Invocation {
	return Invocation{Path: o.rt.Path, Args: args}
}

// copyEndpoint renders one side of a copy for the runtime's cp.
func copyEndpoint(id ID, p api.Path) string {
	if p.InSandbox() {
		return string(id) + ":" + p.Path
	}
	return p.Path
}

// publishSpec renders a port mapping as "host:sandbox[/proto]".
func publishSpec(p api.Port) string {
	s := strconv.Itoa(p.Host) + ":" + strconv.Itoa(p.Sandbox)
	if p.Proto != "" && p.Proto != "tcp" {
		s += "/" + p.Proto
	}
	return s
}

// sortedKeys returns m's keys in a stable order.
func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}

// withProxyEnv layers jard's proxy variables over a spec's own.
//
// jard's win. The point of the proxy is that a sandbox cannot choose its own
// way out, and a spec that could set HTTP_PROXY could choose one.
func withProxyEnv(env map[string]string, sandbox string) map[string]string {
	merged := make(map[string]string, len(env)+6)
	for k, v := range env {
		merged[k] = v
	}
	for k, v := range ProxyEnv(sandbox) {
		merged[k] = v
	}
	return merged
}
