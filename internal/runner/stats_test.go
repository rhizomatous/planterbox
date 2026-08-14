package runner

import (
	"context"
	"strings"
	"testing"
)

func TestParseStats(t *testing.T) {
	cases := []struct {
		name      string
		line      string
		wantCPU   float64
		wantUsed  int64
		wantLimit int64
	}{
		{
			name:      "docker",
			line:      "12.34%\t1.5GiB / 8GiB",
			wantCPU:   12.34,
			wantUsed:  1536 << 20,
			wantLimit: 8 << 30,
		},
		{
			name:      "podman spells the units differently",
			line:      "0.50%\t512MB / 4GB",
			wantCPU:   0.5,
			wantUsed:  512 << 20,
			wantLimit: 4 << 30,
		},
		{
			name:      "idle container",
			line:      "0.00%\t2.5MiB / 8GiB",
			wantCPU:   0,
			wantUsed:  2621440,
			wantLimit: 8 << 30,
		},
		{
			name:      "wrapped in the cursor escapes streaming stats emits",
			line:      "\x1b[2J\x1b[H12.34%\t1.5GiB / 8GiB\x1b[0m",
			wantCPU:   12.34,
			wantUsed:  1536 << 20,
			wantLimit: 8 << 30,
		},
		{
			name:      "over a hundred percent, which multi-core containers report",
			line:      "245.60%\t1GiB / 8GiB",
			wantCPU:   245.6,
			wantUsed:  1 << 30,
			wantLimit: 8 << 30,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseStats(tc.line)
			if !ok {
				t.Fatalf("parseStats(%q) reported unreadable", tc.line)
			}
			if got.CPUPercent != tc.wantCPU {
				t.Errorf("CPUPercent = %v, want %v", got.CPUPercent, tc.wantCPU)
			}
			if got.MemoryBytes != tc.wantUsed {
				t.Errorf("MemoryBytes = %d, want %d", got.MemoryBytes, tc.wantUsed)
			}
			if got.MemoryLimit != tc.wantLimit {
				t.Errorf("MemoryLimit = %d, want %d", got.MemoryLimit, tc.wantLimit)
			}
		})
	}
}

func TestParseStatsRejectsWhatItCannotRead(t *testing.T) {
	// a header line, a blank redraw, or a truncated sample. None should be
	// mistaken for a real reading of zero.
	for _, line := range []string{
		"",
		"\x1b[2J",
		"CPU %\tMEM USAGE / LIMIT",
		"12.34%",
		"12.34%\t1.5GiB",
		"not-a-number\t1.5GiB / 8GiB",
		"12.34%\tnonsense / 8GiB",
		"12.34%\t1.5GiB / nonsense",
	} {
		if _, ok := parseStats(line); ok {
			t.Errorf("parseStats(%q) should have reported unreadable", line)
		}
	}
}

func TestStatsInvocationStreams(t *testing.T) {
	inv := testOCI().StatsInvocation("plbx-demo")
	if inv.Args[0] != "stats" {
		t.Errorf("args[0] = %q, want stats", inv.Args[0])
	}
	// --no-stream would give one sample and exit; the dashboard wants a feed.
	for _, a := range inv.Args {
		if a == "--no-stream" {
			t.Error("stats should stream rather than take a single sample")
		}
	}
	format, ok := argsAfter(inv.Args, "--format")
	if !ok || !strings.Contains(format, "\t") {
		t.Errorf("format = %q, want tab-separated fields", format)
	}
}

// streamExecutor replays canned lines as if they came from a live stream.
type streamExecutor struct {
	scriptedExecutor
	lines []string
}

func (s *streamExecutor) Stream(_ context.Context, inv Invocation) (<-chan string, error) {
	s.ran = append(s.ran, inv)
	if s.err != nil {
		return nil, s.err
	}
	out := make(chan string, len(s.lines))
	for _, l := range s.lines {
		out <- l
	}
	close(out)
	return out, nil
}

func TestStatsSkipsUnreadableSamplesWithoutEndingTheStream(t *testing.T) {
	// one malformed line in the middle must not cost the samples after it.
	e := &streamExecutor{lines: []string{
		"CPU %\tMEM USAGE / LIMIT",
		"10.00%\t1GiB / 8GiB",
		"\x1b[2J",
		"20.00%\t2GiB / 8GiB",
	}}

	ch, err := testOCI(WithExecutor(e)).Stats(context.Background(), "plbx-demo")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	var got []float64
	for s := range ch {
		got = append(got, s.CPUPercent)
	}
	if len(got) != 2 || got[0] != 10 || got[1] != 20 {
		t.Errorf("samples = %v, want [10 20]", got)
	}
}

func TestStatsChannelClosesWhenTheStreamEnds(t *testing.T) {
	e := &streamExecutor{lines: []string{"10.00%\t1GiB / 8GiB"}}
	ch, err := testOCI(WithExecutor(e)).Stats(context.Background(), "plbx-demo")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	for range ch { //nolint:revive // draining is the point
	}
	if _, open := <-ch; open {
		t.Error("the channel should be closed once the stream ends")
	}
}

func TestStatsStopsOnContextCancel(t *testing.T) {
	e := &streamExecutor{lines: []string{
		"10.00%\t1GiB / 8GiB",
		"20.00%\t2GiB / 8GiB",
		"30.00%\t3GiB / 8GiB",
	}}
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := testOCI(WithExecutor(e)).Stats(ctx, "plbx-demo")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if _, ok := <-ch; !ok {
		t.Fatal("expected at least one sample before cancelling")
	}
	cancel()

	// draining must terminate rather than block once the context is done.
	for range ch { //nolint:revive // draining is the point
	}
}
