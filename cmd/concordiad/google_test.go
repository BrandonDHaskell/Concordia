package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bhaskell/Concordia/internal/config"
)

const googleClientSecret = `{"installed":{"client_id":"cid.apps.googleusercontent.com",` +
	`"client_secret":"shh","auth_uri":"https://accounts.google.com/o/oauth2/auth",` +
	`"token_uri":"https://oauth2.googleapis.com/token","redirect_uris":["http://localhost"]}}`

func TestLoadGoogleOAuthConfigPrefersSystemdCredential(t *testing.T) {
	credDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(credDir, "google_oauth"), []byte(googleClientSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", credDir)

	cfg := &config.Config{Google: config.Google{CredentialFile: "/does/not/exist.json"}}
	got, err := loadGoogleOAuthConfig(cfg)
	if err != nil {
		t.Fatalf("loadGoogleOAuthConfig: %v", err)
	}
	if got.ClientID != "cid.apps.googleusercontent.com" {
		t.Errorf("client id = %q", got.ClientID)
	}
}

func TestLoadGoogleOAuthConfigFallsBackToFile(t *testing.T) {
	os.Unsetenv("CREDENTIALS_DIRECTORY")
	path := filepath.Join(t.TempDir(), "google_oauth.json")
	if err := os.WriteFile(path, []byte(googleClientSecret), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Google: config.Google{CredentialFile: path}}
	if _, err := loadGoogleOAuthConfig(cfg); err != nil {
		t.Fatalf("loadGoogleOAuthConfig: %v", err)
	}
}

func TestLoadGoogleOAuthConfigMissing(t *testing.T) {
	os.Unsetenv("CREDENTIALS_DIRECTORY")
	if _, err := loadGoogleOAuthConfig(&config.Config{}); err == nil {
		t.Fatal("expected an error with no credential source")
	}
}

func TestTokenDir(t *testing.T) {
	cfg := &config.Config{Server: config.Server{Database: "/var/lib/concordia/concordia.db"}}

	t.Setenv("STATE_DIRECTORY", "/run/state/concordia")
	if got := tokenDir(cfg); got != "/run/state/concordia/tokens" {
		t.Errorf("with STATE_DIRECTORY: %q", got)
	}

	os.Unsetenv("STATE_DIRECTORY")
	if got := tokenDir(cfg); got != "/var/lib/concordia/tokens" {
		t.Errorf("without STATE_DIRECTORY: %q", got)
	}
}

func TestFindAccount(t *testing.T) {
	cfg := &config.Config{Accounts: []config.Account{
		{Person: "b", Provider: "google", Name: "Work Gmail"},
		{Person: "b", Provider: "icloud", Name: "iCloud", Username: "b@example.com"},
	}}

	if a, err := findAccount(cfg, "Work Gmail"); err != nil || a.Provider != "google" {
		t.Errorf("by name: %+v %v", a, err)
	}
	if a, err := findAccount(cfg, "work-gmail"); err != nil || a.Provider != "google" {
		t.Errorf("by ref: %+v %v", a, err)
	}
	if _, err := findAccount(cfg, "nope"); err == nil {
		t.Error("expected error for unknown account")
	}
}
