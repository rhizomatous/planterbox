package ui

import (
	"testing"
	"time"
)

func TestUptimeReadsAsASpan(t *testing.T) {
	base := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{41 * time.Second, "41s"},
		{90 * time.Second, "1m"},
		{30 * time.Minute, "30m"},
		{90 * time.Minute, "1h30m"},
		{25 * time.Hour, "1d1h"},
		{-time.Minute, "0s"}, // a clock that disagrees is not an error
	} {
		if got := Uptime(base.Add(-tc.d), base); got != tc.want {
			t.Errorf("Uptime(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
	if got := Uptime(time.Time{}, base); got != "-" {
		t.Errorf("a sandbox that never started = %q, want %q", got, "-")
	}
}
