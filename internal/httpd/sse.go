package httpd

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// hub fans one change signal out to all connected SSE clients. A subscriber
// that cannot keep up is skipped for that message, never blocked.
type hub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func newHub() *hub {
	return &hub{subs: make(map[chan string]struct{})}
}

func (h *hub) subscribe() chan string {
	ch := make(chan string, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *hub) broadcast(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

const defaultWatchInterval = 2 * time.Second

// watch polls the database change version and broadcasts on every change, so
// the web view refreshes shortly after a sync-once run. It returns when ctx is
// cancelled.
func (s *Server) watch(ctx context.Context) {
	interval := s.cfg.WatchInterval
	if interval <= 0 {
		interval = defaultWatchInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	last, _ := s.cfg.Store.DataVersion(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			v, err := s.cfg.Store.DataVersion(ctx)
			if err != nil {
				s.log.Warn("watch: data_version failed", "err", err)
				continue
			}
			if v != last {
				last = v
				s.hub.broadcast(fmt.Sprint(v))
			}
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")

	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

	fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "event: change\ndata: %s\n\n", msg)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
