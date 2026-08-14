package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rhizomatous/planterbox/internal/api"
)

// parseWorkspace reads a workspace argument, "path[:ro|rw]". A relative path
// resolves against base, and the result is always absolute, because the sandbox
// binds it at the same path the host has.
//
// Read-write is the default: an agent has to be able to edit the repo it was
// pointed at.
func parseWorkspace(arg, base string) (api.Workspace, error) {
	path, mode, hasMode := strings.Cut(arg, ":")
	if path == "" {
		return api.Workspace{}, fmt.Errorf("workspace %q has no path", arg)
	}

	readOnly := false
	if hasMode {
		switch mode {
		case "ro":
			readOnly = true
		case "rw":
		default:
			// a second colon means the path itself contains one, which a mount
			// spec cannot represent.
			return api.Workspace{}, fmt.Errorf("workspace %q: expected a :ro or :rw suffix, got %q", arg, mode)
		}
	}

	abs, err := resolve(path, base)
	if err != nil {
		return api.Workspace{}, fmt.Errorf("workspace %q: %w", arg, err)
	}
	return api.Workspace{Host: abs, ReadOnly: readOnly}, nil
}

// resolve makes path absolute relative to base, and cleans it.
func resolve(path, base string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// parsePort reads a port argument: "host:sandbox", or a bare port meaning the
// same on both sides. Either form takes an optional "/tcp", which is the
// only protocol a sandbox can publish.
func parsePort(arg string) (api.Port, error) {
	spec, proto, hasProto := strings.Cut(arg, "/")
	if hasProto && proto != "tcp" {
		return api.Port{}, fmt.Errorf("port %q: only tcp ports can be published", arg)
	}
	if proto == "tcp" {
		proto = "" // the default; storing it would only make records noisier
	}

	hostStr, sandboxStr, hasBoth := strings.Cut(spec, ":")
	if !hasBoth {
		sandboxStr = hostStr
	}

	host, err := parsePortNumber(hostStr, arg)
	if err != nil {
		return api.Port{}, err
	}
	sandbox, err := parsePortNumber(sandboxStr, arg)
	if err != nil {
		return api.Port{}, err
	}
	return api.Port{Host: host, Sandbox: sandbox, Proto: proto}, nil
}

func parsePortNumber(s, arg string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("port %q: %q is not a number", arg, s)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port %q: %d is out of range 1-65535", arg, n)
	}
	return n, nil
}

// parseCopyPath reads one side of a `plbx cp` argument. "<sandbox>:/path" is
// inside a sandbox; anything else is a host path.
//
// A host path may legitimately contain a colon, so the two are told apart by
// requiring the text before the first colon to look like a sandbox name, and
// what follows to be non-empty. Absolute paths are therefore unambiguous, and a
// host path that does look like a sandbox reference can be written "./x".
func parseCopyPath(arg string) (api.Path, error) {
	if arg == "" {
		return api.Path{}, fmt.Errorf("empty path")
	}
	name, rest, hasColon := strings.Cut(arg, ":")
	if !hasColon || rest == "" || !api.ValidName(name) {
		return api.Path{Path: arg}, nil
	}
	return api.Path{Sandbox: name, Path: rest}, nil
}
