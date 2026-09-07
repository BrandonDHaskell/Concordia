package main

import (
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/config"
	"github.com/bhaskell/Concordia/internal/store"
)

func TestRulesEngineFromConfig(t *testing.T) {
	cfg := &config.Config{Rules: []config.Rule{
		{MatchTitle: "(?i)soccer", Action: "tag", Tag: "kids"},
		{MatchCalendar: "Work", Action: "redact"},
	}}
	eng, err := rulesEngine(cfg)
	if err != nil {
		t.Fatalf("rulesEngine: %v", err)
	}
	if eng == nil {
		t.Fatal("nil engine")
	}
}

func TestViewPredicateLookup(t *testing.T) {
	cfg := &config.Config{Views: []config.View{
		{Name: "kids", Predicate: "tag:kids"},
		{Name: "no-work", Predicate: "not tag:work"},
	}}

	pred, err := viewPredicate(cfg, "no-work")
	if err != nil {
		t.Fatalf("viewPredicate: %v", err)
	}
	if pred == nil {
		t.Fatal("nil predicate")
	}

	if _, err := viewPredicate(cfg, "missing"); err == nil {
		t.Error("expected an error for an unknown view")
	}
}

func TestFormatOccurrence(t *testing.T) {
	timed := store.OccurrenceRow{
		StartLocal:   "2026-09-10T09:00:00-07:00",
		Owner:        "brandon",
		CalendarName: "Work",
		Summary:      "Standup",
		Tags:         []string{"work"},
	}
	got := formatOccurrence(timed)
	if want := "2026-09-10 09:00 -07:00"; got[:len(want)] != want {
		t.Errorf("timed line = %q", got)
	}
	if got[len(got)-5:] != "#work" {
		t.Errorf("missing tag suffix: %q", got)
	}

	allDay := store.OccurrenceRow{
		Start:        time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC),
		AllDay:       true,
		Owner:        "kim",
		CalendarName: "Family",
		Summary:      "Trip",
	}
	if g := formatOccurrence(allDay); g[:20] != "2026-09-14 (all day)" {
		t.Errorf("all-day line = %q", g)
	}
}
