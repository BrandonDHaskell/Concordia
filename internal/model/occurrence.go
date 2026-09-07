package model

import "time"

// Occurrence is one materialized instance of an event within the rolling
// window. It is derived state: internal/expand is the only writer, and the
// occurrences table can be dropped and rebuilt from events at any time.
//
// Start and End follow the same rules as Event: a real instant for timed
// occurrences (Location is the event's zone), midnight-UTC date-only for
// all-day occurrences, with End exclusive.
type Occurrence struct {
	EventID int64

	Start  time.Time
	End    time.Time
	AllDay bool

	Summary  string
	Location string
	Owner    string

	// Tags is the merged set of source-label tags and rule tags, assigned by
	// the rules pass before the occurrence is written. It is the only thing
	// that populates occurrence_tags.
	Tags []string
}
