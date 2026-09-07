#!/usr/bin/env bash
# Scaffold the Concordia repository tree.
# Idempotent: safe to re-run, never overwrites an existing file.
set -euo pipefail

PROJECT="${1:-Concordia}"
MODULE="${2:-github.com/bhaskell/${PROJECT}}"
BIN="${PROJECT}d"

mkdir -p "$PROJECT"
cd "$PROJECT"

dirs=(
  "cmd/${BIN}"
  internal/config
  internal/model
  internal/store
  internal/provider/google
  internal/provider/graph
  internal/provider/caldav
  internal/auth
  internal/normalize
  internal/expand
  internal/rules
  internal/views
  internal/syncer
  internal/icsout
  internal/davd
  internal/httpd
  migrations
  web/templates
  web/static
  deploy
  testdata/ics
  testdata/fixtures
  docs
)
mkdir -p "${dirs[@]}"

write() {
  # write <path> ; body on stdin ; never clobbers
  if [ -e "$1" ]; then
    echo "skip   $1"
  else
    cat > "$1"
    echo "create $1"
  fi
}

write go.mod <<EOF
module ${MODULE}

go 1.22
EOF

write .gitignore <<'EOF'
/bin/
*.db
*.db-wal
*.db-shm
config.toml
.env
/tmp/
EOF

write "cmd/${BIN}/main.go" <<EOF
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	log.Info("starting")

	<-ctx.Done()
	log.Info("shutting down")
}
EOF

write internal/provider/provider.go <<'EOF'
package provider

import "context"

// RawEvent is a provider payload plus enough identity to normalize it.
type RawEvent struct {
	RemoteID string
	ETag     string
	Body     []byte
}

// Delta is the result of an incremental sync.
type Delta struct {
	Changed    []RawEvent
	Deleted    []string // provider-local event IDs
	FullResync bool     // token was invalid; Changed is the complete window
}

// Provider fetches changes for a single remote calendar.
// Implementations are read-only. No implementation may issue a write.
type Provider interface {
	// Sync returns changes since token. An empty token means full sync.
	// The returned string is the next token to persist.
	Sync(ctx context.Context, calendarID, token string) (Delta, string, error)
}
EOF

write migrations/0001_init.up.sql <<'EOF'
PRAGMA foreign_keys = ON;

CREATE TABLE accounts (
  id             INTEGER PRIMARY KEY,
  person         TEXT NOT NULL,
  provider       TEXT NOT NULL,
  display_name   TEXT NOT NULL,
  credential_ref TEXT NOT NULL,
  enabled        INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE calendars (
  id           INTEGER PRIMARY KEY,
  account_id   INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  remote_id    TEXT NOT NULL,
  display_name TEXT NOT NULL,
  sync_token   TEXT,
  last_sync_at TEXT,
  last_error   TEXT,
  redact       INTEGER NOT NULL DEFAULT 0,
  enabled      INTEGER NOT NULL DEFAULT 1,
  UNIQUE(account_id, remote_id)
);

CREATE TABLE events (
  id            INTEGER PRIMARY KEY,
  calendar_id   INTEGER NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
  uid           TEXT NOT NULL,
  recurrence_id TEXT,
  summary       TEXT,
  location      TEXT,
  dtstart       TEXT NOT NULL,
  dtend         TEXT,
  is_all_day    INTEGER NOT NULL DEFAULT 0,
  tzid          TEXT,
  rrule         TEXT,
  exdates       TEXT,
  status        TEXT,
  etag          TEXT,
  raw           BLOB,
  updated_at    TEXT NOT NULL,
  deleted_at    TEXT,
  UNIQUE(calendar_id, uid, recurrence_id)
);

CREATE TABLE occurrences (
  id          INTEGER PRIMARY KEY,
  event_id    INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  start_utc   TEXT NOT NULL,
  end_utc     TEXT NOT NULL,
  start_local TEXT NOT NULL,
  is_all_day  INTEGER NOT NULL DEFAULT 0,
  summary     TEXT,
  location    TEXT,
  owner       TEXT NOT NULL
);

CREATE INDEX idx_occ_range ON occurrences(start_utc, end_utc);
CREATE INDEX idx_occ_owner ON occurrences(owner, start_utc);

CREATE TABLE occurrence_tags (
  occurrence_id INTEGER NOT NULL REFERENCES occurrences(id) ON DELETE CASCADE,
  tag           TEXT NOT NULL,
  PRIMARY KEY (occurrence_id, tag)
);

CREATE TABLE views (
  name          TEXT PRIMARY KEY,
  predicate_json TEXT NOT NULL,
  kind          TEXT NOT NULL DEFAULT 'both'
);
EOF

write deploy/config.example.toml <<'EOF'
[server]
listen      = "10.0.0.65:8080"
database    = "/var/lib/Concordia/Concordia.db"
window_back = "60d"
window_fwd  = "400d"

[[account]]
person   = "brandon"
provider = "icloud"
name     = "iCloud"
username = "brandon@example.com"
# app-specific password read from the credential file, not stored here

[[account]]
person   = "brandon"
provider = "google"
name     = "Gmail"

[[rule]]
match_calendar = "Work"
action         = "redact"

[[rule]]
match_title = "(?i)soccer|practice"
action      = "tag"
tag         = "kids"

[[view]]
name      = "no-work"
predicate = "not tag:work"

[[view]]
name      = "kids"
predicate = "tag:kids"
EOF

write "deploy/${BIN}.service" <<EOF
[Unit]
Description=Concordia household calendar aggregator
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/${BIN} -config /etc/Concordia/config.toml
Restart=on-failure
RestartSec=5

User=Concordia
Group=Concordia
StateDirectory=Concordia
ConfigurationDirectory=Concordia

NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_INET AF_INET6
LockPersonality=yes
MemoryDenyWriteExecute=yes

[Install]
WantedBy=multi-user.target
EOF

write Makefile <<EOF
BIN := ${BIN}
CONFIG ?= deploy/config.example.toml

.PHONY: build test lint run migrate clean

build:
	go build -o bin/\$(BIN) ./cmd/\$(BIN)

test:
	go test ./...

lint:
	go vet ./...
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed, skipping"

run: build
	./bin/\$(BIN) -config \$(CONFIG)

migrate: build
	./bin/\$(BIN) -config \$(CONFIG) -migrate

clean:
	rm -rf bin
EOF

write README.md <<EOF
# ${PROJECT}

Read-only household calendar aggregator. Pulls from Google, Microsoft Graph,
and iCloud CalDAV into one local store; serves filterable CalDAV collections,
ICS feeds, and a live web view. LAN only.

See \`docs/PROJECT_OUTLINE.md\` for the design and \`CLAUDE.md\` for
contributor and agent guidance.

## Quick start

\`\`\`
make build
cp deploy/config.example.toml config.toml
\$EDITOR config.toml
make migrate
make run
\`\`\`
EOF

# Keep empty dirs in git until they have real content.
for d in "${dirs[@]}"; do
  [ -n "$(ls -A "$d" 2>/dev/null)" ] || touch "$d/.gitkeep"
done

echo
echo "Done. Next:"
echo "  cd ${PROJECT} && git init && git add -A && git commit -m 'Initial scaffold'"
echo "  Drop CLAUDE.md at the repo root and PROJECT_OUTLINE.md in docs/."
