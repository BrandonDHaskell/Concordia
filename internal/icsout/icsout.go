// Package icsout serializes materialized occurrences into iCalendar. It is a
// leaf: it depends only on model semantics and go-ical, never on the store or
// the providers.
//
// Timed events are emitted in UTC (DTSTART:…Z); all-day events as VALUE=DATE.
// No VTIMEZONE is written. Ownership is marked three ways per the design:
// CATEGORIES (owner plus tags), an optional "[owner] " summary prefix, and an
// X-HOUSEHOLD-OWNER property.
package icsout

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bhaskell/Concordia/internal/model"
)

// OccurrenceUID is the stable identity of one materialized occurrence: its
// event's UID plus the instance start. It is used both as the VEVENT UID and,
// with a ".ics" suffix, as the CalDAV resource name.
func OccurrenceUID(eventUID string, start time.Time) string {
	return eventUID + "-" + strconv.FormatInt(start.UTC().Unix(), 10)
}

const (
	prodID          = "-//Concordia//household calendar aggregator//EN"
	propOwner       = "X-HOUSEHOLD-OWNER"
	propCalendarSrc = "X-CONCORDIA-CALENDAR"
)

// Occurrence is the input to serialization: one materialized instance plus the
// display context icsout needs.
type Occurrence struct {
	UID          string
	Owner        string
	CalendarName string
	Summary      string
	Location     string
	Start        time.Time
	End          time.Time
	AllDay       bool
	Status       string
	Tags         []string
	LastModified time.Time
}

// Options tune a serialized calendar.
type Options struct {
	// Name is the X-WR-CALNAME shown by some clients. Optional.
	Name string
	// SummaryPrefix prepends "[owner] " to every SUMMARY.
	SummaryPrefix bool
	// Now is the DTSTAMP for events that have no LastModified. Defaults to
	// time.Now when zero.
	Now time.Time
}

// Calendar builds a VCALENDAR containing one VEVENT per occurrence, ordered by
// start time.
func Calendar(occs []Occurrence, opts Options) *ical.Calendar {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropProductID, prodID)
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropCalendarScale, "GREGORIAN")
	if opts.Name != "" {
		cal.Props.SetText("X-WR-CALNAME", opts.Name)
	}

	sorted := append([]Occurrence(nil), occs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })

	for _, o := range sorted {
		cal.Children = append(cal.Children, event(o, opts, now).Component)
	}
	return cal
}

func event(o Occurrence, opts Options, now time.Time) *ical.Event {
	ev := ical.NewEvent()
	p := ev.Props

	p.SetText(ical.PropUID, o.UID)

	stamp := o.LastModified
	if stamp.IsZero() {
		stamp = now
	}
	p.SetDateTime(ical.PropDateTimeStamp, stamp.UTC())
	if !o.LastModified.IsZero() {
		p.SetDateTime(ical.PropLastModified, o.LastModified.UTC())
	}

	if o.AllDay {
		p.SetDate(ical.PropDateTimeStart, o.Start.UTC())
		if !o.End.IsZero() {
			p.SetDate(ical.PropDateTimeEnd, o.End.UTC())
		}
	} else {
		p.SetDateTime(ical.PropDateTimeStart, o.Start.UTC())
		if !o.End.IsZero() && o.End.After(o.Start) {
			p.SetDateTime(ical.PropDateTimeEnd, o.End.UTC())
		}
	}

	summary := o.Summary
	if opts.SummaryPrefix && o.Owner != "" {
		summary = fmt.Sprintf("[%s] %s", o.Owner, summary)
	}
	p.SetText(ical.PropSummary, summary)

	if o.Location != "" {
		p.SetText(ical.PropLocation, o.Location)
	}
	// X- properties: set the raw value, no VALUE=TEXT parameter.
	if o.Owner != "" {
		p.Set(&ical.Prop{Name: propOwner, Value: o.Owner})
	}
	if o.CalendarName != "" {
		p.Set(&ical.Prop{Name: propCalendarSrc, Value: o.CalendarName})
	}

	// CATEGORIES is owner plus tags, so category-color clients can color by
	// person or by tag.
	cats := make([]string, 0, len(o.Tags)+1)
	if o.Owner != "" {
		cats = append(cats, o.Owner)
	}
	cats = append(cats, o.Tags...)
	if len(cats) > 0 {
		cat := ical.NewProp(ical.PropCategories)
		cat.SetTextList(cats)
		p.Set(cat)
	}

	switch o.Status {
	case model.StatusCancelled:
		ev.SetStatus(ical.EventCancelled)
	case model.StatusTentative:
		ev.SetStatus(ical.EventTentative)
	default:
		ev.SetStatus(ical.EventConfirmed)
	}

	return ev
}

// Encode writes cal to w in iCalendar format with CRLF line endings.
func Encode(w io.Writer, cal *ical.Calendar) error {
	return ical.NewEncoder(w).Encode(cal)
}

// Write is Calendar followed by Encode.
func Write(w io.Writer, occs []Occurrence, opts Options) error {
	return Encode(w, Calendar(occs, opts))
}
