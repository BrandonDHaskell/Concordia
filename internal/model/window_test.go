package model

import (
	"testing"
	"time"
)

func TestWindowOverlaps(t *testing.T) {
	w := Window{
		Start: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC),
	}
	d := func(day, hour int) time.Time { return time.Date(2026, 1, day, hour, 0, 0, 0, time.UTC) }

	tests := []struct {
		name       string
		start, end time.Time
		want       bool
	}{
		{"fully inside", d(12, 0), d(13, 0), true},
		{"straddles start", d(8, 0), d(11, 0), true},
		{"straddles end", d(19, 0), d(22, 0), true},
		{"spans whole window", d(1, 0), d(28, 0), true},
		{"entirely before", d(1, 0), d(9, 0), false},
		{"entirely after", d(21, 0), d(25, 0), false},
		{"touches end exclusively", d(20, 0), d(21, 0), false},
		{"touches start", d(9, 0), d(10, 1), true},
		{"zero-length on start", w.Start, w.Start, true},
		{"zero-length at end is excluded", w.End, w.End, false},
		{"zero-length before", d(1, 0), d(1, 0), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := w.Overlaps(tt.start, tt.end); got != tt.want {
				t.Errorf("Overlaps(%v,%v) = %v, want %v", tt.start, tt.end, got, tt.want)
			}
		})
	}
}

func TestNewWindow(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	w := NewWindow(now, 60*24*time.Hour, 400*24*time.Hour)
	if !w.Start.Equal(now.AddDate(0, 0, -60)) {
		t.Errorf("Start = %v", w.Start)
	}
	if !w.Contains(now) {
		t.Error("window does not contain now")
	}
}
