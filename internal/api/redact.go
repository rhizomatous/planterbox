package api

import (
	"maps"
	"slices"
)

// RedactedSpec is a [Spec] whose environment is named rather than quoted.
//
// The embedded Spec's own Env field is shadowed by this one: both carry the
// `env` tag, and encoding/json takes the shallower.
type RedactedSpec struct {
	Spec
	Env []string `json:"env,omitempty"`
}

// RedactedSandbox is a [Sandbox] carrying a [RedactedSpec].
type RedactedSandbox struct {
	Sandbox
	Spec RedactedSpec `json:"spec"`
}

// Redact returns sb as the CLI prints it: the whole record, except that the
// environment it was given is named rather than quoted.
//
// A sandbox holds live credentials — `docs/concessions.md` says so — and the
// two places they can be read are not the same exposure. The store is a 0700
// file someone has to go looking for. JSON output is piped, redirected into
// CI logs, and pasted into issues without anyone deciding to. Only one of
// those should carry the value.
//
// Not a MarshalJSON on Spec: the store marshals the same type, and it has to
// keep what it is storing.
func Redact(sb Sandbox) RedactedSandbox {
	return RedactedSandbox{
		Sandbox: sb,
		Spec: RedactedSpec{
			Spec: sb.Spec,
			Env:  slices.Sorted(maps.Keys(sb.Spec.Env)),
		},
	}
}

// RedactAll is [Redact] over a listing. It returns an empty slice rather than
// nil, because callers pipe this into jq.
func RedactAll(sandboxes []Sandbox) []RedactedSandbox {
	out := make([]RedactedSandbox, 0, len(sandboxes))
	for _, sb := range sandboxes {
		out = append(out, Redact(sb))
	}
	return out
}
