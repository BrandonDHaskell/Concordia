// Package auth handles OAuth 2.0 for the providers that use it (Google, and
// later Microsoft Graph). It builds oauth2 configs from a downloaded client
// secret, runs a loopback authorization flow, and persists the resulting
// tokens to 0600 files that are rewritten on every refresh.
//
// Scopes are read-only. Nothing here requests write access to a calendar.
package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// ScopeGoogleCalendarReadonly is the only Google scope Concordia ever requests.
const ScopeGoogleCalendarReadonly = "https://www.googleapis.com/auth/calendar.readonly"

const timeLayout = time.RFC3339

// LoadGoogleConfig reads a Google OAuth client secret file (the
// client_secret_*.json downloaded from the Cloud console, with its "installed"
// or "web" wrapper) and returns an oauth2 config scoped to read-only calendar
// access. RedirectURL is left unset; the loopback flow fills it in per run.
func LoadGoogleConfig(path string) (*oauth2.Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: reading Google client secret: %w", err)
	}
	cfg, err := google.ConfigFromJSON(b, ScopeGoogleCalendarReadonly)
	if err != nil {
		return nil, fmt.Errorf("auth: parsing Google client secret %s: %w", path, err)
	}
	// The file's redirect_uris are irrelevant: the loopback flow binds its own
	// redirect to an ephemeral port. Clear it so a stale value can't be used.
	cfg.RedirectURL = ""
	return cfg, nil
}

// SystemdCredentialPath returns the path systemd's LoadCredentialEncrypted
// places a credential at, and whether the credentials directory is set. Callers
// fall back to a configured path when ok is false.
func SystemdCredentialPath(name string) (path string, ok bool) {
	dir := os.Getenv("CREDENTIALS_DIRECTORY")
	if dir == "" {
		return "", false
	}
	return filepath.Join(dir, name), true
}

// tokenJSON is the on-disk shape of a stored token. It mirrors the fields of
// oauth2.Token that survive a round trip.
type tokenJSON struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Expiry       string `json:"expiry,omitempty"`
}

func marshalToken(t *oauth2.Token) ([]byte, error) {
	tj := tokenJSON{
		AccessToken:  t.AccessToken,
		TokenType:    t.TokenType,
		RefreshToken: t.RefreshToken,
	}
	if !t.Expiry.IsZero() {
		tj.Expiry = t.Expiry.UTC().Format(timeLayout)
	}
	return json.Marshal(tj)
}

func unmarshalToken(b []byte) (*oauth2.Token, error) {
	var tj tokenJSON
	if err := json.Unmarshal(b, &tj); err != nil {
		return nil, err
	}
	t := &oauth2.Token{
		AccessToken:  tj.AccessToken,
		TokenType:    tj.TokenType,
		RefreshToken: tj.RefreshToken,
	}
	if tj.Expiry != "" {
		exp, err := time.Parse(timeLayout, tj.Expiry)
		if err != nil {
			return nil, fmt.Errorf("auth: parsing token expiry %q: %w", tj.Expiry, err)
		}
		t.Expiry = exp
	}
	return t, nil
}
