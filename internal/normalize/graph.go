package normalize

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/provider"
)

// graphDateTimeLayout is Graph's dateTime format: no zone suffix, seven
// fractional digits. With Prefer: outlook.timezone="UTC" the value is UTC.
const graphDateTimeLayout = "2006-01-02T15:04:05.999999999"

type graphEvent struct {
	ID       string `json:"id"`
	ICalUID  string `json:"iCalUId"`
	Subject  string `json:"subject"`
	Location *struct {
		DisplayName string `json:"displayName"`
	} `json:"location"`
	Categories   []string   `json:"categories"`
	Start        *graphTime `json:"start"`
	End          *graphTime `json:"end"`
	IsAllDay     bool       `json:"isAllDay"`
	IsCancelled  bool       `json:"isCancelled"`
	Type         string     `json:"type"`
	ShowAs       string     `json:"showAs"`
	LastModified string     `json:"lastModifiedDateTime"`
}

type graphTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

// Graph normalizes one raw Microsoft Graph calendar event. calendarView has
// already expanded recurrences, so every event here is a single occurrence and
// carries no RRULE; each is stored under its own id with no series grouping.
func Graph(raw provider.RawEvent, cal model.Calendar) (model.Event, error) {
	var ge graphEvent
	if err := json.Unmarshal(raw.Body, &ge); err != nil {
		return model.Event{}, fmt.Errorf("normalize graph: decoding %s: %w", raw.RemoteID, err)
	}

	id := firstNonEmpty(ge.ID, raw.RemoteID)
	if id == "" {
		return model.Event{}, fmt.Errorf("normalize graph: event has no id")
	}

	ev := model.Event{
		CalendarID: cal.ID,
		RemoteID:   id,
		UID:        id,
		Summary:    ge.Subject,
		Status:     graphStatus(ge),
		ETag:       raw.ETag,
		Raw:        raw.Body,
	}
	if ge.Location != nil {
		ev.Location = ge.Location.DisplayName
	}
	if ts := parseTimestamp(ge.LastModified); !ts.IsZero() {
		ev.UpdatedAt = ts
	}
	for _, c := range ge.Categories {
		ev.SourceTags = addTag(ev.SourceTags, c)
	}

	if err := applyGraphTimes(&ev, ge); err != nil {
		return model.Event{}, fmt.Errorf("normalize graph %s: %w", id, err)
	}

	if cal.Redact {
		redact(&ev)
	}
	return ev, nil
}

func graphStatus(ge graphEvent) string {
	if ge.IsCancelled {
		return model.StatusCancelled
	}
	if strings.EqualFold(ge.ShowAs, "tentative") {
		return model.StatusTentative
	}
	return model.StatusConfirmed
}

func applyGraphTimes(ev *model.Event, ge graphEvent) error {
	if ge.Start == nil || ge.Start.DateTime == "" {
		if ev.Status == model.StatusCancelled {
			return nil // a cancellation with no time is still usable
		}
		return fmt.Errorf("event has no start")
	}

	if ge.IsAllDay {
		start, err := parseGraphDate(ge.Start.DateTime)
		if err != nil {
			return fmt.Errorf("start %q: %w", ge.Start.DateTime, err)
		}
		end := start.AddDate(0, 0, 1)
		if ge.End != nil && ge.End.DateTime != "" {
			end, err = parseGraphDate(ge.End.DateTime)
			if err != nil {
				return fmt.Errorf("end %q: %w", ge.End.DateTime, err)
			}
		}
		ev.Start, ev.End, ev.AllDay = start, end, true
		return nil
	}

	start, err := parseGraphInstant(ge.Start)
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}
	ev.Start = start
	ev.TZID = "UTC"

	if ge.End != nil && ge.End.DateTime != "" {
		end, err := parseGraphInstant(ge.End)
		if err != nil {
			return fmt.Errorf("end: %w", err)
		}
		ev.End = end
	} else {
		ev.End = start
	}
	return nil
}

// parseGraphInstant returns the UTC instant for a timed value. Graph gives us
// UTC because of the Prefer header; anything else is treated as UTC too, since
// that is what was requested.
func parseGraphInstant(gt *graphTime) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, gt.DateTime); err == nil {
		return t.UTC(), nil
	}
	t, err := time.ParseInLocation(graphDateTimeLayout, gt.DateTime, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("dateTime %q: %w", gt.DateTime, err)
	}
	return t, nil
}

func parseGraphDate(s string) (time.Time, error) {
	if i := strings.IndexByte(s, 'T'); i > 0 {
		s = s[:i]
	}
	return time.ParseInLocation(dateLayout, s, time.UTC)
}
