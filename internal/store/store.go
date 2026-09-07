// Package store owns all SQLite access: connection setup, schema migrations,
// and typed queries. It returns model types, never raw rows and never provider
// types.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store is a handle to the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path in WAL mode with
// foreign keys enforced and a busy timeout set. It does not run migrations;
// call Migrate for that.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("store: empty database path")
	}

	// modernc.org/sqlite takes pragmas as DSN query parameters, applied to
	// every connection in the pool.
	dsn := path + "?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}

	return &Store{db: db}, nil
}

// Ping verifies the database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// DB exposes the underlying handle for packages that run their own queries
// within the store layer's own tests. It is not for use outside internal/store.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}
