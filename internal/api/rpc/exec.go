package rpc

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/x/term"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/api/rpc/plbxv1"
)

// Exec runs a command in a sandbox owned by the daemon, relaying this process's
// stdio to it.
//
// A terminal session is put into raw mode for its duration. In-process there is
// nothing to do — the runtime inherits the real terminal and handles it — but
// here the terminal and the pty are in different processes, and without raw mode
// the local line discipline would eat the keystrokes before they were sent.
func (c *Client) Exec(
	ctx context.Context, ref api.Ref, req api.ExecRequest, streams api.Streams,
) (api.ExecResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if req.TTY {
		if restore, ok := makeRaw(streams.Stdin); ok {
			defer restore()
		}
	}

	stream, err := c.svc.Exec(ctx)
	if err != nil {
		return api.ExecResult{}, localError(err)
	}
	send := &frameSender{stream: stream}

	start := &plbxv1.ExecStart{Ref: protoRef(ref), Request: protoExecRequest(req)}
	// the opening dimensions ride along with the start frame, so the session is
	// the right size from its first redraw rather than after the first resize.
	if size, ok := firstSize(streams.Resize); ok {
		start.Size = protoSize(size)
	}
	if err := send.frame(&plbxv1.ExecClientFrame{
		Frame: &plbxv1.ExecClientFrame_Start{Start: start},
	}); err != nil {
		return api.ExecResult{}, localError(err)
	}

	go relayStdin(send, streams.Stdin)
	go relayResize(ctx, send, streams.Resize)

	return receive(stream, streams)
}

// receive plays the daemon's frames out to the local stdio until the session
// reports how it ended.
func receive(stream plbxv1.Sandboxes_ExecClient, streams api.Streams) (api.ExecResult, error) {
	for {
		frame, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// the daemon closed without an exit frame, which it only does
				// when the session never got to run.
				return api.ExecResult{}, nil
			}
			return api.ExecResult{}, localError(err)
		}
		switch f := frame.GetFrame().(type) {
		case *plbxv1.ExecServerFrame_Stdout:
			if _, err := streams.Stdout.Write(f.Stdout); err != nil {
				return api.ExecResult{}, err
			}
		case *plbxv1.ExecServerFrame_Stderr:
			if _, err := streams.Stderr.Write(f.Stderr); err != nil {
				return api.ExecResult{}, err
			}
		case *plbxv1.ExecServerFrame_ExitCode:
			return api.ExecResult{ExitCode: int(f.ExitCode)}, nil
		}
	}
}

// relayStdin forwards local input until it ends, then says so: a command
// reading to EOF has to learn that input is finished while its own output is
// still on the way back.
func relayStdin(send *frameSender, stdin io.Reader) {
	if stdin == nil {
		return
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := stdin.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := send.frame(&plbxv1.ExecClientFrame{
				Frame: &plbxv1.ExecClientFrame_Stdin{Stdin: chunk},
			}); err != nil {
				return
			}
		}
		if err != nil {
			_ = send.frame(&plbxv1.ExecClientFrame{
				Frame: &plbxv1.ExecClientFrame_StdinClose{StdinClose: &plbxv1.ExecStdinClose{}},
			})
			return
		}
	}
}

