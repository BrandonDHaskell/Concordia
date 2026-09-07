-- Foreign keys, WAL, and busy_timeout are set per-connection in internal/store,
-- not here: PRAGMA foreign_keys is a no-op inside a transaction.

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
