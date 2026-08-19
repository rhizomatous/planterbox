package api

import (
	"fmt"
	"strconv"
	"strings"
)

// byteUnits maps a memory suffix to its multiplier. Both the IEC spelling and
// docker's single-letter shorthand are accepted, and both mean powers of 1024,
// which is what docker's -m does too.
var byteUnits = []struct {
	suffix string
	mult   int64
}{
	{suffix: "gib", mult: 1 << 30},
	{suffix: "mib", mult: 1 << 20},
	{suffix: "kib", mult: 1 << 10},
	{suffix: "gb", mult: 1 << 30},
	{suffix: "mb", mult: 1 << 20},
	{suffix: "kb", mult: 1 << 10},
	{suffix: "g", mult: 1 << 30},
	{suffix: "m", mult: 1 << 20},
	{suffix: "k", mult: 1 << 10},
	{suffix: "b", mult: 1},
}

// ParseBytes reads a memory size like "8GiB", "512m", or a bare byte count into
// the form [Resources] stores. An empty string is zero, meaning unlimited.
func ParseBytes(s string) (int64, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return 0, nil
	}
	lower := strings.ToLower(raw)

	num, mult := lower, int64(1)
	for _, u := range byteUnits {
		if strings.HasSuffix(lower, u.suffix) {
			num, mult = strings.TrimSpace(strings.TrimSuffix(lower, u.suffix)), u.mult
			break
		}
	}

	val, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory size %q: want a number with an optional unit, e.g. \"8GiB\"", s)
	}
	if val < 0 {
		return 0, fmt.Errorf("invalid memory size %q: must not be negative", s)
	}
	return int64(val * float64(mult)), nil
}

// FormatBytes renders a byte count in the largest unit that divides it cleanly,
// for display. Zero renders as "unlimited", matching what [Resources] means by it.
func FormatBytes(n int64) string {
	if n <= 0 {
		return "unlimited"
	}
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{suffix: "GiB", mult: 1 << 30}, {suffix: "MiB", mult: 1 << 20}, {suffix: "KiB", mult: 1 << 10}} {
		if n >= u.mult {
			return strconv.FormatFloat(float64(n)/float64(u.mult), 'g', 4, 64) + u.suffix
		}
	}
	return strconv.FormatInt(n, 10) + "B"
}
