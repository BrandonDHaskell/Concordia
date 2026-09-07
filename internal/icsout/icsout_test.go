package icsout

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bhaskell/Concordia/internal/model"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixedNow() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }

func sampleOccurrences() []Occurrence {
	return []Occurrence{
		{
			UID: "ev1-1757520000", Owner: "brandon", CalendarName: "Work",
			Summary: "Standup", Location: "Room 2", Status: model.StatusConfirmed,
			Tags:  []string{"work"},
			Start: time.Date(2026, 9, 10, 16, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 9, 10, 16, 30, 0, 0, time.UTC),
		},
		{
			UID: "trip-1757808000", Owner: "brandon", CalendarName: "Family",
			Summary: "Trip", Status: model.StatusConfirmed, AllDay: true,
			Start: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC),
		},
		{
			UID: "ev3-1757620800", Owner: "kim", CalendarName: "Family",
			Summary: "Soccer", Status: model.StatusTentative,
			Tags:  []string{"kids", "sports"},
			Start: time.Date(2026, 9, 11, 21, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 9, 11, 22, 0, 0, 0, time.UTC),
		},
	}
}

func render(t *testing.T, occs []Occurrence, opts Options) string {
	t.Helper()
	opts.Now = fixedNow()
	var buf bytes.Buffer
	if err := Write(&buf, occs, opts); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.String()
}

func TestGolden(t *testing.T) {
	got := render(t, sampleOccurrences(), Options{Name: "Concordia"})
	golden := filepath.Join("..", "..", "testdata", "ics", "feed_all.ics")

	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run -update to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("output does not match %s\n--- got ---\n%s", golden, got)
	}
}

func TestTimedEventIsUTC(t *testing.T) {
	out := render(t, sampleOccurrences()[:1], Options{})
	if !strings.Contains(out, "DTSTART:20260910T160000Z") {
		t.Errorf("timed DTSTART not UTC:\n%s", out)
	}
	if strings.Contains(out, "VTIMEZONE") || strings.Contains(out, "TZID") {
		t.Errorf("unexpected timezone data:\n%s", out)
	}
}

func TestAllDayIsDateValued(t *testing.T) {
	out := render(t, sampleOccurrences()[1:2], Options{})
	if !strings.Contains(out, "DTSTART;VALUE=DATE:20260914") {
		t.Errorf("all-day DTSTART not VALUE=DATE:\n%s", out)
	}
	if !strings.Contains(out, "DTEND;VALUE=DATE:20260918") {
		t.Errorf("all-day DTEND wrong (should be exclusive):\n%s", out)
	}
}

func TestOwnershipMarking(t *testing.T) {
	out := render(t, sampleOccurrences()[2:3], Options{SummaryPrefix: true})
	for _, want := range []string{
		"SUMMARY:[kim] Soccer",
		"X-HOUSEHOLD-OWNER:kim",
		"CATEGORIES:kim,kids,sports",
		"STATUS:TENTATIVE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestNoPrefixByDefault(t *testing.T) {
	out := render(t, sampleOccurrences()[2:3], Options{})
	if !strings.Contains(out, "SUMMARY:Soccer") || strings.Contains(out, "[kim]") {
		t.Errorf("prefix applied without the option:\n%s", out)
	}
}

func TestReparseable(t *testing.T) {
	// go-ical must be able to read back what we write.
	out := render(t, sampleOccurrences(), Options{})
	cal, err := ical.NewDecoder(strings.NewReader(out)).Decode()
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if n := len(cal.Events()); n != 3 {
		t.Errorf("re-decoded %d events, want 3", n)
	}
}
