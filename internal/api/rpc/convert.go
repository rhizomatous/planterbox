// Package rpc carries [api.Service] over a gRPC connection, so a CLI or TUI in
// one process can drive sandboxes owned by the daemon in another.
//
// [Client] implements the interface; [Server] wraps one. Neither end knows what
// is on the other side of it.
package rpc

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rhizomatous/jardiniere/internal/api"
	"github.com/rhizomatous/jardiniere/internal/api/rpc/jardv1"
	"github.com/rhizomatous/jardiniere/internal/proxy"
)

// The conversions come in pairs: protoX renders a domain value for the wire,
// apiX reads one back. Every apiX tolerates a nil message, because a field the
// far side left unset arrives that way rather than as a zero struct.

func protoSpec(s api.Spec) *jardv1.Spec {
	spec := &jardv1.Spec{
		Name:      s.Name,
		Agent:     s.Agent,
		Image:     s.Image,
		Resources: &jardv1.Resources{Cpus: s.Resources.CPUs, Memory: s.Resources.Memory},
		Env:       s.Env,
		CreatedAt: protoTime(s.CreatedAt),
	}
	for _, w := range s.Workspaces {
		spec.Workspaces = append(spec.Workspaces, &jardv1.Workspace{Host: w.Host, ReadOnly: w.ReadOnly})
	}
	return spec
}

// protoPorts and apiPorts carry a sandbox's published ports, which live beside
// the spec rather than in it.
func protoPorts(ports []api.Port) []*jardv1.Port {
	out := make([]*jardv1.Port, 0, len(ports))
	for _, p := range ports {
		out = append(out, &jardv1.Port{
			Host:    int32(p.Host),
			Sandbox: int32(p.Sandbox),
			Proto:   p.Proto,
		})
	}
	return out
}

func apiPorts(ports []*jardv1.Port) []api.Port {
	if len(ports) == 0 {
		return nil
	}
	out := make([]api.Port, 0, len(ports))
	for _, p := range ports {
		out = append(out, api.Port{
			Host:    int(p.GetHost()),
			Sandbox: int(p.GetSandbox()),
			Proto:   p.GetProto(),
		})
	}
	return out
}

func apiSpec(p *jardv1.Spec) api.Spec {
	if p == nil {
		return api.Spec{}
	}
	s := api.Spec{
		Name:      p.GetName(),
		Agent:     p.GetAgent(),
		Image:     p.GetImage(),
		Env:       p.GetEnv(),
		CreatedAt: apiTime(p.GetCreatedAt()),
	}
	if r := p.GetResources(); r != nil {
		s.Resources = api.Resources{CPUs: r.GetCpus(), Memory: r.GetMemory()}
	}
	for _, w := range p.GetWorkspaces() {
		s.Workspaces = append(s.Workspaces, api.Workspace{Host: w.GetHost(), ReadOnly: w.GetReadOnly()})
	}
	return s
}

func protoState(s api.State) *jardv1.State {
	return &jardv1.State{
		Status:      string(s.Status),
		ContainerId: s.ContainerID,
		StartedAt:   protoTime(s.StartedAt),
		ExitCode:    int32(s.ExitCode),
	}
}

func apiState(p *jardv1.State) api.State {
	if p == nil {
		return api.State{}
	}
	return api.State{
		Status:      api.Status(p.GetStatus()),
		ContainerID: p.GetContainerId(),
		StartedAt:   apiTime(p.GetStartedAt()),
		ExitCode:    int(p.GetExitCode()),
	}
}

func protoSandbox(sb api.Sandbox) *jardv1.Sandbox {
	return &jardv1.Sandbox{Spec: protoSpec(sb.Spec), State: protoState(sb.State), Ports: protoPorts(sb.Ports)}
}

