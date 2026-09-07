package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"time"

	"github.com/bhaskell/Concordia/internal/config"
	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/normalize"
	"github.com/bhaskell/Concordia/internal/store"
	"github.com/bhaskell/Concordia/internal/syncer"
)

func runSyncOnce(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("sync-once", flag.ExitOnError)
	configPath := fs.String("config", "config.toml", "path to the TOML config file")
	account := fs.String("account", "", "config account name (or credential ref) to sync")
	calendarID := fs.String("calendar", "", "restrict to one calendar by remote id")
	if err := fs.Parse(args); err != nil {
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

	tokStore, err := authFileTokenStore(cfg)
	if err != nil {
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

	row, err := st.AccountByCredentialRef(ctx, acct.Ref())
	if err != nil {
		return fmt.Errorf("account not authorized yet (run: concordiad auth %s --account %q): %w", acct.Provider, acct.Name, err)
	}
	calendars, err := st.Calendars(ctx, row.ID)
	if err != nil {
		return err
	}

	prov, err := buildProvider(ctx, cfg, acct, tokStore)
	if err != nil {
		return err
	}

	engine, err := rulesEngine(cfg)
	if err != nil {
		return err
	}

	sy := &syncer.Syncer{
		Store:  st,
		Window: model.NewWindow(time.Now(), cfg.Server.WindowBack.Duration(), cfg.Server.WindowFwd.Duration()),
		Rules:  engine,
		Log:    log,
	}

	var synced, failed int
	for _, cal := range calendars {
		if !cal.Enabled || (*calendarID != "" && cal.RemoteID != *calendarID) {
			continue
		}
		res, err := sy.Calendar(ctx, prov, normalize.Event, cal, acct.Provider, row.Person)
		if err != nil {
			failed++
			log.Error("calendar sync failed", "calendar", cal.RemoteID, "err", err)
			if serr := st.SetCalendarError(ctx, cal.ID, err.Error()); serr != nil {
				log.Error("recording calendar error", "calendar", cal.RemoteID, "err", serr)
			}
			continue
		}
		synced++
		log.Info("calendar synced",
			"calendar", cal.RemoteID,
			"changed", res.Changed, "deleted", res.Deleted,
			"occurrences", res.Occurrences, "full_resync", res.FullResync)
	}

	log.Info("sync-once complete", "account", acct.Name, "synced", synced, "failed", failed)
	if failed > 0 {
		return fmt.Errorf("%d calendar(s) failed to sync", failed)
	}
	return nil
}
