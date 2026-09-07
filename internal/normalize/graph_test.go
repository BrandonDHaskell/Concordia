package normalize

import (
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/provider"
)

func graphRaw(body string) provider.RawEvent {
	return provider.RawEvent{RemoteID: "fallback", ETag: `W/"etag"`, Body: []byte(body)}
}

func TestGraphTimedEvent(t *testing.T) {
	ev, err := Graph(graphRaw(`{
		"id": "EV1", "iCalUId": "ical-ev1", "subject": "Team sync",
		"type": "singleInstance", "isAllDay": false, "showAs": "busy",
		"location": {"displayName": "Room 2"},
		"start": {"dateTime": "2026-09-10T16:00:00.0000000", "timeZone": "UTC"},
		"end":   {"dateTime": "2026-09-10T16:30:00.0000000", "timeZone": "UTC"},
		"lastModifiedDateTime": "2026-09-01T00:00:00Z"
	}`), plainCal)
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}

	if ev.RemoteID != "EV1" || ev.UID != "EV1" {
		t.Errorf("identity: remote=%q uid=%q (each Graph occurrence is independent)", ev.RemoteID, ev.UID)
	}
	if ev.AllDay || ev.Recurring() || ev.IsOverride() {
		t.Errorf("flags wrong: %+v", ev)
	}
	if ev.TZID != "UTC" {
		t.Errorf("TZID = %q, want UTC", ev.TZID)
	}
	if !ev.Start.Equal(time.Date(2026, 9, 10, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("Start = %s, want 2026-09-10T16:00:00Z", ev.Start)
	}
	if ev.End.Sub(ev.Start) != 30*time.Minute {
		t.Errorf("duration = %v", ev.End.Sub(ev.Start))
	}
	if ev.Location != "Room 2" || ev.Summary != "Team sync" {
		t.Errorf("summary/location: %q / %q", ev.Summary, ev.Location)
	}
}

func TestGraphAllDayEvent(t *testing.T) {
	ev, err := Graph(graphRaw(`{
		"id": "EV2", "subject": "Conference", "type": "singleInstance", "isAllDay": true,
		"start": {"dateTime": "2026-09-14T00:00:00.0000000", "timeZone": "UTC"},
		"end":   {"dateTime": "2026-09-18T00:00:00.0000000", "timeZone": "UTC"}
	}`), plainCal)
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if !ev.AllDay || ev.TZID != "" {
		t.Errorf("all-day flags: allDay=%v tzid=%q", ev.AllDay, ev.TZID)
	}
	if ev.Start.Format(dateLayout) != "2026-09-14" || ev.End.Format(dateLayout) != "2026-09-18" {
		t.Errorf("dates = %s .. %s (end exclusive)", ev.Start.Format(dateLayout), ev.End.Format(dateLayout))
	}
	if ev.Start.Location() != time.UTC {
		t.Errorf("all-day start not UTC-anchored: %s", ev.Start.Location())
	}
}

func TestGraphOccurrenceIsIndependent(t *testing.T) {
	// Two occurrences of the same series share iCalUId but must get distinct
	// UIDs so the expander does not try to group them.
	a, _ := Graph(graphRaw(`{
		"id": "OCC_A", "iCalUId": "series-x", "subject": "Weekly", "type": "occurrence",
		"start": {"dateTime": "2026-09-07T17:00:00.0000000", "timeZone": "UTC"},
		"end":   {"dateTime": "2026-09-07T17:30:00.0000000", "timeZone": "UTC"}
	}`), plainCal)
	b, _ := Graph(graphRaw(`{
		"id": "OCC_B", "iCalUId": "series-x", "subject": "Weekly", "type": "exception",
		"start": {"dateTime": "2026-09-14T18:00:00.0000000", "timeZone": "UTC"},
		"end":   {"dateTime": "2026-09-14T18:30:00.0000000", "timeZone": "UTC"}
	}`), plainCal)

	if a.UID == b.UID {
		t.Fatalf("occurrences share UID %q", a.UID)
	}
	if a.Recurring() || b.Recurring() {
		t.Error("a Graph occurrence carries an RRULE")
	}
}

func TestGraphCancelledEvent(t *testing.T) {
	ev, err := Graph(graphRaw(`{
		"id": "EV4", "subject": "Offsite", "type": "singleInstance", "isCancelled": true,
		"start": {"dateTime": "2026-09-25T15:00:00.0000000", "timeZone": "UTC"},
		"end":   {"dateTime": "2026-09-25T16:00:00.0000000", "timeZone": "UTC"}
	}`), plainCal)
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if ev.Status != model.StatusCancelled {
		t.Errorf("status = %q", ev.Status)
	}
}

func TestGraphRedaction(t *testing.T) {
	ev, err := Graph(graphRaw(`{
		"id": "S1", "subject": "1:1 with report: comp review", "type": "singleInstance",
		"location": {"displayName": "HR office"},
		"start": {"dateTime": "2026-09-10T16:00:00.0000000", "timeZone": "UTC"},
		"end":   {"dateTime": "2026-09-10T17:00:00.0000000", "timeZone": "UTC"}
	}`), model.Calendar{ID: 9, Redact: true})
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if ev.Summary != RedactedSummary || ev.Location != "" || ev.Raw != nil {
		t.Errorf("not redacted: summary=%q location=%q raw=%v", ev.Summary, ev.Location, ev.Raw != nil)
	}
	if ev.Start.IsZero() {
		t.Error("redaction dropped the time")
	}
}

func TestGraphToleratesRFC3339(t *testing.T) {
	ev, err := Graph(graphRaw(`{
		"id": "EV1", "subject": "x", "type": "singleInstance",
		"start": {"dateTime": "2026-09-10T16:00:00Z", "timeZone": "UTC"},
		"end":   {"dateTime": "2026-09-10T16:30:00Z", "timeZone": "UTC"}
	}`), plainCal)
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if !ev.Start.Equal(time.Date(2026, 9, 10, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("Start = %s", ev.Start)
	}
}

func TestEventDispatchGraph(t *testing.T) {
	ev, err := Event(model.ProviderGraph, graphRaw(`{
		"id": "EV1", "subject": "x", "type": "singleInstance",
		"start": {"dateTime": "2026-09-10T16:00:00.0000000", "timeZone": "UTC"}
	}`), plainCal)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if ev.RemoteID != "EV1" {
		t.Errorf("remote id = %q", ev.RemoteID)
	}
}