func apiSandbox(p *jardv1.Sandbox) api.Sandbox {
	if p == nil {
		return api.Sandbox{}
	}
	return api.Sandbox{Spec: apiSpec(p.GetSpec()), State: apiState(p.GetState()), Ports: apiPorts(p.GetPorts())}
}

func protoRef(r api.Ref) *jardv1.Ref {
	return &jardv1.Ref{Name: r.Name, Path: r.Path}
}

func apiRef(p *jardv1.Ref) api.Ref {
	if p == nil {
		return api.Ref{}
	}
	return api.Ref{Name: p.GetName(), Path: p.GetPath()}
}

func protoPath(p api.Path) *jardv1.Path {
	return &jardv1.Path{Sandbox: p.Sandbox, Path: p.Path}
}

func apiPath(p *jardv1.Path) api.Path {
	if p == nil {
		return api.Path{}
	}
	return api.Path{Sandbox: p.GetSandbox(), Path: p.GetPath()}
}

func protoExecRequest(r api.ExecRequest) *jardv1.ExecRequest {
	return &jardv1.ExecRequest{
		Cmd:         r.Cmd,
		Env:         r.Env,
		Workdir:     r.Workdir,
		User:        r.User,
		Interactive: r.Interactive,
		Tty:         r.TTY,
	}
}

func apiExecRequest(p *jardv1.ExecRequest) api.ExecRequest {
	if p == nil {
		return api.ExecRequest{}
	}
	return api.ExecRequest{
		Cmd:         p.GetCmd(),
		Env:         p.GetEnv(),
		Workdir:     p.GetWorkdir(),
		User:        p.GetUser(),
		Interactive: p.GetInteractive(),
		TTY:         p.GetTty(),
	}
}

func protoSample(s api.Stats) *jardv1.Sample {
	return &jardv1.Sample{
		CpuPercent:  s.CPUPercent,
		MemoryBytes: s.MemoryBytes,
		MemoryLimit: s.MemoryLimit,
	}
}

func apiSample(p *jardv1.Sample) api.Stats {
	if p == nil {
		return api.Stats{}
	}
	return api.Stats{
		CPUPercent:  p.GetCpuPercent(),
		MemoryBytes: p.GetMemoryBytes(),
		MemoryLimit: p.GetMemoryLimit(),
	}
}

// protoTime renders a zero time as an absent field rather than the unix epoch,
// which is what a sandbox that has never started should report.
func protoTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func apiTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

func protoPolicy(p proxy.Policy) *jardv1.Policy {
	out := &jardv1.Policy{Preset: string(p.Preset)}
	for _, r := range p.Rules {
		out.Rules = append(out.Rules, &jardv1.Rule{Pattern: r.Pattern, Allow: r.Allow})
	}
	return out
}

func apiPolicy(p *jardv1.Policy) proxy.Policy {
	if p == nil {
		return proxy.Policy{}
	}
	out := proxy.Policy{Preset: proxy.Preset(p.GetPreset())}
	for _, r := range p.GetRules() {
		out.Rules = append(out.Rules, proxy.Rule{Pattern: r.GetPattern(), Allow: r.GetAllow()})
	}
	return out
}

func protoDecision(e proxy.Entry) *jardv1.Decision {
	return &jardv1.Decision{
		Seq:     e.Seq,
		At:      protoTime(e.At),
		Host:    e.Target.Host,
		Port:    int32(e.Target.Port),
		Allowed: e.Allowed,
		Reason:  e.Reason,
		Sandbox: e.Sandbox,
	}
}

func apiDecision(d *jardv1.Decision) proxy.Entry {
	if d == nil {
		return proxy.Entry{}
	}
	return proxy.Entry{
		Seq:     d.GetSeq(),
		At:      apiTime(d.GetAt()),
		Target:  proxy.Target{Host: d.GetHost(), Port: int(d.GetPort())},
		Allowed: d.GetAllowed(),
		Reason:  d.GetReason(),
		Sandbox: d.GetSandbox(),
	}
}
