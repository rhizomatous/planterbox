package sshd

import (
	"fmt"
	"io"
	"strconv"

	"charm.land/ssh"
	gossh "golang.org/x/crypto/ssh"

	"github.com/rhizomatous/planterbox/internal/api"
)

// Forwarding a port into a sandbox cannot be done from here.
//
// `ssh -L` normally means the server dials the target itself. This server
// cannot: a sandbox is alone on an internal network, and on macOS that network
// is inside the runtime's VM with no route from a host process at all. It is
// the same wall the egress proxy met, in the same direction. See
// docs/concessions.md.
//
// So the dial happens inside the sandbox, in an exec, and the channel is wired
// to its stdio. bash opens the socket: /dev/tcp is a bash builtin rather than a
// program, so this needs nothing installed that the image contract does not
// already promise. The login shell is bash for the same reason.
//
// This is what an editor's remote attach uses, and what makes `ssh -L` reach a
// service in a sandbox without publishing it to the whole host.

// forwardData is the payload of a direct-tcpip channel request.
type forwardData struct {
	DestAddr   string
	DestPort   uint32
	OriginAddr string
	OriginPort uint32
}

// directTCPIP serves `ssh -L`, dialling from inside the sandbox.
func (s *Server) directTCPIP(_ *ssh.Server, _ *gossh.ServerConn, newChan gossh.NewChannel, ctx ssh.Context) {
	var data forwardData
	if err := gossh.Unmarshal(newChan.ExtraData(), &data); err != nil {
		_ = newChan.Reject(gossh.ConnectionFailed, "could not read the forward request")
		return
	}
	name, _ := ctx.Value(sandboxKey).(string)
	if name == "" {
		_ = newChan.Reject(gossh.ConnectionFailed, "no sandbox was named for this connection")
		return
	}
	if data.DestPort == 0 || data.DestPort > 65535 {
		_ = newChan.Reject(gossh.ConnectionFailed, "not a port: "+strconv.FormatUint(uint64(data.DestPort), 10))
		return
	}

	ch, reqs, err := newChan.Accept()
	if err != nil {
		return
	}
	go gossh.DiscardRequests(reqs)
	defer func() { _ = ch.Close() }()

	_, err = s.svc.Exec(ctx, api.ByName(name), api.ExecRequest{
		Cmd:         dialCommand(data.DestAddr, int(data.DestPort)),
		User:        agentUser,
		Interactive: true,
	}, api.Streams{Stdin: ch, Stdout: ch, Stderr: io.Discard})
	if err != nil {
		_, _ = fmt.Fprintln(ch.Stderr(), "plbx: "+err.Error())
	}
}

// dialCommand builds the bash that opens a connection inside the sandbox and
// joins it to the exec's stdio.
//
// The read half runs in the background and the write half in the foreground,
// so stdin closing is what ends the command. Closing descriptor 3 then takes
// the socket's write side down, which is what lets a server waiting on the end
// of a request answer instead of hanging.
//
// The target travels in the environment rather than in the script, so a
// hostname is never read as shell.
func dialCommand(host string, port int) []string {
	const script = `exec 3<>/dev/tcp/"$PLBX_FWD_HOST"/"$PLBX_FWD_PORT" || exit 1
cat <&3 &
cat >&3
exec 3<&- 3>&-
wait`
	return []string{
		"/usr/bin/env",
		"PLBX_FWD_HOST=" + host,
		"PLBX_FWD_PORT=" + strconv.Itoa(port),
		loginShell, "-c", script,
	}
}
