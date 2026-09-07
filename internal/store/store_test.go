package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "concordia.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenSetsPragmas(t *testing.T) {
	s := openTemp(t)

	var journal string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}

	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestMigrateCreatesSchema(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, table := range []string{"accounts", "calendars", "events", "occurrences", "occurrence_tags", "views"} {
		var name string
		err := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not created: %v", table, err)
		}
	}

	var version string
	if err := s.db.QueryRow(
		`SELECT version FROM schema_migrations ORDER BY version LIMIT 1`,
	).Scan(&version); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if version != "0001_init" {
		t.Errorf("first migration recorded as %q, want 0001_init", version)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate #1: %v", err)
	}
	var after1 int
	if err := s.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&after1); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if after1 == 0 {
		t.Fatal("no migrations recorded")
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate #2: %v", err)
	}
	var after2 int
	if err := s.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&after2); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if after2 != after1 {
		t.Errorf("second Migrate changed row count: %d -> %d", after1, after2)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO calendars (account_id, remote_id, display_name) VALUES (999, 'r', 'd')`)
	if err == nil {
		t.Fatal("insert with dangling account_id succeeded, foreign keys not enforced")
	}
}
