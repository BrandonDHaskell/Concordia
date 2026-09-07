package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/bhaskell/Concordia/migrations"
)

// migrationSource is the set of migration files to apply. It is a package
// variable so tests can substitute a fixture set.
var migrationSource fs.FS = migrations.FS

// Migrate applies every embedded migration that has not yet been recorded in
// schema_migrations, each in its own transaction, in filename order. It is safe
// to call on every startup.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}

	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return err
	}

	files, err := migrationFiles()
	if err != nil {
		return err
	}

	for _, name := range files {
		version := strings.TrimSuffix(name, ".up.sql")
		if applied[version] {
			continue
		}
		body, err := fs.ReadFile(migrationSource, name)
		if err != nil {
			return fmt.Errorf("store: read migration %s: %w", name, err)
		}
		if err := s.applyMigration(ctx, version, string(body)); err != nil {
			return fmt.Errorf("store: apply migration %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) appliedVersions(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("store: read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func (s *Store) applyMigration(ctx context.Context, version, body string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		version, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func migrationFiles() ([]string, error) {
	entries, err := fs.ReadDir(migrationSource, ".")
	if err != nil {
		return nil, fmt.Errorf("store: list migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return nil, errors.New("store: no migrations found")
	}
	sort.Strings(names)
	return names, nil
}
