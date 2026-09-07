-- Provider tombstones arrive keyed by the provider's opaque per-calendar event
-- id, which is distinct from the iCalUID stored in events.uid (an iCalUID is
-- shared by a recurring master and its overrides). Track both.
ALTER TABLE events ADD COLUMN remote_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX idx_events_calendar_remote
  ON events (calendar_id, remote_id) WHERE remote_id != '';
