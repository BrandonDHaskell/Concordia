package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
)

// ReplaceOccurrences deletes every occurrence for an event and inserts the
// given set. Passing an empty slice just clears them. occurrences is derived
// state, so a full replace per event is the natural unit (invariant 6). Must
// run in the sync transaction.
func (s *Store) ReplaceOccurrences(ctx context.Context, tx *sql.Tx, eventID int64, occs []model.Occurrence) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM occurrences WHERE event_id = ?`, eventID); err != nil {
		return fmt.Errorf("store: clearing occurrences for event %d: %w", eventID, err)
	}
	for _, o := range occs {
		startUTC, endUTC, startLocal := encodeOccurrence(o)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO occurrences
				(event_id, start_utc, end_utc, start_local, is_all_day, summary, location, owner)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			eventID, startUTC, endUTC, startLocal, boolToInt(o.AllDay),
			o.Summary, o.Location, o.Owner,
		); err != nil {
			return fmt.Errorf("store: inserting occurrence for event %d: %w", eventID, err)
		}
	}
	return nil
}

// CountOccurrences returns how many occurrence rows a calendar has, for
// reporting.
func (s *Store) CountOccurrences(ctx context.Context, calendarID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM occurrences o
		JOIN events e ON e.id = o.event_id
		WHERE e.calendar_id = ?`, calendarID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: counting occurrences: %w", err)
	}
	return n, nil
}

// encodeOccurrence renders the three stored time columns. All-day occurrences
// are bare dates in every column and are never turned into a UTC instant
// (invariant 5); timed occurrences store a UTC instant, an exclusive-end UTC
// instant, and the local wall time with its offset.
func encodeOccurrence(o model.Occurrence) (startUTC, endUTC, startLocal string) {
	if o.AllDay {
		d := o.Start.UTC().Format(dateLayout)
		end := d
		if !o.End.IsZero() {
			end = o.End.UTC().Format(dateLayout)
		}
		return d, end, d
	}
	return o.Start.UTC().Format(time.RFC3339),
		o.End.UTC().Format(time.RFC3339),
		o.Start.Format(time.RFC3339)
}
