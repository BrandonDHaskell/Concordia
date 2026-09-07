// Package google is the read-only Google Calendar provider. It fetches changes
// with events.list and a syncToken, handles token invalidation (HTTP 410) by
// doing a full window resync, and returns provider vocabulary only.
package google

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/provider"
)

// statusCancelled is Google's marker for a deleted or cancelled event, both in
// a full listing (showDeleted) and in a delta.
const statusCancelled = "cancelled"

// defaultCallTimeout bounds a single events.list round trip. Every provider
// call gets a timeout.
const defaultCallTimeout = 30 * time.Second

// Provider talks to one Google account. Sync may be called for any calendar in
// that account.
type Provider struct {
	svc         *gcal.Service
	windowBack  time.Duration
	windowFwd   time.Duration
	callTimeout time.Duration
	log         *slog.Logger
	now         func() time.Time
}

// Option configures a Provider.
type Option func(*Provider)

// WithLogger sets the structured logger. The default is slog.Default.
func WithLogger(l *slog.Logger) Option { return func(p *Provider) { p.log = l } }

// WithClock overrides the time source, for tests.
func WithClock(now func() time.Time) Option { return func(p *Provider) { p.now = now } }

// New builds a provider on an already-authenticated HTTP client. windowBack and
// windowFwd bound a full sync around now.
func New(ctx context.Context, client *http.Client, windowBack, windowFwd time.Duration, opts ...Option) (*Provider, error) {
	svc, err := gcal.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("google: building calendar service: %w", err)
	}
	return newProvider(svc, windowBack, windowFwd, opts...), nil
}

func newProvider(svc *gcal.Service, windowBack, windowFwd time.Duration, opts ...Option) *Provider {
	p := &Provider{
		svc:         svc,
		windowBack:  windowBack,
		windowFwd:   windowFwd,
		callTimeout: defaultCallTimeout,
		log:         slog.Default(),
		now:         time.Now,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Calendars lists the account's calendars for the caller to persist. Only
// RemoteID, DisplayName, and Enabled are populated.
func (p *Provider) Calendars(ctx context.Context) ([]model.Calendar, error) {
	ctx, cancel := context.WithTimeout(ctx, p.callTimeout)
	defer cancel()

	var out []model.Calendar
	err := p.svc.CalendarList.List().Context(ctx).Pages(ctx, func(page *gcal.CalendarList) error {
		for _, e := range page.Items {
			name := e.SummaryOverride
			if name == "" {
				name = e.Summary
			}
			out = append(out, model.Calendar{
				RemoteID:    e.Id,
				DisplayName: name,
				Enabled:     true,
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("google: listing calendars: %w", err)
	}
	return out, nil
}

// Sync returns changes to cal since token. An empty token means a full window
// sync. On a 410 (dead token) it clears the token, does a full resync, and sets
// Delta.FullResync.
func (p *Provider) Sync(ctx context.Context, cal model.Calendar, token string) (provider.Delta, string, error) {
	log := p.log.With("calendar_id", cal.RemoteID)

	d, next, err := p.list(ctx, cal, token)
	if err == nil {
		log.Debug("google sync ok",
			"changed", len(d.Changed), "deleted", len(d.Deleted), "full", token == "")
		return d, next, nil
	}

	if token != "" && invalidSyncToken(err) {
		log.Info("google sync token invalid, doing full resync")
		d, next, err = p.list(ctx, cal, "")
		if err != nil {
			return provider.Delta{}, "", err
		}
		d.FullResync = true
		log.Debug("google full resync ok", "changed", len(d.Changed), "deleted", len(d.Deleted))
		return d, next, nil
	}

	return provider.Delta{}, "", err
}

func (p *Provider) list(ctx context.Context, cal model.Calendar, token string) (provider.Delta, string, error) {
	ctx, cancel := context.WithTimeout(ctx, p.callTimeout)
	defer cancel()

	call := p.svc.Events.List(cal.RemoteID).
		Context(ctx).
		ShowDeleted(true).
		SingleEvents(false).
		MaxResults(2500)

	if token == "" {
		now := p.now()
		call = call.
			TimeMin(now.Add(-p.windowBack).Format(time.RFC3339)).
			TimeMax(now.Add(p.windowFwd).Format(time.RFC3339))
	} else {
		call = call.SyncToken(token)
	}

	var (
		d    provider.Delta
		next string
	)
	err := call.Pages(ctx, func(page *gcal.Events) error {
		for _, item := range page.Items {
			if item.Status == statusCancelled {
				d.Deleted = append(d.Deleted, item.Id)
				continue
			}
			body, err := item.MarshalJSON()
			if err != nil {
				return fmt.Errorf("marshalling event %s: %w", item.Id, err)
			}
			d.Changed = append(d.Changed, provider.RawEvent{
				RemoteID: item.Id,
				ETag:     item.Etag,
				Body:     body,
			})
		}
		if page.NextSyncToken != "" {
			next = page.NextSyncToken
		}
		return nil
	})
	if err != nil {
		return provider.Delta{}, "", fmt.Errorf("google: events.list for %s: %w", cal.RemoteID, err)
	}
	return d, next, nil
}

// invalidSyncToken reports whether err is Google's "sync token no longer valid"
// (HTTP 410), which requires a full resync.
func invalidSyncToken(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusGone
	}
	return false
}
