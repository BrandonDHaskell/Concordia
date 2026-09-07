package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/bhaskell/Concordia/internal/auth"
	"github.com/bhaskell/Concordia/internal/config"
	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/store"
)

// authFlowTimeout bounds how long we wait for the user to complete the browser
// authorization.
const authFlowTimeout = 5 * time.Minute

func runAuth(ctx context.Context, args []string, log *slog.Logger) error {
	if len(args) == 0 || args[0] != "google" {
		return fmt.Errorf("usage: concordiad auth google --account <name> [--open]")
	}

	fs := flag.NewFlagSet("auth google", flag.ExitOnError)
	configPath := fs.String("config", "config.toml", "path to the TOML config file")
	account := fs.String("account", "", "config account name (or credential ref) to authorize")
	open := fs.Bool("open", false, "open the authorization URL in a browser")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *account == "" {
		return fmt.Errorf("--account is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	acct, err := findAccount(cfg, *account)
	if err != nil {
		return err
	}
	if acct.Provider != config.ProviderGoogle {
		return fmt.Errorf("account %q has provider %q, not google", acct.Name, acct.Provider)
	}

	oauthCfg, err := loadGoogleOAuthConfig(cfg)
	if err != nil {
		return err
	}

	tokStore, err := auth.NewFileTokenStore(tokenDir(cfg))
	if err != nil {
		return err
	}

	flowCtx, cancel := context.WithTimeout(ctx, authFlowTimeout)
	defer cancel()
	tok, err := auth.Authorize(flowCtx, oauthCfg, auth.AuthorizeOptions{
		Prompt:      os.Stderr,
		OpenBrowser: *open,
	})
	if err != nil {
		return err
	}
	if err := tokStore.Save(acct.Ref(), tok); err != nil {
		return err
	}

	st, err := store.Open(ctx, cfg.Server.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	row, err := st.UpsertAccount(ctx, model.Account{
		Person:        acct.Person,
		Provider:      acct.Provider,
		DisplayName:   acct.Name,
		CredentialRef: acct.Ref(),
		Enabled:       true,
	})
	if err != nil {
		return err
	}

	prov, err := newGoogleProvider(ctx, cfg, oauthCfg, tokStore, acct.Ref())
	if err != nil {
		return err
	}
	cals, err := prov.Calendars(ctx)
	if err != nil {
		return fmt.Errorf("listing calendars: %w", err)
	}
	for _, c := range cals {
		c.AccountID = row.ID
		if _, err := st.UpsertCalendar(ctx, c); err != nil {
			return err
		}
	}

	log.Info("account authorized",
		"account", acct.Name, "credential_ref", acct.Ref(), "calendars", len(cals))
	fmt.Fprintf(os.Stderr, "\nAuthorized %s (%s). %d calendars:\n", acct.Name, acct.Ref(), len(cals))
	for _, c := range cals {
		fmt.Fprintf(os.Stderr, "  %-45s %s\n", c.RemoteID, c.DisplayName)
	}
	return nil
}

func findAccount(cfg *config.Config, name string) (config.Account, error) {
	for _, a := range cfg.Accounts {
		if a.Name == name || a.Ref() == name {
			return a, nil
		}
	}
	return config.Account{}, fmt.Errorf("no account named %q in config", name)
}
