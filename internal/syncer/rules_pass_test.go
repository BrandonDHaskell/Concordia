package syncer

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/normalize"
	"github.com/bhaskell/Concordia/internal/provider"
	"github.com/bhaskell/Concordia/internal/rules"
	"github.com/bhaskell/Concordia/internal/store"
)

// coloredEvent is a Google event JSON with a colorId, for source-tag coverage.
func coloredEvent(id, summary, colorID string, day int) provider.RawEvent {
	body := `{
		"id": "` + id + `", "iCalUId": "` + id + `@g", "status": "confirmed",
		"summary": "` + summary + `", "colorId": "` + colorID + `",
		"start": {"dateTime": "2026-09-` + twoDigit(day) + `T10:00:00-07:00", "timeZone": "America/Los_Angeles"},
		"end":   {"dateTime": "2026-09-` + twoDigit(day) + `T11:00:00-07:00", "timeZone": "America/Los_Angeles"}
	}`
	return provider.RawEvent{RemoteID: id, ETag: `"e"`, Body: []byte(body)}
}

func twoDigit(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func occurrenceTagsBySummary(t *testing.T, s *Syncer, ctx context.Context) map[string][]string {
	t.Helper()
	rows, err := s.Store.Occurrences(ctx, store.OccurrenceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, r := range rows {
		tags := append([]string(nil), r.Tags...)
		sort.Strings(tags)
		out[r.Summary] = tags
	}
	return out
}

func TestRulesPassTagsExcludeRedact(t *testing.T) {
	sy, ctx, cal := newSyncer(t)
	cal.DisplayName = "Family"
	sy.Rules = mustRules(t,
		rules.Spec{MatchTitle: "(?i)board", Action: "redact"},
		rules.Spec{MatchTitle: "(?i)soccer", Action: "tag", Tag: "kids"},
		rules.Spec{MatchTitle: "(?i)secret", Action: "exclude"},
	)

	p := &scriptedProvider{steps: []step{{
		delta: provider.Delta{Changed: []provider.RawEvent{
			coloredEvent("e1", "Kids Soccer", "11", 10),  // colorId 11 -> tomato; title -> kids
			coloredEvent("e2", "Board meeting", "8", 11), // colorId 8 -> graphite; redacted by title rule
			coloredEvent("e3", "Secret thing", "", 12),   // excluded
		}},
		token: "S1",
	}}}

	res, err := sy.Calendar(ctx, p, normalize.Event, cal, model.ProviderGoogle, "brandon")
	if err != nil {
		t.Fatalf("Calendar: %v", err)
	}
	if res.Occurrences != 2 {
		t.Fatalf("occurrences = %d, want 2 (Secret excluded)", res.Occurrences)
	}

	got := occurrenceTagsBySummary(t, sy, ctx)
	if _, excluded := got["Secret thing"]; excluded {
		t.Error("excluded occurrence was written")
	}
	if _, redactedGone := got["Board meeting"]; redactedGone {
		t.Error("redacted occurrence kept its original summary")
	}
	if tags := got[normalize.RedactedSummary]; strings.Join(tags, ",") != "graphite" {
		t.Errorf("redacted busy block tags = %v, want [graphite] (source tag survives)", tags)
	}
	if tags := got["Kids Soccer"]; strings.Join(tags, ",") != "kids,tomato" {
		t.Errorf("Kids Soccer tags = %v, want [kids tomato]", tags)
	}
}

func mustRules(t *testing.T, specs ...rules.Spec) *rules.Engine {
	t.Helper()
	e, err := rules.Compile(specs)
	if err != nil {
		t.Fatalf("rules.Compile: %v", err)
	}
	return e
}
