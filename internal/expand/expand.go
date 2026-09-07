// Package expand materializes events into occurrence rows over a rolling
// window. It resolves RRULE, EXDATE, and RECURRENCE-ID overrides. It is the
// only writer of occurrence data, and its output is a pure function of its
// input: the occurrences table can be dropped and rebuilt at any time.
package expand

import (
	"fmt"
	"strings"
	"time"

	"github.com/teambition/rrule-go"

	"github.com/bhaskell/Concordia/internal/model"
)

// maxInstances caps a single series expansion. A window of ~460 days cannot
// legitimately produce this many instances even for an hourly rule; hitting it
// means a malformed or pathological RRULE.
const maxInstances = 20000

const icalDate = "20060102"

// Expand materializes every event in events into occurrences within w, owned by
// owner. Events are grouped by UID so a recurring master and its overrides
// resolve together. Tombstoned events are skipped.
func Expand(events []model.Event, w model.Window, owner string) ([]model.Occurrence, error) {
	byUID := make(map[string][]model.Event)
	order := make([]string, 0)
	for _, e := range events {
		if e.Deleted() {
			continue
		}
		if _, seen := byUID[e.UID]; !seen {
			order = append(order, e.UID)
		}
		byUID[e.UID] = append(byUID[e.UID], e)
	}

	var out []model.Occurrence
	for _, uid := range order {
		occs, err := expandGroup(byUID[uid], w, owner)
		if err != nil {
			return nil, err
		}
		out = append(out, occs...)
	}
	return out, nil
}

func expandGroup(group []model.Event, w model.Window, owner string) ([]model.Occurrence, error) {
	var master *model.Event
	var overrides []model.Event
	for i := range group {
		if group[i].IsOverride() {
			overrides = append(overrides, group[i])
			continue
		}
		m := group[i]
		master = &m
	}

	switch {
	case master == nil:
		// Orphan overrides (master not in this batch): treat each as a
		// standalone event.
		var out []model.Occurrence
		for _, o := range overrides {
			if o.Status == model.StatusCancelled {
				continue
			}
			out = append(out, single(o, w, owner)...)
		}
		return out, nil

	case master.Recurring():
		return expandSeries(*master, overrides, w, owner)

	default:
		out := single(*master, w, owner)
		for _, o := range overrides {
			if o.Status == model.StatusCancelled {
				continue
			}
			out = append(out, single(o, w, owner)...)
		}
		return out, nil
	}
}

func expandSeries(master model.Event, overrides []model.Event, w model.Window, owner string) ([]model.Occurrence, error) {
	dur := master.End.Sub(master.Start)
	if dur < 0 {
		dur = 0
	}

	opt, err := rrule.StrToROption(strings.TrimPrefix(master.RRULE, "RRULE:"))
	if err != nil {
		return nil, fmt.Errorf("expand %s: parsing RRULE %q: %w", master.UID, master.RRULE, err)
	}
	opt.Dtstart = master.Start
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("expand %s: building RRULE: %w", master.UID, err)
	}

	set := &rrule.Set{}
	set.DTStart(master.Start)
	set.RRule(r)
	for _, line := range master.EXDates {
		exs, err := parseEXDATE(line, master.Start.Location())
		if err != nil {
			return nil, fmt.Errorf("expand %s: EXDATE %q: %w", master.UID, line, err)
		}
		for _, ex := range exs {
			set.ExDate(ex)
		}
	}

	replaced := make(map[string]bool, len(overrides))
	for _, o := range overrides {
		replaced[o.RecurrenceID] = true
	}

	// Start the scan one duration early so a long instance that began before
	// the window but overlaps its start is still produced.
	instants := set.Between(w.Start.Add(-dur), w.End, true)
	if len(instants) > maxInstances {
		return nil, fmt.Errorf("expand %s: %d instances exceeds cap %d", master.UID, len(instants), maxInstances)
	}

	var out []model.Occurrence
	for _, t := range instants {
		if replaced[canonicalRecurrenceID(t, master.AllDay)] {
			continue
		}
		start, end := t, t.Add(dur)
		if !w.Overlaps(start, end) {
			continue
		}
		out = append(out, occurrenceOf(master, start, end, owner))
	}

	for _, o := range overrides {
		if o.Status == model.StatusCancelled {
			continue
		}
		out = append(out, single(o, w, owner)...)
	}
	return out, nil
}

func single(ev model.Event, w model.Window, owner string) []model.Occurrence {
	start, end := ev.Start, ev.End
	if end.Before(start) {
		end = start
	}
	if !w.Overlaps(start, end) {
		return nil
	}
	return []model.Occurrence{occurrenceOf(ev, start, end, owner)}
}

func occurrenceOf(ev model.Event, start, end time.Time, owner string) model.Occurrence {
	return model.Occurrence{
		EventID:  ev.ID,
		Start:    start,
		End:      end,
		AllDay:   ev.AllDay,
		Summary:  ev.Summary,
		Location: ev.Location,
		Owner:    owner,
	}
}

// canonicalRecurrenceID renders an instant the way normalize stores an
// override's RecurrenceID, so the two can be compared.
func canonicalRecurrenceID(t time.Time, allDay bool) string {
	if allDay {
		return t.UTC().Format("2006-01-02")
	}
	return t.UTC().Format(time.RFC3339)
}

// parseEXDATE parses one EXDATE content line into instants. It honors a TZID
// parameter that may differ from the series zone, falling back to defaultLoc.
func parseEXDATE(line string, defaultLoc *time.Location) ([]time.Time, error) {
	rest := strings.TrimPrefix(line, "EXDATE")
	loc := defaultLoc
	if loc == nil {
		loc = time.UTC
	}

	if strings.HasPrefix(rest, ";") {
		colon := strings.IndexByte(rest, ':')
		if colon < 0 {
			return nil, fmt.Errorf("no value")
		}
		params := strings.Split(rest[1:colon], ";")
		rest = rest[colon:]
		for _, p := range params {
			if !strings.HasPrefix(strings.ToUpper(p), "TZID=") {
				continue
			}
			name := p[len("TZID="):]
			l, err := time.LoadLocation(name)
			if err != nil {
				return nil, fmt.Errorf("unknown TZID %q", name)
			}
			loc = l
		}
	}
	rest = strings.TrimPrefix(rest, ":")
	if rest == "" {
		return nil, fmt.Errorf("no value")
	}

	var out []time.Time
	for _, v := range strings.Split(rest, ",") {
		t, err := parseICalTime(strings.TrimSpace(v), loc)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func parseICalTime(v string, loc *time.Location) (time.Time, error) {
	switch {
	case strings.HasSuffix(v, "Z"):
		return time.ParseInLocation("20060102T150405Z", v, time.UTC)
	case strings.Contains(v, "T"):
		return time.ParseInLocation("20060102T150405", v, loc)
	default:
		return time.ParseInLocation(icalDate, v, loc)
	}
}
