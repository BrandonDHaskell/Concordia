package main

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

	"github.com/emersion/go-webdav/caldav"

	"github.com/bhaskell/Concordia/internal/config"
	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/store"
)

func TestBuildServerServesFeedsAndDAV(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir()+"/c.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	acct, _ := st.UpsertAccount(ctx, model.Account{
		Person: "brandon", Provider: model.ProviderGoogle,
		DisplayName: "G", CredentialRef: "g", Enabled: true,
	})
	cal, _ := st.UpsertCalendar(ctx, model.Calendar{AccountID: acct.ID, RemoteID: "c", DisplayName: "Work", Enabled: true})
	start := time.Now().Add(48 * time.Hour)
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		id, e := st.UpsertEvent(ctx, tx, model.Event{
			CalendarID: cal.ID, RemoteID: "e1", UID: "e1@x", Status: model.StatusConfirmed,
			Start: start, End: start.Add(time.Hour),
		})
		if e != nil {
			return e
		}
		return st.ReplaceOccurrences(ctx, tx, id, []model.Occurrence{{
			EventID: id, Owner: "brandon", Summary: "Standup", Tags: []string{"work"},
			Start: start, End: start.Add(time.Hour),
		}})
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Server: config.Server{
			Listen:     "127.0.0.1:0",
			WindowBack: config.Duration(60 * 24 * time.Hour),
			WindowFwd:  config.Duration(400 * 24 * time.Hour),
		},
		Serve:    config.Serve{SummaryPrefix: true},
		Accounts: []config.Account{{Person: "brandon", Provider: "google", Name: "Gmail"}},
		Views:    []config.View{{Name: "no-work", Predicate: "not tag:work"}},
	}

	srv, err := buildServer(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Web view renders the agenda and a chip for the person.
	page := get(t, ts, "/")
	if !strings.Contains(page, "<!doctype html>") || !strings.Contains(page, "Standup") {
		t.Errorf("web view:\n%s", page)
	}
	if !strings.Contains(page, `href="/?owner=brandon"`) {
		t.Errorf("web view missing the brandon owner chip:\n%s", page)
	}

	// ICS feed, with the [serve] summary prefix applied.
	body := get(t, ts, "/feeds/p-brandon.ics")
	if !strings.Contains(body, "SUMMARY:[brandon] Standup") {
		t.Errorf("person feed:\n%s", body)
	}

	// The no-work view feed excludes the work-tagged event.
	if v := get(t, ts, "/feeds/v-no-work.ics"); strings.Contains(v, "Standup") {
		t.Errorf("no-work feed still has Standup:\n%s", v)
	}

	// CalDAV: the mounted handler lists the collections.
	c, err := caldav.NewClient(ts.Client(), ts.URL+"/dav/")
	if err != nil {
		t.Fatal(err)
	}
	cals, err := c.FindCalendars(context.Background(), "/dav/principal/cal/")
	if err != nil {
		t.Fatalf("FindCalendars: %v", err)
	}
	paths := map[string]bool{}
	for _, cal := range cals {
		paths[cal.Path] = true
	}
	if !paths["/dav/principal/cal/p-brandon/"] || !paths["/dav/principal/cal/v-no-work/"] {
		t.Errorf("collections = %v", paths)
	}
}

func get(t *testing.T, ts *httptest.Server, path string) string {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", path, resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
