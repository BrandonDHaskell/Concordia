package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
)

func calendarFixture(t *testing.T, ctx context.Context, s *Store) model.Calendar {
	t.Helper()
	acct, err := s.UpsertAccount(ctx, model.Account{
		Person: "brandon", Provider: model.ProviderGoogle,
		DisplayName: "Gmail", CredentialRef: "gmail", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cal, err := s.UpsertCalendar(ctx, model.Calendar{
		AccountID: acct.ID, RemoteID: "primary", DisplayName: "Primary", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func inTx(t *testing.T, ctx context.Context, s *Store, fn func(tx *sql.Tx) error) {
	t.Helper()
	if err := s.WithTx(ctx, fn); err != nil {
		t.Fatalf("WithTx: %v", err)
	}
}

func TestUpsertEventRoundTrip(t *testing.T) {
	s, ctx := migratedStore(t)
	cal := calendarFixture(t, ctx, s)
	la := mustLoadLocation(t, "America/Los_Angeles")

	ev := model.Event{
		CalendarID: cal.ID, RemoteID: "g1", UID: "u1@google.com",
		Summary: "Standup", Location: "Room 2", Status: model.StatusConfirmed,
		Start:     time.Date(2026, 9, 10, 9, 0, 0, 0, la),
		End:       time.Date(2026, 9, 10, 9, 30, 0, 0, la),
		TZID:      "America/Los_Angeles",
		RRULE:     "FREQ=WEEKLY;BYDAY=MO",
		EXDates:   []string{"EXDATE;TZID=America/Los_Angeles:20260914T090000"},
		ETag:      `"v1"`,
		Raw:       []byte(`{"id":"g1"}`),
		UpdatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}

	var id int64
	inTx(t, ctx, s, func(tx *sql.Tx) error {
		var err error
		id, err = s.UpsertEvent(ctx, tx, ev)
		return err
	})
	if id == 0 {
		t.Fatal("UpsertEvent returned zero id")
	}

	// Update: same uid + recurrence_id -> same row.
	ev.Summary = "Standup (renamed)"
	ev.RemoteID = "g1"
	var id2 int64
	inTx(t, ctx, s, func(tx *sql.Tx) error {
		var err error
		id2, err = s.UpsertEvent(ctx, tx, ev)
		return err
	})
	if id2 != id {
		t.Errorf("update made a new row: %d != %d", id2, id)
	}

	var got []model.Event
	inTx(t, ctx, s, func(tx *sql.Tx) error {
		var err error
		got, err = s.EventsByUIDs(ctx, tx, cal.ID, []string{"u1@google.com"})
		return err
	})
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	e := got[0]
	if e.Summary != "Standup (renamed)" {
		t.Errorf("summary = %q", e.Summary)
	}
	if e.TZID != "America/Los_Angeles" || e.Start.Location().String() != "America/Los_Angeles" {
		t.Errorf("zone not restored: tzid=%q loc=%s", e.TZID, e.Start.Location())
	}
	if h, m, _ := e.Start.Clock(); h != 9 || m != 0 {
		t.Errorf("start wall clock = %02d:%02d", h, m)
	}
	if e.RRULE != "FREQ=WEEKLY;BYDAY=MO" || len(e.EXDates) != 1 {
		t.Errorf("recurrence not restored: %q %v", e.RRULE, e.EXDates)
	}
	if string(e.Raw) != `{"id":"g1"}` {
		t.Errorf("raw = %s", e.Raw)
	}
}

func TestUpsertEventAllDay(t *testing.T) {
	s, ctx := migratedStore(t)
	cal := calendarFixture(t, ctx, s)

	ev := model.Event{
		CalendarID: cal.ID, RemoteID: "trip", UID: "trip@google.com",
		Summary: "Trip", Status: model.StatusConfirmed, AllDay: true,
		Start: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC),
	}
	inTx(t, ctx, s, func(tx *sql.Tx) error {
		_, err := s.UpsertEvent(ctx, tx, ev)
		return err
	})

	var got []model.Event
	inTx(t, ctx, s, func(tx *sql.Tx) error {
		var err error
		got, err = s.EventsByUIDs(ctx, tx, cal.ID, []string{"trip@google.com"})
		return err
	})
	e := got[0]
	if !e.AllDay || e.TZID != "" {
		t.Errorf("all-day flags: allDay=%v tzid=%q", e.AllDay, e.TZID)
	}
	if e.Start.Format(dateLayout) != "2026-09-14" || e.End.Format(dateLayout) != "2026-09-18" {
		t.Errorf("dates = %s .. %s", e.Start.Format(dateLayout), e.End.Format(dateLayout))
	}
	if e.Start.Location() != time.UTC {
		t.Errorf("all-day start not in UTC: %s", e.Start.Location())
	}
}

func TestUpsertEventMasterAndOverride(t *testing.T) {
	s, ctx := migratedStore(t)
	cal := calendarFixture(t, ctx, s)

	master := model.Event{
		CalendarID: cal.ID, RemoteID: "m", UID: "shared@google.com",
		Summary: "Series", Status: model.StatusConfirmed, RRULE: "FREQ=DAILY",
		Start: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC),
	}
	override := model.Event{
		CalendarID: cal.ID, RemoteID: "o", UID: "shared@google.com",
		RecurrenceID: "2026-09-05T12:00:00Z", Summary: "Moved",
		Status: model.StatusConfirmed,
		Start:  time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 9, 5, 16, 0, 0, 0, time.UTC),
	}

	inTx(t, ctx, s, func(tx *sql.Tx) error {
		if _, err := s.UpsertEvent(ctx, tx, master); err != nil {
			return err
		}
		_, err := s.UpsertEvent(ctx, tx, override)
		return err
	})

	var got []model.Event
	inTx(t, ctx, s, func(tx *sql.Tx) error {
		var err error
		got, err = s.EventsByUIDs(ctx, tx, cal.ID, []string{"shared@google.com"})
		return err
	})
	if len(got) != 2 {
		t.Fatalf("got %d rows, want master + override", len(got))
	}
}

func TestTombstoneEvent(t *testing.T) {
	s, ctx := migratedStore(t)
	cal := calendarFixture(t, ctx, s)

	var eventID int64
	inTx(t, ctx, s, func(tx *sql.Tx) error {
		var err error
		eventID, err = s.UpsertEvent(ctx, tx, model.Event{
			CalendarID: cal.ID, RemoteID: "g1", UID: "u1", Status: model.StatusConfirmed,
			Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC),
		})
		if err != nil {
			return err
		}
		return s.ReplaceOccurrences(ctx, tx, eventID, []model.Occurrence{{
			EventID: eventID, Owner: "brandon",
			Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC),
		}})
	})

	var uid string
	inTx(t, ctx, s, func(tx *sql.Tx) error {
		var err error
		uid, err = s.TombstoneEvent(ctx, tx, cal.ID, "g1")
		return err
	})
	if uid != "u1" {
		t.Errorf("TombstoneEvent uid = %q, want u1", uid)
	}

	n, err := s.CountOccurrences(ctx, cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("occurrences after tombstone = %d, want 0", n)
	}

	// Second tombstone of the same (now deleted) event is a no-op.
	inTx(t, ctx, s, func(tx *sql.Tx) error {
		got, err := s.TombstoneEvent(ctx, tx, cal.ID, "g1")
		if err != nil {
			return err
		}
		if got != "" {
			t.Errorf("re-tombstone returned uid %q, want empty", got)
		}
		return nil
	})

	// Unknown remote id: no-op, no error.
	inTx(t, ctx, s, func(tx *sql.Tx) error {
		got, err := s.TombstoneEvent(ctx, tx, cal.ID, "nope")
		if got != "" || err != nil {
			t.Errorf("TombstoneEvent(nope) = (%q, %v)", got, err)
		}
		return nil
	})
}

