package doctor

import (
	"strings"
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/daemon"
)

// TestCheckVersionsNamesBothBuilds covers the failure that hides: every
// command works, and answers the way the older build did.
func TestCheckVersionsNamesBothBuilds(t *testing.T) {
	if c := checkVersions("1.2.3", "1.2.3"); !c.OK {
		t.Errorf("matching builds should pass: %+v", c)
	}

	mismatch := checkVersions("1.2.3", "1.0.0")
	if mismatch.OK {
		t.Error("a mismatch should fail")
	}
	for _, want := range []string{"1.2.3", "1.0.0"} {
		if !strings.Contains(mismatch.Detail, want) {
			t.Errorf("detail %q should name %q", mismatch.Detail, want)
		}
	}
	if mismatch.Fix == "" {
		t.Error("a failing check without a fix has moved the problem, not helped")
	}

	// a daemon too old to answer reports nothing, which is itself the answer
	old := checkVersions("1.2.3", "")
	if old.OK || old.Fix == "" {
		t.Errorf("an unanswering daemon should fail with a fix: %+v", old)
	}
}

// a daemon that is not running is a resting state, not a fault: plbx starts
// one on demand.
func TestCheckDaemonTreatsAbsenceAsFine(t *testing.T) {
	c := checkDaemon(daemon.HostEnv("darwin"), api.DaemonInfo{}, false)
	if !c.OK {
		t.Errorf("a stopped daemon should not be a failure: %+v", c)
	}
	if !strings.Contains(c.Detail, "starts one") {
		t.Errorf("it should say the daemon is started on demand: %q", c.Detail)
	}
}

// every failing check has to carry a fix, or the report has only moved the
// problem to the reader.
func TestEveryFailureCarriesAFix(t *testing.T) {
	for _, c := range []Check{
		checkVersions("a", "b"),
		checkVersions("a", ""),
	} {
		if !c.OK && c.Fix == "" {
			t.Errorf("check %q fails without a fix", c.Name)
		}
	}
}
