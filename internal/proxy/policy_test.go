package proxy

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

func TestMatchesExactHost(t *testing.T) {
	cases := []struct {
		pattern string
		host    string
		want    bool
	}{
		{pattern: "example.com", host: "example.com", want: true},
		{pattern: "example.com", host: "other.com", want: false},
		{pattern: "example.com", host: "sub.example.com", want: false},
		{pattern: "example.com", host: "notexample.com", want: false},
		// a trailing dot is the same name in DNS, and resolvers send both.
		{pattern: "example.com", host: "example.com.", want: true},
		// hostnames are case-insensitive, and a policy that misses on case
		// would deny requests the user believes they allowed.
		{pattern: "Example.COM", host: "example.com", want: true},
		{pattern: "example.com", host: "EXAMPLE.com", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+" vs "+tc.host, func(t *testing.T) {
			if got := Matches(tc.pattern, Target{Host: tc.host, Port: 443}); got != tc.want {
				t.Errorf("Matches(%q, %q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
			}
		})
	}
}

func TestMatchesWildcardCoversSubdomainsButNotTheApex(t *testing.T) {
	// this is why real policies list both forms. Getting it backwards would
	// silently widen every wildcard rule by one domain.
	cases := []struct {
		host string
		want bool
	}{
		{host: "a.example.com", want: true},
		{host: "a.b.example.com", want: true},
		{host: "example.com", want: false},
		{host: "notexample.com", want: false},
		{host: "example.com.evil.test", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			if got := Matches("*.example.com", Target{Host: tc.host, Port: 443}); got != tc.want {
				t.Errorf("Matches(*.example.com, %q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestMatchesBareStarCoversEverything(t *testing.T) {
	for _, host := range []string{"example.com", "a.b.c", "10.0.0.1"} {
		if !Matches("*", Target{Host: host, Port: 443}) {
			t.Errorf("* should cover %q", host)
		}
	}
}

func TestMatchesPorts(t *testing.T) {
	cases := []struct {
		pattern string
		host    string
		port    int
		want    bool
	}{
		{pattern: "example.com:443", host: "example.com", port: 443, want: true},
		{pattern: "example.com:443", host: "example.com", port: 80, want: false},
		// no port means every port.
		{pattern: "example.com", host: "example.com", port: 443, want: true},
		{pattern: "example.com", host: "example.com", port: 80, want: true},
		{pattern: "*.example.com:8080", host: "a.example.com", port: 8080, want: true},
		{pattern: "*.example.com:8080", host: "a.example.com", port: 443, want: false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s vs %s:%d", tc.pattern, tc.host, tc.port), func(t *testing.T) {
			if got := Matches(tc.pattern, Target{Host: tc.host, Port: tc.port}); got != tc.want {
				t.Errorf("Matches(%q, %s:%d) = %v, want %v", tc.pattern, tc.host, tc.port, got, tc.want)
			}
		})
	}
}

func TestMatchesTreatsAMalformedPortAsPartOfTheHost(t *testing.T) {
	// reading "example.com:https" as "every port on example.com" would grant
	// more than the rule says. Matching nothing is the safe reading.
	if Matches("example.com:https", Target{Host: "example.com", Port: 443}) {
		t.Error("a rule with an unparseable port should not cover every port")
	}
}

func TestOpenAllowsEverything(t *testing.T) {
	p := New(PresetOpen)
	v := p.Check(Target{Host: "anything.test", Port: 443})
	if !v.Allowed {
		t.Errorf("open denied %v: %s", "anything.test", v.Reason)
	}
}

func TestLockedDownAllowsNothingUntilTold(t *testing.T) {
	p := New(PresetLockedDown)
	if v := p.Check(Target{Host: "api.anthropic.com", Port: 443}); v.Allowed {
		t.Error("locked-down should refuse even the model providers")
	}

	p.Rules = append(p.Rules, Rule{Pattern: "api.anthropic.com", Allow: true})
	if v := p.Check(Target{Host: "api.anthropic.com", Port: 443}); !v.Allowed {
		t.Errorf("an explicit allow should still work: %s", v.Reason)
	}
}

func TestBalancedAllowsTheWorkAnAgentActuallyDoes(t *testing.T) {
	// balanced has to be usable for real work, and these are the requests that
	// means.
	p := New(PresetBalanced)
	for _, host := range []string{
		"api.anthropic.com",
		"registry.npmjs.org",
		"pypi.org",
		"files.pythonhosted.org",
		"proxy.golang.org",
		"crates.io",
		"static.crates.io",
		"github.com",
		"codeload.github.com",
		"raw.githubusercontent.com",
		"archive.ubuntu.com",
		"deb.debian.org",
		"dl-cdn.alpinelinux.org",
	} {
		if v := p.Check(Target{Host: host, Port: 443}); !v.Allowed {
			t.Errorf("balanced denied %s: %s", host, v.Reason)
		}
	}
}

// An editor attaching to a sandbox installs its own server into it first, and
// a preset that stops halfway through that is a preset that hangs.
func TestBalancedAllowsAnEditorToAttach(t *testing.T) {
	p := New(PresetBalanced)
	for _, host := range []string{
		"update.code.visualstudio.com",
		"vscode.download.prss.microsoft.com",
		"main.vscode-cdn.net",
		"marketplace.visualstudio.com",
		"gallerycdn.vsassets.io",
	} {
		if v := p.Check(Target{Host: host, Port: 443}); !v.Allowed {
			t.Errorf("balanced denied %s: %s", host, v.Reason)
		}
	}
}

// Telemetry is not work. It stays denied, and nothing breaks for it.
func TestBalancedDeniesEditorTelemetry(t *testing.T) {
	p := New(PresetBalanced)
	for _, host := range []string{
		"mobile.events.data.microsoft.com",
		"dc.services.visualstudio.com",
	} {
		if v := p.Check(Target{Host: host, Port: 443}); v.Allowed {
			t.Errorf("balanced allowed telemetry to %s: %s", host, v.Reason)
		}
	}
}

// An agent does more than call the model API: it signs in and checks what it
// is entitled to, and a preset that allows only the API is one it will not
// start under. platform.claude.com is the one Claude Code stops without.
func TestBalancedAllowsAnAgentToStart(t *testing.T) {
	p := New(PresetBalanced)
	for _, host := range []string{
		"api.anthropic.com",
		"platform.claude.com",
		"claude.com",
		"statsig.anthropic.com",
		"api.openai.com",
	} {
		if v := p.Check(Target{Host: host, Port: 443}); !v.Allowed {
			t.Errorf("balanced denied %s: %s", host, v.Reason)
		}
	}
}

func TestBalancedStillDeniesEverythingElse(t *testing.T) {
	p := New(PresetBalanced)
	for _, host := range []string{"example.com", "evil.test", "pastebin.com"} {
		if v := p.Check(Target{Host: host, Port: 443}); v.Allowed {
			t.Errorf("balanced allowed %s, which it should not", host)
		}
	}
}

func TestDenyBeatsAllowHoweverTheyAreOrdered(t *testing.T) {
	// a denial someone bothered to write must not be undone by a broader allow
	// sitting beside it, whichever came first.
	target := Target{Host: "blocked.example.com", Port: 443}

	denyFirst := Policy{Preset: PresetBalanced, Rules: []Rule{
		{Pattern: "blocked.example.com"},
		{Pattern: "*.example.com", Allow: true},
	}}
	allowFirst := Policy{Preset: PresetBalanced, Rules: []Rule{
		{Pattern: "*.example.com", Allow: true},
		{Pattern: "blocked.example.com"},
	}}

	for name, p := range map[string]Policy{"deny first": denyFirst, "allow first": allowFirst} {
		if v := p.Check(target); v.Allowed {
			t.Errorf("%s: deny should win, got %s", name, v.Reason)
		}
	}
}

func TestDenyBeatsEvenTheOpenPreset(t *testing.T) {
	p := Policy{Preset: PresetOpen, Rules: []Rule{{Pattern: "blocked.test"}}}
	if v := p.Check(Target{Host: "blocked.test", Port: 443}); v.Allowed {
		t.Error("a deny rule should hold under the open preset too")
	}
	if v := p.Check(Target{Host: "other.test", Port: 443}); !v.Allowed {
		t.Error("open should still allow everything else")
	}
}

func TestPrivateSpaceIsNeverReachable(t *testing.T) {
	// a sandbox that could reach these would be on the host's network: the
	// daemon's own socket, every other sandbox, and whatever else is bound.
	for _, host := range []string{
		"127.0.0.1",
		"::1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.169.254", // the cloud metadata endpoint
		"0.0.0.0",
		"100.64.0.1",       // carrier-grade NAT
		"fe80::1",          // link-local
		"fc00::1",          // unique local
		"::ffff:127.0.0.1", // loopback wearing an IPv6 wrapper
	} {
		// even under open, and even with an explicit allow for it.
		p := Policy{Preset: PresetOpen, Rules: []Rule{{Pattern: host, Allow: true}, {Pattern: "*", Allow: true}}}
		if v := p.Check(Target{Host: host, Port: 443}); v.Allowed {
			t.Errorf("%s was reachable, and no policy should make it so", host)
		}
	}
}

func TestPublicAddressesAreStillReachableByIP(t *testing.T) {
	p := Policy{Preset: PresetOpen}
	if v := p.Check(Target{Host: "1.1.1.1", Port: 443}); !v.Allowed {
		t.Errorf("a public address should be reachable under open: %s", v.Reason)
	}
}

func TestAllowsAddressRejectsTheInvalid(t *testing.T) {
	if AllowsAddress(netip.Addr{}) {
		t.Error("the zero address should never be connectable")
	}
}

func TestVerdictSaysWhatDecided(t *testing.T) {
	// the reason reaches the user and the connection log, so "denied" alone
	// would leave them nothing to act on.
	p := Policy{Preset: PresetBalanced, Rules: []Rule{{Pattern: "allowed.test", Allow: true}}}

	if v := p.Check(Target{Host: "allowed.test", Port: 443}); !strings.Contains(v.Reason, "allowed.test") {
		t.Errorf("reason = %q, want it to name the rule", v.Reason)
	}
	if v := p.Check(Target{Host: "other.test", Port: 443}); !strings.Contains(v.Reason, "balanced") {
		t.Errorf("reason = %q, want it to name the default that applied", v.Reason)
	}
}

func TestPresetsAreAllValidAndDescribed(t *testing.T) {
	for _, p := range Presets {
		if !p.Valid() {
			t.Errorf("%q is offered but not valid", p)
		}
		if p.Description() == "" {
			t.Errorf("%q has no description for the wizard", p)
		}
	}
	if Preset("nonsense").Valid() {
		t.Error("an unknown preset should not validate")
	}
}

func TestTargetString(t *testing.T) {
	if got := (Target{Host: "example.com", Port: 443}).String(); got != "example.com:443" {
		t.Errorf("String() = %q", got)
	}
	if got := (Target{Host: "example.com"}).String(); got != "example.com" {
		t.Errorf("String() with no port = %q", got)
	}
}

func TestSwitchingPresetActuallyChangesTheBaseline(t *testing.T) {
	// the preset's own allowances must not be copied into Rules. Materialised,
	// a switch would carry the old preset's whole list forward and the new
	// preset would decide nothing.
	p := New(PresetBalanced)
	if len(p.Rules) != 0 {
		t.Fatalf("a fresh policy has %d rules, want none of its own", len(p.Rules))
	}
	if v := p.Check(Target{Host: "api.anthropic.com", Port: 443}); !v.Allowed {
		t.Fatalf("balanced should allow the model provider: %s", v.Reason)
	}

	p.Preset = PresetLockedDown
	if v := p.Check(Target{Host: "api.anthropic.com", Port: 443}); v.Allowed {
		t.Errorf("locked-down still allowed it: %s", v.Reason)
	}
}

func TestRulesSurviveAPresetSwitch(t *testing.T) {
	// a rule is what someone added by hand, so changing the baseline must not
	// quietly discard it.
	p := New(PresetBalanced)
	p.Rules = append(p.Rules, Rule{Pattern: "mine.test", Allow: true})

	p.Preset = PresetLockedDown
	if v := p.Check(Target{Host: "mine.test", Port: 443}); !v.Allowed {
		t.Errorf("a hand-added allow was lost switching preset: %s", v.Reason)
	}
}

func TestARuleBeatsThePresetInBothDirections(t *testing.T) {
	// denying something balanced allows has to work, and so does allowing
	// something under locked-down.
	balancedWithDeny := Policy{Preset: PresetBalanced, Rules: []Rule{{Pattern: "github.com"}}}
	if v := balancedWithDeny.Check(Target{Host: "github.com", Port: 443}); v.Allowed {
		t.Errorf("a deny should override the preset: %s", v.Reason)
	}

	lockedWithAllow := Policy{Preset: PresetLockedDown, Rules: []Rule{{Pattern: "github.com", Allow: true}}}
	if v := lockedWithAllow.Check(Target{Host: "github.com", Port: 443}); !v.Allowed {
		t.Errorf("an allow should override the preset: %s", v.Reason)
	}
}

func TestPresetAllowancesAreOnlyBalanced(t *testing.T) {
	if len(PresetBalanced.Allowances()) == 0 {
		t.Error("balanced should list what it allows, for display")
	}
	for _, p := range []Preset{PresetOpen, PresetLockedDown} {
		if len(p.Allowances()) != 0 {
			t.Errorf("%s should carry no list of its own", p)
		}
	}
}
