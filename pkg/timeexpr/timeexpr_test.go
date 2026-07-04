package timeexpr_test

import (
	"testing"
	"time"

	"github.com/sbordeyne/vlbackup/pkg/timeexpr"
)

// now is a fixed reference: Wednesday, 2024-01-17 15:30:45 UTC.
var now = time.Date(2024, 1, 17, 15, 30, 45, 0, time.UTC)

func utc(y int, m time.Month, d, h, min, s int) time.Time {
	return time.Date(y, m, d, h, min, s, 0, time.UTC)
}

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want time.Time
	}{
		{"now", "now", utc(2024, 1, 17, 15, 30, 45)},
		{"minus days", "now-7d", utc(2024, 1, 10, 15, 30, 45)},
		{"minus days rounded", "now-7d/d", utc(2024, 1, 10, 0, 0, 0)},
		{"round to day", "now/d", utc(2024, 1, 17, 0, 0, 0)},
		{"round to week monday", "now/w", utc(2024, 1, 15, 0, 0, 0)},
		{"round to month", "now/M", utc(2024, 1, 1, 0, 0, 0)},
		{"round to year", "now/y", utc(2024, 1, 1, 0, 0, 0)},
		{"plus hour", "now+1h", utc(2024, 1, 17, 16, 30, 45)},
		{"plus hours crosses day", "now+12h", utc(2024, 1, 18, 3, 30, 45)},
		{"whitespace trimmed", "  now/d  ", utc(2024, 1, 17, 0, 0, 0)},
		{"bare rfc3339", "2024-01-01T00:00:00Z", utc(2024, 1, 1, 0, 0, 0)},
		{"rfc3339 with math normalized", "2024-01-10T06:00:00Z-2d/d", utc(2024, 1, 8, 0, 0, 0)},
		{"explicit pipes preserved", "2024-01-10T06:00:00Z||-2d/d", utc(2024, 1, 8, 0, 0, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := timeexpr.Parse(tt.expr, now)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.expr, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("Parse(%q) = %s, want %s", tt.expr, got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, expr := range []string{
		"",
		"   ",
		"garbage",
		"now-3z",     // unknown unit
		"now-1ns",    // sub-second unit unsupported by the underlying lib
		"2024-13-99", // invalid date
	} {
		t.Run(expr, func(t *testing.T) {
			if _, err := timeexpr.Parse(expr, now); err == nil {
				t.Errorf("Parse(%q) expected error, got nil", expr)
			}
		})
	}
}
