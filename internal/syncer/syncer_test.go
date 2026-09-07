package syncer

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/normalize"
	"github.com/bhaskell/Concordia/internal/provider"
	"github.com/bhaskell/Concordia/internal/store"
)

// scriptedProvider returns a queued Delta per Sync call.
type scriptedProvider struct {
	steps []step
	i     int
}

type step struct {
	delta provider.Delta
	token string
}

func (p *scriptedProvider) Sync(context.Context, model.Calendar, string) (provider.Delta, string, error) {
	s := p.steps[p.i]
	p.i++
	return s.delta, s.token, nil
}

func changed(remoteID, body string) provider.RawEvent {
	return provider.RawEvent{RemoteID: remoteID, ETag: `"e"`, Body: []byte(body)}
}

func newSyncer(t *testing.T) (*Syncer, context.Context, model.Calendar) {
	t.Helper()
	s := openStore(t)
	ctx := context.Background()

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

	sy := &Syncer{
		Store: s,
		Window: model.Window{
			Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return sy, ctx, cal
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	path := t.TempDir() + "/concordia.db"
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

const weeklyMaster = `{
	"id": "rec1", "iCalUID": "rec1@g", "status": "confirmed", "summary": "Weekly sync",
	"start": {"dateTime": "2026-09-07T10:00:00-07:00", "timeZone": "America/Los_Angeles"},
	"end":   {"dateTime": "2026-09-07T10:30:00-07:00", "timeZone": "America/Los_Angeles"},
	"recurrence": ["RRULE:FREQ=WEEKLY;BYDAY=MO"]
}`

const oneShot = `{
	"id": "d1", "iCalUID": "d1@g", "status": "confirmed", "summary": "Dentist",
	"start": {"dateTime": "2026-09-11T14:00:00-07:00", "timeZone": "America/Los_Angeles"},
	"end":   {"dateTime": "2026-09-11T15:00:00-07:00", "timeZone": "America/Los_Angeles"}
}`

func TestCalendarInitialSync(t *testing.T) {
	sy, ctx, cal := newSyncer(t)
	p := &scriptedProvider{steps: []step{{
		delta: provider.Delta{Changed: []provider.RawEvent{
			changed("rec1", weeklyMaster),
			changed("d1", oneShot),
		}},
		token: "SYNC1",
	}}}

	res, err := sy.Calendar(ctx, p, normalize.Event, cal, model.ProviderGoogle, "brandon")
	if err != nil {
		t.Fatalf("Calendar: %v", err)
	}
	if res.Changed != 2 || res.Deleted != 0 {
		t.Errorf("res = %+v", res)
	}
	// 4 weekly (Sep 7,14,21,28) + 1 dentist.
	if res.Occurrences != 5 {
		t.Errorf("occurrences = %d, want 5", res.Occurrences)
	}
	if n, _ := sy.Store.CountOccurrences(ctx, cal.ID); n != 5 {
		t.Errorf("stored occurrences = %d, want 5", n)
	}

	cals, _ := sy.Store.Calendars(ctx, cal.AccountID)
	if cals[0].SyncToken != "SYNC1" {
		t.Errorf("sync token = %q, want SYNC1", cals[0].SyncToken)
	}
	if cals[0].LastSyncAt.IsZero() {
		t.Error("last_sync_at not set")
	}
}

func TestCalendarIncrementalTombstone(t *testing.T) {
	sy, ctx, cal := newSyncer(t)
	p := &scriptedProvider{steps: []step{
		{delta: provider.Delta{Changed: []provider.RawEvent{
			changed("rec1", weeklyMaster), changed("d1", oneShot),
		}}, token: "SYNC1"},
		{delta: provider.Delta{Deleted: []string{"d1"}}, token: "SYNC2"},
	}}

	if _, err := sy.Calendar(ctx, p, normalize.Event, cal, model.ProviderGoogle, "brandon"); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	cal.SyncToken = "SYNC1"
	res, err := sy.Calendar(ctx, p, normalize.Event, cal, model.ProviderGoogle, "brandon")
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if res.Deleted != 1 {
		t.Errorf("deleted = %d, want 1", res.Deleted)
	}
	// The dentist occurrence is gone; the 4 weekly ones remain.
	if n, _ := sy.Store.CountOccurrences(ctx, cal.ID); n != 4 {
		t.Errorf("occurrences after tombstone = %d, want 4", n)
	}
	cals, _ := sy.Store.Calendars(ctx, cal.AccountID)
	if cals[0].SyncToken != "SYNC2" {
		t.Errorf("token = %q, want SYNC2", cals[0].SyncToken)
	}
}

func TestCalendarOverrideThenCancelReappears(t *testing.T) {
	sy, ctx, cal := newSyncer(t)

	override := `{
		"id": "rec1_20260914", "iCalUID": "rec1@g", "status": "confirmed", "summary": "Weekly (moved)",
		"recurringEventId": "rec1",
		"originalStartTime": {"dateTime": "2026-09-14T10:00:00-07:00", "timeZone": "America/Los_Angeles"},
		"start": {"dateTime": "2026-09-15T10:00:00-07:00", "timeZone": "America/Los_Angeles"},
		"end":   {"dateTime": "2026-09-15T10:30:00-07:00", "timeZone": "America/Los_Angeles"}
	}`

	p := &scriptedProvider{steps: []step{
		{delta: provider.Delta{Changed: []provider.RawEvent{changed("rec1", weeklyMaster)}}, token: "S1"},
		{delta: provider.Delta{Changed: []provider.RawEvent{changed("rec1_20260914", override)}}, token: "S2"},
		{delta: provider.Delta{Deleted: []string{"rec1_20260914"}}, token: "S3"},
	}}

	run := func(token string) Result {
		cal.SyncToken = token
		res, err := sy.Calendar(ctx, p, normalize.Event, cal, model.ProviderGoogle, "brandon")
		if err != nil {
			t.Fatalf("sync at %s: %v", token, err)
		}
		return res
	}

	run("")   // master: 4 instances
	run("S1") // override moves the 14th to the 15th: still 4 occurrences

	// The moved instance should be on the 15th, not the 14th.
	dates := occurrenceDates(t, sy.Store, ctx, cal.ID)
	if has(dates, "2026-09-14") || !has(dates, "2026-09-15") {
		t.Fatalf("override not applied: %v", dates)
	}

	run("S2") // override deleted: the 14th instance comes back
	dates = occurrenceDates(t, sy.Store, ctx, cal.ID)
	if !has(dates, "2026-09-14") || has(dates, "2026-09-15") {
		t.Fatalf("instance did not revert after override deletion: %v", dates)
	}
}

func TestCalendarFullResyncReconciles(t *testing.T) {
	sy, ctx, cal := newSyncer(t)
	p := &scriptedProvider{steps: []step{
		{delta: provider.Delta{Changed: []provider.RawEvent{
			changed("rec1", weeklyMaster), changed("d1", oneShot),
		}}, token: "SYNC1"},
		// Full resync that no longer contains d1.
		{delta: provider.Delta{
			Changed:    []provider.RawEvent{changed("rec1", weeklyMaster)},
			FullResync: true,
		}, token: "SYNC9"},
	}}

	if _, err := sy.Calendar(ctx, p, normalize.Event, cal, model.ProviderGoogle, "brandon"); err != nil {
		t.Fatalf("first: %v", err)
	}
	cal.SyncToken = "SYNC1"
	res, err := sy.Calendar(ctx, p, normalize.Event, cal, model.ProviderGoogle, "brandon")
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if !res.FullResync || res.Deleted != 1 {
		t.Errorf("res = %+v, want FullResync with 1 reconciled deletion", res)
	}
	if n, _ := sy.Store.CountOccurrences(ctx, cal.ID); n != 4 {
		t.Errorf("occurrences after resync = %d, want 4 (d1 dropped)", n)
	}
}

func occurrenceDates(t *testing.T, s *store.Store, ctx context.Context, calID int64) []string {
	t.Helper()
	rows, err := s.DB().QueryContext(ctx, `
		SELECT substr(o.start_local, 1, 10) FROM occurrences o
		JOIN events e ON e.id = o.event_id WHERE e.calendar_id = ?
		ORDER BY o.start_utc`, calID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func has(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
