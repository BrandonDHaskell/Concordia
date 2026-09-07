package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/store"
)

func seedForOccurrences(t *testing.T, dbPath string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	acct, _ := s.UpsertAccount(ctx, model.Account{
		Person: "brandon", Provider: model.ProviderGoogle,
		DisplayName: "Gmail", CredentialRef: "gmail", Enabled: true,
	})
	work, _ := s.UpsertCalendar(ctx, model.Calendar{AccountID: acct.ID, RemoteID: "w", DisplayName: "Work", Enabled: true})
	fam, _ := s.UpsertCalendar(ctx, model.Calendar{AccountID: acct.ID, RemoteID: "f", DisplayName: "Family", Enabled: true})

	mk := func(cal model.Calendar, rid, summary, owner string, offset int, tags ...string) {
		start := time.Now().AddDate(0, 0, offset)
		err := s.WithTx(ctx, func(tx *sql.Tx) error {
			id, err := s.UpsertEvent(ctx, tx, model.Event{
				CalendarID: cal.ID, RemoteID: rid, UID: rid, Status: model.StatusConfirmed,
				Start: start, End: start.Add(time.Hour),
			})
			if err != nil {
				return err
			}
			return s.ReplaceOccurrences(ctx, tx, id, []model.Occurrence{{
				EventID: id, Owner: owner, Summary: summary, Tags: tags,
				Start: start, End: start.Add(time.Hour),
			}})
		})
		if err != nil {
			t.Fatalf("seed %s: %v", rid, err)
		}
	}

	mk(work, "w1", "Standup", "brandon", 1, "work")
	mk(fam, "f1", "Soccer", "brandon", 2, "kids", "tomato")
	mk(fam, "f2", "Dinner", "kim", 3)
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := fn()
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	return string(out), err
}

func TestRunOccurrences(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "c.db")
	seedForOccurrences(t, dbPath)

	cfgPath := filepath.Join(dir, "config.toml")
	os.WriteFile(cfgPath, []byte(`
[server]
listen = "127.0.0.1:8096"
database = "`+dbPath+`"
window_back = "60d"
window_fwd = "400d"

[[view]]
name = "no-work"
predicate = "not tag:work"

[[view]]
name = "brandons-kids"
predicate = "tag:kids and owner:brandon"
`), 0o600)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := func(args ...string) string {
		out, err := captureStdout(t, func() error {
			return runOccurrences(context.Background(), append([]string{"--config", cfgPath}, args...), log)
		})
		if err != nil {
			t.Fatalf("runOccurrences %v: %v", args, err)
		}
		return out
	}

	all := run()
	for _, want := range []string{"Standup", "Soccer", "Dinner"} {
		if !strings.Contains(all, want) {
			t.Errorf("default listing missing %q:\n%s", want, all)
		}
	}

	mine := run("--owner", "brandon")
	if strings.Contains(mine, "Dinner") || !strings.Contains(mine, "Soccer") {
		t.Errorf("--owner brandon wrong:\n%s", mine)
	}

	noWork := run("--view", "no-work")
	if strings.Contains(noWork, "Standup") || !strings.Contains(noWork, "Soccer") || !strings.Contains(noWork, "Dinner") {
		t.Errorf("--view no-work wrong:\n%s", noWork)
	}

	kids := run("--view", "brandons-kids")
	if !strings.Contains(kids, "Soccer") || strings.Contains(kids, "Dinner") || strings.Contains(kids, "Standup") {
		t.Errorf("--view brandons-kids wrong:\n%s", kids)
	}

	tagged := run("--tag", "tomato")
	if !strings.Contains(tagged, "Soccer") || strings.Contains(tagged, "Standup") {
		t.Errorf("--tag tomato wrong:\n%s", tagged)
	}
	if !strings.Contains(tagged, "#kids #tomato") {
		t.Errorf("tag suffix missing:\n%s", tagged)
	}
}
