package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
)

// seedOccurrences writes a small fixed set of occurrences across two calendars
// and returns the store.
func seedOccurrences(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, ctx := migratedStore(t)

	acct, err := s.UpsertAccount(ctx, model.Account{
		Person: "brandon", Provider: model.ProviderGoogle,
		DisplayName: "Gmail", CredentialRef: "gmail", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	work, _ := s.UpsertCalendar(ctx, model.Calendar{AccountID: acct.ID, RemoteID: "work", DisplayName: "Work", Enabled: true})
	fam, _ := s.UpsertCalendar(ctx, model.Calendar{AccountID: acct.ID, RemoteID: "fam", DisplayName: "Family", Enabled: true})

	mk := func(cal model.Calendar, remoteID, summary, owner string, day int, tags ...string) {
		t.Helper()
		err := s.WithTx(ctx, func(tx *sql.Tx) error {
			id, err := s.UpsertEvent(ctx, tx, model.Event{
				CalendarID: cal.ID, RemoteID: remoteID, UID: remoteID,
				Status: model.StatusConfirmed,
				Start:  time.Date(2026, 9, day, 12, 0, 0, 0, time.UTC),
				End:    time.Date(2026, 9, day, 13, 0, 0, 0, time.UTC),
			})
			if err != nil {
				return err
			}
			return s.ReplaceOccurrences(ctx, tx, id, []model.Occurrence{{
				EventID: id, Owner: owner, Summary: summary, Tags: tags,
				Start: time.Date(2026, 9, day, 12, 0, 0, 0, time.UTC),
				End:   time.Date(2026, 9, day, 13, 0, 0, 0, time.UTC),
			}})
		})
		if err != nil {
			t.Fatalf("seed %s: %v", remoteID, err)
		}
	}

	mk(work, "w1", "Standup", "brandon", 10, "work")
	mk(fam, "f1", "Soccer", "brandon", 11, "kids", "sports")
	mk(fam, "f2", "Dinner", "kim", 12)
	mk(fam, "f3", "Old thing", "brandon", 1) // before a typical window

	return s, ctx
}

func TestOccurrenceTagsPersistAndCascade(t *testing.T) {
	s, ctx := seedOccurrences(t)

	rows, err := s.Occurrences(ctx, OccurrenceQuery{})
	if err != nil {
		t.Fatalf("Occurrences: %v", err)
	}

	var soccer OccurrenceRow
	for _, r := range rows {
		if r.Summary == "Soccer" {
			soccer = r
		}
	}
	if strings.Join(soccer.Tags, ",") != "kids,sports" {
		t.Errorf("Soccer tags = %v, want [kids sports]", soccer.Tags)
	}
	if soccer.CalendarName != "Family" {
		t.Errorf("Soccer calendar = %q", soccer.CalendarName)
	}

	// Re-materializing the event with no tags clears the old ones (cascade on
	// the occurrence delete).
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM events WHERE remote_id = 'f1'`).Scan(&id); err != nil {
			return err
		}
		return s.ReplaceOccurrences(ctx, tx, id, []model.Occurrence{{
			EventID: id, Owner: "brandon", Summary: "Soccer",
			Start: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC),
		}})
	})
	if err != nil {
		t.Fatal(err)
	}
	var soccerTags, total int
	s.db.QueryRow(`
		SELECT count(*) FROM occurrence_tags ot
		JOIN occurrences o ON o.id = ot.occurrence_id WHERE o.summary = 'Soccer'`).Scan(&soccerTags)
	s.db.QueryRow(`SELECT count(*) FROM occurrence_tags`).Scan(&total)
	if soccerTags != 0 {
		t.Errorf("Soccer still has %d tags after re-materializing without any", soccerTags)
	}
	if total != 1 { // Standup's "work" tag is untouched
		t.Errorf("occurrence_tags total = %d, want 1 (Standup/work)", total)
	}
}

func TestOccurrencesFilters(t *testing.T) {
	s, ctx := seedOccurrences(t)
	window := OccurrenceQuery{
		From: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
	}

	all, err := s.Occurrences(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("windowed = %d, want 3 (the Sep-1 one excluded): %v", len(all), summaries(all))
	}

	q := window
	q.Owner = "brandon"
	mine, _ := s.Occurrences(ctx, q)
	if len(mine) != 2 {
		t.Errorf("owner=brandon = %v, want Standup + Soccer", summaries(mine))
	}

	q = window
	q.Tag = "kids"
	kids, _ := s.Occurrences(ctx, q)
	if len(kids) != 1 || kids[0].Summary != "Soccer" {
		t.Errorf("tag=kids = %v", summaries(kids))
	}

	q = window
	q.Owner, q.Tag = "kim", "kids"
	none, _ := s.Occurrences(ctx, q)
	if len(none) != 0 {
		t.Errorf("kim + kids = %v, want none", summaries(none))
	}
}

func TestOccurrencesOpenBounds(t *testing.T) {
	s, ctx := seedOccurrences(t)
	all, err := s.Occurrences(ctx, OccurrenceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Errorf("no bounds = %d, want all 4", len(all))
	}
}

func summaries(rows []OccurrenceRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Summary
	}
	return out
}
