package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Duration is a time.Duration that unmarshals from a TOML string and also
// understands a leading day component, for example "60d" or "1d12h". The Go
// standard library stops at hours, but window sizes read more naturally in
// days.
type Duration time.Duration

// UnmarshalText implements encoding.TextUnmarshaler so the TOML decoder can
// parse duration strings directly into config fields.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := parseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Duration returns the value as a standard time.Duration.
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

func (d Duration) String() string {
	return time.Duration(d).String()
}

// parseDuration accepts an optional "<n>d" day prefix followed by an optional
// standard Go duration ("36h", "90m", ...). Either part may be omitted but the
// string must not be empty.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	rest := s
	var total time.Duration

	if i := strings.IndexByte(rest, 'd'); i >= 0 {
		days, err := strconv.Atoi(rest[:i])
		if err != nil {
			return 0, fmt.Errorf("invalid day count in %q: %w", s, err)
		}
		total += time.Duration(days) * 24 * time.Hour
		rest = rest[i+1:]
	}

	if rest != "" {
		v, err := time.ParseDuration(rest)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		total += v
	}

	return total, nil
}
