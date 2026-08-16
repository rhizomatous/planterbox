package rpc

import (
	"context"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/api/rpc/plbxv1"
)

// Server exposes an [api.Service] over gRPC. The daemon wraps its in-process
// service in one of these; everything it serves is the service's own behaviour,
// so the two implementations cannot drift.
type Server struct {
	plbxv1.UnimplementedSandboxesServer
	svc     api.Service
	version string
	started time.Time
}

var _ plbxv1.SandboxesServer = (*Server)(nil)

// ServerOption configures a [Server].
type ServerOption func(*Server)

// WithVersion tells the server what build it is, for [Server.Info]. Without
// it the daemon reports an empty version, which a client reads as "cannot
// say" rather than as agreement.
func WithVersion(version string) ServerOption {
	return func(s *Server) { s.version = version }
}

// NewServer returns a gRPC service backed by svc.
func NewServer(svc api.Service, opts ...ServerOption) *Server {
	s := &Server{svc: svc, started: time.Now()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Info reports the daemon's own build and uptime.
func (s *Server) Info(_ context.Context, _ *plbxv1.InfoRequest) (*plbxv1.InfoResponse, error) {
	return &plbxv1.InfoResponse{
		Version:   s.version,
		StartedAt: timestamppb.New(s.started),
		Pid:       int32(os.Getpid()),
	}, nil
}

// Register attaches the service to a gRPC server.
func (s *Server) Register(g *grpc.Server) { plbxv1.RegisterSandboxesServer(g, s) }

// Create builds a sandbox and returns it as stored.
func (s *Server) Create(ctx context.Context, req *plbxv1.CreateRequest) (*plbxv1.CreateResponse, error) {
	sb, err := s.svc.Create(ctx, apiSpec(req.GetSpec()))
	if err != nil {
		return nil, wireError(err)
	}
	return &plbxv1.CreateResponse{Sandbox: protoSandbox(sb)}, nil
}

// List returns every sandbox the daemon knows about.
func (s *Server) List(ctx context.Context, _ *plbxv1.ListRequest) (*plbxv1.ListResponse, error) {
	sandboxes, err := s.svc.List(ctx)
	if err != nil {
		return nil, wireError(err)
	}
	resp := &plbxv1.ListResponse{Sandboxes: make([]*plbxv1.Sandbox, 0, len(sandboxes))}
	for _, sb := range sandboxes {
		resp.Sandboxes = append(resp.Sandboxes, protoSandbox(sb))
	}
	return resp, nil
}

// Inspect returns one sandbox, refreshed against the runtime.
func (s *Server) Inspect(ctx context.Context, req *plbxv1.InspectRequest) (*plbxv1.InspectResponse, error) {
	sb, err := s.svc.Inspect(ctx, apiRef(req.GetRef()))
	if err != nil {
		return nil, wireError(err)
	}
	return &plbxv1.InspectResponse{Sandbox: protoSandbox(sb)}, nil
}

// Start boots a created or stopped sandbox.
func (s *Server) Start(ctx context.Context, req *plbxv1.StartRequest) (*plbxv1.StartResponse, error) {
	if err := s.svc.Start(ctx, apiRef(req.GetRef())); err != nil {
		return nil, wireError(err)
	}
	return &plbxv1.StartResponse{}, nil
}

// Stop halts a running sandbox, leaving its contents intact.
func (s *Server) Stop(ctx context.Context, req *plbxv1.StopRequest) (*plbxv1.StopResponse, error) {
	if err := s.svc.Stop(ctx, apiRef(req.GetRef())); err != nil {
		return nil, wireError(err)
	}
	return &plbxv1.StopResponse{}, nil
}

// PullImage fetches an image and streams the runtime's progress out.
func (s *Server) PullImage(req *plbxv1.PullImageRequest, stream grpc.ServerStreamingServer[plbxv1.PullProgress]) error {
	lines, err := s.svc.PullImage(stream.Context(), req.GetImage())
	if err != nil {
		return wireError(err)
	}
	for line := range lines {
		if err := stream.Send(&plbxv1.PullProgress{Line: line}); err != nil {
			return err
		}
	}
	return nil
}

// Remove deletes a sandbox, refusing a running one unless force is set.
func (s *Server) Remove(ctx context.Context, req *plbxv1.RemoveRequest) (*plbxv1.RemoveResponse, error) {
	if err := s.svc.Remove(ctx, apiRef(req.GetRef()), req.GetForce()); err != nil {
		return nil, wireError(err)
	}
	return &plbxv1.RemoveResponse{}, nil
}

// Copy moves files between the host and a sandbox.
func (s *Server) Copy(ctx context.Context, req *plbxv1.CopyRequest) (*plbxv1.CopyResponse, error) {
	if err := s.svc.Copy(ctx, apiPath(req.GetSrc()), apiPath(req.GetDst())); err != nil {
		return nil, wireError(err)
	}
	return &plbxv1.CopyResponse{}, nil
}

// Publish replaces the ports a sandbox publishes on the host.
func (s *Server) Publish(ctx context.Context, req *plbxv1.PublishRequest) (*plbxv1.PublishResponse, error) {
	if err := s.svc.Publish(ctx, apiRef(req.GetRef()), apiPorts(req.GetPorts())); err != nil {
		return nil, wireError(err)
	}
	return &plbxv1.PublishResponse{}, nil
}

// Stats relays samples until the underlying feed closes or the client hangs up.
//
// The stream's own context is what cancels the feed, so a client that
// disconnects mid-sample stops the work behind it rather than leaving a
// `docker stats` running in the daemon.
func (s *Server) Stats(req *plbxv1.StatsRequest, stream grpc.ServerStreamingServer[plbxv1.Sample]) error {
	ctx := stream.Context()
	samples, err := s.svc.Stats(ctx, apiRef(req.GetRef()))
	if err != nil {
		return wireError(err)
	}
	for sample := range samples {
		if err := stream.Send(protoSample(sample)); err != nil {
			return err
		}
	}
	return wireError(ctx.Err())
}

// GetPolicy returns the host's egress policy.
func (s *Server) GetPolicy(ctx context.Context, _ *plbxv1.GetPolicyRequest) (*plbxv1.GetPolicyResponse, error) {
	p, err := s.svc.Policy(ctx)
	if err != nil {
		return nil, wireError(err)
	}
	return &plbxv1.GetPolicyResponse{Policy: protoPolicy(p)}, nil
}

// SetPolicy replaces the host's egress policy.
func (s *Server) SetPolicy(ctx context.Context, req *plbxv1.SetPolicyRequest) (*plbxv1.SetPolicyResponse, error) {
	if err := s.svc.SetPolicy(ctx, apiPolicy(req.GetPolicy())); err != nil {
		return nil, wireError(err)
	}
	return &plbxv1.SetPolicyResponse{}, nil
}

// Connections returns the proxy's decisions newer than the caller's sequence.
func (s *Server) Connections(ctx context.Context, req *plbxv1.ConnectionsRequest) (*plbxv1.ConnectionsResponse, error) {
	entries, err := s.svc.Connections(ctx, req.GetSince())
	if err != nil {
		return nil, wireError(err)
	}
	resp := &plbxv1.ConnectionsResponse{Decisions: make([]*plbxv1.Decision, 0, len(entries))}
	for _, e := range entries {
		resp.Decisions = append(resp.Decisions, protoDecision(e))
	}
	return resp, nil
}
