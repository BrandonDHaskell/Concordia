package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/bhaskell/Concordia/internal/config"
	"github.com/bhaskell/Concordia/internal/store"
	"github.com/bhaskell/Concordia/internal/views"
)

func runOccurrences(ctx context.Context, args []string, _ *slog.Logger) error {
	fs := flag.NewFlagSet("occurrences", flag.ExitOnError)
	configPath := fs.String("config", "config.toml", "path to the TOML config file")
	owner := fs.String("owner", "", "filter by owner")
	tag := fs.String("tag", "", "filter by tag")
	viewName := fs.String("view", "", "apply a named view's predicate")
	days := fs.Int("days", 14, "days forward from now")
	back := fs.Int("back", 0, "days back from now")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	var pred views.Predicate
	if *viewName != "" {
		pred, err = viewPredicate(cfg, *viewName)
		if err != nil {
			return err
		}
	}

	st, err := store.Open(ctx, cfg.Server.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	now := time.Now()
	rows, err := st.Occurrences(ctx, store.OccurrenceQuery{
		From:  now.AddDate(0, 0, -*back),
		To:    now.AddDate(0, 0, *days),
		Owner: *owner,
		Tag:   *tag,
	})
	if err != nil {
		return err
	}

	shown := 0
	for _, r := range rows {
		if pred != nil && !pred(views.Subject{Owner: r.Owner, Tags: r.Tags}) {
			continue
		}
		fmt.Fprintln(os.Stdout, formatOccurrence(r))
		shown++
	}
	fmt.Fprintf(os.Stderr, "\n%d occurrence(s)\n", shown)
	return nil
}

func formatOccurrence(r store.OccurrenceRow) string {
	when := r.StartLocal
	if r.AllDay {
		when = fmt.Sprintf("%s (all day)", r.Start.Format("2006-01-02"))
	} else if t, err := time.Parse(time.RFC3339, r.StartLocal); err == nil {
		when = t.Format("2006-01-02 15:04 -07:00")
	}

	line := fmt.Sprintf("%-25s  %-10s  [%s] %s", when, r.Owner, r.CalendarName, r.Summary)
	if len(r.Tags) > 0 {
		line += "  #" + strings.Join(r.Tags, " #")
	}
	return line
}
