package rules

import (
	"strings"
	"testing"
)

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
	}{
		{"no match field", Spec{Action: "exclude"}},
		{"two match fields", Spec{MatchCalendar: "a", MatchTitle: "b", Action: "exclude"}},
		{"bad regexp", Spec{MatchTitle: "(", Action: "exclude"}},
		{"unknown action", Spec{MatchTitle: "x", Action: "highlight"}},
		{"tag action without tag", Spec{MatchTitle: "x", Action: "tag"}},
		{"exclude with a tag", Spec{MatchTitle: "x", Action: "exclude", Tag: "nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Compile([]Spec{tt.spec}); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestEvaluate(t *testing.T) {
	eng, err := Compile([]Spec{
		{MatchCalendar: "Work", Action: "redact"},
		{MatchTitle: "(?i)soccer|practice", Action: "tag", Tag: "kids"},
		{MatchTitle: "(?i)soccer", Action: "tag", Tag: "sports"},
		{MatchLocation: "(?i)dentist", Action: "exclude"},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	tests := []struct {
		name string
		subj Subject
		want Result
	}{
		{
			name: "work calendar redacts",
			subj: Subject{CalendarName: "Work Calendar", Summary: "1:1"},
			want: Result{Redact: true},
		},
		{
			name: "soccer gets both tags in order",
			subj: Subject{CalendarName: "Family", Summary: "U10 Soccer game"},
			want: Result{Tags: []string{"kids", "sports"}},
		},
		{
			name: "practice gets only kids",
			subj: Subject{CalendarName: "Family", Summary: "Piano practice"},
			want: Result{Tags: []string{"kids"}},
		},
		{
			name: "dentist location excluded",
			subj: Subject{CalendarName: "Family", Summary: "Checkup", Location: "Dentist office"},
			want: Result{Exclude: true},
		},
		{
			name: "no match, empty result",
			subj: Subject{CalendarName: "Family", Summary: "Dinner"},
			want: Result{},
		},
		{
			name: "work soccer redacts and tags",
			subj: Subject{CalendarName: "Work", Summary: "Charity soccer"},
			want: Result{Redact: true, Tags: []string{"kids", "sports"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eng.Evaluate(tt.subj)
			if got.Exclude != tt.want.Exclude || got.Redact != tt.want.Redact {
				t.Errorf("flags: got %+v want %+v", got, tt.want)
			}
			if strings.Join(got.Tags, ",") != strings.Join(tt.want.Tags, ",") {
				t.Errorf("tags: got %v want %v", got.Tags, tt.want.Tags)
			}
		})
	}
}

func TestEvaluateEmptyEngine(t *testing.T) {
	eng, _ := Compile(nil)
	if got := eng.Evaluate(Subject{Summary: "anything"}); got.Exclude || got.Redact || len(got.Tags) > 0 {
		t.Errorf("empty engine returned %+v", got)
	}
}
