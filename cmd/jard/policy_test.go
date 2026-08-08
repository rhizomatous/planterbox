package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rhizomatous/jardiniere/internal/api"
	"github.com/rhizomatous/jardiniere/internal/proxy"
)

// withPolicy returns a fake already holding a policy, so a test exercising a
// command does not also exercise the first-run chooser.
func withPolicy(p proxy.Policy) *api.Fake {
	f := api.NewFake()
	f.NetworkPolicy = &p
	return f
}

func TestPolicyLsShowsThePresetAndTheRules(t *testing.T) {
	fake := withPolicy(proxy.Policy{
		Preset: proxy.PresetBalanced,
		Rules:  []proxy.Rule{{Pattern: "mine.test", Allow: true}, {Pattern: "bad.test"}},
	})

	out, err := runCLI(t, fake, "policy", "ls")
	if err != nil {
		t.Fatalf("policy ls: %v", err)
	}
	for _, want := range []string{"balanced", "mine.test", "bad.test", "allowed", "denied"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPolicyLsSummarisesThePresetsOwnHosts(t *testing.T) {
	// listing all forty would bury the rules someone actually added.
	out, err := runCLI(t, withPolicy(proxy.New(proxy.PresetBalanced)), "policy", "ls")
	if err != nil {
		t.Fatalf("policy ls: %v", err)
	}
	if strings.Contains(out, "registry.npmjs.org") {
		t.Errorf("the preset's own hosts should be summarised, not listed:\n%s", out)
	}
	if !strings.Contains(out, "on its own") {
		t.Errorf("output should say the preset allows hosts of its own:\n%s", out)
	}
}

func TestPolicyAllowAndDenyReachTheService(t *testing.T) {
	fake := withPolicy(proxy.New(proxy.PresetBalanced))

	if _, err := runCLI(t, fake, "policy", "allow", "a.test", "b.test"); err != nil {
		t.Fatalf("policy allow: %v", err)
	}
	if _, err := runCLI(t, fake, "policy", "deny", "c.test"); err != nil {
		t.Fatalf("policy deny: %v", err)
	}

	got := *fake.NetworkPolicy
	if len(got.Rules) != 3 {
		t.Fatalf("rules = %+v, want three", got.Rules)
	}
	if !got.Rules[0].Allow || got.Rules[0].Pattern != "a.test" {
		t.Errorf("first rule = %+v", got.Rules[0])
	}
	if got.Rules[2].Allow || got.Rules[2].Pattern != "c.test" {
		t.Errorf("third rule = %+v", got.Rules[2])
	}
}

func TestAllowingSomethingDeniedReplacesTheRule(t *testing.T) {
	// otherwise the deny stays, keeps winning, and `allow` appears to do
	// nothing at all.
	fake := withPolicy(proxy.Policy{Preset: proxy.PresetBalanced, Rules: []proxy.Rule{{Pattern: "x.test"}}})

	if _, err := runCLI(t, fake, "policy", "allow", "x.test"); err != nil {
		t.Fatalf("policy allow: %v", err)
	}
	got := *fake.NetworkPolicy
	if len(got.Rules) != 1 || !got.Rules[0].Allow {
		t.Errorf("rules = %+v, want the deny replaced by an allow", got.Rules)
	}
	if v := got.Check(proxy.Target{Host: "x.test", Port: 443}); !v.Allowed {
		t.Errorf("x.test still denied: %s", v.Reason)
	}
}

func TestPolicyRejectsAPatternThatWouldNeverMatch(t *testing.T) {
	// a rule that matches nothing is a silent no-op the user only discovers
	// when their traffic is still blocked.
	for _, pattern := range []string{"https://example.com", "example.com/path", "*example.com", ""} {
		if _, err := runCLI(t, withPolicy(proxy.New(proxy.PresetBalanced)), "policy", "allow", pattern); err == nil {
			t.Errorf("allow %q should have been refused", pattern)
		}
	}
}

func TestPolicyRmDropsARuleAndReportsAnAbsentOne(t *testing.T) {
	fake := withPolicy(proxy.Policy{Preset: proxy.PresetBalanced, Rules: []proxy.Rule{{Pattern: "x.test", Allow: true}}})

	if _, err := runCLI(t, fake, "policy", "rm", "x.test"); err != nil {
		t.Fatalf("policy rm: %v", err)
	}
	if len(fake.NetworkPolicy.Rules) != 0 {
		t.Errorf("rules = %+v, want none", fake.NetworkPolicy.Rules)
	}
	if _, err := runCLI(t, fake, "policy", "rm", "nothing.test"); err == nil {
		t.Error("removing a rule that isn't there should say so rather than succeed silently")
	}
}

func TestPolicyCheckExplainsItself(t *testing.T) {
	fake := withPolicy(proxy.Policy{Preset: proxy.PresetBalanced, Rules: []proxy.Rule{{Pattern: "yes.test", Allow: true}}})

	out, err := runCLI(t, fake, "policy", "check", "yes.test")
	if err != nil {
		t.Fatalf("policy check: %v", err)
	}
	if !strings.Contains(out, "allowed") || !strings.Contains(out, "yes.test") {
		t.Errorf("output = %q, want it to say what it decided", out)
	}

	// a denial is an answer, not a failure: the command did what was asked.
	out, err = runCLI(t, fake, "policy", "check", "no.test")
	if err != nil {
		t.Fatalf("a check reporting a denial should still exit clean: %v", err)
	}
	if !strings.Contains(out, "denied") {
		t.Errorf("output = %q, want a denial", out)
	}
}

func TestPolicyCheckDefaultsToHTTPS(t *testing.T) {
	fake := withPolicy(proxy.Policy{Preset: proxy.PresetLockedDown, Rules: []proxy.Rule{{Pattern: "x.test:443", Allow: true}}})

	out, err := runCLI(t, fake, "policy", "check", "x.test")
	if err != nil {
		t.Fatalf("policy check: %v", err)
	}
	if !strings.Contains(out, "allowed") {
		t.Errorf("output = %q, want :443 assumed", out)
	}
}

func TestPolicyPresetSwitchesTheBaselineAndKeepsTheRules(t *testing.T) {
	fake := withPolicy(proxy.Policy{
		Preset: proxy.PresetBalanced,
		Rules:  []proxy.Rule{{Pattern: "mine.test", Allow: true}},
	})

	if _, err := runCLI(t, fake, "policy", "preset", "locked-down"); err != nil {
		t.Fatalf("policy preset: %v", err)
	}
	got := *fake.NetworkPolicy
	if got.Preset != proxy.PresetLockedDown {
		t.Errorf("preset = %q, want locked-down", got.Preset)
	}
	if len(got.Rules) != 1 || got.Rules[0].Pattern != "mine.test" {
		t.Errorf("rules = %+v, want the hand-added one kept", got.Rules)
	}
	if v := got.Check(proxy.Target{Host: "api.anthropic.com", Port: 443}); v.Allowed {
		t.Errorf("locked-down still allows the model provider: %s", v.Reason)
	}
}

func TestPolicyPresetRejectsAnUnknownName(t *testing.T) {
	if _, err := runCLI(t, withPolicy(proxy.New(proxy.PresetBalanced)), "policy", "preset", "paranoid"); err == nil {
		t.Error("an unknown preset should be refused, with the valid ones named")
	}
}

func TestPolicyLogShowsDecisions(t *testing.T) {
	fake := withPolicy(proxy.New(proxy.PresetBalanced))
	fake.Decisions = []proxy.Entry{
		{Seq: 1, At: time.Now(), Target: proxy.Target{Host: "ok.test", Port: 443}, Allowed: true, Reason: "allowed by rule ok.test"},
		{Seq: 2, At: time.Now(), Target: proxy.Target{Host: "no.test", Port: 443}, Reason: "denied by default", Sandbox: "web"},
	}

	out, err := runCLI(t, fake, "policy", "log")
	if err != nil {
		t.Fatalf("policy log: %v", err)
	}
	for _, want := range []string{"ok.test", "no.test", "web", "denied by default"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}

	out, err = runCLI(t, fake, "policy", "log", "--denied")
	if err != nil {
		t.Fatalf("policy log --denied: %v", err)
	}
	if strings.Contains(out, "ok.test") {
		t.Errorf("--denied should hide what was allowed:\n%s", out)
	}
	if !strings.Contains(out, "no.test") {
		t.Errorf("--denied should still show what was refused:\n%s", out)
	}
}

func TestPolicyLogSaysWhenThereIsNothing(t *testing.T) {
	out, err := runCLI(t, withPolicy(proxy.New(proxy.PresetBalanced)), "policy", "log")
	if err != nil {
		t.Fatalf("policy log: %v", err)
	}
	if !strings.Contains(out, "nothing") {
		t.Errorf("an empty log should say so rather than print a bare header:\n%s", out)
	}
}

func TestReadingThePolicyNeverWritesOne(t *testing.T) {
	// asking what the policy is, or what it would do, must not be the thing
	// that decides it. A question in the way of `jard policy ls` answers
	// something the user did not ask.
	for _, args := range [][]string{
		{"policy", "ls"},
		{"policy", "check", "example.com"},
		{"policy", "log"},
	} {
		fake := api.NewFake()
		if _, err := runCLI(t, fake, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if fake.NetworkPolicy != nil {
			t.Errorf("%v stored a policy: %+v", args, fake.NetworkPolicy)
		}
		if slices.Contains(fake.Calls, "SetPolicy") {
			t.Errorf("%v called SetPolicy", args)
		}
	}
}

func TestReadingWithNoPolicyShowsTheDefaultThatApplies(t *testing.T) {
	// "no policy" is not "no rules": the default is in force, and a sandbox
	// created right now would run under it.
	out, err := runCLI(t, api.NewFake(), "policy", "check", "api.anthropic.com")
	if err != nil {
		t.Fatalf("policy check: %v", err)
	}
	if !strings.Contains(out, "allowed") {
		t.Errorf("output = %q, want the default's answer", out)
	}
	if !strings.Contains(out, "no policy chosen yet") {
		t.Errorf("output = %q, want it to say the default is standing in", out)
	}
}

func TestEditingWithNoPolicyStartsFromTheDefault(t *testing.T) {
	// the answer to `jard policy allow x` is to allow x, not to ask which
	// preset it should be allowed under.
	fake := api.NewFake()
	if _, err := runCLI(t, fake, "policy", "allow", "x.test"); err != nil {
		t.Fatalf("policy allow: %v", err)
	}
	if fake.NetworkPolicy == nil {
		t.Fatal("the rule was not stored")
	}
	if fake.NetworkPolicy.Preset != proxy.PresetBalanced {
		t.Errorf("preset = %q, want the default", fake.NetworkPolicy.Preset)
	}
	if len(fake.NetworkPolicy.Rules) != 1 {
		t.Errorf("rules = %+v, want the one just added", fake.NetworkPolicy.Rules)
	}
}

func TestCreatingASandboxSettlesThePolicyFirst(t *testing.T) {
	// this is the moment the answer starts mattering. Without a terminal the
	// default is taken rather than the command stopping on a question nobody
	// is there to answer.
	fake := api.NewFake()
	if _, err := runCLI(t, fake, "create", "shell", t.TempDir(), "--name", "probe"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if fake.NetworkPolicy == nil {
		t.Fatal("a sandbox was created without a policy ever being settled")
	}
	if fake.NetworkPolicy.Preset != proxy.PresetBalanced {
		t.Errorf("preset = %q, want the default", fake.NetworkPolicy.Preset)
	}
}

func TestCreatingASandboxLeavesAChosenPolicyAlone(t *testing.T) {
	fake := withPolicy(proxy.New(proxy.PresetLockedDown))
	if _, err := runCLI(t, fake, "create", "shell", t.TempDir(), "--name", "probe"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if fake.NetworkPolicy.Preset != proxy.PresetLockedDown {
		t.Errorf("preset = %q, want the one already chosen", fake.NetworkPolicy.Preset)
	}
}
