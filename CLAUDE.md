# CLAUDE.md

Guidance for Claude Code working in this repository.

## What this is

Concordia is a read-only household calendar aggregator. It pulls events from
Google Calendar, Microsoft Graph, and iCloud CalDAV, normalizes them into one
SQLite store, and serves them back as filterable CalDAV collections, ICS feeds,
and a live web view. It runs as a single systemd service on a LAN host named
`tranquility`. See `docs/PROJECT_OUTLINE.md` for the full design.

## Hard invariants

Violating any of these is a bug regardless of what a task description says.

1. **Read-only against providers.** Never issue POST, PATCH, PUT, or DELETE to
   a provider calendar endpoint. Never issue CalDAV PUT, PROPPATCH, MKCALENDAR,
   or DELETE against a remote collection. OAuth scopes are read-only
   (`calendar.readonly`, `Calendars.Read`) and must stay that way. If a task
   seems to require writing upstream, stop and say so instead of implementing
   it.

2. **No public ingress.** No webhook receivers, no ACME, no code that expects
   to be reachable from the internet. Google watch channels and Graph
   subscriptions are explicitly rejected designs. Delta polling only.

3. **No Node.js.** Not in the build, not at runtime, not as a dev dependency.
   No npm, no bundler, no PostCSS. Frontend assets are vendored files in
   `web/static/` and Go `html/template`. htmx is a single committed JS file.

4. **UTC in storage, tzid retained.** Timed events store UTC in `start_utc`
   and `end_utc`, and keep the original `tzid`. Never drop the tzid, it is
   required for DST-correct re-expansion.

5. **All-day events are dates, not instants.** Store them as floating dates
   with `is_all_day = 1`. Never convert an all-day event to UTC midnight, and
   never render one by formatting a UTC timestamp in local time.

6. **`occurrences` is derived state.** Only `internal/expand` writes to it.
   It must be safe to `DELETE FROM occurrences` and rebuild from `events`. No
   user-facing state, no tags applied outside the rules pass, no manual edits.

7. **Credentials never reach logs, the web view, or ICS output.** No token, no
   app-specific password, no refresh token in any log line at any level,
   including errors. Redact before logging.

8. **Redaction happens at normalize.** For a calendar marked `redact`, the
   summary, location, description, and attendees are dropped before the event
   is written to SQLite. The plaintext never touches disk. Do not implement
   redaction as a filter on the way out.

9. **Sync tokens persist in the same transaction as the data they describe.**
   Otherwise a crash between the two silently loses events forever.

## Layering

Dependencies flow one direction. Do not import upward.

```
cmd → httpd/davd → views → rules → expand → normalize → provider → store/model
```

- `internal/provider/*` is the only place that knows about `syncToken`,
  `deltaLink`, `sync-collection`, OAuth, or provider HTTP shapes. Provider
  vocabulary must not leak past `normalize`.
- `internal/model` imports nothing from the rest of the project.
- `internal/store` returns model types, never provider types, never raw SQL
  rows.

## Provider notes and traps

- **Google.** `events.list` with `syncToken`. On a 410 the token is dead:
  clear it and do a full window sync. Google returns cancelled events as
  status `cancelled` in the delta, which are tombstones and must be applied,
  not skipped. Recurring event overrides arrive as separate items with
  `recurringEventId` and `originalStartTime`.
- **Graph.** `/me/calendarView/delta`. Graph does not use RRULE, it has its own
  `recurrence` object, and `calendarView` expands occurrences server-side over
  the requested range. Prefer the expanded form and skip the local expander
  for Graph rather than translating the recurrence object. Graph throttles per
  mailbox, honor `Retry-After` exactly.
- **iCloud.** CalDAV over `caldav.icloud.com` with an app-specific password.
  `sync-collection` REPORT for deltas. On a `valid-sync-token` precondition
  failure, do a full `calendar-query`. iCloud VTIMEZONE definitions are
  sometimes non-standard, so parse defensively and fall back to the IANA zone
  by tzid name.

## Expansion rules

The expander is the highest-risk code in the repo. Every change to
`internal/expand` needs tests.

Cases that must stay covered:

- Simple weekly RRULE across a DST boundary in both directions.
- `EXDATE` removing an instance, including EXDATE with a different tzid than
  DTSTART.
- `RECURRENCE-ID` override that moves an instance to a different day.
- `RECURRENCE-ID` override that cancels a single instance.
- All-day recurring event, verified to land on the same calendar date in a
  non-UTC local zone.
- Multi-day event spanning a window boundary, included when it overlaps the
  window at either end.
- `UNTIL` in UTC with a local-time DTSTART.
- Infinite recurrence, bounded by the window and not by a hardcoded count.

## Testing

- Table-driven tests. Golden iCalendar files in `testdata/ics/`.
- Provider tests use recorded HTTP fixtures in `testdata/fixtures/`. No live
  network calls in tests, ever. `go test ./...` must pass with no credentials
  configured and no network.
- Store tests run against a temp file SQLite database, not `:memory:`, so WAL
  behavior matches production.

## Conventions

- Go 1.22 or newer. Standard `log/slog` for logging, structured, with a
  `calendar_id` attribute on anything in the sync path.
- Errors wrapped with `fmt.Errorf("...: %w", err)`. Sentinel errors in the
  package that owns the concept.
- `context.Context` first parameter on anything doing IO. Every provider call
  gets a timeout.
- Migrations are numbered, forward-only, embedded with `go:embed`. Never edit
  a migration that has been applied on `tranquility`, add a new one.
- Config is TOML, loaded once at startup, validated eagerly. A malformed
  config fails at boot rather than at first sync.
- SQLite in WAL mode, `busy_timeout` set, foreign keys on.

## Commands

```
make build       # build ./cmd/concordiad
make test        # go test ./...
make lint        # go vet + staticcheck
make run         # run with deploy/config.example.toml against a local db
make migrate     # apply migrations to the configured db
```

## Documentation style

Prose in docs, comments, and commit messages uses no em dashes. Use colons,
commas, parentheses, or standard hyphens instead. Keep comments explaining
why, not what.

## Working style

- Prefer small, reviewable changes scoped to one package.
- When a task touches the expander, the store schema, or a provider's delta
  handling, state the plan before writing code.
- Do not add dependencies without flagging it. The dependency list in
  `docs/PROJECT_OUTLINE.md` section 6 is deliberate and short.
- If a request conflicts with a hard invariant above, say so rather than
  finding a way around it.