// Package syncer runs one calendar through the pipeline: provider delta,
// normalize, persist, expand. It performs the writes of one sync in a single
// transaction so the new sync token lands with the data it describes
// (invariant 9). Scheduling, backoff, and jitter are not here yet.
package syncer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/bhaskell/Concordia/internal/expand"
	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/provider"
	"github.com/bhaskell/Concordia/internal/store"
)

// Normalizer turns a raw provider event into a model.Event. normalize.Event
// satisfies it.
type Normalizer func(providerKind string, raw provider.RawEvent, cal model.Calendar) (model.Event, error)

// Result summarizes one calendar sync.
type Result struct {
	Changed     int
	Deleted     int
	Occurrences int
	FullResync  bool
	NextToken   string
}

// Syncer holds the pieces shared across calendars.
type Syncer struct {
	Store  *store.Store
	Window model.Window
	Log    *slog.Logger
}

// Calendar syncs one calendar. providerKind selects the normalizer path; owner
// is stamped on every occurrence. On a provider error nothing is written and
// the caller should record it with store.SetCalendarError.
func (s *Syncer) Calendar(ctx context.Context, p provider.Provider, norm Normalizer, cal model.Calendar, providerKind, owner string) (Result, error) {
	log := s.log().With("calendar_id", cal.RemoteID)

	delta, next, err := p.Sync(ctx, cal, cal.SyncToken)
	if err != nil {
		return Result{}, fmt.Errorf("syncer: provider sync: %w", err)
	}

	normalized := make([]model.Event, 0, len(delta.Changed))
	touched := make(map[string]struct{})
	for _, rawEv := range delta.Changed {
		ev, err := norm(providerKind, rawEv, cal)
		if err != nil {
			return Result{}, fmt.Errorf("syncer: normalize %s: %w", rawEv.RemoteID, err)
		}
		ev.CalendarID = cal.ID
		normalized = append(normalized, ev)
		touched[ev.UID] = struct{}{}
	}

	res := Result{
		Changed:    len(delta.Changed),
		Deleted:    len(delta.Deleted),
		FullResync: delta.FullResync,
		NextToken:  next,
	}

	err = s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, ev := range normalized {
			if _, err := s.Store.UpsertEvent(ctx, tx, ev); err != nil {
				return err
			}
		}

		for _, remoteID := range delta.Deleted {
			uid, err := s.Store.TombstoneEvent(ctx, tx, cal.ID, remoteID)
			if err != nil {
				return err
			}
			if uid != "" {
				touched[uid] = struct{}{}
			}
		}

		if delta.FullResync {
			extra, err := s.reconcile(ctx, tx, cal.ID, normalized, touched)
			if err != nil {
				return err
			}
			res.Deleted += extra
		}

		occCount, err := s.reexpand(ctx, tx, cal.ID, touched, owner)
		if err != nil {
			return err
		}
		res.Occurrences = occCount

		return s.Store.SetSyncToken(ctx, tx, cal.ID, next, time.Now())
	})
	if err != nil {
		return Result{}, fmt.Errorf("syncer: persisting %s: %w", cal.RemoteID, err)
	}

	log.Debug("calendar synced",
		"changed", res.Changed, "deleted", res.Deleted,
		"occurrences", res.Occurrences, "full", res.FullResync)
	return res, nil
}

// reconcile tombstones live events absent from a full-resync payload.
func (s *Syncer) reconcile(ctx context.Context, tx *sql.Tx, calID int64, present []model.Event, touched map[string]struct{}) (int, error) {
	live, err := s.Store.LiveEvents(ctx, tx, calID)
	if err != nil {
		return 0, err
	}
	keep := make(map[string]struct{}, len(present))
	for _, ev := range present {
		keep[ev.RemoteID] = struct{}{}
	}

	deleted := 0
	for remoteID := range live {
		if _, ok := keep[remoteID]; ok {
			continue
		}
		uid, err := s.Store.TombstoneEvent(ctx, tx, calID, remoteID)
		if err != nil {
			return 0, err
		}
		if uid != "" {
			touched[uid] = struct{}{}
		}
		deleted++
	}
	return deleted, nil
}

// reexpand rebuilds occurrences for every touched series.
func (s *Syncer) reexpand(ctx context.Context, tx *sql.Tx, calID int64, touched map[string]struct{}, owner string) (int, error) {
	if len(touched) == 0 {
		return 0, nil
	}
	uids := make([]string, 0, len(touched))
	for uid := range touched {
		uids = append(uids, uid)
	}

	events, err := s.Store.EventsByUIDs(ctx, tx, calID, uids)
	if err != nil {
		return 0, err
	}

	occs, err := expand.Expand(events, s.Window, owner)
	if err != nil {
		return 0, fmt.Errorf("expand: %w", err)
	}
	byEvent := make(map[int64][]model.Occurrence, len(events))
	for _, o := range occs {
		byEvent[o.EventID] = append(byEvent[o.EventID], o)
	}

	total := 0
	for _, ev := range events {
		set := byEvent[ev.ID]
		if err := s.Store.ReplaceOccurrences(ctx, tx, ev.ID, set); err != nil {
			return 0, err
		}
		total += len(set)
	}
	return total, nil
}

func (s *Syncer) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}
