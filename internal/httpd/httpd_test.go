package httpd

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/store"
	"github.com/bhaskell/Concordia/internal/views"
)

func fixedNow() time.Time {
	t, _ := time.Parse(time.RFC3339, "2026-09-06T12:00:00Z")
	return t
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir()+"/c.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func seed(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	acct, _ := st.UpsertAccount(ctx, model.Account{
		Person: "brandon", Provider: model.ProviderGoogle,
		DisplayName: "G", CredentialRef: "g", Enabled: true,
	})
	cal, _ := st.UpsertCalendar(ctx, model.Calendar{AccountID: acct.ID, RemoteID: "c", DisplayName: "Work", Enabled: true})

	mk := func(uid, summary, owner string, offset int, tags ...string) {
		start := fixedNow().AddDate(0, 0, offset)
		err := st.WithTx(ctx, func(tx *sql.Tx) error {
			id, err := st.UpsertEvent(ctx, tx, model.Event{
				CalendarID: cal.ID, RemoteID: uid, UID: uid, Status: model.StatusConfirmed,
				Start: start, End: start.Add(time.Hour),
			})
			if err != nil {
				return err
			}
			return st.ReplaceOccurrences(ctx, tx, id, []model.Occurrence{{
				EventID: id, Owner: owner, Summary: summary, Tags: tags,
				Start: start, End: start.Add(time.Hour),
			}})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	mk("s1", "Standup", "brandon", 2, "work")
	mk("s2", "Soccer", "kim", 3, "kids")
}

func newServer(t *testing.T, st *store.Store) *Server {
	t.Helper()
	noWork, _ := views.Parse("not tag:work")
	return New(Config{
		Addr:  "127.0.0.1:0",
		Store: st,
		Feeds: []Feed{
			{Slug: "all", Name: "Household"},
			{Slug: "p-brandon", Name: "brandon", Owner: "brandon"},
			{Slug: "v-no-work", Name: "no-work", Predicate: noWork},
		},
		WindowBack: 30 * 24 * time.Hour,
		WindowFwd:  60 * 24 * time.Hour,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        fixedNow,
	})
}

func do(t *testing.T, s *Server, method, target string, hdr http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	st := testStore(t)
	s := newServer(t, st)

	if rec := do(t, s, http.MethodGet, "/healthz", nil); rec.Code != http.StatusOK {
		t.Errorf("healthz = %d", rec.Code)
	}
	if rec := do(t, s, http.MethodPost, "/healthz", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST healthz = %d, want 405", rec.Code)
	}

	st.Close()
	if rec := do(t, s, http.MethodGet, "/healthz", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("healthz with store down = %d, want 503", rec.Code)
	}
}

func TestFeedAll(t *testing.T) {
	st := testStore(t)
	seed(t, st)
	s := newServer(t, st)

	rec := do(t, s, http.MethodGet, "/feeds/all.ics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "SUMMARY:Standup") || !strings.Contains(body, "SUMMARY:Soccer") {
		t.Errorf("all feed missing events:\n%s", body)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag")
	}
}

func TestFeedPersonAndView(t *testing.T) {
	st := testStore(t)
	seed(t, st)
	s := newServer(t, st)

	person := do(t, s, http.MethodGet, "/feeds/p-brandon.ics", nil).Body.String()
	if !strings.Contains(person, "Standup") || strings.Contains(person, "Soccer") {
		t.Errorf("brandon feed wrong:\n%s", person)
	}

	view := do(t, s, http.MethodGet, "/feeds/v-no-work.ics", nil).Body.String()
	if strings.Contains(view, "Standup") || !strings.Contains(view, "Soccer") {
		t.Errorf("no-work feed wrong:\n%s", view)
	}
}

func TestFeedConditionalGet(t *testing.T) {
	st := testStore(t)
	seed(t, st)
	s := newServer(t, st)

	first := do(t, s, http.MethodGet, "/feeds/all.ics", nil)
	etag := first.Header().Get("ETag")

	second := do(t, s, http.MethodGet, "/feeds/all.ics", http.Header{"If-None-Match": {etag}})
	if second.Code != http.StatusNotModified {
		t.Errorf("conditional GET = %d, want 304", second.Code)
	}
}

func TestUnknownFeed(t *testing.T) {
	s := newServer(t, testStore(t))
	if rec := do(t, s, http.MethodGet, "/feeds/nope.ics", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown feed = %d, want 404", rec.Code)
	}
}

func TestDAVMountAndRun(t *testing.T) {
	st := testStore(t)
	cfg := Config{
		Addr:  "127.0.0.1:0",
		Store: st,
		DAVHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: fixedNow,
	}
	s := New(cfg)

	if rec := do(t, s, http.MethodGet, "/dav/anything", nil); rec.Code != http.StatusTeapot {
		t.Errorf("dav mount = %d, want 418", rec.Code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v", err)
	}
}
