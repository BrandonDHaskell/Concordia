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

	googleprov "github.com/bhaskell/Concordia/internal/provider/google"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/normalize"
	"github.com/bhaskell/Concordia/internal/store"
	"github.com/bhaskell/Concordia/internal/syncer"
)

const fixtures = "../../testdata/fixtures/google"

// TestEndToEndGoogleSync runs the whole ingest chain with the real Google
// provider (its actual HTTP client, pointed at a local fixture server) feeding
// normalize, expand, and the store.
func TestEndToEndGoogleSync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		switch {
		case strings.Contains(r.URL.Path, "/events"):
			name := "events_full_page1.json"
			if r.URL.Query().Get("pageToken") == "PAGE2" {
				name = "events_full_page2.json"
			}
			b, err := os.ReadFile(filepath.Join(fixtures, name))
			if err != nil {
				t.Fatal(err)
			}
			w.Write(b)
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "concordia.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	acct, err := st.UpsertAccount(ctx, model.Account{
		Person: "brandon", Provider: model.ProviderGoogle,
		DisplayName: "Gmail", CredentialRef: "gmail", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cal, err := st.UpsertCalendar(ctx, model.Calendar{
		AccountID: acct.ID, RemoteID: "brandon@example.com",
		DisplayName: "Primary", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	prov, err := googleprov.New(ctx, srv.Client(),
		60*24*time.Hour, 400*24*time.Hour,
		googleprov.WithEndpoint(srv.URL),
		googleprov.WithClock(func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }),
		googleprov.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	if err != nil {
		t.Fatal(err)
	}

	sy := &syncer.Syncer{
		Store: st,
		Window: model.Window{
			Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	res, err := sy.Calendar(ctx, prov, normalize.Event, cal, model.ProviderGoogle, acct.Person)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if res.Changed != 3 || res.Occurrences != 3 || res.NextToken != "SYNC1" {
		t.Fatalf("result = %+v, want 3 changed / 3 occurrences / token SYNC1", res)
	}

	// Dump what landed, for a visible record of the run.
	rows, err := st.DB().QueryContext(ctx, `
		SELECT o.start_local, o.is_all_day, o.summary, o.owner
		FROM occurrences o JOIN events e ON e.id = o.event_id
		WHERE e.calendar_id = ? ORDER BY o.start_utc`, cal.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var n int
	for rows.Next() {
		var startLocal, summary, owner string
		var allDay int
		if err := rows.Scan(&startLocal, &allDay, &summary, &owner); err != nil {
			t.Fatal(err)
		}
		n++
		t.Logf("occurrence: %-25s all_day=%d  %-8s  %q", startLocal, allDay, owner, summary)
	}
	if n != 3 {
		t.Fatalf("dumped %d occurrences, want 3", n)
	}

	cals, _ := st.Calendars(ctx, acct.ID)
	t.Logf("calendar sync_token=%q last_sync_at=%s", cals[0].SyncToken, cals[0].LastSyncAt.Format(time.RFC3339))
}
