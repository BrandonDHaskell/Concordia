// Package rules is the ordered rule engine applied during materialization. A
// rule matches an occurrence on its source calendar name, title, or location
// and contributes a tag, an exclusion, or a redaction. Rules never touch the
// stored event, only the occurrence being written.
package rules

import (
	"fmt"
	"regexp"
)

// Rule actions.
const (
	ActionTag     = "tag"
	ActionExclude = "exclude"
	ActionRedact  = "redact"
)

// Spec is one rule in its configured (string) form. Exactly one match field
// must be set.
type Spec struct {
	MatchCalendar string
	MatchTitle    string
	MatchLocation string
	Action        string
	Tag           string
}

type matchField int

const (
	fieldCalendar matchField = iota
	fieldTitle
	fieldLocation
)

type compiled struct {
	field  matchField
	re     *regexp.Regexp
	action string
	tag    string
}

// Engine evaluates a fixed, ordered list of rules.
type Engine struct {
	rules []compiled
}

// Subject is the occurrence context a rule matches against.
type Subject struct {
	CalendarName string
	Summary      string
	Location     string
}

// Result is the combined effect of every rule that matched a subject.
type Result struct {
	Exclude bool
	Redact  bool
	Tags    []string
}

// Compile validates and compiles specs into an Engine, preserving order.
func Compile(specs []Spec) (*Engine, error) {
	e := &Engine{rules: make([]compiled, 0, len(specs))}
	for i, s := range specs {
		c, err := compileSpec(s)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		e.rules = append(e.rules, c)
	}
	return e, nil
}

func compileSpec(s Spec) (compiled, error) {
	var (
		field matchField
		pat   string
		set   int
	)
	if s.MatchCalendar != "" {
		field, pat, set = fieldCalendar, s.MatchCalendar, set+1
	}
	if s.MatchTitle != "" {
		field, pat, set = fieldTitle, s.MatchTitle, set+1
	}
	if s.MatchLocation != "" {
		field, pat, set = fieldLocation, s.MatchLocation, set+1
	}
	if set != 1 {
		return compiled{}, fmt.Errorf("exactly one of match_calendar, match_title, match_location must be set")
	}

	re, err := regexp.Compile(pat)
	if err != nil {
		return compiled{}, fmt.Errorf("invalid match pattern %q: %w", pat, err)
	}

	switch s.Action {
	case ActionTag:
		if s.Tag == "" {
			return compiled{}, fmt.Errorf("action %q requires a tag", s.Action)
		}
	case ActionExclude, ActionRedact:
		if s.Tag != "" {
			return compiled{}, fmt.Errorf("action %q does not take a tag", s.Action)
		}
	default:
		return compiled{}, fmt.Errorf("unknown action %q", s.Action)
	}

	return compiled{field: field, re: re, action: s.Action, tag: s.Tag}, nil
}

// Evaluate runs every rule against s, in order, and returns their combined
// effect. Rules accumulate: all matching tag rules contribute, and a single
// matching exclude or redact rule sets that flag.
func (e *Engine) Evaluate(s Subject) Result {
	var r Result
	for _, c := range e.rules {
		if !c.re.MatchString(c.value(s)) {
			continue
		}
		switch c.action {
		case ActionTag:
			r.Tags = appendUnique(r.Tags, c.tag)
		case ActionExclude:
			r.Exclude = true
		case ActionRedact:
			r.Redact = true
		}
	}
	return r
}

func (c compiled) value(s Subject) string {
	switch c.field {
	case fieldCalendar:
		return s.CalendarName
	case fieldTitle:
		return s.Summary
	default:
		return s.Location
	}
}

func appendUnique(ss []string, s string) []string {
	for _, existing := range ss {
		if existing == s {
			return ss
		}
	}
	return append(ss, s)
}
