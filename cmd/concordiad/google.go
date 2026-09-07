package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"

	"github.com/bhaskell/Concordia/internal/auth"
	"github.com/bhaskell/Concordia/internal/config"
	googleprov "github.com/bhaskell/Concordia/internal/provider/google"
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

// tokenDir is where per-account token files live: under the systemd state
// directory in production, alongside the database in a dev checkout.
func tokenDir(cfg *config.Config) string {
	if d := os.Getenv("STATE_DIRECTORY"); d != "" {
		return filepath.Join(d, "tokens")
	}
	return filepath.Join(filepath.Dir(cfg.Server.Database), "tokens")
}

// newGoogleProvider builds a Google provider for one account from its stored
// token.
func newGoogleProvider(ctx context.Context, cfg *config.Config, oauthCfg *oauth2.Config, store *auth.FileTokenStore, ref string) (*googleprov.Provider, error) {
	ts, err := auth.TokenSource(ctx, oauthCfg, store, ref)
	if err != nil {
		return nil, err
	}
	client := oauth2.NewClient(ctx, ts)
	return googleprov.New(ctx, client,
		cfg.Server.WindowBack.Duration(), cfg.Server.WindowFwd.Duration())
}
