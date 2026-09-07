package davd

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/store"
	"github.com/bhaskell/Concordia/internal/views"
)

const fixedNowRFC = "2026-09-06T12:00:00Z"

func fixedNow() time.Time {
	t, _ := time.Parse(time.RFC3339, fixedNowRFC)
	return t
}

func seededBackend(t *testing.T) (*Backend, *store.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir()+"/c.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	acct, _ := st.UpsertAccount(ctx, model.Account{
		Person: "brandon", Provider: model.ProviderGoogle,
		DisplayName: "Gmail", CredentialRef: "gmail", Enabled: true,
	})
	work, _ := st.UpsertCalendar(ctx, model.Calendar{AccountID: acct.ID, RemoteID: "w", DisplayName: "Work", Enabled: true})
	fam, _ := st.UpsertCalendar(ctx, model.Calendar{AccountID: acct.ID, RemoteID: "f", DisplayName: "Family", Enabled: true})

	mk := func(cal model.Calendar, uid, summary, owner string, dayOffset int, allDay bool, tags ...string) {
		start := fixedNow().AddDate(0, 0, dayOffset)
		end := start.Add(time.Hour)
		if allDay {
			start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
			end = start.AddDate(0, 0, 1)
		}
		err := st.WithTx(ctx, func(tx *sql.Tx) error {
			id, err := st.UpsertEvent(ctx, tx, model.Event{
				CalendarID: cal.ID, RemoteID: uid, UID: uid, Status: model.StatusConfirmed,
				AllDay: allDay, Start: start, End: end,
			})
			if err != nil {
				return err
			}
			return st.ReplaceOccurrences(ctx, tx, id, []model.Occurrence{{
				EventID: id, Owner: owner, Summary: summary, AllDay: allDay,
				Start: start, End: end, Tags: tags,
			}})
		})
		if err != nil {
			t.Fatalf("seed %s: %v", uid, err)
		}
	}

	mk(work, "standup@x", "Standup", "brandon", 2, false, "work")
	mk(fam, "soccer@x", "Soccer", "brandon", 3, false, "kids")
	mk(fam, "trip@x", "Trip", "brandon", 5, true)

	noWork, _ := views.Parse("not tag:work")
	b := New(st, Options{
		People:     []string{"brandon"},
		Views:      []View{{Name: "no-work", Predicate: noWork}},
		WindowBack: 30 * 24 * time.Hour,
		WindowFwd:  60 * 24 * time.Hour,
		Prefix:     "/dav",
		Now:        fixedNow,
	})
	return b, st
}

func newClient(t *testing.T, b *Backend) (*caldav.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(b.Handler())
	t.Cleanup(srv.Close)
	c, err := caldav.NewClient(srv.Client(), srv.URL+"/dav/")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, srv
}

func TestListCalendars(t *testing.T) {
	b, _ := seededBackend(t)
	c, _ := newClient(t, b)

	cals, err := c.FindCalendars(context.Background(), "/dav/principal/cal/")
	if err != nil {
		t.Fatalf("FindCalendars: %v", err)
	}
	got := map[string]bool{}
	for _, cal := range cals {
		got[cal.Path] = true
	}
	if !got["/dav/principal/cal/p-brandon/"] || !got["/dav/principal/cal/v-no-work/"] {
		t.Fatalf("collections = %v", got)
	}
}

func TestQueryPersonCollection(t *testing.T) {
	b, _ := seededBackend(t)
	c, _ := newClient(t, b)

	objs, err := c.QueryCalendar(context.Background(), "/dav/principal/cal/p-brandon/", &caldav.CalendarQuery{
		CompFilter: caldav.CompFilter{Name: ical.CompCalendar, Comps: []caldav.CompFilter{{Name: ical.CompEvent}}},
		CompRequest: caldav.CalendarCompRequest{
			Name: ical.CompCalendar, AllProps: true,
			Comps: []caldav.CalendarCompRequest{{Name: ical.CompEvent, AllProps: true}},
		},
	})
	if err != nil {
		t.Fatalf("QueryCalendar: %v", err)
	}
	if len(objs) != 3 {
		t.Fatalf("brandon has %d objects, want 3", len(objs))
	}
	for _, o := range objs {
		if o.ETag == "" || !strings.HasSuffix(o.Path, ".ics") {
			t.Errorf("bad object: %+v", o)
		}
	}
}

func TestViewCollectionFiltersByPredicate(t *testing.T) {
	b, _ := seededBackend(t)
	c, _ := newClient(t, b)

	objs, err := c.QueryCalendar(context.Background(), "/dav/principal/cal/v-no-work/", &caldav.CalendarQuery{
		CompFilter: caldav.CompFilter{Name: ical.CompCalendar, Comps: []caldav.CompFilter{{Name: ical.CompEvent}}},
		CompRequest: caldav.CalendarCompRequest{
			Name: ical.CompCalendar, AllProps: true,
			Comps: []caldav.CalendarCompRequest{{Name: ical.CompEvent, AllProps: true}},
		},
	})
	if err != nil {
		t.Fatalf("QueryCalendar: %v", err)
	}
	// Standup has tag "work" and is excluded; Soccer and Trip remain.
	summaries := map[string]bool{}
	for _, o := range objs {
		for _, ev := range o.Data.Events() {
			s, _ := ev.Props.Text(ical.PropSummary)
			summaries[s] = true
		}
	}
	if summaries["Standup"] || !summaries["Soccer"] || !summaries["Trip"] {
		t.Fatalf("no-work view summaries = %v", summaries)
	}
}

func TestGetSingleObject(t *testing.T) {
	b, _ := seededBackend(t)
	c, srv := newClient(t, b)

	objs, err := c.QueryCalendar(context.Background(), "/dav/principal/cal/p-brandon/", &caldav.CalendarQuery{
		CompFilter:  caldav.CompFilter{Name: ical.CompCalendar, Comps: []caldav.CompFilter{{Name: ical.CompEvent}}},
		CompRequest: caldav.CalendarCompRequest{Name: ical.CompCalendar, AllProps: true},
	})
	if err != nil || len(objs) == 0 {
		t.Fatalf("query: %v (%d objs)", err, len(objs))
	}

	one, err := c.GetCalendarObject(context.Background(), objs[0].Path)
	if err != nil {
		t.Fatalf("GetCalendarObject: %v", err)
	}
	if one.ETag != objs[0].ETag {
		t.Errorf("etag mismatch: %q vs %q", one.ETag, objs[0].ETag)
	}

	// A missing object is 404.
	resp, err := srv.Client().Get(srv.URL + "/dav/principal/cal/p-brandon/nope-123.ics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing object status = %d, want 404", resp.StatusCode)
	}
}

func TestWritesRefused(t *testing.T) {
	b, _ := seededBackend(t)
	_, srv := newClient(t, b)

	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:x\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		req, _ := http.NewRequest(method, srv.URL+"/dav/principal/cal/p-brandon/x.ics", strings.NewReader(body))
		req.Header.Set("Content-Type", ical.MIMEType)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403", method, resp.StatusCode)
		}
	}
}
