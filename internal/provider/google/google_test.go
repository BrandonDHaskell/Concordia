package google

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/provider"
)

const fixtureDir = "../../../testdata/fixtures/google"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func writeFixture(t *testing.T, w http.ResponseWriter, name string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if _, err := w.Write(readFixture(t, name)); err != nil {
		t.Fatalf("writing fixture response: %v", err)
	}
}

func newFakeProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	svc, err := gcal.NewService(context.Background(),
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("gcal.NewService: %v", err)
	}

	fixedNow := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	return newProvider(svc, 60*24*time.Hour, 400*24*time.Hour,
		WithClock(func() time.Time { return fixedNow }),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
}

var testCal = model.Calendar{RemoteID: "brandon@example.com", DisplayName: "Primary"}

func TestSyncFullPaginates(t *testing.T) {
	p := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("syncToken") != "" {
			t.Errorf("full sync sent a syncToken: %s", r.URL)
		}
		if q.Get("timeMin") == "" || q.Get("timeMax") == "" {
			t.Errorf("full sync missing time window: %s", r.URL)
		}
		if q.Get("pageToken") == "PAGE2" {
			writeFixture(t, w, "events_full_page2.json")
			return
		}
		writeFixture(t, w, "events_full_page1.json")
	})

	d, next, err := p.Sync(context.Background(), testCal, "")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if next != "SYNC1" {
		t.Errorf("next token = %q, want SYNC1", next)
	}
	if len(d.Changed) != 3 || len(d.Deleted) != 0 {
		t.Fatalf("changed=%d deleted=%d, want 3/0", len(d.Changed), len(d.Deleted))
	}
	if d.FullResync {
		t.Error("FullResync set on a normal full sync")
	}
	ids := changedIDs(d.Changed)
	if ids != "ev1,ev2,ev3" {
		t.Errorf("changed ids = %s", ids)
	}
}

func TestSyncIncrementalWithTombstone(t *testing.T) {
	p := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("syncToken"); got != "SYNC1" {
			t.Errorf("syncToken = %q, want SYNC1", got)
		}
		writeFixture(t, w, "events_delta_changed.json")
	})

	d, next, err := p.Sync(context.Background(), testCal, "SYNC1")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if next != "SYNC2" {
		t.Errorf("next token = %q, want SYNC2", next)
	}
	if len(d.Changed) != 1 || d.Changed[0].RemoteID != "ev1" {
		t.Errorf("changed = %+v, want just ev1", d.Changed)
	}
	if len(d.Deleted) != 1 || d.Deleted[0] != "ev2" {
		t.Errorf("deleted = %v, want [ev2]", d.Deleted)
	}
	if d.FullResync {
		t.Error("FullResync set on an incremental sync")
	}
}

func TestSyncStaleTokenTriggersFullResync(t *testing.T) {
	var sawStale, sawRetry bool
	p := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("syncToken") == "STALE" {
			sawStale = true
			w.Header().Set("Content-Type", "application/json; charset=UTF-8")
			w.WriteHeader(http.StatusGone)
			w.Write(readFixture(t, "events_sync_gone.json"))
			return
		}
		sawRetry = true
		if q.Get("timeMin") == "" {
			t.Errorf("resync missing time window: %s", r.URL)
		}
		if q.Get("pageToken") == "PAGE2" {
			writeFixture(t, w, "events_full_page2.json")
			return
		}
		writeFixture(t, w, "events_full_page1.json")
	})

	d, next, err := p.Sync(context.Background(), testCal, "STALE")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !sawStale || !sawRetry {
		t.Fatalf("expected a 410 then a resync (stale=%v retry=%v)", sawStale, sawRetry)
	}
	if !d.FullResync {
		t.Error("FullResync not set after 410")
	}
	if next != "SYNC1" || len(d.Changed) != 3 {
		t.Errorf("resync result: next=%q changed=%d", next, len(d.Changed))
	}
}

func TestSyncCancelledInstanceIsAChange(t *testing.T) {
	p := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeFixture(t, w, "events_delta_cancel_instance.json")
	})

	d, next, err := p.Sync(context.Background(), testCal, "SYNC3")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if next != "SYNC4" {
		t.Errorf("next = %q, want SYNC4", next)
	}
	if len(d.Deleted) != 0 {
		t.Errorf("deleted = %v, want none (cancelled instance is not a deletion)", d.Deleted)
	}
	if len(d.Changed) != 1 || d.Changed[0].RemoteID != "rec1_20260921T170000Z" {
		t.Fatalf("changed = %+v, want the cancelled instance", d.Changed)
	}
	body := string(d.Changed[0].Body)
	if !strings.Contains(body, `"recurringEventId"`) || !strings.Contains(body, `"cancelled"`) {
		t.Errorf("body lost the exception markers: %s", body)
	}
}

func TestSyncRecurringOverridePassesThrough(t *testing.T) {
	p := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeFixture(t, w, "events_recurring.json")
	})

	d, _, err := p.Sync(context.Background(), testCal, "SYNC2")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(d.Changed) != 2 {
		t.Fatalf("changed = %d, want 2 (master + override)", len(d.Changed))
	}
	var override string
	for _, e := range d.Changed {
		if strings.Contains(string(e.Body), `"recurringEventId"`) {
			override = e.RemoteID
		}
	}
	if override != "rec1_20260914T170000Z" {
		t.Errorf("override not carried through with recurringEventId: %q", override)
	}
}

func TestCalendars(t *testing.T) {
	p := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "calendarList") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeFixture(t, w, "calendarlist.json")
	})

	cals, err := p.Calendars(context.Background())
	if err != nil {
		t.Fatalf("Calendars: %v", err)
	}
	if len(cals) != 2 {
		t.Fatalf("got %d calendars, want 2", len(cals))
	}
	if cals[0].RemoteID != "brandon@example.com" || !cals[0].Enabled {
		t.Errorf("cal[0] = %+v", cals[0])
	}
	if cals[1].DisplayName != "Household" {
		t.Errorf("cal[1] display name = %q, want the summaryOverride", cals[1].DisplayName)
	}
}

func changedIDs(evs []provider.RawEvent) string {
	parts := make([]string, len(evs))
	for i, e := range evs {
		parts[i] = e.RemoteID
	}
	return strings.Join(parts, ",")
}
