package rpc

import (
	"context"

	"google.golang.org/grpc"

	"github.com/rhizomatous/jardiniere/internal/api"
	"github.com/rhizomatous/jardiniere/internal/api/rpc/jardv1"
)

// Server exposes an [api.Service] over gRPC. The daemon wraps its in-process
// service in one of these; everything it serves is the service's own behaviour,
// so the two implementations cannot drift.
type Server struct {
	jardv1.UnimplementedSandboxesServer
	svc api.Service
}

var _ jardv1.SandboxesServer = (*Server)(nil)

// NewServer returns a gRPC service backed by svc.
func NewServer(svc api.Service) *Server { return &Server{svc: svc} }

// Register attaches the service to a gRPC server.
func (s *Server) Register(g *grpc.Server) { jardv1.RegisterSandboxesServer(g, s) }

// Create builds a sandbox and returns it as stored.
func (s *Server) Create(ctx context.Context, req *jardv1.CreateRequest) (*jardv1.CreateResponse, error) {
	sb, err := s.svc.Create(ctx, apiSpec(req.GetSpec()))
	if err != nil {
		return nil, wireError(err)
	}
	return &jardv1.CreateResponse{Sandbox: protoSandbox(sb)}, nil
}

// List returns every sandbox the daemon knows about.
func (s *Server) List(ctx context.Context, _ *jardv1.ListRequest) (*jardv1.ListResponse, error) {
	sandboxes, err := s.svc.List(ctx)
	if err != nil {
		return nil, wireError(err)
	}
	resp := &jardv1.ListResponse{Sandboxes: make([]*jardv1.Sandbox, 0, len(sandboxes))}
	for _, sb := range sandboxes {
		resp.Sandboxes = append(resp.Sandboxes, protoSandbox(sb))
	}
	return resp, nil
}

// Inspect returns one sandbox, refreshed against the runtime.
func (s *Server) Inspect(ctx context.Context, req *jardv1.InspectRequest) (*jardv1.InspectResponse, error) {
	sb, err := s.svc.Inspect(ctx, apiRef(req.GetRef()))
	if err != nil {
		return nil, wireError(err)
	}
	return &jardv1.InspectResponse{Sandbox: protoSandbox(sb)}, nil
}

// Start boots a created or stopped sandbox.
func (s *Server) Start(ctx context.Context, req *jardv1.StartRequest) (*jardv1.StartResponse, error) {
	if err := s.svc.Start(ctx, apiRef(req.GetRef())); err != nil {
		return nil, wireError(err)
	}
	return &jardv1.StartResponse{}, nil
}

// Stop halts a running sandbox, leaving its contents intact.
func (s *Server) Stop(ctx context.Context, req *jardv1.StopRequest) (*jardv1.StopResponse, error) {
	if err := s.svc.Stop(ctx, apiRef(req.GetRef())); err != nil {
		return nil, wireError(err)
	}
	return &jardv1.StopResponse{}, nil
}

// Remove deletes a sandbox, refusing a running one unless force is set.
func (s *Server) Remove(ctx context.Context, req *jardv1.RemoveRequest) (*jardv1.RemoveResponse, error) {
	if err := s.svc.Remove(ctx, apiRef(req.GetRef()), req.GetForce()); err != nil {
		return nil, wireError(err)
	}
	return &jardv1.RemoveResponse{}, nil
}

// Copy moves files between the host and a sandbox.
func (s *Server) Copy(ctx context.Context, req *jardv1.CopyRequest) (*jardv1.CopyResponse, error) {
	if err := s.svc.Copy(ctx, apiPath(req.GetSrc()), apiPath(req.GetDst())); err != nil {
		return nil, wireError(err)
	}
	return &jardv1.CopyResponse{}, nil
}

// Publish replaces the ports a sandbox publishes on the host.
func (s *Server) Publish(ctx context.Context, req *jardv1.PublishRequest) (*jardv1.PublishResponse, error) {
	if err := s.svc.Publish(ctx, apiRef(req.GetRef()), apiPorts(req.GetPorts())); err != nil {
		return nil, wireError(err)
	}
	return &jardv1.PublishResponse{}, nil
}

// Stats relays samples until the underlying feed closes or the client hangs up.
//
// The stream's own context is what cancels the feed, so a client that
// disconnects mid-sample stops the work behind it rather than leaving a
// `docker stats` running in the daemon.
func (s *Server) Stats(req *jardv1.StatsRequest, stream grpc.ServerStreamingServer[jardv1.Sample]) error {
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
func (s *Server) GetPolicy(ctx context.Context, _ *jardv1.GetPolicyRequest) (*jardv1.GetPolicyResponse, error) {
	p, err := s.svc.Policy(ctx)
	if err != nil {
		return nil, wireError(err)
	}
	return &jardv1.GetPolicyResponse{Policy: protoPolicy(p)}, nil
}

// SetPolicy replaces the host's egress policy.
func (s *Server) SetPolicy(ctx context.Context, req *jardv1.SetPolicyRequest) (*jardv1.SetPolicyResponse, error) {
	if err := s.svc.SetPolicy(ctx, apiPolicy(req.GetPolicy())); err != nil {
		return nil, wireError(err)
	}
	return &jardv1.SetPolicyResponse{}, nil
}

// Connections returns the proxy's decisions newer than the caller's sequence.
func (s *Server) Connections(ctx context.Context, req *jardv1.ConnectionsRequest) (*jardv1.ConnectionsResponse, error) {
	entries, err := s.svc.Connections(ctx, req.GetSince())
	if err != nil {
		return nil, wireError(err)
	}
	resp := &jardv1.ConnectionsResponse{Decisions: make([]*jardv1.Decision, 0, len(entries))}
	for _, e := range entries {
		resp.Decisions = append(resp.Decisions, protoDecision(e))
	}
	return resp, nil
}
