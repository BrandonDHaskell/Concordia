package httpd

import (
	"bufio"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/store"
)

func TestHubBroadcastAndDrop(t *testing.T) {
	h := newHub()
	a := h.subscribe()
	b := h.subscribe()
	if h.count() != 2 {
		t.Fatalf("count = %d", h.count())
	}

	h.broadcast("v1")
	if got := <-a; got != "v1" {
		t.Errorf("a got %q", got)
	}
	<-b // drain

	// b's buffer is full (cap 1) and unread; a second broadcast is dropped for
	// it, never blocks.
	h.broadcast("v2")
	h.broadcast("v3")
	if got := <-b; got != "v2" {
		t.Errorf("b got %q, want the first buffered message", got)
	}

	h.unsubscribe(a)
	h.unsubscribe(b)
	if h.count() != 0 {
		t.Errorf("count after unsubscribe = %d", h.count())
	}
}

func TestEventsEndpointStreamsChanges(t *testing.T) {
	s := webServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	// Wait for the subscription to register, then broadcast.
	waitFor(t, func() bool { return s.hub.count() == 1 })
	s.hub.broadcast("42")

	sc := bufio.NewScanner(resp.Body)
	var sawEvent, sawData bool
	deadline := time.After(2 * time.Second)
	for !sawData {
		select {
		case <-deadline:
			t.Fatal("no SSE change received")
		default:
		}
		if !sc.Scan() {
			t.Fatalf("stream ended: %v", sc.Err())
		}
		line := sc.Text()
		if line == "event: change" {
			sawEvent = true
		}
		if line == "data: 42" {
			sawData = true
		}
	}
	if !sawEvent {
		t.Error("missing 'event: change' line")
	}
}

func TestWatchBroadcastsOnExternalWrite(t *testing.T) {
	s := webServer(t)
	s.cfg.WatchInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watch(ctx)

	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

	// A separate handle to the same file writes, as sync-once would.
	w, err := store.Open(ctx, s.cfg.Store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	acct, _ := w.UpsertAccount(ctx, model.Account{
		Person: "z", Provider: model.ProviderGoogle,
		DisplayName: "Z", CredentialRef: "z", Enabled: true,
	})
	cal, _ := w.UpsertCalendar(ctx, model.Calendar{AccountID: acct.ID, RemoteID: "z", DisplayName: "Z", Enabled: true})
	_ = w.WithTx(ctx, func(tx *sql.Tx) error {
		_, e := w.UpsertEvent(ctx, tx, model.Event{
			CalendarID: cal.ID, RemoteID: "z", UID: "z", Status: model.StatusConfirmed,
			Start: time.Now(), End: time.Now().Add(time.Hour),
		})
		return e
	})

	select {
	case <-ch:
		// watch saw the external change and broadcast
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not broadcast after an external write")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}
