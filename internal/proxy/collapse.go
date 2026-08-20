package proxy

import (
	"sort"
	"time"
)

// Group is a run of identical decisions folded into one, with a count.
//
// A log of two hundred lines is usually a handful of distinct decisions, and
// the repetition is the least interesting thing in it: an agent retrying a
// host it cannot reach says the same thing two hundred times. What matters is
// which host, what the policy said, and how recently.
type Group struct {
	Target  Target    `json:"target"`
	Sandbox string    `json:"sandbox,omitempty"`
	Allowed bool      `json:"allowed"`
	Reason  string    `json:"reason"`
	Hits    int       `json:"hits"`
	First   time.Time `json:"first_seen"`
	Last    time.Time `json:"last_seen"`
}

// Collapse folds entries that agree on target, sandbox and verdict, keeping
// the reason from the most recent of them: a decision that changed because the
// policy changed should report what the policy says now.
//
// The result is ordered by when each group was last seen, oldest first, so it
// reads the way the log it came from does.
func Collapse(entries []Entry) []Group {
	type key struct {
		target  Target
		sandbox string
		allowed bool
	}
	index := make(map[key]int, len(entries))
	groups := make([]Group, 0, len(entries))

	for _, e := range entries {
		k := key{e.Target, e.Sandbox, e.Allowed}
		i, seen := index[k]
		if !seen {
			index[k] = len(groups)
			groups = append(groups, Group{
				Target: e.Target, Sandbox: e.Sandbox, Allowed: e.Allowed,
				Reason: e.Reason, Hits: 1, First: e.At, Last: e.At,
			})
			continue
		}
		g := &groups[i]
		g.Hits++
		if e.At.Before(g.First) {
			g.First = e.At
		}
		if !e.At.Before(g.Last) {
			g.Last = e.At
			g.Reason = e.Reason
		}
	}

	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Last.Before(groups[j].Last) })
	return groups
}
