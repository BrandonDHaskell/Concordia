package httpd

import (
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/store"
)

func at(h, m int) time.Time {
	return time.Date(2026, 9, 8, h, m, 0, 0, time.UTC)
}

func TestMarkConflicts(t *testing.T) {
	rows := []store.OccurrenceRow{
		{Start: at(9, 0), End: at(9, 30)},              // 0
		{Start: at(9, 15), End: at(10, 0)},             // 1 overlaps 0
		{Start: at(10, 0), End: at(11, 0)},             // 2 back-to-back with 1, no overlap
		{Start: at(10, 30), End: at(12, 0)},            // 3 overlaps 2
		{Start: at(13, 0), End: at(14, 0)},             // 4 alone
		{Start: at(0, 0), End: at(0, 0), AllDay: true}, // 5 all-day, ignored
	}

	got := markConflicts(rows)
	want := map[int]bool{0: true, 1: true, 2: true, 3: true}
	if len(got) != len(want) {
		t.Fatalf("conflicts = %v, want %v", got, want)
	}
	for i := range want {
		if !got[i] {
			t.Errorf("index %d not marked", i)
		}
	}
	if got[4] || got[5] {
		t.Errorf("marked a non-conflicting event: %v", got)
	}
}

func TestMarkConflictsZeroLengthAndEmpty(t *testing.T) {
	if got := markConflicts(nil); len(got) != 0 {
		t.Errorf("nil -> %v", got)
	}
	rows := []store.OccurrenceRow{
		{Start: at(9, 0), End: at(9, 0)}, // zero length, skipped
		{Start: at(9, 0), End: at(10, 0)},
	}
	if got := markConflicts(rows); len(got) != 0 {
		t.Errorf("zero-length event should not conflict: %v", got)
	}
}
