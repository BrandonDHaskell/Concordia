# Concordia: Household Calendar Aggregator

Working name. Roman goddess of agreement, fits alongside Portunus. Rename is a
single find-and-replace on the module path if you want something else.

Read-only aggregation of household calendars from Google, Microsoft 365, and
iCloud into one local store, projected back out as filterable CalDAV
collections, ICS feeds, and a live web view. LAN only, single host.

---

## 1. Goals and non-goals

### Goals

- Pull calendar data from multiple providers and multiple accounts per provider.
- Near real-time ingest via provider delta APIs, not published ICS URLs.
- Normalize everything into one internal model with a known owner per event.
- Filterable output: per-person collections, tag-based views, named feeds.
- Serve to the clients the household already uses: KOrganizer, DAVx5, iOS
  Calendar, plus a browser view for a wall tablet.
- Run as one systemd unit on `tranquility` with a single SQLite file.

### Non-goals

- No write-back. The app never creates, edits, or deletes an event on any
  provider. This is a load-bearing constraint, not a phase-one simplification.
- No public ingress. No inbound webhooks, no ACME, no reverse proxy to the
  internet. Remote access, if ever wanted, goes over the existing Tailscale
  tailnet.
- No multi-tenancy. One household, config-file driven, no admin UI for
  account management.
- No Node.js in the build or runtime for anything authored here.

---

## 2. Architecture

Five stages, each a separate package with a narrow interface.

```
  providers          normalize          expand           rules           serve
 ┌──────────┐      ┌───────────┐    ┌───────────┐   ┌───────────┐   ┌──────────┐
 │ google   │      │           │    │           │   │ tag       │   │ caldav   │
 │ graph    │ ───▶ │ RawEvent  │──▶ │ Event     │──▶│ exclude   │──▶│ ics      │
 │ caldav   │      │ to Event  │    │ to        │   │ redact    │   │ web/SSE  │
 └──────────┘      └───────────┘    │ Occurrence│   └───────────┘   └──────────┘
   delta sync                       └───────────┘      views
```

1. **Providers** own credentials, delta tokens, and HTTP. Each returns a
   provider-shaped payload plus a new sync token. Nothing else in the codebase
   knows what a `syncToken` or a `deltaLink` is.
2. **Normalize** converts provider payloads into `model.Event`. Absorbs
   provider quirks here: Graph's non-RRULE recurrence representation, Google's
   `dateTime` vs `date` split, iCloud's VTIMEZONE variance.
3. **Expand** materializes `Event` into `Occurrence` rows over a rolling
   window. RRULE, EXDATE, and `RECURRENCE-ID` overrides all resolve here.
4. **Rules** apply tags, exclusions, and busy-redaction to occurrences.
   Redaction for calendars marked `redact` happens earlier, at normalize, so
   event detail never reaches disk.
5. **Serve** projects occurrences through view predicates into CalDAV
   collections, ICS feeds, and the web UI.

### Sync loop

Per calendar, on its own interval, with jittered start so twelve calendars do
not all fire on the same second:

1. Call provider delta with stored token.
2. On token invalidation (Google returns 410, Graph returns a fresh
   `deltaLink` with full state, CalDAV returns a 403 with
   `valid-sync-token`), fall back to a full window resync.
3. Upsert changed events, tombstone deleted ones.
4. Re-expand occurrences for touched events only.
5. Re-apply rules for touched occurrences.
6. Broadcast a change notification to SSE subscribers.
7. Store the new token, in the same transaction as the data it describes.

Exponential backoff on 429 and 5xx, honoring `Retry-After` where present.
A calendar that fails repeatedly goes into a degraded state and surfaces on the
web view rather than failing silently.

### Polling intervals

| Provider  | Mechanism                          | Interval |
|-----------|------------------------------------|----------|
| Google    | `events.list` with `syncToken`     | 30s      |
| Graph     | `/me/calendarView/delta`           | 30-60s   |
| iCloud    | CalDAV `sync-collection` REPORT    | 60s      |

### Freshness tiers, delivery side

| Channel            | Latency        | Use                          |
|--------------------|----------------|------------------------------|
| Web view over SSE  | about 1s       | Wall tablet, planning screen  |
| CalDAV             | minutes        | DAVx5, KOrganizer, iOS        |
| ICS subscription   | hours          | Convenience only              |

---

## 3. Data model

```sql
accounts(
  id, person, provider, display_name, credential_ref, enabled
)

calendars(
  id, account_id, remote_id, display_name,
  sync_token, last_sync_at, last_error, redact, enabled
)

events(
  id, calendar_id, uid, recurrence_id, summary, location,
  dtstart, dtend, is_all_day, tzid, rrule, exdates,
  status, etag, raw, updated_at, deleted_at
)

occurrences(
  id, event_id, start_utc, end_utc, start_local, is_all_day,
  summary, location, owner
)

occurrence_tags(
  occurrence_id, tag
)

views(
  name, predicate_json, kind  -- caldav | ics | both
)
```

Notes:

- All timed values stored UTC in `*_utc`, with `tzid` retained for display and
  for correct DST-aware re-expansion.
- All-day events are floating dates. Store them as dates. Never coerce to
  UTC midnight, that is how an all-day event ends up on the wrong day for
  anyone west of UTC.
- `occurrences` is derived state. It can be dropped and rebuilt from `events`
  at any time. Nothing writes to it except the expander.
- Rolling window: 60 days back, 400 days forward. Re-expanded nightly to keep
  the window sliding, and per-event on delta.

---

## 4. Filtering model

Three layers, cheapest first.

1. **Owner.** Every account belongs to a person, so every event inherits an
   owner with zero tagging effort. One CalDAV collection per person.
