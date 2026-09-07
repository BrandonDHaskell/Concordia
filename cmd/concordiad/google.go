package main

import (
	"fmt"
	"os"

	"golang.org/x/oauth2"

	"github.com/bhaskell/Concordia/internal/auth"
	"github.com/bhaskell/Concordia/internal/config"
)

// systemdGoogleCredential is the name of the systemd LoadCredentialEncrypted
// entry that carries the Google OAuth client secret in production.
const systemdGoogleCredential = "google_oauth"

// loadGoogleOAuthConfig resolves the Google OAuth client secret: the systemd
// credential if present, otherwise the configured dev file.
func loadGoogleOAuthConfig(cfg *config.Config) (*oauth2.Config, error) {
	if p, ok := auth.SystemdCredentialPath(systemdGoogleCredential); ok {
		if _, err := os.Stat(p); err == nil {
			return auth.LoadGoogleConfig(p)
		}
	}
	if cfg.Google.CredentialFile != "" {
		return auth.LoadGoogleConfig(cfg.Google.CredentialFile)
	}
	return nil, fmt.Errorf("no Google client secret: provide the systemd credential %q or set [google] credential_file", systemdGoogleCredential)
}
