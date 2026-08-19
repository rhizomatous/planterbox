// Package proxy is plbx's egress control: the policy that decides what a
// sandbox may reach, and the filtering proxy that enforces it.
//
// The policy engine is a pure function of (rules, target). Nothing in this file
// opens a connection or reads a file, so the decisions can be tested
// exhaustively rather than observed.
package proxy

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// Preset is a starting posture, chosen once and then adjusted with rules.
type Preset string

// the presets a policy can start from.
const (
	// PresetOpen allows everything. Egress is unrestricted.
	PresetOpen Preset = "open"
	// PresetBalanced denies by default and allows the places development
	// actually needs: package registries, source hosts, and model providers.
	PresetBalanced Preset = "balanced"
	// PresetLockedDown denies by default and allows nothing until told to,
	// including the model provider APIs.
	PresetLockedDown Preset = "locked-down"
)

// Presets are the choices offered on first run, in the order to offer them.
var Presets = []Preset{PresetBalanced, PresetOpen, PresetLockedDown}

// Valid reports whether p names a preset plbx knows.
func (p Preset) Valid() bool { return slices.Contains(Presets, p) }

// Description is the one-line explanation the first-run wizard shows.
func (p Preset) Description() string {
	switch p {
	case PresetOpen:
		return "everything is reachable; nothing is filtered"
	case PresetBalanced:
		return "package registries, source hosts, and model providers; everything else denied"
	case PresetLockedDown:
		return "nothing is reachable until you allow it, model providers included"
	default:
		return ""
	}
}

// Rule is one entry in a policy.
//
// Pattern is a host, optionally with a port: "example.com", "example.com:443",
// "*.example.com". A rule with no port applies to every port.
type Rule struct {
	Pattern string `json:"pattern"`
	Allow   bool   `json:"allow"`
}

// Policy is a preset plus the rules layered over it.
type Policy struct {
	Preset Preset `json:"preset"`
	Rules  []Rule `json:"rules,omitempty"`
}

// Target is one thing a sandbox is trying to reach.
type Target struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// String renders a target the way rules are written, for logs and messages.
func (t Target) String() string {
	if t.Port == 0 {
		return t.Host
	}
	return t.Host + ":" + strconv.Itoa(t.Port)
}

// Verdict is what a policy decided, and what decided it.
type Verdict struct {
	Allowed bool
	// Reason names the rule that matched, or the default that applied. It is
	// shown to the user, so it says which rule rather than merely "denied".
	Reason string
}

// Check decides whether a target may be reached.
//
// Order is deliberate. Private space is refused before any rule is consulted,
// so no policy can hand a sandbox the host's own network. Then deny rules,
// which beat allow rules however they were written: a denial anyone bothered
// to express should not be undone by a broader allow sitting next to it. Then
// allow rules, then whatever the preset permits on its own.
//
// A rule therefore beats the preset in both directions: denying something the
// balanced preset allows works, and so does allowing something under
// locked-down.
func (p Policy) Check(t Target) Verdict {
	if addr, err := netip.ParseAddr(t.Host); err == nil && !AllowsAddress(addr) {
		return Verdict{Reason: "private, loopback, and link-local addresses are never reachable"}
	}

	if rule, ok := match(p.Rules, t, false); ok {
		return Verdict{Reason: "denied by rule " + rule.Pattern}
	}
	if rule, ok := match(p.Rules, t, true); ok {
		return Verdict{Allowed: true, Reason: "allowed by rule " + rule.Pattern}
	}

	if p.Preset == PresetOpen {
		return Verdict{Allowed: true, Reason: "the open preset allows everything"}
	}
	if pattern, ok := p.Preset.Allows(t); ok {
		return Verdict{Allowed: true, Reason: "allowed by the " + string(p.Preset) + " preset (" + pattern + ")"}
	}
	return Verdict{Reason: "not allowed by any rule, and the " + string(p.Preset) + " preset denies by default"}
}