2. **Source labels.** Google `colorId`, Outlook `categories`. Map these to
   tags in normalize. Household members maintain their own labels in the app
   they already use, no config edits required.
3. **Rules.** Ordered list matching on source calendar, title regex, or
   location, with actions `tag`, `exclude`, `redact`. Evaluated at
   materialization.

**Views** are named predicates over owner plus tags, each with a stable URL.
Named views only for feeds. Ad-hoc query parameters exist in the web UI but are
never a subscription target, since a feed URL that changes meaning breaks
every subscriber silently.

For anyone subscribed to a single merged collection, mark ownership two ways:

- `CATEGORIES` set to owner plus tags. KOrganizer and Outlook can color by
  category. Apple Calendar ignores it.
- Optional `SUMMARY` prefix, `[BH] Dentist`. Universal, works everywhere.
- `X-HOUSEHOLD-OWNER` as well. Most clients drop it, it costs nothing.

---

## 5. Repository layout

```
concordia/
├── cmd/
│   └── concordiad/main.go        entrypoint, wiring, signal handling
├── internal/
│   ├── config/                   TOML load, validate, defaults
│   ├── model/                    Event, Occurrence, Owner, Tag, View
│   ├── store/                    SQLite access, migrations, queries
│   ├── provider/
│   │   ├── provider.go           Provider interface, Delta type
│   │   ├── google/
│   │   ├── graph/
│   │   └── caldav/               iCloud and generic CalDAV
│   ├── auth/                     OAuth flows, token storage and refresh
│   ├── normalize/                provider payload to model.Event
│   ├── expand/                   RRULE expansion to occurrences
│   ├── rules/                    tagging, exclusion, redaction
│   ├── views/                    predicate evaluation
│   ├── syncer/                   scheduler, backoff, delta orchestration
│   ├── icsout/                   model to iCalendar serialization
│   ├── davd/                     CalDAV server
│   └── httpd/                    web UI, SSE, health
├── migrations/                   NNNN_name.up.sql, embedded via go:embed
├── web/
│   ├── templates/                html/template
│   └── static/                   vendored htmx, CSS
├── deploy/
│   ├── concordiad.service
│   └── config.example.toml
├── testdata/
│   ├── ics/                      golden files for expander tests
│   └── fixtures/                 recorded provider HTTP responses
├── docs/
│   ├── PROJECT_OUTLINE.md        this file
│   ├── PROVIDERS.md              per-provider credential setup
│   └── RUNBOOK.md                deploy, backup, recovery
├── CLAUDE.md
├── Makefile
├── go.mod
└── README.md
```

### Core interface

```go
// internal/provider/provider.go
type Provider interface {
    // Sync returns changes since token. An empty token means full sync.
    // Returns the next token to persist.
    Sync(ctx context.Context, cal model.Calendar, token string) (Delta, string, error)
}

type Delta struct {
    Changed    []RawEvent
    Deleted    []string  // provider-local event IDs
    FullResync bool      // token was invalid, Changed is the complete window
}
```

---

## 6. Dependencies

| Purpose            | Package                                  |
|--------------------|------------------------------------------|
| iCalendar parse    | `github.com/emersion/go-ical`            |
| CalDAV client+srv  | `github.com/emersion/go-webdav`          |
| RRULE expansion    | `github.com/teambition/rrule-go`         |
| SQLite             | `modernc.org/sqlite` (cgo-free)          |
| OAuth              | `golang.org/x/oauth2`                    |
| Google Calendar    | `google.golang.org/api/calendar/v3`      |
| Graph              | plain `net/http`, no SDK                 |
| Config             | `github.com/BurntSushi/toml`             |

The Graph Go SDK is enormous and generated. Two endpoints do not justify it.

---

## 7. Milestones

**M0. Skeleton.** Repo, module, config load, SQLite open, migrations,
`/healthz`, systemd unit, Makefile. Runs and does nothing, correctly.

**M1. First provider and first output.** CalDAV provider against iCloud,
normalize, expand, `/cal/all.ics`. iCloud first because an app-specific
password takes two minutes and needs no app registration, so you get the whole
pipeline working end to end before touching OAuth.

**M2. Google.** OAuth device or loopback flow, token persistence and refresh,
`events.list` with `syncToken`, 410 handling.

**M3. Microsoft Graph.** Entra app registration, delegated `Calendars.Read`,
`calendarView/delta`. Graph expands recurrences server-side, so this path
mostly bypasses the expander.

**M4. Filtering.** Rules engine, tags from source labels, named views, one
CalDAV collection per person plus configured views.

**M5. CalDAV server.** `go-webdav` collections, `sync-collection` support so
DAVx5 and KOrganizer do incremental pulls. This is the point the household
actually starts using it.

**M6. Web view.** Merged agenda, per-person columns, free/busy overlap and
conflict detection, filter chips, SSE live update.

**M7. Hardening.** Backoff tuning, degraded-calendar surfacing, nightly window
slide, SQLite backup to the Pi, basic metrics.

M0 through M2 is the useful core. M5 is where it stops being a demo.

---

## 8. Open decisions

- **Redaction policy per calendar.** Whether a work calendar is ingested in
  full or reduced to busy blocks at normalize time. Redacting at ingest means
  the detail never lands on `tranquility`, which is a much easier conversation
  with the household than "stored but filtered."
- **Credential storage.** 0600 file under `StateDirectory` is the pragmatic
  answer for a single-host LAN service. systemd credentials with an encrypted
  blob is tidier. Decide before M2, since it shapes the `auth` package.
- **Web auth.** LAN-only may mean no auth at all, or a single shared
  passphrase. Affects nothing structurally, decide at M6.