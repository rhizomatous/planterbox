package rpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rhizomatous/planterbox/internal/api"
)

// sentinels pairs the errors callers match on with the gRPC codes that carry
// them across the socket.
//
// Every code here is distinct, and has to stay that way: decoding takes the
// first entry whose code matches, so two sentinels sharing one would silently
// hand callers the wrong error. The codes are our own encoding rather than a
// claim about gRPC semantics; nothing outside this package reads them.
//
// Without this, every error would arrive as an opaque string and the checks
// that drive behaviour (the TUI's ErrRunning guard, create's ErrExists) would
// silently stop matching.
var sentinels = []struct {
	err  error
	code codes.Code
}{
	{api.ErrNotFound, codes.NotFound},
	{api.ErrExists, codes.AlreadyExists},
	{api.ErrRunning, codes.FailedPrecondition},
	{api.ErrNotImplemented, codes.Unimplemented},
	{api.ErrNoPolicy, codes.Unavailable},
	{api.ErrPortsUnavailable, codes.ResourceExhausted},
	{api.ErrCloneFailed, codes.Aborted},
	{api.ErrRemoteNotAdded, codes.DataLoss},
	{context.Canceled, codes.Canceled},
	{context.DeadlineExceeded, codes.DeadlineExceeded},
}

// remoteError is a failure that happened in the daemon. It reports the message
// the daemon gave and unwraps to the sentinel that message was built around, so
// errors.Is behaves identically either side of the socket.
type remoteError struct {
	msg      string
	sentinel error
}

func (e remoteError) Error() string { return e.msg }
func (e remoteError) Unwrap() error { return e.sentinel }

// wireError encodes err for the wire, preserving both its message and whichever
// sentinel it wraps.
func wireError(err error) error {
	if err == nil {
		return nil
	}
	for _, s := range sentinels {
		if errors.Is(err, s.err) {
			return status.Error(s.code, err.Error())
		}
	}
	return status.Error(codes.Unknown, err.Error())
}

// localError decodes a wire error back into one a caller can match on.
func localError(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	for _, s := range sentinels {
		if st.Code() == s.code {
			return remoteError{msg: st.Message(), sentinel: s.err}
		}
	}
	return errors.New(st.Message())
}
