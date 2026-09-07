package main

import (
	"log/slog"

	"github.com/bhaskell/Concordia/internal/config"
	"github.com/bhaskell/Concordia/internal/davd"
	"github.com/bhaskell/Concordia/internal/httpd"
	"github.com/bhaskell/Concordia/internal/store"
)

// buildServer assembles the daemon's HTTP server: the CalDAV collections plus
// ICS feeds, one merged plus one per person and one per configured view, over
// the config window.
func buildServer(cfg *config.Config, st *store.Store, log *slog.Logger) (*httpd.Server, error) {
	parsed, err := cfg.ParsedViews()
	if err != nil {
		return nil, err
	}
	people := cfg.People()
	wb := cfg.Server.WindowBack.Duration()
	wf := cfg.Server.WindowFwd.Duration()

	davViews := make([]davd.View, len(parsed))
	feeds := []httpd.Feed{{Slug: "all", Name: "Household"}}
	for _, p := range people {
		feeds = append(feeds, httpd.Feed{Slug: "p-" + davd.Slug(p), Name: p, Owner: p})
	}
	for i, v := range parsed {
		davViews[i] = davd.View{Name: v.Name, Predicate: v.Predicate}
		feeds = append(feeds, httpd.Feed{Slug: "v-" + davd.Slug(v.Name), Name: v.Name, Predicate: v.Predicate})
	}

	backend := davd.New(st, davd.Options{
		People:        people,
		Views:         davViews,
		SummaryPrefix: cfg.Serve.SummaryPrefix,
		WindowBack:    wb,
		WindowFwd:     wf,
		Prefix:        "/dav",
	})

	return httpd.New(httpd.Config{
		Addr:          cfg.Server.Listen,
		Store:         st,
		Feeds:         feeds,
		SummaryPrefix: cfg.Serve.SummaryPrefix,
		WindowBack:    wb,
		WindowFwd:     wf,
		DAVHandler:    backend.Handler(),
		Log:           log,
	}), nil
}
