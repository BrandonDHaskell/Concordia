// Package config loads and validates the Concordia TOML configuration once at
// startup. A malformed or incomplete config is a boot-time failure, never a
// first-sync surprise.
package config

import (
	"fmt"
	"net"
	"os"
	"regexp"

	"github.com/BurntSushi/toml"
)

// Supported provider identifiers for an account.
const (
	ProviderICloud = "icloud"
	ProviderGoogle = "google"
	ProviderGraph  = "graph"
)

// Supported rule actions.
const (
	ActionTag     = "tag"
	ActionExclude = "exclude"
	ActionRedact  = "redact"
)

// Config is the fully parsed and validated configuration.
type Config struct {
	Server   Server    `toml:"server"`
	Accounts []Account `toml:"account"`
	Rules    []Rule    `toml:"rule"`
	Views    []View    `toml:"view"`
}

// Server holds process-wide settings.
type Server struct {
	Listen     string   `toml:"listen"`
	Database   string   `toml:"database"`
	WindowBack Duration `toml:"window_back"`
	WindowFwd  Duration `toml:"window_fwd"`
}

// Account is one provider login belonging to one person. Credentials are never
// stored here; CredentialRef names an out-of-band secret.
type Account struct {
	Person        string `toml:"person"`
	Provider      string `toml:"provider"`
	Name          string `toml:"name"`
	Username      string `toml:"username"`
	CredentialRef string `toml:"credential_ref"`
}

// Rule matches occurrences and applies an action during materialization.
// Exactly one match_* field must be set.
type Rule struct {
	MatchCalendar string `toml:"match_calendar"`
	MatchTitle    string `toml:"match_title"`
	MatchLocation string `toml:"match_location"`
	Action        string `toml:"action"`
	Tag           string `toml:"tag"`
}

// View is a named predicate over owner and tags with a stable feed URL.
type View struct {
	Name      string `toml:"name"`
	Predicate string `toml:"predicate"`
}

// Load reads, decodes, and validates the config file at path.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	md, err := toml.Decode(string(b), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("unknown keys in config %s: %v", path, undecoded)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if err := c.Server.validate(); err != nil {
		return fmt.Errorf("[server]: %w", err)
	}
	for i, a := range c.Accounts {
		if err := a.validate(); err != nil {
			return fmt.Errorf("[[account]] %d (%q): %w", i, a.Name, err)
		}
	}
	for i, r := range c.Rules {
		if err := r.validate(); err != nil {
			return fmt.Errorf("[[rule]] %d: %w", i, err)
		}
	}
	seen := make(map[string]bool, len(c.Views))
	for i, v := range c.Views {
		if err := v.validate(); err != nil {
			return fmt.Errorf("[[view]] %d (%q): %w", i, v.Name, err)
		}
		if seen[v.Name] {
			return fmt.Errorf("[[view]] %d: duplicate view name %q", i, v.Name)
		}
		seen[v.Name] = true
	}
	return nil
}

func (s *Server) validate() error {
	if s.Listen == "" {
		return fmt.Errorf("listen is required")
	}
	if _, _, err := net.SplitHostPort(s.Listen); err != nil {
		return fmt.Errorf("listen %q is not host:port: %w", s.Listen, err)
	}
	if s.Database == "" {
		return fmt.Errorf("database is required")
	}
	if s.WindowBack.Duration() <= 0 {
		return fmt.Errorf("window_back must be positive")
	}
	if s.WindowFwd.Duration() <= 0 {
		return fmt.Errorf("window_fwd must be positive")
	}
	return nil
}

func (a *Account) validate() error {
	if a.Person == "" {
		return fmt.Errorf("person is required")
	}
	if a.Name == "" {
		return fmt.Errorf("name is required")
	}
	switch a.Provider {
	case ProviderICloud:
		if a.Username == "" {
			return fmt.Errorf("username is required for provider %q", a.Provider)
		}
	case ProviderGoogle, ProviderGraph:
		// OAuth accounts carry no username in config.
	case "":
		return fmt.Errorf("provider is required")
	default:
		return fmt.Errorf("unknown provider %q", a.Provider)
	}
	return nil
}

func (r *Rule) validate() error {
	set := 0
	for _, m := range []string{r.MatchCalendar, r.MatchTitle, r.MatchLocation} {
		if m != "" {
			set++
		}
	}
	if set != 1 {
		return fmt.Errorf("exactly one of match_calendar, match_title, match_location must be set")
	}
	if r.MatchTitle != "" {
		if _, err := regexp.Compile(r.MatchTitle); err != nil {
			return fmt.Errorf("match_title is not a valid regexp: %w", err)
		}
	}
	if r.MatchLocation != "" {
		if _, err := regexp.Compile(r.MatchLocation); err != nil {
			return fmt.Errorf("match_location is not a valid regexp: %w", err)
		}
	}
	switch r.Action {
	case ActionTag:
		if r.Tag == "" {
			return fmt.Errorf("action %q requires tag", r.Action)
		}
	case ActionExclude, ActionRedact:
		if r.Tag != "" {
			return fmt.Errorf("action %q does not take tag", r.Action)
		}
	case "":
		return fmt.Errorf("action is required")
	default:
		return fmt.Errorf("unknown action %q", r.Action)
	}
	return nil
}

func (v *View) validate() error {
	if v.Name == "" {
		return fmt.Errorf("name is required")
	}
	if v.Predicate == "" {
		return fmt.Errorf("predicate is required")
	}
	return nil
}
