package model

import "time"

// Event statuses, matching the provider vocabulary Concordia keeps.
const (
	StatusConfirmed = "confirmed"
	StatusTentative = "tentative"
	StatusCancelled = "cancelled"
)

// Event is a normalized calendar event: one master, or one RECURRENCE-ID
// override. It is provider-agnostic; internal/normalize produces it and
// internal/expand consumes it.
//
// Time handling:
//   - Timed events set Start and End to wall-clock times whose Location is the
//     IANA zone named by TZID. The instant is well defined, and because the
//     Location is a real zone (not a fixed offset) the expander re-derives the
//     correct offset for each instance across DST.
//   - All-day events set AllDay and store Start and End as midnight in
//     time.UTC, meaning the calendar date only. They are never converted to a
//     UTC instant. End is exclusive (DTEND semantics): a one-day event on the
//     14th has End on the 15th.
type Event struct {
	ID         int64
	CalendarID int64

	// RemoteID is the provider's opaque per-calendar event id. Tombstones
	// arrive keyed by this.
	RemoteID string
	// UID is the iCalendar UID, shared by a recurring master and all its
	// overrides (and by the same event seen on multiple calendars).
	UID string
	// RecurrenceID is empty for a master. For an override it is the original
	// start of the instance being replaced: an RFC3339 UTC instant for a timed
	// series, or a YYYY-MM-DD date for an all-day series.
	RecurrenceID string

	Summary  string
	Location string

	Start  time.Time
	End    time.Time
	AllDay bool
	TZID   string

	// RRULE is the RRULE property value (without the "RRULE:" prefix), empty
	// when the event does not recur.
	RRULE string
	// EXDates are EXDATE property lines as delivered, including any TZID
	// parameter, so the expander can resolve them in their own zone.
	EXDates []string

	// SourceTags are tags derived from the provider's own labels (Google
	// colorId, Outlook categories), assigned in normalize. They survive
	// calendar-level redaction: a label is not event content.
	SourceTags []string

	Status    string
	ETag      string
	Raw       []byte
	UpdatedAt time.Time
	DeletedAt time.Time
}

// Recurring reports whether the event carries an RRULE.
func (e Event) Recurring() bool { return e.RRULE != "" }

// IsOverride reports whether the event replaces a single instance of a series.
func (e Event) IsOverride() bool { return e.RecurrenceID != "" }

// Deleted reports whether the event has been tombstoned.
func (e Event) Deleted() bool { return !e.DeletedAt.IsZero() }
