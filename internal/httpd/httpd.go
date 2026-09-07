// Package httpd serves the Concordia web surface: the agenda web view, a health
// endpoint, read-only ICS feeds, and (mounted here) the CalDAV handler. SSE
// live updates arrive in a later milestone.
package httpd

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bhaskell/Concordia/internal/icsout"
	"github.com/bhaskell/Concordia/internal/store"
	"github.com/bhaskell/Concordia/internal/views"
	"github.com/bhaskell/Concordia/web"
)

// Health is the subset of the store the health check needs.
type Health interface {
	Ping(ctx context.Context) error
}

// Feed is one named ICS feed: a merged household feed (Owner and Predicate
// both zero), a per-person feed (Owner set), or a view feed (Predicate set).
type Feed struct {
	Slug      string
	Name      string
	Owner     string
	Predicate views.Predicate
}

// Config assembles a Server.
type Config struct {
	Addr          string
	Store         *store.Store
	Feeds         []Feed
	SummaryPrefix bool
	WindowBack    time.Duration
	WindowFwd     time.Duration
	// DisplayTZ is the zone the web view renders timed events in. Defaults to
	// time.Local.
	DisplayTZ *time.Location
	// DAVHandler is mounted at /dav/ when non-nil.
	DAVHandler http.Handler
	Log        *slog.Logger
	// WatchInterval is how often the daemon polls for a data change to push
	// over SSE. Defaults to 2s.
	WatchInterval time.Duration
	// Now overrides the clock, for tests.
	Now func() time.Time
}

// Server wraps an http.Server with Concordia's routes and lifecycle.
type Server struct {
	srv  *http.Server
	cfg  Config
	log  *slog.Logger
	now  func() time.Time
	tmpl *template.Template
	hub  *hub
	byID map[string]Feed
}

// New builds a Server. It does not start listening; call Run.
func New(cfg Config) *Server {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	s := &Server{
		cfg:  cfg,
		log:  cfg.Log,
		now:  now,
		tmpl: template.Must(template.ParseFS(web.Templates, "templates/*.html")),
		hub:  newHub(),
		byID: make(map[string]Feed, len(cfg.Feeds)),
	}
	for _, f := range cfg.Feeds {
		s.byID[f.Slug] = f
	}

	staticFS, _ := fs.Sub(web.Static, "static")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /view", s.handleViewFrag)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheControl(http.FileServerFS(staticFS))))
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /feeds/{name}", s.handleFeed)
	if cfg.DAVHandler != nil {
		mux.Handle("/dav/", cfg.DAVHandler)
	}

	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Handler returns the server's routes, for testing.
func (s *Server) Handler() http.Handler { return s.srv.Handler }

func (s *Server) displayLoc() *time.Location {
	if s.cfg.DisplayTZ != nil {
		return s.cfg.DisplayTZ
	}
	return time.Local
}

// cacheControl adds a day-long cache header to vendored static assets.
func cacheControl(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		h.ServeHTTP(w, r)
	})
}

// Run listens and serves until ctx is cancelled, then shuts down gracefully.
// It also runs the change watcher that drives SSE.
func (s *Server) Run(ctx context.Context) error {
	go s.watch(ctx)

	errc := make(chan error, 1)
	go func() {
		s.log.Info("http listening", "addr", s.srv.Addr)
		err := s.srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errc <- err
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errc
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Store.Ping(r.Context()); err != nil {
		s.log.Warn("healthz: store unreachable", "err", err)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSuffix(r.PathValue("name"), ".ics")
	feed, ok := s.byID[slug]
	if !ok {
		http.NotFound(w, r)
		return
	}

	now := s.now()
	rows, err := s.cfg.Store.Occurrences(r.Context(), store.OccurrenceQuery{
		From:  now.Add(-s.cfg.WindowBack),
		To:    now.Add(s.cfg.WindowFwd),
		Owner: feed.Owner,
	})
	if err != nil {
		s.log.Error("feed query failed", "slug", feed.Slug, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	occs := make([]icsout.Occurrence, 0, len(rows))
	var newest time.Time
	for _, row := range rows {
		if feed.Predicate != nil && !feed.Predicate(views.Subject{Owner: row.Owner, Tags: row.Tags}) {
			continue
		}
		if row.LastModified.After(newest) {
			newest = row.LastModified
		}
		occs = append(occs, icsout.Occurrence{
			UID:          icsout.OccurrenceUID(row.EventUID, row.Start),
			Owner:        row.Owner,
			CalendarName: row.CalendarName,
			Summary:      row.Summary,
			Location:     row.Location,
			Start:        row.Start,
			End:          row.End,
			AllDay:       row.AllDay,
			Status:       row.Status,
			Tags:         row.Tags,
			LastModified: row.LastModified,
		})
	}

	cal := icsout.Calendar(occs, icsout.Options{Name: feed.Name, SummaryPrefix: s.cfg.SummaryPrefix, Now: now})
	var buf strings.Builder
	if err := icsout.Encode(&buf, cal); err != nil {
		s.log.Error("feed encode failed", "slug", feed.Slug, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	body := buf.String()

	etag := fmt.Sprintf(`"%x"`, sha1.Sum([]byte(body)))
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if !newest.IsZero() {
		w.Header().Set("Last-Modified", newest.UTC().Format(http.TimeFormat))
	}
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write([]byte(body))
}
