// Command concordiad is the Concordia household calendar aggregator daemon.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bhaskell/Concordia/internal/config"
	"github.com/bhaskell/Concordia/internal/httpd"
	"github.com/bhaskell/Concordia/internal/store"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to the TOML config file")
	migrateOnly := flag.Bool("migrate", false, "apply database migrations and exit")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(*configPath, *migrateOnly, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, migrateOnly bool, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	log.Info("config loaded", "path", configPath, "accounts", len(cfg.Accounts))

	st, err := store.Open(ctx, cfg.Server.Database)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return err
	}
	log.Info("migrations applied", "database", cfg.Server.Database)

	if migrateOnly {
		return nil
	}

	srv := httpd.New(cfg.Server.Listen, log, st)
	if err := srv.Run(ctx); err != nil {
		return err
	}

	log.Info("shutdown complete")
	return nil
}
