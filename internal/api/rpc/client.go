package rpc

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/api/rpc/plbxv1"
	"github.com/rhizomatous/planterbox/internal/proxy"
)

// Client is an [api.Service] backed by a daemon on the other end of a unix
// socket. Callers cannot tell it apart from the in-process implementation,
// which is the whole point of the interface.
type Client struct {
	conn *grpc.ClientConn
	svc  plbxv1.SandboxesClient
}

var _ api.Service = (*Client)(nil)

// Dial opens a client against the daemon listening on socket.
//
// It does not connect: gRPC dials lazily, on the first call. Probing daemon
// health belongs to whoever decides whether to start one, not here.
func Dial(socket string) (*Client, error) {
	conn, err := grpc.NewClient("unix:"+socket,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("connecting to the plbx daemon at %s: %w", socket, err)
	}
	return NewClient(conn), nil
}

// NewClient wraps an established connection. Tests use it to run against an
// in-memory transport rather than a socket on disk.
func NewClient(conn *grpc.ClientConn) *Client {
	return &Client{conn: conn, svc: plbxv1.NewSandboxesClient(conn)}
}

// Close releases the connection to the daemon. It does not stop the daemon.
func (c *Client) Close() error { return c.conn.Close() }

// Info reports what the daemon is. It is not part of [api.Service], since the
// in-process implementation has no daemon to describe, so it lives here where
// the only callers that can ask are the ones talking to one.
func (c *Client) Info(ctx context.Context) (api.DaemonInfo, error) {
	resp, err := c.svc.Info(ctx, &plbxv1.InfoRequest{})
	if err != nil {
		return api.DaemonInfo{}, localError(err)
	}
	return api.DaemonInfo{
		Version:   resp.GetVersion(),
		StartedAt: resp.GetStartedAt().AsTime(),
		PID:       int(resp.GetPid()),
	}, nil
}

// Create builds a sandbox and returns it as stored, reporting a clone remote
// that could not be written the same way the in-process service does.
func (c *Client) Create(ctx context.Context, spec api.Spec) (api.Sandbox, error) {
	resp, err := c.svc.Create(ctx, &plbxv1.CreateRequest{Spec: protoSpec(spec)})
	if err != nil {
		return api.Sandbox{}, localError(err)
	}
	if msg := resp.GetRemoteError(); msg != "" {
		return apiSandbox(resp.GetSandbox()), fmt.Errorf("%w: %s", api.ErrRemoteNotAdded, msg)
	}
	return apiSandbox(resp.GetSandbox()), nil
}

// List returns every sandbox the daemon knows about.
func (c *Client) List(ctx context.Context) ([]api.Sandbox, error) {
	resp, err := c.svc.List(ctx, &plbxv1.ListRequest{})
	if err != nil {
		return nil, localError(err)
	}
	sandboxes := make([]api.Sandbox, 0, len(resp.GetSandboxes()))
	for _, sb := range resp.GetSandboxes() {
		sandboxes = append(sandboxes, apiSandbox(sb))
	}
	return sandboxes, nil
}

// Inspect returns one sandbox, refreshed against the runtime.
func (c *Client) Inspect(ctx context.Context, ref api.Ref) (api.Sandbox, error) {
	resp, err := c.svc.Inspect(ctx, &plbxv1.InspectRequest{Ref: protoRef(ref)})
	if err != nil {
		return api.Sandbox{}, localError(err)
	}
	return apiSandbox(resp.GetSandbox()), nil
}

// Start boots a created or stopped sandbox.
func (c *Client) Start(ctx context.Context, ref api.Ref) error {
	_, err := c.svc.Start(ctx, &plbxv1.StartRequest{Ref: protoRef(ref)})
	return localError(err)
}

// Stop halts a running sandbox, leaving its contents intact.
func (c *Client) Stop(ctx context.Context, ref api.Ref) error {
	_, err := c.svc.Stop(ctx, &plbxv1.StopRequest{Ref: protoRef(ref)})
	return localError(err)
}

// Remove deletes a sandbox, refusing a running one unless force is set.
func (c *Client) Remove(ctx context.Context, ref api.Ref, force bool) error {
	_, err := c.svc.Remove(ctx, &plbxv1.RemoveRequest{Ref: protoRef(ref), Force: force})
	return localError(err)
}

// Copy moves files between the host and a sandbox.
func (c *Client) Copy(ctx context.Context, src, dst api.Path) error {
	_, err := c.svc.Copy(ctx, &plbxv1.CopyRequest{Src: protoPath(src), Dst: protoPath(dst)})
	return localError(err)
}

// Publish replaces the ports a sandbox publishes on the host.
func (c *Client) Publish(ctx context.Context, ref api.Ref, ports []api.Port) error {
	_, err := c.svc.Publish(ctx, &plbxv1.PublishRequest{Ref: protoRef(ref), Ports: protoPorts(ports)})
	return localError(err)
}

// Stats relays the daemon's samples onto a channel, matching the in-process
// contract: the channel closes when the feed ends or ctx is cancelled.
func (c *Client) Stats(ctx context.Context, ref api.Ref) (<-chan api.Stats, error) {
	stream, err := c.svc.Stats(ctx, &plbxv1.StatsRequest{Ref: protoRef(ref)})
	if err != nil {
		return nil, localError(err)
	}
	return relayStream(ctx, stream, apiSample), nil
}

// PullImage fetches an image, relaying the daemon's progress.
func (c *Client) PullImage(ctx context.Context, image string) (<-chan string, error) {
	stream, err := c.svc.PullImage(ctx, &plbxv1.PullImageRequest{Image: image})
	if err != nil {
		return nil, localError(err)
	}
	return relayStream(ctx, stream, func(progress *plbxv1.PullProgress) string {
		return progress.GetLine()
	}), nil
}

// relayStream wraps the gRPC Recv pattern, forwarding messages onto a channel
// until the stream closes or ctx is done. T is the protocol message type, U is
// what the caller wants on the channel, and transform converts one to the other.
func relayStream[T any, U any](ctx context.Context, stream interface{ Recv() (T, error) }, transform func(T) U) <-chan U {
	out := make(chan U)
	go func() {
		defer close(out)
		for {
			msg, err := stream.Recv()
			if err != nil {
				return // EOF, a cancelled context, or a dead daemon: all end the feed
			}
			select {
			case out <- transform(msg):
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// Policy returns the host's egress policy.
func (c *Client) Policy(ctx context.Context) (proxy.Policy, error) {
	resp, err := c.svc.GetPolicy(ctx, &plbxv1.GetPolicyRequest{})
	if err != nil {
		return proxy.Policy{}, localError(err)
	}
	return apiPolicy(resp.GetPolicy()), nil
}

// SetPolicy replaces the host's egress policy.
func (c *Client) SetPolicy(ctx context.Context, p proxy.Policy) error {
	_, err := c.svc.SetPolicy(ctx, &plbxv1.SetPolicyRequest{Policy: protoPolicy(p)})
	return localError(err)
}

// Connections returns the proxy's decisions newer than since.
func (c *Client) Connections(ctx context.Context, since uint64) ([]proxy.Entry, error) {
	resp, err := c.svc.Connections(ctx, &plbxv1.ConnectionsRequest{Since: since})
	if err != nil {
		return nil, localError(err)
	}
	entries := make([]proxy.Entry, 0, len(resp.GetDecisions()))
	for _, d := range resp.GetDecisions() {
		entries = append(entries, apiDecision(d))
	}
	return entries, nil
}
