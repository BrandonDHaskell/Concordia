package syncer_test

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

	graphprov "github.com/bhaskell/Concordia/internal/provider/graph"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/normalize"
	"github.com/bhaskell/Concordia/internal/store"
	"github.com/bhaskell/Concordia/internal/syncer"
)

const graphFixtures = "../../testdata/fixtures/graph"

// TestEndToEndGraphSync runs the whole ingest chain with the real Graph
// provider (its actual HTTP client, pointed at a local fixture server) feeding
// normalize, expand, and the store. Graph occurrences are server-expanded, so
// each lands as one occurrence without the local expander doing recurrence work.
func TestEndToEndGraphSync(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve := func(name string) {
			b, err := os.ReadFile(filepath.Join(graphFixtures, name))
			if err != nil {
				t.Fatal(err)
			}
			b = []byte(strings.ReplaceAll(string(b), "{{BASE}}", srv.URL))
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
		}
		q := r.URL.Query()
		switch {
		case q.Get("$skiptoken") == "SKIP2":
			serve("delta_page2.json")
		case q.Get("startDateTime") != "":
			serve("delta_page1.json")
		default:
			http.Error(w, "unexpected "+r.URL.RawQuery, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "concordia.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	acct, err := st.UpsertAccount(ctx, model.Account{
		Person: "brandon", Provider: model.ProviderGraph,
		DisplayName: "Work Outlook", CredentialRef: "work-outlook", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cal, err := st.UpsertCalendar(ctx, model.Calendar{
		AccountID: acct.ID, RemoteID: "cal1", DisplayName: "Calendar", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	prov := graphprov.New(srv.Client(), 60*24*time.Hour, 400*24*time.Hour,
		graphprov.WithEndpoint(srv.URL),
		graphprov.WithClock(func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }),
		graphprov.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	sy := &syncer.Syncer{
		Store: st,
		Window: model.Window{
			Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	res, err := sy.Calendar(ctx, prov, normalize.Event, cal, model.ProviderGraph, acct.Person)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	// EV1 (timed), EV2 (all-day), EV3 occurrence -> 3 events, 3 occurrences.
	// The seriesMaster item is dropped by the provider.
	if res.Changed != 3 || res.Occurrences != 3 || res.NextToken != "DELTA1" {
		t.Fatalf("result = %+v, want 3/3 and token DELTA1", res)
	}

	rows, err := st.DB().QueryContext(ctx, `
		SELECT o.start_utc, o.is_all_day, o.summary
		FROM occurrences o JOIN events e ON e.id = o.event_id
		WHERE e.calendar_id = ? ORDER BY o.start_utc`, cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		var startUTC, summary string
		var allDay int
		if err := rows.Scan(&startUTC, &allDay, &summary); err != nil {
			t.Fatal(err)
		}
		n++
		t.Logf("occurrence: %-25s all_day=%d  %q", startUTC, allDay, summary)
	}
	if n != 3 {
		t.Fatalf("dumped %d occurrences, want 3", n)
	}
}
