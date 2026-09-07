package httpd

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/views"
)

func webServer(t *testing.T) *Server {
	t.Helper()
	st := testStore(t)

	ctx := t.Context()
	acct, _ := st.UpsertAccount(ctx, model.Account{
		Person: "brandon", Provider: model.ProviderGoogle,
		DisplayName: "G", CredentialRef: "g", Enabled: true,
	})
	other, _ := st.UpsertAccount(ctx, model.Account{
		Person: "kim", Provider: model.ProviderGoogle,
		DisplayName: "K", CredentialRef: "k", Enabled: true,
	})
	work, _ := st.UpsertCalendar(ctx, model.Calendar{AccountID: acct.ID, RemoteID: "w", DisplayName: "Work", Enabled: true})
	fam, _ := st.UpsertCalendar(ctx, model.Calendar{AccountID: other.ID, RemoteID: "f", DisplayName: "Family", Enabled: true})

	mk := func(cal model.Calendar, uid, summary, owner, status string, dayOffset, hour int, allDay bool, tags ...string) {
		base := fixedNow().In(time.UTC)
		start := time.Date(base.Year(), base.Month(), base.Day()+dayOffset, hour, 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		if allDay {
			start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
			end = start.AddDate(0, 0, 1)
		}
		err := st.WithTx(ctx, func(tx *sql.Tx) error {
			id, e := st.UpsertEvent(ctx, tx, model.Event{
				CalendarID: cal.ID, RemoteID: uid, UID: uid, Status: status,
				AllDay: allDay, Start: start, End: end,
			})
			if e != nil {
				return e
			}
			return st.ReplaceOccurrences(ctx, tx, id, []model.Occurrence{{
				EventID: id, Owner: owner, Summary: summary, AllDay: allDay,
				Start: start, End: end, Tags: tags,
			}})
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	mk(work, "e1", "Standup", "brandon", model.StatusConfirmed, 1, 9, false, "work")
	mk(fam, "e2", "Soccer", "kim", model.StatusTentative, 1, 17, false, "kids")
	mk(fam, "e3", "Trip", "brandon", model.StatusConfirmed, 3, 0, true)

	noWork, _ := views.Parse("not tag:work")
	return New(Config{
		Addr:  "127.0.0.1:0",
		Store: st,
		Feeds: []Feed{
			{Slug: "all", Name: "Household"},
			{Slug: "p-brandon", Name: "brandon", Owner: "brandon"},
			{Slug: "p-kim", Name: "kim", Owner: "kim"},
			{Slug: "v-no-work", Name: "no-work", Predicate: noWork},
		},
		DisplayTZ: time.UTC,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:       fixedNow,
	})
}

func TestIndexRendersAgenda(t *testing.T) {
	s := webServer(t)
	rec := do(t, s, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<!doctype html>", `id="view"`, "app.css", "htmx.min.js",
		"Standup", "Soccer", "Trip",
		`class="chip`, // owner/view/tag chips
		"Tomorrow",    // day grouping label
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
	if !strings.Contains(body, "all day") {
		t.Errorf("all-day event not labelled:\n%s", body)
	}
	if !strings.Contains(body, "tentative") {
		t.Errorf("tentative status not rendered")
	}
}

func TestViewFragmentIsNotFullPage(t *testing.T) {
	s := webServer(t)
	body := do(t, s, http.MethodGet, "/view", nil).Body.String()
	if strings.Contains(body, "<!doctype html>") {
		t.Errorf("/view returned a full page:\n%s", body[:120])
	}
	if !strings.Contains(body, "<header>") || !strings.Contains(body, "Standup") {
		t.Errorf("/view fragment incomplete")
	}
}

func TestFilterByOwner(t *testing.T) {
	s := webServer(t)
	body := do(t, s, http.MethodGet, "/view?owner=kim", nil).Body.String()
	if strings.Contains(body, "Standup") || !strings.Contains(body, "Soccer") {
		t.Errorf("owner=kim filter wrong:\n%s", body)
	}
	// The kim chip is active.
	if !strings.Contains(body, `class="chip active owner-`) {
		t.Errorf("active owner chip not marked:\n%s", body)
	}
}

func TestFilterByViewAndTag(t *testing.T) {
	s := webServer(t)

	view := do(t, s, http.MethodGet, "/view?view=no-work", nil).Body.String()
	if strings.Contains(view, "Standup") || !strings.Contains(view, "Soccer") {
		t.Errorf("view=no-work wrong:\n%s", view)
	}

	tag := do(t, s, http.MethodGet, "/view?tag=work", nil).Body.String()
	if !strings.Contains(tag, "Standup") || strings.Contains(tag, "Soccer") {
		t.Errorf("tag=work wrong:\n%s", tag)
	}
}

func TestStaticAssets(t *testing.T) {
	s := webServer(t)
	rec := do(t, s, http.MethodGet, "/static/app.css", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("app.css status = %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "max-age") {
		t.Errorf("no cache header on static asset")
	}
	if js := do(t, s, http.MethodGet, "/static/htmx.min.js", nil); js.Code != http.StatusOK {
		t.Errorf("htmx.min.js status = %d", js.Code)
	}
}

func TestEmptyWindow(t *testing.T) {
	s := webServer(t)
	body := do(t, s, http.MethodGet, "/view?days=1", nil).Body.String()
	// Nothing is scheduled for today (events start tomorrow).
	if !strings.Contains(body, "Nothing scheduled") {
		t.Errorf("empty window should show the empty state:\n%s", body)
	}
}
