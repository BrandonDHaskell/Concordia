// Package httpd serves the Concordia web surface. For now that is only a
// health endpoint; the agenda view, SSE, and feeds arrive in later milestones.
package httpd

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Health is the subset of the store the health check needs.
type Health interface {
	Ping(ctx context.Context) error
}

// Server wraps an http.Server with Concordia's routes and lifecycle.
type Server struct {
	srv    *http.Server
	log    *slog.Logger
	health Health
}

// New builds a Server listening on addr. It does not start listening; call Run.
func New(addr string, log *slog.Logger, health Health) *Server {
	s := &Server{
		log:    log,
		health: health,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Run listens and serves until ctx is cancelled, then shuts down gracefully.
// It returns nil on a clean shutdown.
func (s *Server) Run(ctx context.Context) error {
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
	if err := s.health.Ping(r.Context()); err != nil {
		s.log.Warn("healthz: store unreachable", "err", err)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