// match finds the first rule of the given kind that covers the target.
func match(rules []Rule, t Target, allow bool) (Rule, bool) {
	for _, r := range rules {
		if r.Allow == allow && Matches(r.Pattern, t) {
			return r, true
		}
	}
	return Rule{}, false
}

// Matches reports whether a pattern covers a target.
//
// "*" covers everything. A leading "*." covers subdomains but not the domain
// itself: "*.example.com" is not "example.com", which is why policies written
// against real services list both. A pattern with no port covers every port.
func Matches(pattern string, t Target) bool {
	host, port := splitPattern(pattern)
	if port != 0 && port != t.Port {
		return false
	}

	target := strings.ToLower(strings.TrimSuffix(t.Host, "."))
	host = strings.ToLower(host)

	switch {
	case host == "*":
		return true
	case strings.HasPrefix(host, "*."):
		return strings.HasSuffix(target, host[1:])
	default:
		return target == host
	}
}

// ParseAuthority reads a "host" or "host:port" authority, defaulting the port.
//
// Brackets around an IPv6 literal are stripped, so a host compares equal
// wherever it was written. Everything that turns text into a [Target] comes
// through here: a rule that parsed differently from the request it is meant to
// cover would match nothing and say nothing.
func ParseAuthority(authority string, defaultPort int) (Target, error) {
	if authority == "" {
		return Target{}, errors.New("no host given")
	}
	// an authority with no port is normal for plain HTTP, and SplitHostPort
	// calls that an error. Deciding first keeps a genuinely malformed
	// authority from being read as a bare hostname.
	if !hasPort(authority) {
		return Target{Host: strings.Trim(authority, "[]"), Port: defaultPort}, nil
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		return Target{}, fmt.Errorf("invalid host %q", authority)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return Target{}, fmt.Errorf("invalid port %q", port)
	}
	return Target{Host: strings.Trim(host, "[]"), Port: n}, nil
}

// hasPort reports whether an authority carries one. A bracketed address keeps
// its colons inside the brackets, so only what follows them counts; an
// unbracketed address with several colons is a bare IPv6 literal rather than a
// host and port.
func hasPort(authority string) bool {
	if i := strings.LastIndex(authority, "]"); i >= 0 {
		return strings.Contains(authority[i:], ":")
	}
	return strings.Count(authority, ":") == 1
}

// splitPattern separates a pattern's host from its port, tolerating a pattern
// with no port at all. A malformed port leaves the pattern whole, so it matches
// nothing rather than silently covering every port.
func splitPattern(pattern string) (host string, port int) {
	if !hasPort(pattern) {
		return strings.Trim(pattern, "[]"), 0
	}
	h, p, err := net.SplitHostPort(pattern)
	if err != nil {
		return pattern, 0
	}
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 || n > 65535 {
		return pattern, 0
	}
	return strings.Trim(h, "[]"), n
}

// AllowsAddress reports whether an address may be connected to at all.
//
// This is not policy and cannot be overridden by a rule. A sandbox that could
// reach loopback or private space would be on the host's network rather than
// its own, and every service the host runs, the daemon's own socket and any
// other sandbox included, would be a request away.
func AllowsAddress(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	// an IPv4-in-IPv6 address is the IPv4 one for every purpose here, and
	// checking the wrapper instead would miss ::ffff:127.0.0.1.
	addr = addr.Unmap()

	switch {
	case addr.IsLoopback(),
		addr.IsPrivate(),
		addr.IsLinkLocalUnicast(),
		addr.IsLinkLocalMulticast(),
		addr.IsInterfaceLocalMulticast(),
		addr.IsMulticast(),
		addr.IsUnspecified():
		return false
	}
	// carrier-grade NAT space, which netip has no predicate for and which
	// runtimes hand out on some hosts.
	if addr.Is4() && cgnat.Contains(addr) {
		return false
	}
	return true
}

// cgnat is 100.64.0.0/10, RFC 6598.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")
