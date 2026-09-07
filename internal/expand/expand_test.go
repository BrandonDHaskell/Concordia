package expand

import (
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("zone %s unavailable: %v", name, err)
	}
	return loc
}

func utcWindow(fromY, fromM, fromD, toY, toM, toD int) model.Window {
	return model.Window{
		Start: time.Date(fromY, time.Month(fromM), fromD, 0, 0, 0, 0, time.UTC),
		End:   time.Date(toY, time.Month(toM), toD, 0, 0, 0, 0, time.UTC),
	}
}

const owner = "brandon"

func starts(occs []model.Occurrence) []string {
	out := make([]string, len(occs))
	for i, o := range occs {
		if o.AllDay {
			out[i] = o.Start.UTC().Format("2006-01-02")
		} else {
			out[i] = o.Start.UTC().Format(time.RFC3339)
		}
	}
	return out
}

func TestWeeklyAcrossDSTBothDirections(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	master := model.Event{
		ID: 1, UID: "w", Summary: "Standup", Status: model.StatusConfirmed,
		Start: time.Date(2026, 2, 2, 9, 0, 0, 0, la), // Monday, PST
		End:   time.Date(2026, 2, 2, 9, 30, 0, 0, la),
		RRULE: "FREQ=WEEKLY;BYDAY=MO",
	}
	// Spans the 2026-03-08 spring-forward and the 2026-11-01 fall-back.
	w := utcWindow(2026, 1, 1, 2026, 12, 1)

	occs, err := Expand([]model.Event{master}, w, owner)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(occs) < 40 {
		t.Fatalf("only %d weekly occurrences over ~10 months", len(occs))
	}

	var sawPST, sawPDT bool
	for _, o := range occs {
		wall := o.Start.In(la)
		if h, m, _ := wall.Clock(); h != 9 || m != 0 {
			t.Errorf("%s is %02d:%02d LA, want 09:00", o.Start.UTC().Format(time.RFC3339), h, m)
		}
		switch _, off := o.Start.In(la).Zone(); off {
		case -8 * 3600:
			sawPST = true
		case -7 * 3600:
			sawPDT = true
		}
		if o.End.Sub(o.Start) != 30*time.Minute {
			t.Errorf("instance duration = %v", o.End.Sub(o.Start))
		}
	}
	if !sawPST || !sawPDT {
		t.Errorf("expected instances on both sides of DST (PST=%v PDT=%v)", sawPST, sawPDT)
	}
}

func TestEXDATERemovesInstance(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	master := model.Event{
		ID: 1, UID: "w", Summary: "Sync", Status: model.StatusConfirmed,
		Start:   time.Date(2026, 9, 7, 10, 0, 0, 0, la), // Monday
		End:     time.Date(2026, 9, 7, 10, 30, 0, 0, la),
		RRULE:   "FREQ=WEEKLY;BYDAY=MO",
		EXDates: []string{"EXDATE;TZID=America/Los_Angeles:20260914T100000"},
	}
	w := utcWindow(2026, 9, 1, 2026, 10, 1)

	occs, err := Expand([]model.Event{master}, w, owner)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	got := starts(occs)
	for _, s := range got {
		if s == "2026-09-14T17:00:00Z" {
			t.Fatalf("EXDATE instance not removed; got %v", got)
		}
	}
	// 7th, 21st, 28th expected (14th excluded).
	if len(occs) != 3 {
		t.Fatalf("got %d occurrences, want 3: %v", len(occs), got)
	}
}

func TestEXDATEWithDifferentTZID(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	mustLoc(t, "America/New_York")
	master := model.Event{
		ID: 1, UID: "w", Status: model.StatusConfirmed,
		Start: time.Date(2026, 9, 7, 10, 0, 0, 0, la),
		End:   time.Date(2026, 9, 7, 10, 30, 0, 0, la),
		RRULE: "FREQ=WEEKLY;BYDAY=MO",
		// 13:00 New York == 10:00 Los Angeles: same instant, different zone.
		EXDates: []string{"EXDATE;TZID=America/New_York:20260914T130000"},
	}
	w := utcWindow(2026, 9, 1, 2026, 10, 1)

	occs, err := Expand([]model.Event{master}, w, owner)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	for _, s := range starts(occs) {
		if s == "2026-09-14T17:00:00Z" {
			t.Fatalf("cross-zone EXDATE did not match; got %v", starts(occs))
		}
	}
	if len(occs) != 3 {
		t.Fatalf("got %d, want 3: %v", len(occs), starts(occs))
	}
}

