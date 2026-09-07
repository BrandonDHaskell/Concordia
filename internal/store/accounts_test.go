package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
)

func setSyncToken(t *testing.T, ctx context.Context, s *Store, calID int64, token string, at time.Time) {
	t.Helper()
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		return s.SetSyncToken(ctx, tx, calID, token, at)
	})
	if err != nil {
		t.Fatalf("SetSyncToken: %v", err)
	}
}

func migratedStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s := openTemp(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s, ctx
}

func TestUpsertAccountInsertThenUpdate(t *testing.T) {
	s, ctx := migratedStore(t)

	a, err := s.UpsertAccount(ctx, model.Account{
		Person: "brandon", Provider: model.ProviderGoogle,
		DisplayName: "Gmail", CredentialRef: "gmail", Enabled: true,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("insert returned zero ID")
	}

	updated, err := s.UpsertAccount(ctx, model.Account{
		Person: "brandon", Provider: model.ProviderGoogle,
		DisplayName: "Personal Gmail", CredentialRef: "gmail", Enabled: false,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.ID != a.ID {
		t.Errorf("update created a new row: %d != %d", updated.ID, a.ID)
	}

	got, err := s.AccountByCredentialRef(ctx, "gmail")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.DisplayName != "Personal Gmail" || got.Enabled {
		t.Errorf("update not applied: %+v", got)
	}
}

func TestAccountByCredentialRefNotFound(t *testing.T) {
	s, ctx := migratedStore(t)
	_, err := s.AccountByCredentialRef(ctx, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUpsertCalendarPreservesSyncState(t *testing.T) {
	s, ctx := migratedStore(t)
	acct, err := s.UpsertAccount(ctx, model.Account{
		Person: "b", Provider: model.ProviderGoogle,
		DisplayName: "G", CredentialRef: "g", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	cal, err := s.UpsertCalendar(ctx, model.Calendar{
		AccountID: acct.ID, RemoteID: "primary",
		DisplayName: "Primary", Enabled: true,
	})
	if err != nil {
		t.Fatalf("insert calendar: %v", err)
	}

	syncedAt := time.Now().UTC().Truncate(time.Second)
	setSyncToken(t, ctx, s, cal.ID, "tok-123", syncedAt)

	// A later discovery pass re-upserts the calendar with a new name.
	if _, err := s.UpsertCalendar(ctx, model.Calendar{
		AccountID: acct.ID, RemoteID: "primary",
		DisplayName: "Primary (renamed)", Redact: true, Enabled: true,
	}); err != nil {
		t.Fatalf("re-upsert calendar: %v", err)
	}

	cals, err := s.Calendars(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cals) != 1 {
		t.Fatalf("got %d calendars, want 1", len(cals))
	}
	got := cals[0]
	if got.DisplayName != "Primary (renamed)" || !got.Redact {
		t.Errorf("discovery fields not updated: %+v", got)
	}
	if got.SyncToken != "tok-123" {
		t.Errorf("sync_token clobbered by upsert: %q", got.SyncToken)
	}
	if !got.LastSyncAt.Equal(syncedAt) {
		t.Errorf("last_sync_at = %v, want %v", got.LastSyncAt, syncedAt)
	}
}

func TestSetCalendarErrorKeepsToken(t *testing.T) {
	s, ctx := migratedStore(t)
	acct, _ := s.UpsertAccount(ctx, model.Account{
		Person: "b", Provider: model.ProviderGoogle, DisplayName: "G",
		CredentialRef: "g", Enabled: true,
	})
	cal, _ := s.UpsertCalendar(ctx, model.Calendar{
		AccountID: acct.ID, RemoteID: "primary", DisplayName: "P", Enabled: true,
	})
	setSyncToken(t, ctx, s, cal.ID, "tok-abc", time.Now())

	if err := s.SetCalendarError(ctx, cal.ID, "410 gone"); err != nil {
		t.Fatalf("SetCalendarError: %v", err)
	}

	cals, _ := s.Calendars(ctx, acct.ID)
	if cals[0].LastError != "410 gone" {
		t.Errorf("LastError = %q", cals[0].LastError)
	}
	if cals[0].SyncToken != "tok-abc" {
		t.Errorf("SyncToken = %q, want it preserved", cals[0].SyncToken)
	}

	// A subsequent success clears the error.
	setSyncToken(t, ctx, s, cal.ID, "tok-def", time.Now())
	cals, _ = s.Calendars(ctx, acct.ID)
	if cals[0].LastError != "" {
		t.Errorf("LastError not cleared: %q", cals[0].LastError)
	}
}

func TestEnabledCalendarsFilters(t *testing.T) {
	s, ctx := migratedStore(t)

	on, _ := s.UpsertAccount(ctx, model.Account{
		Person: "b", Provider: model.ProviderGoogle, DisplayName: "On",
		CredentialRef: "on", Enabled: true,
	})
	off, _ := s.UpsertAccount(ctx, model.Account{
		Person: "b", Provider: model.ProviderGoogle, DisplayName: "Off",
		CredentialRef: "off", Enabled: false,
	})

	s.UpsertCalendar(ctx, model.Calendar{AccountID: on.ID, RemoteID: "a", DisplayName: "A", Enabled: true})
	s.UpsertCalendar(ctx, model.Calendar{AccountID: on.ID, RemoteID: "b", DisplayName: "B", Enabled: false})
	s.UpsertCalendar(ctx, model.Calendar{AccountID: off.ID, RemoteID: "c", DisplayName: "C", Enabled: true})

	cals, err := s.EnabledCalendars(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cals) != 1 || cals[0].RemoteID != "a" {
		t.Fatalf("EnabledCalendars = %+v, want just calendar a", cals)
	}
}

func TestWithTxRollback(t *testing.T) {
	s, ctx := migratedStore(t)
	acct, _ := s.UpsertAccount(ctx, model.Account{
		Person: "b", Provider: model.ProviderGoogle, DisplayName: "G",
		CredentialRef: "g", Enabled: true,
	})

	sentinel := errors.New("boom")
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO calendars (account_id, remote_id, display_name) VALUES (?, 'x', 'X')`,
			acct.ID); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx err = %v, want sentinel", err)
	}

	cals, _ := s.Calendars(ctx, acct.ID)
	if len(cals) != 0 {
		t.Errorf("rollback failed, %d calendars remain", len(cals))
	}
}
