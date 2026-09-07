package main

import (
	"fmt"

	"github.com/bhaskell/Concordia/internal/config"
	"github.com/bhaskell/Concordia/internal/rules"
	"github.com/bhaskell/Concordia/internal/views"
)

// rulesEngine compiles the config's ordered rule list.
func rulesEngine(cfg *config.Config) (*rules.Engine, error) {
	specs := make([]rules.Spec, len(cfg.Rules))
	for i, r := range cfg.Rules {
		specs[i] = rules.Spec{
			MatchCalendar: r.MatchCalendar,
			MatchTitle:    r.MatchTitle,
			MatchLocation: r.MatchLocation,
			Action:        r.Action,
			Tag:           r.Tag,
		}
	}
	return rules.Compile(specs)
}

// viewPredicate looks up a named view's compiled predicate.
func viewPredicate(cfg *config.Config, name string) (views.Predicate, error) {
	parsed, err := cfg.ParsedViews()
	if err != nil {
		return nil, err
	}
	for _, v := range parsed {
		if v.Name == name {
			return v.Predicate, nil
		}
	}
	return nil, fmt.Errorf("no view named %q in config", name)
}
