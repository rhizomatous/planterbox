package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func withSecrets() Sandbox {
	return Sandbox{
		Spec: Spec{
			Name:       "demo",
			Agent:      "claude",
			Image:      "base:1",
			Workspaces: []Workspace{{Host: "/home/viv/demo"}},
			Env:        map[string]string{"ANTHROPIC_API_KEY": "sk-ant-secret", "B_TOKEN": "ghp_secret"},
		},
		State: State{Status: StatusRunning},
		Ports: []Port{{Host: 3000, Sandbox: 3000}},
	}
}

// TestRedactNamesTheEnvironmentAndKeepsEverythingElse is the whole contract:
// the record survives, the values do not.
func TestRedactNamesTheEnvironmentAndKeepsEverythingElse(t *testing.T) {
	out, err := json.Marshal(Redact(withSecrets()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(out)

	for _, gone := range []string{"sk-ant-secret", "ghp_secret"} {
		if strings.Contains(got, gone) {
			t.Errorf("a value survived redaction: %s", got)
		}
	}
	// names stay, sorted, so a reader can still see what was set
	for _, want := range []string{`"ANTHROPIC_API_KEY"`, `"B_TOKEN"`, `"demo"`, `"base:1"`, `"running"`, `"3000"`} {
		if !strings.Contains(got, strings.Trim(want, `"`)) {
			t.Errorf("redaction lost %s: %s", want, got)
		}
	}
	if i, j := strings.Index(got, "ANTHROPIC_API_KEY"), strings.Index(got, "B_TOKEN"); i > j {
		t.Errorf("names should be sorted: %s", got)
	}
}

// the store marshals the same Spec and has to keep what it is storing.
func TestRedactLeavesTheSpecItselfAlone(t *testing.T) {
	sb := withSecrets()
	_ = Redact(sb)
	out, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "sk-ant-secret") {
		t.Errorf("redacting a copy changed the original: %s", out)
	}
}

// an empty listing is [], not null: callers pipe this into jq.
func TestRedactAllOfNothingIsAnEmptyList(t *testing.T) {
	out, err := json.Marshal(RedactAll(nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != "[]" {
		t.Errorf("got %s, want []", out)
	}
}

// a sandbox with no environment should carry no env key at all, rather than
// an empty list suggesting one was considered.
func TestRedactOmitsAnAbsentEnvironment(t *testing.T) {
	out, err := json.Marshal(Redact(Sandbox{Spec: Spec{Name: "bare"}}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "env") {
		t.Errorf("got %s, want no env key", out)
	}
}
