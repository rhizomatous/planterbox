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
		{d: 0, want: "0s"},
		{d: 41 * time.Second, want: "41s"},
		{d: 90 * time.Second, want: "1m"},
		{d: 30 * time.Minute, want: "30m"},
		{d: 90 * time.Minute, want: "1h30m"},
		{d: 25 * time.Hour, want: "1d1h"},
		{d: -time.Minute, want: "0s"}, // a clock that disagrees is not an error
	} {
		t.Run(tc.d.String(), func(t *testing.T) {
			if got := Uptime(base.Add(-tc.d), base); got != tc.want {
				t.Errorf("Uptime(%s) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
	if got := Uptime(time.Time{}, base); got != "-" {
		t.Errorf("a sandbox that never started = %q, want %q", got, "-")
	}
}
