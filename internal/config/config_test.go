package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const validConfig = `
[server]
listen      = "10.0.0.65:8080"
database    = "/var/lib/concordia/concordia.db"
window_back = "60d"
window_fwd  = "400d"

[[account]]
person   = "brandon"
provider = "icloud"
name     = "iCloud"
username = "brandon@example.com"

[[account]]
person   = "brandon"
provider = "google"
name     = "Gmail"

[[rule]]
match_calendar = "Work"
action         = "redact"

[[rule]]
match_title = "(?i)soccer|practice"
action      = "tag"
tag         = "kids"

[[view]]
name      = "no-work"
predicate = "not tag:work"
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	cfg, err := Load(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Listen != "10.0.0.65:8080" {
		t.Errorf("Listen = %q", cfg.Server.Listen)
	}
	if got := cfg.Server.WindowBack.Duration(); got != 60*24*time.Hour {
		t.Errorf("WindowBack = %v, want 1440h", got)
	}
	if got := cfg.Server.WindowFwd.Duration(); got != 400*24*time.Hour {
		t.Errorf("WindowFwd = %v, want 9600h", got)
	}
	if len(cfg.Accounts) != 2 {
		t.Fatalf("Accounts = %d, want 2", len(cfg.Accounts))
	}
	if len(cfg.Rules) != 2 || len(cfg.Views) != 1 {
		t.Errorf("Rules = %d, Views = %d", len(cfg.Rules), len(cfg.Views))
	}
}

func TestLoadExampleConfig(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "config.example.toml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("example config not present: %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("shipped example config does not validate: %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing listen",
			body: `[server]
database    = "/db"
window_back = "1d"
window_fwd  = "1d"`,
		},
		{
			name: "listen not host:port",
			body: `[server]
listen      = "8080"
database    = "/db"
window_back = "1d"
window_fwd  = "1d"`,
		},
		{
			name: "missing database",
			body: `[server]
listen      = "0.0.0.0:8080"
window_back = "1d"
window_fwd  = "1d"`,
		},
		{
			name: "zero window",
			body: `[server]
listen      = "0.0.0.0:8080"
database    = "/db"
window_back = "0d"
window_fwd  = "1d"`,
		},
		{
			name: "bad duration unit",
			body: `[server]
listen      = "0.0.0.0:8080"
database    = "/db"
window_back = "60x"
window_fwd  = "1d"`,
		},
		{
			name: "unknown key",
			body: `[server]
listen      = "0.0.0.0:8080"
database    = "/db"
window_back = "1d"
window_fwd  = "1d"
frobnicate  = true`,
		},
		{
			name: "unknown provider",
			body: serverOK + `
[[account]]
person   = "a"
provider = "yahoo"
name     = "Yahoo"`,
		},
		{
			name: "icloud without username",
			body: serverOK + `
[[account]]
person   = "a"
provider = "icloud"
name     = "iCloud"`,
		},
		{
			name: "colliding credential refs",
			body: serverOK + `
[[account]]
person   = "a"
provider = "google"
name     = "Gmail"
credential_ref = "shared"

[[account]]
person   = "b"
provider = "google"
name     = "Other"
credential_ref = "shared"`,
		},
		{
			name: "graph account without client_id",
			body: serverOK + `
[[account]]
person   = "a"
provider = "graph"
name     = "Work"`,
		},
		{
			name: "credential ref with path separator",
			body: serverOK + `
[[account]]
person   = "a"
provider = "google"
name     = "Gmail"
credential_ref = "../etc/passwd"`,
		},
		{
			name: "rule with no match",
			body: serverOK + `
[[rule]]
action = "exclude"`,
		},
		{
			name: "rule with two matches",
			body: serverOK + `
[[rule]]
match_calendar = "Work"
match_title    = "x"
action         = "exclude"`,
		},
		{
			name: "rule tag action without tag",
			body: serverOK + `
[[rule]]
match_title = "x"
action      = "tag"`,
		},
		{
			name: "rule bad regexp",
			body: serverOK + `
[[rule]]
match_title = "("
action      = "exclude"`,
		},
		{
			name: "unknown action",
			body: serverOK + `
[[rule]]
match_title = "x"
action      = "highlight"`,
		},
		{
			name: "duplicate view name",
			body: serverOK + `
[[view]]
name      = "dup"
predicate = "tag:a"

[[view]]
name      = "dup"
predicate = "tag:b"`,
		},
		{
			name: "view without predicate",
			body: serverOK + `
[[view]]
name = "empty"`,
		},
		{
			name: "view with unparseable predicate",
			body: serverOK + `
[[view]]
name      = "bad"
predicate = "tag:kids and"`,
		},
		{
			name: "view with unknown selector",
			body: serverOK + `
[[view]]
name      = "bad"
predicate = "color:red"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tt.body)); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestLoadGraphAccount(t *testing.T) {
	body := serverOK + `
[graph]
client_id = "app-guid"
tenant    = "organizations"

[[account]]
person   = "brandon"
provider = "graph"
name     = "Work Outlook"`

	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Graph.ClientID != "app-guid" || cfg.Graph.Tenant != "organizations" {
		t.Errorf("graph settings = %+v", cfg.Graph)
	}
	if cfg.Accounts[0].Ref() != "work-outlook" {
		t.Errorf("ref = %q", cfg.Accounts[0].Ref())
	}
}

func TestServeTimezone(t *testing.T) {
	cfg, err := Load(writeConfig(t, serverOK+"\n[serve]\ntimezone = \"America/Los_Angeles\"\n"))
	if err != nil {
		t.Fatalf("valid zone: %v", err)
	}
	if cfg.Serve.Location().String() != "America/Los_Angeles" {
		t.Errorf("Location = %s", cfg.Serve.Location())
	}

	if _, err := Load(writeConfig(t, serverOK+"\n[serve]\n")); err != nil {
		t.Errorf("empty [serve] should be fine: %v", err)
	}
	if _, err := Load(writeConfig(t, serverOK+"\n[serve]\ntimezone = \"Mars/Olympus\"\n")); err == nil {
		t.Error("bad timezone should fail validation")
	}
}

func TestAccountRef(t *testing.T) {
	tests := []struct {
		name string
		a    Account
		want string
	}{
		{name: "explicit ref wins", a: Account{Name: "Work Gmail", CredentialRef: "work"}, want: "work"},
		{name: "slug of name", a: Account{Name: "Work Gmail"}, want: "work-gmail"},
		{name: "slug collapses punctuation", a: Account{Name: "brandon@example.com"}, want: "brandon-example-com"},
		{name: "slug trims edges", a: Account{Name: "  iCloud!  "}, want: "icloud"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Ref(); got != tt.want {
				t.Errorf("Ref() = %q, want %q", got, tt.want)
			}
		})
	}
}

const serverOK = `[server]
listen      = "0.0.0.0:8080"
database    = "/db"
window_back = "1d"
window_fwd  = "1d"
`

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "60d", want: 60 * 24 * time.Hour},
		{in: "1d12h", want: 36 * time.Hour},
		{in: "90m", want: 90 * time.Minute},
		{in: "  2d  ", want: 48 * time.Hour},
		{in: "", wantErr: true},
		{in: "5", wantErr: true},
		{in: "d", wantErr: true},
		{in: "1d5x", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseDuration(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseDuration(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDuration(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
