package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
		var occID int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO occurrences
				(event_id, start_utc, end_utc, start_local, is_all_day, summary, location, owner)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING id`,
			eventID, startUTC, endUTC, startLocal, boolToInt(o.AllDay),
			o.Summary, o.Location, o.Owner,
		).Scan(&occID); err != nil {
			return fmt.Errorf("store: inserting occurrence for event %d: %w", eventID, err)
		}
		for _, tag := range o.Tags {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO occurrence_tags (occurrence_id, tag) VALUES (?, ?)`,
				occID, tag,
			); err != nil {
				return fmt.Errorf("store: tagging occurrence %d: %w", occID, err)
			}
		}
	}
	return nil
}

// tagSep separates concatenated tags in the Occurrences query. ASCII unit
// separator; it never appears in a tag slug.
const tagSep = "\x1f"

// OccurrenceQuery filters a read of the occurrences table. Zero fields are
// "any". Owner and Tag filter in SQL; view predicates are applied by the
// caller, since store must not depend on internal/views.
type OccurrenceQuery struct {
	From  time.Time
	To    time.Time
	Owner string
	Tag   string
}

// OccurrenceRow is one materialized occurrence with its calendar and tags, for
// display and feeds. EventUID plus Start form a resource's stable identity.
type OccurrenceRow struct {
	EventUID     string
	Start        time.Time
	End          time.Time
	StartLocal   string
	AllDay       bool
	Summary      string
	Location     string
	Owner        string
	Status       string
	CalendarName string
	Tags         []string
	LastModified time.Time
}

// Occurrences returns occurrences overlapping [From, To), optionally filtered
// by owner and tag, ordered by start.
func (s *Store) Occurrences(ctx context.Context, q OccurrenceQuery) ([]OccurrenceRow, error) {
	where := []string{"o.start_utc < ?", "o.end_utc > ?"}
	args := []any{bound(q.To, "9999-12-31T23:59:59Z"), bound(q.From, "")}
	if q.Owner != "" {
		where = append(where, "o.owner = ?")
		args = append(args, q.Owner)
	}
	if q.Tag != "" {
		where = append(where, "EXISTS (SELECT 1 FROM occurrence_tags f WHERE f.occurrence_id = o.id AND f.tag = ?)")
		args = append(args, q.Tag)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT e.uid, o.start_utc, o.end_utc, o.start_local, o.is_all_day,
		       o.summary, o.location, o.owner, COALESCE(e.status, ''),
		       c.display_name, COALESCE(e.updated_at, ''),
		       COALESCE(GROUP_CONCAT(ot.tag, char(31)), '')
		FROM occurrences o
		JOIN events e ON e.id = o.event_id
		JOIN calendars c ON c.id = e.calendar_id
		LEFT JOIN occurrence_tags ot ON ot.occurrence_id = o.id
		WHERE `+strings.Join(where, " AND ")+`
		GROUP BY o.id
		ORDER BY o.start_utc, c.display_name`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: querying occurrences: %w", err)
	}
	defer rows.Close()

	var out []OccurrenceRow
	for rows.Next() {
		var (
			r                OccurrenceRow
			startUTC, endUTC string
			allDay           int
			updatedAt, tags  string
		)
		if err := rows.Scan(&r.EventUID, &startUTC, &endUTC, &r.StartLocal, &allDay,
			&r.Summary, &r.Location, &r.Owner, &r.Status, &r.CalendarName,
			&updatedAt, &tags); err != nil {
			return nil, fmt.Errorf("store: scanning occurrence: %w", err)
		}
		r.AllDay = allDay != 0
		if updatedAt != "" {
			if t, perr := time.Parse(timeLayout, updatedAt); perr == nil {
				r.LastModified = t
			}
		}
		if r.Start, err = decodeOccurrenceTime(startUTC, r.AllDay); err != nil {
			return nil, fmt.Errorf("store: occurrence start %q: %w", startUTC, err)
		}
		if r.End, err = decodeOccurrenceTime(endUTC, r.AllDay); err != nil {
			return nil, fmt.Errorf("store: occurrence end %q: %w", endUTC, err)
		}
		if tags != "" {
			r.Tags = strings.Split(tags, tagSep)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func decodeOccurrenceTime(s string, allDay bool) (time.Time, error) {
	if allDay {
		return time.ParseInLocation(dateLayout, s, time.UTC)
	}
	return time.Parse(time.RFC3339, s)
}

// bound renders a query bound, substituting open when the time is zero.
func bound(t time.Time, open string) string {
	if t.IsZero() {
		return open
	}
	return t.UTC().Format(time.RFC3339)
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
