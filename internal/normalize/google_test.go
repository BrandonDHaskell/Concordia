package normalize

import (
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/provider"
)

func raw(body string) provider.RawEvent {
	return provider.RawEvent{RemoteID: "fallback-id", ETag: `"etag"`, Body: []byte(body)}
}

var plainCal = model.Calendar{ID: 7, Redact: false}

func TestGoogleTimedEvent(t *testing.T) {
	ev, err := Google(raw(`{
		"id": "ev1", "iCalUID": "ev1@google.com", "status": "confirmed",
		"summary": "Standup", "location": "Room 2",
		"start": {"dateTime": "2026-09-10T09:00:00-07:00", "timeZone": "America/Los_Angeles"},
		"end":   {"dateTime": "2026-09-10T09:15:00-07:00", "timeZone": "America/Los_Angeles"}
	}`), plainCal)
	if err != nil {
		t.Fatalf("Google: %v", err)
	}

	if ev.CalendarID != 7 || ev.RemoteID != "ev1" || ev.UID != "ev1@google.com" {
		t.Errorf("identity wrong: %+v", ev)
	}
	if ev.AllDay {
		t.Error("timed event marked all-day")
	}
	if ev.TZID != "America/Los_Angeles" {
		t.Errorf("TZID = %q", ev.TZID)
	}
	if ev.Start.Location().String() != "America/Los_Angeles" {
		t.Errorf("Start location = %q, want the IANA zone", ev.Start.Location())
	}
	h, m, _ := ev.Start.Clock()
	if h != 9 || m != 0 {
		t.Errorf("Start wall clock = %02d:%02d, want 09:00", h, m)
	}
	if ev.End.Sub(ev.Start) != 15*time.Minute {
		t.Errorf("duration = %v", ev.End.Sub(ev.Start))
	}
}

func TestGoogleTimedEventNoTimeZone(t *testing.T) {
	ev, err := Google(raw(`{
		"id": "ev1", "status": "confirmed",
		"start": {"dateTime": "2026-09-10T09:00:00-07:00"},
		"end":   {"dateTime": "2026-09-10T10:00:00-07:00"}
	}`), plainCal)
	if err != nil {
		t.Fatalf("Google: %v", err)
	}
	if ev.TZID != "" {
		t.Errorf("TZID = %q, want empty when no timeZone given", ev.TZID)
	}
	if _, off := ev.Start.Zone(); off != -7*3600 {
		t.Errorf("offset = %d, want -25200 (kept from the literal)", off)
	}
	if ev.UID != "ev1" {
		t.Errorf("UID = %q, want the id as fallback", ev.UID)
	}
}

func TestGoogleAllDayMultiDay(t *testing.T) {
	ev, err := Google(raw(`{
		"id": "trip", "status": "confirmed", "summary": "Trip",
		"start": {"date": "2026-09-14"},
		"end":   {"date": "2026-09-18"}
	}`), plainCal)
	if err != nil {
		t.Fatalf("Google: %v", err)
	}
	if !ev.AllDay || ev.TZID != "" {
		t.Errorf("all-day flags wrong: allDay=%v tzid=%q", ev.AllDay, ev.TZID)
	}
	if ev.Start.Format(dateLayout) != "2026-09-14" || ev.End.Format(dateLayout) != "2026-09-18" {
		t.Errorf("dates = %s .. %s (end is exclusive)", ev.Start.Format(dateLayout), ev.End.Format(dateLayout))
	}
	if ev.Start.Location() != time.UTC {
		t.Errorf("all-day Start not anchored to UTC midnight: %v", ev.Start.Location())
	}
}

func TestGoogleAllDaySingleDay(t *testing.T) {
	ev, err := Google(raw(`{"id":"h","status":"confirmed","start":{"date":"2026-12-25"}}`), plainCal)
	if err != nil {
		t.Fatalf("Google: %v", err)
	}
	if ev.End.Format(dateLayout) != "2026-12-26" {
		t.Errorf("single all-day end = %s, want next day (exclusive)", ev.End.Format(dateLayout))
	}
}

func TestGoogleRecurringMaster(t *testing.T) {
	ev, err := Google(raw(`{
		"id": "rec1", "iCalUID": "rec1@google.com", "status": "confirmed",
		"summary": "Weekly sync",
		"start": {"dateTime": "2026-09-07T10:00:00-07:00", "timeZone": "America/Los_Angeles"},
		"end":   {"dateTime": "2026-09-07T10:30:00-07:00", "timeZone": "America/Los_Angeles"},
		"recurrence": ["RRULE:FREQ=WEEKLY;BYDAY=MO", "EXDATE;TZID=America/Los_Angeles:20260914T100000"]
	}`), plainCal)
	if err != nil {
		t.Fatalf("Google: %v", err)
	}
	if !ev.Recurring() || ev.RRULE != "FREQ=WEEKLY;BYDAY=MO" {
		t.Errorf("RRULE = %q", ev.RRULE)
	}
	if len(ev.EXDates) != 1 || ev.EXDates[0] != "EXDATE;TZID=America/Los_Angeles:20260914T100000" {
		t.Errorf("EXDates = %v", ev.EXDates)
	}
	if ev.IsOverride() {
		t.Error("master marked as override")
	}
}

