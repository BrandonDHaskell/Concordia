package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
)

const timeLayout = time.RFC3339

// UpsertAccount inserts a, or updates the existing account with the same
// credential_ref, and returns it with ID populated.
func (s *Store) UpsertAccount(ctx context.Context, a model.Account) (model.Account, error) {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO accounts (person, provider, display_name, credential_ref, enabled)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (credential_ref) DO UPDATE SET
			person       = excluded.person,
			provider     = excluded.provider,
			display_name = excluded.display_name,
			enabled      = excluded.enabled
		RETURNING id`,
		a.Person, a.Provider, a.DisplayName, a.CredentialRef, boolToInt(a.Enabled),
	).Scan(&a.ID)
	if err != nil {
		return model.Account{}, fmt.Errorf("store: upsert account %q: %w", a.CredentialRef, err)
	}
	return a, nil
}

// AccountByCredentialRef looks up an account by its credential_ref. It returns
// ErrNotFound if there is no such account.
func (s *Store) AccountByCredentialRef(ctx context.Context, ref string) (model.Account, error) {
	var a model.Account
	var enabled int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, person, provider, display_name, credential_ref, enabled
		FROM accounts WHERE credential_ref = ?`, ref,
	).Scan(&a.ID, &a.Person, &a.Provider, &a.DisplayName, &a.CredentialRef, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Account{}, fmt.Errorf("store: account %q: %w", ref, ErrNotFound)
	}
	if err != nil {
		return model.Account{}, fmt.Errorf("store: account %q: %w", ref, err)
	}
	a.Enabled = enabled != 0
	return a, nil
}

// UpsertCalendar inserts c, or updates the existing calendar with the same
// (account_id, remote_id). It only writes discovery fields: display_name,
// redact, and enabled. sync_token, last_sync_at, and last_error belong to the
// sync path and are left untouched.
func (s *Store) UpsertCalendar(ctx context.Context, c model.Calendar) (model.Calendar, error) {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO calendars (account_id, remote_id, display_name, redact, enabled)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (account_id, remote_id) DO UPDATE SET
			display_name = excluded.display_name,
			redact       = excluded.redact,
			enabled      = excluded.enabled
		RETURNING id`,
		c.AccountID, c.RemoteID, c.DisplayName, boolToInt(c.Redact), boolToInt(c.Enabled),
	).Scan(&c.ID)
	if err != nil {
		return model.Calendar{}, fmt.Errorf("store: upsert calendar %q: %w", c.RemoteID, err)
	}
	return c, nil
}

// Calendars returns every calendar for an account, ordered by display name.
func (s *Store) Calendars(ctx context.Context, accountID int64) ([]model.Calendar, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, account_id, remote_id, display_name,
		       COALESCE(sync_token, ''), COALESCE(last_sync_at, ''),
		       COALESCE(last_error, ''), redact, enabled
		FROM calendars WHERE account_id = ? ORDER BY display_name`, accountID)
	if err != nil {
		return nil, fmt.Errorf("store: list calendars for account %d: %w", accountID, err)
	}
	return scanCalendars(rows)
}

// EnabledCalendars returns every enabled calendar whose account is also
// enabled, ordered by account then display name. This is the sync worklist.
func (s *Store) EnabledCalendars(ctx context.Context) ([]model.Calendar, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.account_id, c.remote_id, c.display_name,
		       COALESCE(c.sync_token, ''), COALESCE(c.last_sync_at, ''),
		       COALESCE(c.last_error, ''), c.redact, c.enabled
		FROM calendars c
		JOIN accounts a ON a.id = c.account_id
		WHERE c.enabled = 1 AND a.enabled = 1
		ORDER BY c.account_id, c.display_name`)
	if err != nil {
		return nil, fmt.Errorf("store: list enabled calendars: %w", err)
	}
	return scanCalendars(rows)
}

// SetSyncToken records a successful sync of a calendar: the new delta token,
// the sync time, and a cleared error. It takes a transaction because the token
// must be persisted together with the event data it describes (invariant 9).
func (s *Store) SetSyncToken(ctx context.Context, tx *sql.Tx, calID int64, token string, syncedAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE calendars SET sync_token = ?, last_sync_at = ?, last_error = ''
		WHERE id = ?`, token, syncedAt.UTC().Format(timeLayout), calID)
	if err != nil {
		return fmt.Errorf("store: set sync token for calendar %d: %w", calID, err)
	}
	return nil
}

// SetCalendarError records that a calendar's last sync failed. The sync token
// is left in place so the next attempt resumes from the same cursor.
func (s *Store) SetCalendarError(ctx context.Context, calID int64, msg string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE calendars SET last_error = ?, last_sync_at = ? WHERE id = ?`,
		msg, time.Now().UTC().Format(timeLayout), calID)
	if err != nil {
		return fmt.Errorf("store: set error for calendar %d: %w", calID, err)
	}
	return nil
}

func scanCalendars(rows *sql.Rows) ([]model.Calendar, error) {
	defer rows.Close()

	var out []model.Calendar
	for rows.Next() {
		var (
			c              model.Calendar
			lastSyncAt     string
			redact, enable int
		)
		if err := rows.Scan(
			&c.ID, &c.AccountID, &c.RemoteID, &c.DisplayName,
			&c.SyncToken, &lastSyncAt, &c.LastError, &redact, &enable,
		); err != nil {
			return nil, fmt.Errorf("store: scan calendar: %w", err)
		}
		if lastSyncAt != "" {
			t, err := time.Parse(timeLayout, lastSyncAt)
			if err != nil {
				return nil, fmt.Errorf("store: parse last_sync_at %q: %w", lastSyncAt, err)
			}
			c.LastSyncAt = t
		}
		c.Redact = redact != 0
		c.Enabled = enable != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