func TestRecurrenceIDMovesInstance(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	master := model.Event{
		ID: 1, UID: "w", Summary: "Sync", Status: model.StatusConfirmed,
		Start: time.Date(2026, 9, 7, 10, 0, 0, 0, la),
		End:   time.Date(2026, 9, 7, 10, 30, 0, 0, la),
		RRULE: "FREQ=WEEKLY;BYDAY=MO",
	}
	// The 2026-09-14 instance (17:00Z) moves to the 16th at 14:00 LA.
	override := model.Event{
		ID: 2, UID: "w", Summary: "Sync (moved)", Status: model.StatusConfirmed,
		RecurrenceID: "2026-09-14T17:00:00Z",
		Start:        time.Date(2026, 9, 16, 14, 0, 0, 0, la),
		End:          time.Date(2026, 9, 16, 14, 30, 0, 0, la),
	}
	w := utcWindow(2026, 9, 1, 2026, 10, 1)

	occs, err := Expand([]model.Event{master, override}, w, owner)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	got := starts(occs)
	if len(got) != 4 {
		t.Fatalf("got %d occurrences, want 4: %v", len(got), got)
	}
	var hasMoved, hasOriginal bool
	for _, o := range occs {
		switch o.Start.UTC().Format(time.RFC3339) {
		case "2026-09-16T21:00:00Z":
			hasMoved = true
			if o.Summary != "Sync (moved)" || o.EventID != 2 {
				t.Errorf("moved instance not attributed to the override: %+v", o)
			}
		case "2026-09-14T17:00:00Z":
			hasOriginal = true
		}
	}
	if !hasMoved || hasOriginal {
		t.Errorf("moved=%v original-still-present=%v", hasMoved, hasOriginal)
	}
}

func TestRecurrenceIDCancelsInstance(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	master := model.Event{
		ID: 1, UID: "w", Status: model.StatusConfirmed,
		Start: time.Date(2026, 9, 7, 10, 0, 0, 0, la),
		End:   time.Date(2026, 9, 7, 10, 30, 0, 0, la),
		RRULE: "FREQ=WEEKLY;BYDAY=MO",
	}
	cancel := model.Event{
		ID: 2, UID: "w", Status: model.StatusCancelled,
		RecurrenceID: "2026-09-21T17:00:00Z",
	}
	w := utcWindow(2026, 9, 1, 2026, 10, 1)

	occs, err := Expand([]model.Event{master, cancel}, w, owner)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	got := starts(occs)
	for _, s := range got {
		if s == "2026-09-21T17:00:00Z" {
			t.Fatalf("cancelled instance still present: %v", got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %d, want 3 (7th, 14th, 28th): %v", len(got), got)
	}
}

func TestAllDayRecurringLandsOnSameDate(t *testing.T) {
	master := model.Event{
		ID: 1, UID: "bin", Summary: "Bin day", Status: model.StatusConfirmed,
		Start:  time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), // Wednesday
		End:    time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC),
		AllDay: true,
		RRULE:  "FREQ=WEEKLY;BYDAY=WE",
	}
	w := utcWindow(2026, 9, 1, 2026, 10, 1)

	occs, err := Expand([]model.Event{master}, w, owner)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	want := []string{"2026-09-02", "2026-09-09", "2026-09-16", "2026-09-23", "2026-09-30"}
	got := starts(occs)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("occurrence %d = %s, want %s", i, got[i], want[i])
		}
		if !occs[i].AllDay {
			t.Errorf("occurrence %d lost the all-day flag", i)
		}
		// The instant must sit exactly on midnight UTC so the date is
		// unambiguous and never needs a rendering zone (invariant 5).
		if h, m, s := occs[i].Start.UTC().Clock(); h != 0 || m != 0 || s != 0 {
			t.Errorf("all-day occurrence %d not anchored to UTC midnight: %s", i, occs[i].Start.UTC())
		}
	}
}

