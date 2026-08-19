package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rhizomatous/planterbox/internal/proxy"
)

// policyFile holds the host's egress policy.
//
// It sits beside the record directory rather than inside it, because a record
// is named after its sandbox and one called "policy" would otherwise land on
// exactly this path.
const policyFile = "policy.json"

// ErrNoPolicy means no policy has been chosen yet, which is how the first run
// knows to ask.
var ErrNoPolicy = errors.New("no network policy has been set")

// hostKeyFile is the ssh gateway's identity. It sits beside the policy, in the
// state directory rather than the runtime one: the runtime directory is under
// /tmp on macOS, and a host key that changed on every reboot would trip every
// client's known-hosts check.
const hostKeyFile = "ssh_host_ed25519_key"

// SSHHostKeyPath reports where the ssh gateway's identity is kept.
func (s *Store) SSHHostKeyPath() string {
	return filepath.Join(filepath.Dir(s.dir), hostKeyFile)
}

// PolicyPath reports where the policy is kept.
func (s *Store) PolicyPath() string {
	return filepath.Join(filepath.Dir(s.dir), policyFile)
}

// Policy reads the host's egress policy, reporting [ErrNoPolicy] when there is
// none to read.
func (s *Store) Policy() (proxy.Policy, error) {
	data, err := os.ReadFile(s.PolicyPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return proxy.Policy{}, ErrNoPolicy
		}
		return proxy.Policy{}, fmt.Errorf("reading the network policy: %w", err)
	}
	var p proxy.Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return proxy.Policy{}, fmt.Errorf("decoding the network policy: %w", err)
	}
	return p, nil
}

// SetPolicy replaces the host's egress policy.
func (s *Store) SetPolicy(p proxy.Policy) error {
	if !p.Preset.Valid() {
		return fmt.Errorf("unknown preset %q", p.Preset)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the network policy: %w", err)
	}
	if s.readOnly {
		return nil
	}
	path := s.PolicyPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	return writeAtomic(path, append(data, '\n'))
}
