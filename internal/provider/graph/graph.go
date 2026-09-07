// Package graph is the read-only Microsoft Graph calendar provider. It uses
// calendarView/delta, which expands recurrences server-side, so events reach
// the store as individual occurrences and the local expander is a no-op for
// them. Plain net/http, no SDK.
package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/provider"
)

const (
	defaultBaseURL     = "https://graph.microsoft.com/v1.0"
	defaultCallTimeout = 30 * time.Second
	defaultMaxRetries  = 5
	defaultMinWait     = time.Second
	defaultMaxWait     = 2 * time.Minute
)

// errSyncTokenExpired is Graph's "the delta token is too old" (HTTP 410),
// which requires a full resync.
var errSyncTokenExpired = errors.New("graph: sync token expired")

// Provider talks to one Microsoft account. Sync may be called for any calendar
// in that account.
type Provider struct {
	http        *http.Client
	baseURL     string
	windowBack  time.Duration
	windowFwd   time.Duration
	callTimeout time.Duration
	maxRetries  int
	minWait     time.Duration
	maxWait     time.Duration
	now         func() time.Time
	log         *slog.Logger
}

// Option configures a Provider.
type Option func(*Provider)

// WithEndpoint overrides the Graph base URL (tests, or an on-LAN proxy).
func WithEndpoint(u string) Option {
	return func(p *Provider) { p.baseURL = strings.TrimRight(u, "/") }
}

// WithClock overrides the time source, for tests.
func WithClock(now func() time.Time) Option { return func(p *Provider) { p.now = now } }

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option { return func(p *Provider) { p.log = l } }