func TestGoogleOverride(t *testing.T) {
	ev, err := Google(raw(`{
		"id": "rec1_20260921T170000Z", "iCalUID": "rec1@google.com", "status": "confirmed",
		"summary": "Weekly sync (moved)",
		"recurringEventId": "rec1",
		"originalStartTime": {"dateTime": "2026-09-21T10:00:00-07:00", "timeZone": "America/Los_Angeles"},
		"start": {"dateTime": "2026-09-21T11:00:00-07:00", "timeZone": "America/Los_Angeles"},
		"end":   {"dateTime": "2026-09-21T11:30:00-07:00", "timeZone": "America/Los_Angeles"},
		"recurrence": ["RRULE:FREQ=DAILY"]
	}`), plainCal)
	if err != nil {
		t.Fatalf("Google: %v", err)
	}
	if !ev.IsOverride() || ev.RecurrenceID != "2026-09-21T17:00:00Z" {
		t.Errorf("RecurrenceID = %q, want the UTC instant of the original start", ev.RecurrenceID)
	}
	if ev.Recurring() {
		t.Errorf("override kept an RRULE: %q", ev.RRULE)
	}
	if ev.UID != "rec1@google.com" {
		t.Errorf("UID = %q, want the shared iCalUID", ev.UID)
	}
}

func TestGoogleCancelledInstance(t *testing.T) {
	ev, err := Google(raw(`{
		"id": "rec1_20260928T170000Z", "iCalUID": "rec1@google.com", "status": "cancelled",
		"recurringEventId": "rec1",
		"originalStartTime": {"dateTime": "2026-09-28T10:00:00-07:00", "timeZone": "America/Los_Angeles"}
	}`), plainCal)
	if err != nil {
		t.Fatalf("Google: %v", err)
	}
	if ev.Status != model.StatusCancelled {
		t.Errorf("Status = %q", ev.Status)
	}
	if ev.RecurrenceID != "2026-09-28T17:00:00Z" {
		t.Errorf("RecurrenceID = %q", ev.RecurrenceID)
	}
	// Start is taken from originalStartTime so storage has something coherent;
	// expansion ignores it and suppresses the instance by RecurrenceID.
	if ev.Start.IsZero() {
		t.Error("cancelled instance should borrow Start from originalStartTime")
	}
	if h, _, _ := ev.Start.UTC().Clock(); h != 17 {
		t.Errorf("borrowed Start = %s, want 17:00Z", ev.Start.UTC())
	}
}

func TestGoogleColorIdBecomesTag(t *testing.T) {
	ev, err := Google(raw(`{
		"id": "e", "status": "confirmed", "summary": "Soccer", "colorId": "11",
		"start": {"date": "2026-09-10"}
	}`), plainCal)
	if err != nil {
		t.Fatalf("Google: %v", err)
	}
	if len(ev.SourceTags) != 1 || ev.SourceTags[0] != "tomato" {
		t.Errorf("SourceTags = %v, want [tomato]", ev.SourceTags)
	}
}

func TestGoogleNoColorIdNoTag(t *testing.T) {
	ev, _ := Google(raw(`{"id":"e","status":"confirmed","start":{"date":"2026-09-10"}}`), plainCal)
	if len(ev.SourceTags) != 0 {
		t.Errorf("SourceTags = %v, want none", ev.SourceTags)
	}
}

func TestGoogleColorTagSurvivesRedaction(t *testing.T) {
	ev, _ := Google(raw(`{
		"id": "e", "status": "confirmed", "summary": "Therapy", "colorId": "3",
		"start": {"dateTime": "2026-09-10T09:00:00-07:00", "timeZone": "America/Los_Angeles"},
		"end":   {"dateTime": "2026-09-10T10:00:00-07:00", "timeZone": "America/Los_Angeles"}
	}`), model.Calendar{ID: 1, Redact: true})
	if ev.Summary != RedactedSummary {
		t.Fatal("not redacted")
	}
	if len(ev.SourceTags) != 1 || ev.SourceTags[0] != "grape" {
		t.Errorf("color tag dropped by redaction: %v", ev.SourceTags)
	}
}

func TestGoogleRedaction(t *testing.T) {
	body := `{
		"id": "secret", "iCalUID": "s@google.com", "status": "confirmed",
		"summary": "Board meeting: layoffs", "location": "Exec suite",
		"start": {"dateTime": "2026-09-10T09:00:00-07:00", "timeZone": "America/Los_Angeles"},
		"end":   {"dateTime": "2026-09-10T10:00:00-07:00", "timeZone": "America/Los_Angeles"}
	}`
	ev, err := Google(raw(body), model.Calendar{ID: 3, Redact: true})
	if err != nil {
		t.Fatalf("Google: %v", err)
	}
	if ev.Summary != RedactedSummary || ev.Location != "" {
		t.Errorf("not redacted: summary=%q location=%q", ev.Summary, ev.Location)
	}
	if ev.Raw != nil {
		t.Error("raw payload retained for a redacted calendar")
	}
	// Time survives so it can still show as a busy block.
	if ev.Start.IsZero() {
		t.Error("redaction dropped the time")
	}
}

func TestGoogleNoID(t *testing.T) {
	_, err := Google(provider.RawEvent{Body: []byte(`{"status":"confirmed","start":{"date":"2026-01-01"}}`)}, plainCal)
	if err == nil {
		t.Fatal("expected an error for an event with no id")
	}
}

func TestEventDispatch(t *testing.T) {
	_, err := Event("microsoft", raw(`{}`), plainCal)
	if err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
	if _, err := Event(model.ProviderGoogle, raw(`{"id":"x","status":"confirmed","start":{"date":"2026-01-01"}}`), plainCal); err != nil {
		t.Fatalf("google dispatch: %v", err)
	}
}
