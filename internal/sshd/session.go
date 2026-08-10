package sshd

import (
	"context"
	"fmt"
	"strings"

	"charm.land/ssh"

	"github.com/rhizomatous/jardiniere/internal/api"
)

// agentUser is who a session runs as inside the sandbox. The base image
// contract puts a non-root `agent` at UID 1000, and an ssh client's own
// username says nothing about what is in there.
const agentUser = "agent"

// loginShell is what a session runs. The image contract is Ubuntu with bash as
// the agent's shell; an image without one cannot be attached to this way.
const loginShell = "/bin/bash"

// handle runs one ssh session as an exec inside the sandbox it was routed to.
func (s *Server) handle(sess ssh.Session) {
	name, _ := sess.Context().Value(sandboxKey).(string)
	if name == "" {
		fail(sess, "no sandbox was named for this connection")
		return
	}

	ctx := sess.Context()
	sb, err := s.svc.Inspect(ctx, api.ByName(name))
	if err != nil {
		fail(sess, err.Error())
		return
	}
	if sb.State.Status != api.StatusRunning {
		fail(sess, fmt.Sprintf("%s is not running; start it with `jard start %s`", name, name))
		return
	}

	req := api.ExecRequest{
		Cmd:         command(sess.RawCommand()),
		Env:         allowedEnv(sess.Environ()),
		Workdir:     sb.Spec.Primary().Host,
		User:        agentUser,
		Interactive: true,
	}
	streams := api.Streams{Stdin: sess, Stdout: sess, Stderr: sess.Stderr()}

	if pty, windows, ok := sess.Pty(); ok {
		req.TTY = true
		if req.Env == nil {
			req.Env = map[string]string{}
		}
		req.Env["TERM"] = pty.Term
		streams.Resize = resizes(ctx, pty, windows)
	}

	res, err := s.svc.Exec(ctx, api.ByName(name), req, streams)
	if err != nil {
		fail(sess, err.Error())
		return
	}
	_ = sess.Exit(res.ExitCode)
}

// command turns what the client asked for into something to run.
//
// A bare `ssh host` gets a login shell. `ssh host <command>` runs it through
// one, as sshd does, so a PATH set by the profile and a shell's own quoting
// both behave the way the caller expects.
func command(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{loginShell, "-l"}
	}
	return []string{loginShell, "-lc", raw}
}

// resizes carries the client's terminal size to the session, opening size
// first.
//
// The first value goes in before this returns rather than from the goroutine:
// produced asynchronously it would race the session's start, and a pty that
// starts unsized lays a full-screen program out against nothing.
func resizes(ctx context.Context, pty ssh.Pty, windows <-chan ssh.Window) <-chan api.Size {
	sizes := make(chan api.Size, 1)
	sizes <- api.Size{Rows: uint16(pty.Window.Height), Cols: uint16(pty.Window.Width)}

	go func() {
		defer close(sizes)
		for {
			select {
			case w, ok := <-windows:
				if !ok {
					return
				}
				select {
				case sizes <- api.Size{Rows: uint16(w.Height), Cols: uint16(w.Width)}:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return sizes
}

// envAllowed are the variables a client may set on a session.
//
// An allowlist rather than a blocklist. The variables worth refusing are the
// ones that change what runs — PATH, NODE_OPTIONS, LD_PRELOAD — and the ones
// carrying credentials, and neither set can be enumerated: every runtime adds
// its own hook variable, and a credential can be called anything. So the
// sandbox's own environment stands, and a client adjusts only what cannot
// redirect execution.
var envAllowed = map[string]bool{
	"COLORTERM": true,
	"LANG":      true,
	"TERM":      true,
	"TZ":        true,
}

// envAllowedPrefixes are the families allowed wholesale. LC_* is locale, and
// is the one thing ssh clients routinely forward.
var envAllowedPrefixes = []string{"LC_"}

// allowedEnv keeps the variables a session may carry in from the client.
func allowedEnv(environ []string) map[string]string {
	var env map[string]string
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !envAllows(k) {
			continue
		}
		if env == nil {
			env = map[string]string{}
		}
		env[k] = v
	}
	return env
}

// envAllows reports whether a client may set this variable.
func envAllows(name string) bool {
	if envAllowed[name] {
		return true
	}
	for _, prefix := range envAllowedPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// fail tells the client why, and exits non-zero. The message goes to stderr so
// it does not land in the output of whatever command was asked for.
func fail(sess ssh.Session, msg string) {
	_, _ = fmt.Fprintln(sess.Stderr(), "jard: "+msg)
	_ = sess.Exit(1)
}
