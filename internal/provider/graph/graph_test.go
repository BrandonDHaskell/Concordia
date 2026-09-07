package graph

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

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/provider"
)

const fixtureDir = "../../../testdata/fixtures/graph"

var testCal = model.Calendar{RemoteID: "cal1", DisplayName: "Calendar"}

// fakeGraph serves fixtures, rewriting the {{BASE}} placeholder in nextLink and
// deltaLink to its own URL.
func fakeGraph(t *testing.T, h func(w http.ResponseWriter, r *http.Request, serve func(string))) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	serve := func(w http.ResponseWriter) func(string) {
		return func(name string) {
			b, err := os.ReadFile(filepath.Join(fixtureDir, name))
			if err != nil {
				t.Fatalf("fixture %s: %v", name, err)
			}
			b = []byte(strings.ReplaceAll(string(b), "{{BASE}}", srv.URL))
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
		}
	}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r, serve(w))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newProvider(t *testing.T, srv *httptest.Server) *Provider {
	t.Helper()
	p := New(srv.Client(), 60*24*time.Hour, 400*24*time.Hour,
		WithEndpoint(srv.URL),
		WithClock(func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	p.minWait = time.Millisecond
	return p
}

func changedIDs(evs []provider.RawEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.RemoteID
	}
	return out
}

func TestSyncFullPaginatesAndSkipsSeriesMaster(t *testing.T) {
	srv := fakeGraph(t, func(w http.ResponseWriter, r *http.Request, serve func(string)) {
		q := r.URL.Query()
		switch {
		case !strings.Contains(r.URL.Path, "calendarView/delta"):
			t.Fatalf("unexpected path %s", r.URL.Path)
		case q.Get("$skiptoken") == "SKIP2":
			serve("delta_page2.json")
		case q.Get("startDateTime") != "":
			if q.Get("endDateTime") == "" {
				t.Error("full delta missing endDateTime")
			}
			serve("delta_page1.json")
		default:
			t.Fatalf("unexpected query %s", r.URL.RawQuery)
		}
	})
	p := newProvider(t, srv)

	d, next, err := p.Sync(context.Background(), testCal, "")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if next != "DELTA1" {
		t.Errorf("next token = %q, want DELTA1", next)
	}
	if got := changedIDs(d.Changed); strings.Join(got, ",") != "EV1,EV2,EV3_OCC_20260921" {
		t.Errorf("changed = %v, want EV1, EV2, EV3 occurrence (seriesMaster dropped)", got)
	}
	if len(d.Deleted) != 0 || d.FullResync {
		t.Errorf("deleted=%v full=%v", d.Deleted, d.FullResync)
	}
}

func TestSyncIncrementalRemovalAndCancellation(t *testing.T) {
	srv := fakeGraph(t, func(w http.ResponseWriter, r *http.Request, serve func(string)) {
		if r.URL.Query().Get("$deltatoken") != "DELTA1" {
			t.Errorf("deltatoken = %q, want DELTA1", r.URL.Query().Get("$deltatoken"))
		}
		serve("delta_incremental.json")
	})
	p := newProvider(t, srv)

	d, next, err := p.Sync(context.Background(), testCal, "DELTA1")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if next != "DELTA2" {
		t.Errorf("next = %q, want DELTA2", next)
	}
	if got := changedIDs(d.Changed); strings.Join(got, ",") != "EV1" {
		t.Errorf("changed = %v, want [EV1]", got)
	}
	if strings.Join(d.Deleted, ",") != "EV2,EV4" {
		t.Errorf("deleted = %v, want [EV2 (removed), EV4 (cancelled)]", d.Deleted)
	}
}

func TestSyncStaleTokenTriggersFullResync(t *testing.T) {
	var saw410, sawFull bool
	srv := fakeGraph(t, func(w http.ResponseWriter, r *http.Request, serve func(string)) {
		q := r.URL.Query()
		switch {
		case q.Get("$deltatoken") == "STALE":
			saw410 = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGone)
			b, _ := os.ReadFile(filepath.Join(fixtureDir, "delta_gone.json"))
			w.Write(b)
		case q.Get("$skiptoken") == "SKIP2":
			serve("delta_page2.json")
		case q.Get("startDateTime") != "":
			sawFull = true
			serve("delta_page1.json")
		default:
			t.Fatalf("unexpected query %s", r.URL.RawQuery)
		}
	})
	p := newProvider(t, srv)

	d, next, err := p.Sync(context.Background(), testCal, "STALE")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !saw410 || !sawFull {
		t.Fatalf("expected 410 then full resync (410=%v full=%v)", saw410, sawFull)
	}
	if !d.FullResync || next != "DELTA1" || len(d.Changed) != 3 {
		t.Errorf("resync: full=%v next=%q changed=%d", d.FullResync, next, len(d.Changed))
	}
}

func TestSyncRetriesOnThrottle(t *testing.T) {
	var calls int
	srv := fakeGraph(t, func(w http.ResponseWriter, r *http.Request, serve func(string)) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"code":"TooManyRequests"}}`))
			return
		}
		serve("delta_page2.json")
	})
	p := newProvider(t, srv)

	d, next, err := p.Sync(context.Background(), testCal, "DELTA1")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one throttled, one ok)", calls)
	}
	if next != "DELTA1" || len(d.Changed) != 1 {
		t.Errorf("after retry: next=%q changed=%d", next, len(d.Changed))
	}
}

func TestCalendars(t *testing.T) {
	srv := fakeGraph(t, func(w http.ResponseWriter, r *http.Request, serve func(string)) {
		if !strings.HasSuffix(r.URL.Path, "/me/calendars") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		serve("calendars.json")
	})
	p := newProvider(t, srv)

	cals, err := p.Calendars(context.Background())
	if err != nil {
		t.Fatalf("Calendars: %v", err)
	}
	if len(cals) != 2 || cals[0].RemoteID != "cal1" || cals[1].DisplayName != "Team events" {
		t.Fatalf("calendars = %+v", cals)
	}
	if !cals[0].Enabled {
		t.Error("discovered calendar not enabled")
	}
}

func TestRetryAfterParsing(t *testing.T) {
	p := New(nil, 0, 0)
	p.minWait = time.Second
	p.maxWait = time.Minute

	if got := p.retryAfter("10"); got != 10*time.Second {
		t.Errorf(`retryAfter("10") = %v`, got)
	}
	if got := p.retryAfter("0"); got != time.Second {
		t.Errorf(`retryAfter("0") = %v, want clamped to minWait`, got)
	}
	if got := p.retryAfter("9999"); got != time.Minute {
		t.Errorf(`retryAfter("9999") = %v, want clamped to maxWait`, got)
	}
	if got := p.retryAfter(""); got != 5*time.Second {
		t.Errorf(`retryAfter("") = %v, want 5*minWait`, got)
	}
	if got := p.retryAfter("garbage"); got != 5*time.Second {
		t.Errorf(`retryAfter("garbage") = %v`, got)
	}
}

func TestDeltaToken(t *testing.T) {
	if got := deltaToken("https://graph.microsoft.com/v1.0/me/calendarView/delta?$deltatoken=abc123"); got != "abc123" {
		t.Errorf("deltaToken = %q", got)
	}
	if got := deltaToken(""); got != "" {
		t.Errorf("deltaToken(empty) = %q", got)
	}
}
