// Command concordiad is the Concordia household calendar aggregator daemon.
//
// Usage:
//
//	concordiad [-config path] [-migrate]        run the daemon (or migrate and exit)
//	concordiad auth google --account <name>     authorize a Google account
//	concordiad sync-once --account <name>       run one sync pass for an account
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
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	args := os.Args[1:]

	if len(args) > 0 {
		switch args[0] {
		case "auth":
			exitOn(log, "auth failed", runAuth(context.Background(), args[1:], log))
			return
		case "sync-once":
			exitOn(log, "sync-once failed", runSyncOnce(context.Background(), args[1:], log))
			return
		}
	}

	fs := flag.NewFlagSet("concordiad", flag.ExitOnError)
	configPath := fs.String("config", "config.toml", "path to the TOML config file")
	migrateOnly := fs.Bool("migrate", false, "apply database migrations and exit")
	_ = fs.Parse(args)

	if err := runDaemon(*configPath, *migrateOnly, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// exitOn logs and exits non-zero when err is non-nil.
func exitOn(log *slog.Logger, msg string, err error) {
	if err != nil {
		log.Error(msg, "err", err)
		os.Exit(1)
	}
}

func runDaemon(configPath string, migrateOnly bool, log *slog.Logger) error {
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
