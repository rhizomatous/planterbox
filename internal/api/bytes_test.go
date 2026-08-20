package api

import "testing"

func TestParseBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{in: "", want: 0},
		{in: "1024", want: 1024},
		{in: "512b", want: 512},
		{in: "1k", want: 1 << 10},
		{in: "1KiB", want: 1 << 10},
		{in: "2m", want: 2 << 20},
		{in: "2MiB", want: 2 << 20},
		{in: "8GiB", want: 8 << 30},
		{in: "8g", want: 8 << 30},
		{in: " 4 GiB ", want: 4 << 30},
		{in: "1.5GiB", want: 1536 << 20},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseBytes(tc.in)
			if err != nil {
				t.Fatalf("ParseBytes(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseBytes(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseBytesRejectsGarbage(t *testing.T) {
	for _, in := range []string{"eight", "8 gigs", "-1GiB", "GiB", "1..5g"} {
		if _, err := ParseBytes(in); err == nil {
			t.Errorf("ParseBytes(%q) succeeded, want an error", in)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{in: 0, want: "unlimited"},
		{in: -1, want: "unlimited"},
		{in: 512, want: "512B"},
		{in: 2 << 10, want: "2KiB"},
		{in: 2 << 20, want: "2MiB"},
		{in: 8 << 30, want: "8GiB"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := FormatBytes(tc.in); got != tc.want {
				t.Errorf("FormatBytes(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBytesRoundTrip(t *testing.T) {
	// what FormatBytes prints must parse back to the same count, or a limit
	// read off a display no longer means what it says.
	for _, want := range []int64{512, 2 << 10, 2 << 20, 8 << 30} {
		got, err := ParseBytes(FormatBytes(want))
		if err != nil {
			t.Errorf("ParseBytes(FormatBytes(%d)): %v", want, err)
			continue
		}
		if got != want {
			t.Errorf("round trip of %d = %d", want, got)
		}
	}
}
