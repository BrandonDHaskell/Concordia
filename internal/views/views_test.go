package views

import "testing"

func TestParseAndMatch(t *testing.T) {
	brandon := Subject{Owner: "brandon", Tags: []string{"kids", "work"}}
	kim := Subject{Owner: "kim", Tags: []string{"kids"}}
	nobody := Subject{Owner: "sam", Tags: nil}

	tests := []struct {
		pred    string
		subject Subject
		want    bool
	}{
		{"tag:kids", brandon, true},
		{"tag:kids", nobody, false},
		{"owner:brandon", brandon, true},
		{"owner:brandon", kim, false},
		{"not tag:work", kim, true},
		{"not tag:work", brandon, false},
		{"tag:kids and not tag:work", kim, true},
		{"tag:kids and not tag:work", brandon, false},
		{"owner:brandon or owner:kim", kim, true},
		{"owner:brandon or owner:kim", nobody, false},
		{"(owner:brandon or owner:kim) and tag:kids", brandon, true},
		{"(owner:brandon or owner:kim) and tag:kids", nobody, false},
		{"NOT tag:work", kim, true}, // keywords are case-insensitive
		{"tag:kids AND owner:kim", kim, true},
	}
	for _, tt := range tests {
		t.Run(tt.pred, func(t *testing.T) {
			p, err := Parse(tt.pred)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.pred, err)
			}
			if got := p(tt.subject); got != tt.want {
				t.Errorf("%q against %+v = %v, want %v", tt.pred, tt.subject, got, tt.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, pred := range []string{
		"",
		"   ",
		"tag:",
		"kids",
		"color:red",
		"tag:kids and",
		"and tag:kids",
		"(tag:kids",
		"tag:kids)",
		"not",
		"tag:kids tag:work",
	} {
		t.Run(pred, func(t *testing.T) {
			if _, err := Parse(pred); err == nil {
				t.Errorf("Parse(%q) succeeded, want error", pred)
			}
		})
	}
}
