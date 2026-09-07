package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"

	"github.com/bhaskell/Concordia/internal/auth"
	"github.com/bhaskell/Concordia/internal/config"
	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/provider"
	googleprov "github.com/bhaskell/Concordia/internal/provider/google"
	graphprov "github.com/bhaskell/Concordia/internal/provider/graph"
)

// calendarProvider is a provider that can also enumerate its calendars.
type calendarProvider interface {
	provider.Provider
	Calendars(ctx context.Context) ([]model.Calendar, error)
}

// tokenDir is where per-account OAuth token files live: under the systemd state
// directory in production, alongside the database in a dev checkout.
func tokenDir(cfg *config.Config) string {
	if d := os.Getenv("STATE_DIRECTORY"); d != "" {
		return filepath.Join(d, "tokens")
	}
	return filepath.Join(filepath.Dir(cfg.Server.Database), "tokens")
}

func authFileTokenStore(cfg *config.Config) (*auth.FileTokenStore, error) {
	return auth.NewFileTokenStore(tokenDir(cfg))
}

// oauthConfigFor returns the OAuth config for an account's provider.
func oauthConfigFor(cfg *config.Config, acct config.Account) (*oauth2.Config, error) {
	switch acct.Provider {
	case config.ProviderGoogle:
		return loadGoogleOAuthConfig(cfg)
	case config.ProviderGraph:
		if cfg.Graph.ClientID == "" {
			return nil, fmt.Errorf("[graph] client_id is not set")
		}
		return auth.GraphConfig(cfg.Graph.ClientID, cfg.Graph.Tenant), nil
	default:
		return nil, fmt.Errorf("provider %q does not use OAuth", acct.Provider)
	}
}

// buildProvider constructs the calendar provider for an account from its stored
// token.
func buildProvider(ctx context.Context, cfg *config.Config, acct config.Account, tokStore *auth.FileTokenStore) (calendarProvider, error) {
	oauthCfg, err := oauthConfigFor(cfg, acct)
	if err != nil {
		return nil, err
	}
	ts, err := auth.TokenSource(ctx, oauthCfg, tokStore, acct.Ref())
	if err != nil {
		return nil, err
	}
	client := oauth2.NewClient(ctx, ts)
	wb, wf := cfg.Server.WindowBack.Duration(), cfg.Server.WindowFwd.Duration()

	switch acct.Provider {
	case config.ProviderGoogle:
		return googleprov.New(ctx, client, wb, wf)
	case config.ProviderGraph:
		return graphprov.New(client, wb, wf), nil
	default:
		return nil, fmt.Errorf("no provider implementation for %q", acct.Provider)
	}
}