func TestMultiDayEventSpanningWindowEdges(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	w := utcWindow(2026, 9, 10, 2026, 9, 20)

	before := model.Event{
		ID: 1, UID: "a", Summary: "Conference", Status: model.StatusConfirmed,
		Start: time.Date(2026, 9, 7, 9, 0, 0, 0, la),
		End:   time.Date(2026, 9, 12, 17, 0, 0, 0, la), // ends inside the window
	}
	after := model.Event{
		ID: 2, UID: "b", Summary: "Trip", Status: model.StatusConfirmed,
		Start:  time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), // starts inside, ends after
		AllDay: true,
	}
	outside := model.Event{
		ID: 3, UID: "c", Summary: "Old", Status: model.StatusConfirmed,
		Start: time.Date(2026, 9, 1, 9, 0, 0, 0, la),
		End:   time.Date(2026, 9, 3, 9, 0, 0, 0, la),
	}

	occs, err := Expand([]model.Event{before, after, outside}, w, owner)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(occs) != 2 {
		t.Fatalf("got %d occurrences, want 2 (the two straddlers): %v", len(occs), starts(occs))
	}
}

func TestUNTILInUTCWithLocalDtstart(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	master := model.Event{
		ID: 1, UID: "u", Status: model.StatusConfirmed,
		Start: time.Date(2026, 9, 7, 10, 0, 0, 0, la),
		End:   time.Date(2026, 9, 7, 11, 0, 0, 0, la),
		RRULE: "FREQ=WEEKLY;BYDAY=MO;UNTIL=20260922T000000Z",
	}
	w := utcWindow(2026, 1, 1, 2027, 1, 1)

	occs, err := Expand([]model.Event{master}, w, owner)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	got := starts(occs)
	want := []string{"2026-09-07T17:00:00Z", "2026-09-14T17:00:00Z", "2026-09-21T17:00:00Z"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestInfiniteRecurrenceBoundedByWindow(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	master := model.Event{
		ID: 1, UID: "d", Status: model.StatusConfirmed,
		Start: time.Date(2020, 1, 1, 8, 0, 0, 0, la), // long before the window
		End:   time.Date(2020, 1, 1, 8, 15, 0, 0, la),
		RRULE: "FREQ=DAILY", // no COUNT, no UNTIL
	}
	w := utcWindow(2026, 9, 1, 2026, 9, 11)

	occs, err := Expand([]model.Event{master}, w, owner)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	// 10 days in the window, one per day.
	if len(occs) != 10 {
		t.Fatalf("got %d occurrences, want 10: %v", len(occs), starts(occs))
	}
	for _, o := range occs {
		if !w.Contains(o.Start) {
			t.Errorf("occurrence outside window: %s", o.Start.UTC())
		}
	}
}

func TestNonRecurringInsideAndOutside(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	w := utcWindow(2026, 9, 1, 2026, 9, 30)

	in := model.Event{
		ID: 1, UID: "a", Summary: "Dentist", Status: model.StatusConfirmed,
		Start: time.Date(2026, 9, 15, 14, 0, 0, 0, la),
		End:   time.Date(2026, 9, 15, 15, 0, 0, 0, la),
	}
	out := model.Event{
		ID: 2, UID: "b", Summary: "Later", Status: model.StatusConfirmed,
		Start: time.Date(2026, 11, 1, 9, 0, 0, 0, la),
		End:   time.Date(2026, 11, 1, 10, 0, 0, 0, la),
	}
	occs, err := Expand([]model.Event{in, out}, w, owner)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(occs) != 1 || occs[0].Summary != "Dentist" {
		t.Fatalf("got %v, want just Dentist", starts(occs))
	}
}

func TestDeletedEventProducesNothing(t *testing.T) {
	w := utcWindow(2026, 9, 1, 2026, 9, 30)
	ev := model.Event{
		ID: 1, UID: "a", Status: model.StatusCancelled,
		Start:     time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC),
		DeletedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
	}
	occs, err := Expand([]model.Event{ev}, w, owner)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(occs) != 0 {
		t.Fatalf("got %d occurrences for a tombstoned event", len(occs))
	}
}
