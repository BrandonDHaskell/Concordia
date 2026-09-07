package httpd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubHealth struct{ err error }

func (s stubHealth) Ping(context.Context) error { return s.err }

func newTestServer(t *testing.T, h Health) *Server {
	t.Helper()
	return New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)), h)
}

func TestHealthzOK(t *testing.T) {
	s := newTestServer(t, stubHealth{})
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body, _ := io.ReadAll(rec.Body); string(body) != "ok\n" {
		t.Errorf("body = %q", body)
	}
}

func TestHealthzStoreDown(t *testing.T) {
	s := newTestServer(t, stubHealth{err: errors.New("boom")})
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestHealthzRejectsPost(t *testing.T) {
	s := newTestServer(t, stubHealth{})
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestRunShutsDownOnContextCancel(t *testing.T) {
	s := newTestServer(t, stubHealth{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v, want nil on clean shutdown", err)
	}
}
