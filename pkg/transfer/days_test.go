package transfer

import (
	"reflect"
	"testing"
	"time"
)

func TestDaysInRange(t *testing.T) {
	now := time.Date(2026, 7, 4, 15, 30, 0, 0, time.UTC)
	tests := []struct {
		name    string
		from    time.Time
		to      time.Time
		want    []string
		wantErr bool
	}{
		{
			name: "normal range",
			from: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
			want: []string{"20260701", "20260702"},
		},
		{
			name: "zero to defaults to today",
			from: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			want: []string{"20260701", "20260702", "20260703"},
		},
		{
			name: "from equals to yields empty",
			from: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			want: nil,
		},
		{
			name:    "from after to errors",
			from:    time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
			to:      time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			wantErr: true,
		},
		{
			name: "range past today is clamped, today excluded",
			from: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			to:   time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
			want: []string{"20260702", "20260703"},
		},
		{
			name: "today only yields empty",
			from: time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC),
			want: nil,
		},
		{
			name: "non-UTC timestamps truncate to UTC days",
			from: time.Date(2026, 7, 1, 23, 0, 0, 0, time.FixedZone("UTC+2", 2*3600)), // 21:00 UTC July 1st
			to:   time.Date(2026, 7, 3, 1, 0, 0, 0, time.FixedZone("UTC+2", 2*3600)),  // 23:00 UTC July 2nd
			want: []string{"20260701"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DaysInRange(tt.from, tt.to, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DaysInRange() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DaysInRange() = %v, want %v", got, tt.want)
			}
		})
	}
}
