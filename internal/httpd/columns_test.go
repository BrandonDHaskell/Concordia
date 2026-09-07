package httpd

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/store"
)

func TestColumnsLayoutRenders(t *testing.T) {
	s := webServer(t)
	body := do(t, s, http.MethodGet, "/view?layout=columns", nil).Body.String()

	if !strings.Contains(body, `class="columns"`) {
		t.Fatalf("columns layout not rendered:\n%s", body)
	}
	// One column per configured person.
	for _, p := range []string{"brandon", "kim"} {
		if !strings.Contains(body, `class="col-head"><span class="dot"></span>`+p) {
			t.Errorf("no column head for %q:\n%s", p, body)
		}
	}
	// Events land in their owner's column.
	if !strings.Contains(body, "Standup") || !strings.Contains(body, "Soccer") {
		t.Errorf("events missing from columns:\n%s", body)
	}
	// The overlap is still flagged.
	if !strings.Contains(body, "col-event conflict") && !strings.Contains(body, "conflict\"") {
		t.Errorf("conflict not carried into column layout:\n%s", body)
	}
}

func TestLayoutToggle(t *testing.T) {
	s := webServer(t)

	list := do(t, s, http.MethodGet, "/", nil).Body.String()
	if !strings.Contains(list, `hx-get="/view?layout=columns"`) {
		t.Errorf("list view should link to the columns layout:\n%s", list)
	}
	if strings.Contains(list, `class="columns"`) {
		t.Error("default view rendered columns")
	}

	cols := do(t, s, http.MethodGet, "/?layout=columns", nil).Body.String()
	// From columns, the toggle goes back to the list (no layout param).
	if !strings.Contains(cols, `hx-get="/view"`) {
		t.Errorf("columns view should link back to the list:\n%s", cols)
	}
}

func TestColumnsFilterInteraction(t *testing.T) {
	s := webServer(t)
	// Filter to kim while in columns layout: filters compose with layout.
	body := do(t, s, http.MethodGet, "/view?layout=columns&owner=kim", nil).Body.String()
	if strings.Contains(body, "Standup") {
		t.Errorf("owner filter not applied in columns layout:\n%s", body)
	}
	if !strings.Contains(body, "Soccer") {
		t.Errorf("kim's event missing:\n%s", body)
	}
}

func TestGroupByDayColumns(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, loc)
	d := func(day, h int) time.Time { return time.Date(2026, 9, day, h, 0, 0, 0, loc) }

	rows := []store.OccurrenceRow{
		{EventUID: "a", Owner: "brandon", Summary: "AM", Start: d(9, 9), End: d(9, 10)},
		{EventUID: "b", Owner: "kim", Summary: "Lunch", Start: d(9, 12), End: d(9, 13)},
		{EventUID: "c", Owner: "brandon", Summary: "Next day", Start: d(10, 9), End: d(10, 10)},
	}
	days := groupByDayColumns(rows, map[int]bool{}, []string{"brandon", "kim"}, loc, now)

	if len(days) != 2 {
		t.Fatalf("days = %d, want 2", len(days))
	}
	day0 := days[0]
	if len(day0.People) != 2 {
		t.Fatalf("day 0 columns = %d, want 2", len(day0.People))
	}
	if day0.People[0].Name != "brandon" || len(day0.People[0].Events) != 1 {
		t.Errorf("brandon column: %+v", day0.People[0])
	}
	if day0.People[1].Name != "kim" || len(day0.People[1].Events) != 1 {
		t.Errorf("kim column: %+v", day0.People[1])
	}
	// Day 1: kim has nothing.
	if len(days[1].People[1].Events) != 0 {
		t.Errorf("kim should have no events on day 1")
	}
}
