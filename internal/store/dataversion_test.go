package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
)

func TestDataVersionChangesOnExternalWrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "concordia.db")

	writer, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// A second handle stands in for the daemon polling while sync-once writes.
	reader, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	v0, err := reader.DataVersion(ctx)
	if err != nil {
		t.Fatalf("DataVersion: %v", err)
	}

	// No write yet: stable.
	if v1, _ := reader.DataVersion(ctx); v1 != v0 {
		t.Errorf("data_version changed with no write: %d -> %d", v0, v1)
	}

	acct, _ := writer.UpsertAccount(ctx, model.Account{
		Person: "b", Provider: model.ProviderGoogle,
		DisplayName: "G", CredentialRef: "g", Enabled: true,
	})
	cal, _ := writer.UpsertCalendar(ctx, model.Calendar{
		AccountID: acct.ID, RemoteID: "c", DisplayName: "C", Enabled: true,
	})
	err = writer.WithTx(ctx, func(tx *sql.Tx) error {
		_, e := writer.UpsertEvent(ctx, tx, model.Event{
			CalendarID: cal.ID, RemoteID: "e", UID: "e", Status: model.StatusConfirmed,
			Start: time.Now(), End: time.Now().Add(time.Hour),
		})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}

	v2, err := reader.DataVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v2 == v0 {
		t.Errorf("data_version did not change after an external write (still %d)", v0)
	}
}
