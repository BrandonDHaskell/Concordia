package normalize

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/provider"
)

const dateLayout = "2006-01-02"

// googleEvent is the subset of a Google Calendar event resource that Concordia
// stores. The JSON is what the provider recorded (calendar/v3's own encoding).
type googleEvent struct {
	ID                string      `json:"id"`
	ICalUID           string      `json:"iCalUID"`
	Etag              string      `json:"etag"`
	Status            string      `json:"status"`
	Summary           string      `json:"summary"`
	Location          string      `json:"location"`
	Start             *googleTime `json:"start"`
	End               *googleTime `json:"end"`
	Recurrence        []string    `json:"recurrence"`
	RecurringEventID  string      `json:"recurringEventId"`
	OriginalStartTime *googleTime `json:"originalStartTime"`
	Updated           string      `json:"updated"`
}

type googleTime struct {
	Date     string `json:"date"`
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

func (gt *googleTime) allDay() bool { return gt != nil && gt.Date != "" }

// Google normalizes one raw Google Calendar event.
func Google(raw provider.RawEvent, cal model.Calendar) (model.Event, error) {
	var ge googleEvent
	if err := json.Unmarshal(raw.Body, &ge); err != nil {
		return model.Event{}, fmt.Errorf("normalize google: decoding %s: %w", raw.RemoteID, err)
	}

	ev := model.Event{
		CalendarID: cal.ID,
		RemoteID:   firstNonEmpty(ge.ID, raw.RemoteID),
		UID:        firstNonEmpty(ge.ICalUID, ge.ID, raw.RemoteID),
		Summary:    ge.Summary,
		Location:   ge.Location,
		Status:     firstNonEmpty(ge.Status, model.StatusConfirmed),
		ETag:       firstNonEmpty(raw.ETag, ge.Etag),
		Raw:        raw.Body,
	}
	if ev.RemoteID == "" {
		return model.Event{}, fmt.Errorf("normalize google: event has no id")
	}
	if ts := parseTimestamp(ge.Updated); !ts.IsZero() {
		ev.UpdatedAt = ts
	}

	if err := applyTimes(&ev, ge); err != nil {
		return model.Event{}, fmt.Errorf("normalize google %s: %w", ev.RemoteID, err)
	}
	applyRecurrence(&ev, ge.Recurrence)

	if ge.RecurringEventID != "" {
		rid, err := recurrenceID(ge.OriginalStartTime)
		if err != nil {
			return model.Event{}, fmt.Errorf("normalize google %s: originalStartTime: %w", ev.RemoteID, err)
		}
		ev.RecurrenceID = rid
		// An override never carries its own RRULE.
		ev.RRULE = ""
		ev.EXDates = nil
	}

	if cal.Redact {
		redact(&ev)
	}
	return ev, nil
}

// applyTimes fills Start, End, AllDay, and TZID. A cancelled instance carries
// no start/end of its own, so it takes them from originalStartTime; only its
// RecurrenceID actually matters downstream.
func applyTimes(ev *model.Event, ge googleEvent) error {
	src := ge.Start
	if src == nil && ev.Status == model.StatusCancelled {
		src = ge.OriginalStartTime
	}
	if src == nil {
		return fmt.Errorf("event has no start")
	}
	ge.Start = src
	if src == ge.OriginalStartTime {
		ge.End = nil // no explicit end; fall through to the default
	}

	if ge.Start.allDay() {
		start, err := time.ParseInLocation(dateLayout, ge.Start.Date, time.UTC)
		if err != nil {
			return fmt.Errorf("start date %q: %w", ge.Start.Date, err)
		}
		end := start.AddDate(0, 0, 1)
		if ge.End != nil && ge.End.Date != "" {
			end, err = time.ParseInLocation(dateLayout, ge.End.Date, time.UTC)
			if err != nil {
				return fmt.Errorf("end date %q: %w", ge.End.Date, err)
			}
		}
		ev.Start, ev.End, ev.AllDay = start, end, true
		return nil
	}

	start, tzid, err := parseWall(ge.Start)
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}
	ev.Start, ev.TZID = start, tzid

	if ge.End != nil && ge.End.DateTime != "" {
		end, _, err := parseWall(ge.End)
		if err != nil {
			return fmt.Errorf("end: %w", err)
		}
		ev.End = end
	} else {
		ev.End = start
	}
	return nil
}

// parseWall parses an RFC3339 dateTime and re-expresses it in the named IANA
// zone so downstream expansion re-derives the offset per instance. When no
// timeZone is given it keeps the literal offset as a fixed zone and returns an
// empty tzid.
func parseWall(gt *googleTime) (time.Time, string, error) {
	t, err := time.Parse(time.RFC3339, gt.DateTime)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("dateTime %q: %w", gt.DateTime, err)
	}
	if gt.TimeZone == "" {
		return t, "", nil
	}
	loc, err := time.LoadLocation(gt.TimeZone)
	if err != nil {
		// Unknown zone: keep the instant, drop the tzid rather than fail.
		return t, "", nil
	}
	return t.In(loc), gt.TimeZone, nil
}

func applyRecurrence(ev *model.Event, lines []string) {
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "RRULE:"):
			if ev.RRULE == "" {
				ev.RRULE = strings.TrimPrefix(line, "RRULE:")
			}
		case strings.HasPrefix(line, "EXDATE"):
			ev.EXDates = append(ev.EXDates, line)
		}
		// RDATE and other lines are not yet handled.
	}
}

// recurrenceID reduces an override's originalStartTime to the canonical form
// stored in Event.RecurrenceID: an RFC3339 UTC instant, or a date for an
// all-day series.
func recurrenceID(gt *googleTime) (string, error) {
	if gt == nil {
		return "", fmt.Errorf("missing")
	}
	if gt.Date != "" {
		return gt.Date, nil
	}
	t, err := time.Parse(time.RFC3339, gt.DateTime)
	if err != nil {
		return "", fmt.Errorf("dateTime %q: %w", gt.DateTime, err)
	}
	return t.UTC().Format(time.RFC3339), nil
}

func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
