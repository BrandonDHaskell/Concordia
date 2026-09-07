package model

import "time"

// Window is the half-open rolling time range [Start, End) that expansion
// materializes into. The outline default is 60 days back and 400 days forward,
// slid nightly.
type Window struct {
	Start time.Time
	End   time.Time
}

// NewWindow returns the window [now-back, now+fwd).
func NewWindow(now time.Time, back, fwd time.Duration) Window {
	return Window{Start: now.Add(-back), End: now.Add(fwd)}
}

// Overlaps reports whether an interval [start, end) intersects the window. A
// zero-length interval is treated as [start, start] so it still matches when it
// falls on the window edge.
func (w Window) Overlaps(start, end time.Time) bool {
	if !end.After(start) {
		return !start.Before(w.Start) && start.Before(w.End)
	}
	return start.Before(w.End) && end.After(w.Start)
}

// Contains reports whether t is within [Start, End).
func (w Window) Contains(t time.Time) bool {
	return !t.Before(w.Start) && t.Before(w.End)
}