// relayResize forwards terminal dimensions for as long as the session lasts.
func relayResize(ctx context.Context, send *frameSender, sizes <-chan api.Size) {
	for {
		select {
		case size, ok := <-sizes:
			if !ok {
				return
			}
			if err := send.frame(&plbxv1.ExecClientFrame{
				Frame: &plbxv1.ExecClientFrame_Resize{Resize: protoSize(size)},
			}); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// frameSender serialises writes to a stream. Stdin and resize frames come from
// separate goroutines, and a gRPC stream permits only one sender at a time.
type frameSender struct {
	mu     sync.Mutex
	stream plbxv1.Sandboxes_ExecClient
}

func (s *frameSender) frame(f *plbxv1.ExecClientFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream.Send(f)
}

// Exec runs a session on behalf of a client, wiring the stream to the service's
// stdio. The daemon owns the session for its whole life, which is what lets it
// hold the pty and what the SSH gateway will later attach to.
func (s *Server) Exec(stream grpc.BidiStreamingServer[plbxv1.ExecClientFrame, plbxv1.ExecServerFrame]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "a session must open with a start frame")
	}

	ctx := stream.Context()
	req := apiExecRequest(start.GetRequest())

	stdin, stdinWriter := io.Pipe()
	sizes := make(chan api.Size, 1)
	if size := start.GetSize(); size != nil {
		sizes <- apiSize(size)
	}
	go serveFrames(ctx, stream, stdinWriter, sizes)

	out := &serverFrames{stream: stream}
	streams := api.Streams{
		Stdin:  stdin,
		Stdout: out.writer(stdoutFrame),
		Stderr: out.writer(stderrFrame),
	}
	// only a terminal session tracks size; leaving this nil elsewhere is what
	// tells the executor no pty is wanted.
	if req.TTY {
		streams.Resize = sizes
	}

	res, err := s.svc.Exec(ctx, apiRef(start.GetRef()), req, streams)
	if err != nil {
		return wireError(err)
	}
	return out.exit(int32(res.ExitCode))
}

// serveFrames feeds the client's input into the session until the stream ends.
func serveFrames(
	ctx context.Context,
	stream grpc.BidiStreamingServer[plbxv1.ExecClientFrame, plbxv1.ExecServerFrame],
	stdin *io.PipeWriter,
	sizes chan api.Size,
) {
	defer close(sizes)
	defer func() { _ = stdin.Close() }()

	for {
		frame, err := stream.Recv()
		if err != nil {
			return
		}
		switch f := frame.GetFrame().(type) {
		case *plbxv1.ExecClientFrame_Stdin:
			if _, err := stdin.Write(f.Stdin); err != nil {
				return
			}
		case *plbxv1.ExecClientFrame_StdinClose:
			_ = stdin.Close()
		case *plbxv1.ExecClientFrame_Resize:
			select {
			case sizes <- apiSize(f.Resize):
			case <-ctx.Done():
				return
			}
		}
	}
}

// frameKind picks which of the server's output frames a writer produces.
type frameKind int

const (
	stdoutFrame frameKind = iota
	stderrFrame
)

// serverFrames serialises the daemon's writes back to one client.
type serverFrames struct {
	mu     sync.Mutex
	stream grpc.BidiStreamingServer[plbxv1.ExecClientFrame, plbxv1.ExecServerFrame]
}

func (s *serverFrames) writer(kind frameKind) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		s.mu.Lock()
		defer s.mu.Unlock()

		chunk := make([]byte, len(p))
		copy(chunk, p)

		frame := &plbxv1.ExecServerFrame{}
		if kind == stderrFrame {
			frame.Frame = &plbxv1.ExecServerFrame_Stderr{Stderr: chunk}
		} else {
			frame.Frame = &plbxv1.ExecServerFrame_Stdout{Stdout: chunk}
		}
		if err := s.stream.Send(frame); err != nil {
			return 0, err
		}
		return len(p), nil
	})
}

func (s *serverFrames) exit(code int32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream.Send(&plbxv1.ExecServerFrame{
		Frame: &plbxv1.ExecServerFrame_ExitCode{ExitCode: code},
	})
}

// writerFunc adapts a function to io.Writer.
type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// makeRaw puts a terminal into raw mode, reporting whether there was one to put.
func makeRaw(stdin io.Reader) (restore func(), ok bool) {
	f, isFile := stdin.(*os.File)
	if !isFile || !term.IsTerminal(f.Fd()) {
		return nil, false
	}
	state, err := term.MakeRaw(f.Fd())
	if err != nil {
		return nil, false
	}
	return func() { _ = term.Restore(f.Fd(), state) }, true
}

// firstSize takes the opening dimensions if they are already known, without
// waiting for a terminal that may never report any.
func firstSize(sizes <-chan api.Size) (api.Size, bool) {
	if sizes == nil {
		return api.Size{}, false
	}
	select {
	case size, ok := <-sizes:
		return size, ok
	default:
		return api.Size{}, false
	}
}

func protoSize(s api.Size) *plbxv1.Size {
	return &plbxv1.Size{Rows: uint32(s.Rows), Cols: uint32(s.Cols)}
}

func apiSize(s *plbxv1.Size) api.Size {
	if s == nil {
		return api.Size{}
	}
	return api.Size{Rows: uint16(s.GetRows()), Cols: uint16(s.GetCols())}
}
