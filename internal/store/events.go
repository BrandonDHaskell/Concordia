package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
)

// UpsertEvent inserts ev or updates the existing row with the same
// (calendar_id, uid, recurrence_id), returning the row id. recurrence_id is
// stored as "" (never NULL) for masters so the unique key behaves. Must run in
// the sync transaction.
func (s *Store) UpsertEvent(ctx context.Context, tx *sql.Tx, ev model.Event) (int64, error) {
	dtstart, dtend := encodeEventBounds(ev)

	exdates := ""
	if len(ev.EXDates) > 0 {
		b, err := json.Marshal(ev.EXDates)
		if err != nil {
			return 0, fmt.Errorf("store: encoding exdates for %s: %w", ev.UID, err)
		}
		exdates = string(b)
	}

	updatedAt := ev.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	var deletedAt any
	if !ev.DeletedAt.IsZero() {
		deletedAt = ev.DeletedAt.UTC().Format(timeLayout)
	}

	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO events (
			calendar_id, remote_id, uid, recurrence_id, summary, location,
			dtstart, dtend, is_all_day, tzid, rrule, exdates, status, etag, raw,
			updated_at, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (calendar_id, uid, recurrence_id) DO UPDATE SET
			remote_id  = excluded.remote_id,
			summary    = excluded.summary,
			location   = excluded.location,
			dtstart    = excluded.dtstart,
			dtend      = excluded.dtend,
			is_all_day = excluded.is_all_day,
			tzid       = excluded.tzid,
			rrule      = excluded.rrule,
			exdates    = excluded.exdates,
			status     = excluded.status,
			etag       = excluded.etag,
			raw        = excluded.raw,
			updated_at = excluded.updated_at,
			deleted_at = excluded.deleted_at
		RETURNING id`,
		ev.CalendarID, ev.RemoteID, ev.UID, ev.RecurrenceID, ev.Summary, ev.Location,
		dtstart, dtend, boolToInt(ev.AllDay), ev.TZID, ev.RRULE, exdates,
		ev.Status, ev.ETag, ev.Raw, updatedAt.UTC().Format(timeLayout), deletedAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: upsert event %s (%s): %w", ev.UID, ev.RemoteID, err)
	}
	return id, nil
}

// TombstoneEvent marks the event with the given remote id deleted and clears
// its occurrences. It returns the event's uid so the caller can re-expand that
// series (a deleted override lets the underlying instance reappear), or "" if
// no live event matched.
func (s *Store) TombstoneEvent(ctx context.Context, tx *sql.Tx, calendarID int64, remoteID string) (string, error) {
	var id int64
	var uid string
	err := tx.QueryRowContext(ctx, `
		SELECT id, uid FROM events
		WHERE calendar_id = ? AND remote_id = ? AND deleted_at IS NULL`,
		calendarID, remoteID,
	).Scan(&id, &uid)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: finding event %s to tombstone: %w", remoteID, err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE events SET deleted_at = ?, status = ? WHERE id = ?`,
		time.Now().UTC().Format(timeLayout), model.StatusCancelled, id,
	); err != nil {
		return "", fmt.Errorf("store: tombstoning event %s: %w", remoteID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM occurrences WHERE event_id = ?`, id); err != nil {
		return "", fmt.Errorf("store: clearing occurrences for %s: %w", remoteID, err)
	}
	return uid, nil
}

// LiveEvents returns id and remote_id for every non-deleted event in a
// calendar. Used to reconcile a full resync.
func (s *Store) LiveEvents(ctx context.Context, tx *sql.Tx, calendarID int64) (map[string]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT remote_id, id FROM events
		WHERE calendar_id = ? AND deleted_at IS NULL`, calendarID)
	if err != nil {
		return nil, fmt.Errorf("store: listing live events: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int64)
	for rows.Next() {
		var remoteID string
		var id int64
		if err := rows.Scan(&remoteID, &id); err != nil {
			return nil, err
		}
		out[remoteID] = id
	}
	return out, rows.Err()
}

// EventsByUIDs loads every event row (masters and overrides, deleted or not)
// for the given uids in a calendar, so the expander sees a whole series.
func (s *Store) EventsByUIDs(ctx context.Context, tx *sql.Tx, calendarID int64, uids []string) ([]model.Event, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(uids))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, 0, len(uids)+1)
	args = append(args, calendarID)
	for _, u := range uids {
		args = append(args, u)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id, calendar_id, remote_id, uid, recurrence_id, summary, location,
		       dtstart, COALESCE(dtend, ''), is_all_day, COALESCE(tzid, ''),
		       COALESCE(rrule, ''), COALESCE(exdates, ''), COALESCE(status, ''),
		       COALESCE(etag, ''), raw, updated_at, COALESCE(deleted_at, '')
		FROM events
		WHERE calendar_id = ? AND uid IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: loading events by uid: %w", err)
	}
	defer rows.Close()

	var out []model.Event
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func scanEvent(rows *sql.Rows) (model.Event, error) {
	var (
		ev                   model.Event
		dtstart, dtend       string
		isAllDay             int
		tzid, exdates        string
		updatedAt, deletedAt string
		raw                  []byte
	)
	if err := rows.Scan(
		&ev.ID, &ev.CalendarID, &ev.RemoteID, &ev.UID, &ev.RecurrenceID,
		&ev.Summary, &ev.Location, &dtstart, &dtend, &isAllDay, &tzid,
		&ev.RRULE, &exdates, &ev.Status, &ev.ETag, &raw, &updatedAt, &deletedAt,
	); err != nil {
		return model.Event{}, fmt.Errorf("store: scanning event: %w", err)
	}

	ev.AllDay = isAllDay != 0
	ev.TZID = tzid
	ev.Raw = raw

	if exdates != "" {
		if err := json.Unmarshal([]byte(exdates), &ev.EXDates); err != nil {
			return model.Event{}, fmt.Errorf("store: decoding exdates for %s: %w", ev.UID, err)
		}
	}

	start, err := decodeEventTime(dtstart, tzid, ev.AllDay)
	if err != nil {
		return model.Event{}, fmt.Errorf("store: event %s dtstart %q: %w", ev.UID, dtstart, err)
	}
	ev.Start = start
	if dtend != "" {
		end, err := decodeEventTime(dtend, tzid, ev.AllDay)
		if err != nil {
			return model.Event{}, fmt.Errorf("store: event %s dtend %q: %w", ev.UID, dtend, err)
		}
		ev.End = end
	}

	if t, err := time.Parse(timeLayout, updatedAt); err == nil {
		ev.UpdatedAt = t
	}
	if deletedAt != "" {
		if t, err := time.Parse(timeLayout, deletedAt); err == nil {
			ev.DeletedAt = t
		}
	}
	return ev, nil
}

// encodeEventBounds renders dtstart and dtend for storage: a bare date for
// all-day events, RFC3339 with offset for timed. An empty dtend is stored NULL.
func encodeEventBounds(ev model.Event) (dtstart string, dtend any) {
	if ev.AllDay {
		dtstart = ev.Start.UTC().Format(dateLayout)
		if !ev.End.IsZero() {
			dtend = ev.End.UTC().Format(dateLayout)
		}
		return dtstart, dtend
	}
	if !ev.Start.IsZero() {
		dtstart = ev.Start.Format(time.RFC3339)
	}
	if !ev.End.IsZero() {
		dtend = ev.End.Format(time.RFC3339)
	}
	return dtstart, dtend
}

func decodeEventTime(s, tzid string, allDay bool) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if allDay {
		return time.ParseInLocation(dateLayout, s, time.UTC)
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	if tzid != "" {
		if loc, err := time.LoadLocation(tzid); err == nil {
			return t.In(loc), nil
		}
	}
	return t, nil
}

const dateLayout = "2006-01-02"