// New builds a provider on an already-authenticated HTTP client.
func New(client *http.Client, windowBack, windowFwd time.Duration, opts ...Option) *Provider {
	p := &Provider{
		http:        client,
		baseURL:     defaultBaseURL,
		windowBack:  windowBack,
		windowFwd:   windowFwd,
		callTimeout: defaultCallTimeout,
		maxRetries:  defaultMaxRetries,
		minWait:     defaultMinWait,
		maxWait:     defaultMaxWait,
		now:         time.Now,
		log:         slog.Default(),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Sync returns changes to cal since token. An empty token means a full window
// sync. On a 410 (dead delta token) it clears the token, does a full resync,
// and sets Delta.FullResync.
func (p *Provider) Sync(ctx context.Context, cal model.Calendar, token string) (provider.Delta, string, error) {
	log := p.log.With("calendar_id", cal.RemoteID)

	d, next, err := p.delta(ctx, cal, token)
	if err == nil {
		log.Debug("graph sync ok", "changed", len(d.Changed), "deleted", len(d.Deleted), "full", token == "")
		return d, next, nil
	}
	if token != "" && errors.Is(err, errSyncTokenExpired) {
		log.Info("graph delta token invalid, doing full resync")
		d, next, err = p.delta(ctx, cal, "")
		if err != nil {
			return provider.Delta{}, "", err
		}
		d.FullResync = true
		return d, next, nil
	}
	return provider.Delta{}, "", err
}

func (p *Provider) delta(ctx context.Context, cal model.Calendar, token string) (provider.Delta, string, error) {
	next := p.firstDeltaURL(cal.RemoteID, token)

	var d provider.Delta
	for next != "" {
		page, err := p.get(ctx, next)
		if err != nil {
			return provider.Delta{}, "", err
		}
		for _, raw := range page.Value {
			var m itemMeta
			if err := json.Unmarshal(raw, &m); err != nil {
				return provider.Delta{}, "", fmt.Errorf("graph: decoding delta item: %w", err)
			}
			if m.ID == "" {
				continue
			}
			if m.Removed != nil || m.IsCancelled {
				d.Deleted = append(d.Deleted, m.ID)
				continue
			}
			if m.Type == "seriesMaster" {
				// calendarView already expands the series; the master is not
				// an occurrence and must not be stored.
				continue
			}
			d.Changed = append(d.Changed, provider.RawEvent{
				RemoteID: m.ID,
				ETag:     m.ETag,
				Body:     append([]byte(nil), raw...),
			})
		}

		if page.NextLink != "" {
			next = page.NextLink
			continue
		}
		return d, deltaToken(page.DeltaLink), nil
	}
	return d, "", nil
}

func (p *Provider) firstDeltaURL(calendarID, token string) string {
	base := fmt.Sprintf("%s/me/calendars/%s/calendarView/delta", p.baseURL, url.PathEscape(calendarID))
	q := url.Values{}
	if token == "" {
		now := p.now().UTC()
		q.Set("startDateTime", now.Add(-p.windowBack).Format(time.RFC3339))
		q.Set("endDateTime", now.Add(p.windowFwd).Format(time.RFC3339))
	} else {
		q.Set("$deltatoken", token)
	}
	return base + "?" + q.Encode()
}

// Calendars lists the account's calendars for the caller to persist.
func (p *Provider) Calendars(ctx context.Context) ([]model.Calendar, error) {
	next := p.baseURL + "/me/calendars"
	var out []model.Calendar
	for next != "" {
		var page struct {
			Value []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"value"`
			NextLink string `json:"@odata.nextLink"`
		}
		resp, err := p.getRaw(ctx, next)
		if err != nil {
			return nil, err
		}
		err = json.Unmarshal(resp, &page)
		if err != nil {
			return nil, fmt.Errorf("graph: decoding calendars: %w", err)
		}
		for _, c := range page.Value {
			out = append(out, model.Calendar{RemoteID: c.ID, DisplayName: c.Name, Enabled: true})
		}
		next = page.NextLink
	}
	return out, nil
}

type deltaResponse struct {
	Value     []json.RawMessage `json:"value"`
	NextLink  string            `json:"@odata.nextLink"`
	DeltaLink string            `json:"@odata.deltaLink"`
}

type itemMeta struct {
	ID          string       `json:"id"`
	ETag        string       `json:"@odata.etag"`
	Type        string       `json:"type"`
	IsCancelled bool         `json:"isCancelled"`
	Removed     *removedInfo `json:"@removed"`
}

type removedInfo struct {
	Reason string `json:"reason"`
}

func (p *Provider) get(ctx context.Context, u string) (*deltaResponse, error) {
	body, err := p.getRaw(ctx, u)
	if err != nil {
		return nil, err
	}
	var dr deltaResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return nil, fmt.Errorf("graph: decoding delta page: %w", err)
	}
	return &dr, nil
}

// getRaw performs one GET with the Graph headers, retrying on throttling.
func (p *Provider) getRaw(ctx context.Context, u string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		body, retry, wait, err := p.attempt(ctx, u)
		if err != nil {
			return nil, err
		}
		if !retry {
			return body, nil
		}
		if attempt >= p.maxRetries {
			return nil, fmt.Errorf("graph: still throttled after %d retries", attempt)
		}
		p.log.Warn("graph throttled, backing off", "wait", wait.String())
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (p *Provider) attempt(ctx context.Context, u string) (body []byte, retry bool, wait time.Duration, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, p.callTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return nil, false, 0, fmt.Errorf("graph: building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Prefer", `outlook.timezone="UTC", odata.maxpagesize=100`)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, false, 0, fmt.Errorf("graph: request failed: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, false, 0, fmt.Errorf("graph: reading body: %w", err)
		}
		return b, false, 0, nil

	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
		return nil, true, p.retryAfter(resp.Header.Get("Retry-After")), nil

	case resp.StatusCode == http.StatusGone:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, false, 0, fmt.Errorf("graph: %s: %w", strings.TrimSpace(string(snippet)), errSyncTokenExpired)

	default:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, false, 0, fmt.Errorf("graph: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
}

// retryAfter parses a Retry-After header (delta seconds or HTTP date) and
// clamps it to [minWait, maxWait]. A missing or unparseable value backs off by
// five times minWait.
func (p *Provider) retryAfter(h string) time.Duration {
	fallback := 5 * p.minWait
	h = strings.TrimSpace(h)
	if h == "" {
		return p.clampWait(fallback)
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return p.clampWait(time.Duration(secs) * time.Second)
	}
	if t, err := http.ParseTime(h); err == nil {
		return p.clampWait(time.Until(t))
	}
	return p.clampWait(fallback)
}

func (p *Provider) clampWait(d time.Duration) time.Duration {
	switch {
	case d < p.minWait:
		return p.minWait
	case d > p.maxWait:
		return p.maxWait
	default:
		return d
	}
}

// deltaToken extracts the $deltatoken value from an @odata.deltaLink URL.
func deltaToken(deltaLink string) string {
	if deltaLink == "" {
		return ""
	}
	u, err := url.Parse(deltaLink)
	if err != nil {
		return ""
	}
	return u.Query().Get("$deltatoken")
}
