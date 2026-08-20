package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/ui"
)

// The managed block is delimited so it can be rewritten without touching
// anything a user put around it. Both markers must be present for a rewrite;
// a file with only one is left alone rather than guessed at.
const (
	blockStart = "# >>> plbx >>> managed by `plbx setup ssh`; edits inside are lost"
	blockEnd   = "# <<< plbx <<<"
)

// sshConfigBlock renders the managed block.
//
// It offers no key and consults no agent. The gateway accepts any key and
// requires none, since the socket's permissions are what decide, so a client
// presenting its whole keyring hands credentials to something that will not
// look at them. Left to itself ssh does exactly that.
//
// `IdentityFile none` rather than /dev/null, which is the other way to say it:
// ssh reads /dev/null, fails to parse it, and says so on every connection.
//
// StrictHostKeyChecking is left on, against its own known-hosts file. The
// gateway's key is stable, living in the state directory precisely so it
// survives a reboot, so trust-on-first-use behaves the way it should. Turning
// the check off in a user's config to save one prompt would weaken something
// real to solve nothing.
func sshConfigBlock(exe, knownHosts string) string {
	return strings.Join([]string{
		blockStart,
		"Host *." + api.SSHDomain,
		// %n rather than %h: the sandbox is named by the hostname itself, and
		// %h is what a HostName line would have replaced it with.
		"    ProxyCommand " + shellQuote(exe) + " ssh-proxy %n",
		"    User " + sandboxUser,
		"    UserKnownHostsFile " + shellQuote(knownHosts),
		"    StrictHostKeyChecking accept-new",
		"",
		"    # The gateway accepts any key and needs none, so offer it nothing.",
		"    # Otherwise ssh walks your agent and presents every key you own to a",
		"    # connection that was never going to check them.",
		"    IdentityAgent none",
		"    IdentityFile none",
		"    IdentitiesOnly yes",
		"    ForwardAgent no",
		"",
		"    # A session gets its own proxy. Multiplexing would run several",
		"    # through one, which is not a shape this has been built for.",
		"    ControlMaster no",
		"    ControlPath none",
		blockEnd,
		"",
	}, "\n")
}

// sandboxUser is who a session runs as. It matches the base image contract's
// non-root user; the gateway does not take the client's word for it either
// way, but ssh has to send something and this is what is true.
const sandboxUser = "agent"

// writeManagedBlock puts the block into an ssh config, replacing the one
// already there. It reports whether anything changed.
func writeManagedBlock(path, block string) (changed bool, err error) {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}

	updated, ok := replaceBlock(string(existing), block)
	if !ok {
		// no block yet. Appending rather than prepending leaves the user's own
		// file in the order they wrote it.
		updated = string(existing)
		if updated != "" && !strings.HasSuffix(updated, "\n") {
			updated += "\n"
		}
		if updated != "" {
			updated += "\n"
		}
		updated += block
	}
	if updated == string(existing) {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// replaceBlock swaps the managed block for a new one, reporting whether it
// found one to swap.
func replaceBlock(content, block string) (string, bool) {
	start := strings.Index(content, blockStart)
	if start < 0 {
		return content, false
	}
	end := strings.Index(content[start:], blockEnd)
	if end < 0 {
		// an opening marker with no close: the file has been edited into a
		// shape we did not write, and rewriting it would eat whatever follows.
		return content, false
	}
	end += start + len(blockEnd)
	// take the newline after the end marker too, so replacing does not
	// accumulate blank lines.
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[:start] + block + content[end:], true
}

// sshConfigPath is the ssh client's own config.
func sshConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding your home directory: %w", err)
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// knownHostsPath is where the gateway's host key is remembered.
//
// Its own file rather than the usual one: these hosts exist only through plbx,
// and a key plbx generated does not belong among the machines you actually
// connect to.
func knownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding your home directory: %w", err)
	}
	return filepath.Join(home, ".ssh", "plbx_known_hosts"), nil
}

// shellQuote wraps a value the ssh config would otherwise split on spaces.
func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// reportSetup says what happened, and how to use it.
func reportSetup(cmd *cobra.Command, path string, changed bool) error {
	verb, style := "already set up in ", ui.Faint
	if changed {
		verb, style = "wrote the plbx block to ", ui.OK
	}
	_, err := lipgloss.Fprintln(cmd.OutOrStdout(),
		style.Render(verb)+ui.Value.Render(path)+"\n"+
			ui.Faint.Render("  ssh ")+ui.Value.Render(api.SSHHost("<sandbox>"))+
			ui.Faint.Render("   or point an editor's remote-ssh at the same host"))
	return err
}