func TestReplaceOccurrences(t *testing.T) {
	s, ctx := migratedStore(t)
	cal := calendarFixture(t, ctx, s)

	var eventID int64
	inTx(t, ctx, s, func(tx *sql.Tx) error {
		var err error
		eventID, err = s.UpsertEvent(ctx, tx, model.Event{
			CalendarID: cal.ID, RemoteID: "g1", UID: "u1", Status: model.StatusConfirmed,
			Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), AllDay: true,
			End: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		})
		return err
	})

	mk := func(day int) model.Occurrence {
		return model.Occurrence{
			EventID: eventID, AllDay: true, Owner: "brandon", Summary: "Bin day",
			Start: time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 9, day+1, 0, 0, 0, 0, time.UTC),
		}
	}

	inTx(t, ctx, s, func(tx *sql.Tx) error {
		return s.ReplaceOccurrences(ctx, tx, eventID, []model.Occurrence{mk(2), mk(9), mk(16)})
	})
	if n, _ := s.CountOccurrences(ctx, cal.ID); n != 3 {
		t.Fatalf("after first replace = %d, want 3", n)
	}

	inTx(t, ctx, s, func(tx *sql.Tx) error {
		return s.ReplaceOccurrences(ctx, tx, eventID, []model.Occurrence{mk(2)})
	})
	if n, _ := s.CountOccurrences(ctx, cal.ID); n != 1 {
		t.Fatalf("after shrink = %d, want 1", n)
	}

	var startUTC, startLocal string
	var isAllDay int
	if err := s.db.QueryRow(
		`SELECT start_utc, start_local, is_all_day FROM occurrences`).Scan(&startUTC, &startLocal, &isAllDay); err != nil {
		t.Fatal(err)
	}
	if startUTC != "2026-09-02" || startLocal != "2026-09-02" || isAllDay != 1 {
		t.Errorf("all-day occurrence stored as instant: utc=%q local=%q allDay=%d", startUTC, startLocal, isAllDay)
	}

	inTx(t, ctx, s, func(tx *sql.Tx) error {
		return s.ReplaceOccurrences(ctx, tx, eventID, nil)
	})
	if n, _ := s.CountOccurrences(ctx, cal.ID); n != 0 {
		t.Fatalf("after clear = %d, want 0", n)
	}
}

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("zone %s unavailable: %v", name, err)
	}
	return loc
}
