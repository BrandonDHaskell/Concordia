# Concordia

Read-only household calendar aggregator. Pulls from Google, Microsoft Graph,
and iCloud CalDAV into one local store; serves filterable CalDAV collections,
ICS feeds, and a live web view. LAN only.

See `docs/PROJECT_OUTLINE.md` for the design and `CLAUDE.md` for
contributor and agent guidance.

## Quick start

```
make build
cp deploy/config.example.toml config.toml
$EDITOR config.toml
make migrate
bin/concordiad auth google --account Gmail        # or: auth graph --account "..."
bin/concordiad sync-once   --account Gmail
bin/concordiad occurrences --view no-work
```

## Filtering

Three layers, applied when occurrences are materialized:

- **Owner** — every account belongs to a person; every occurrence inherits it.
- **Source labels** — a Google event's color and an Outlook event's categories
  become tags automatically, no config.
- **Rules** — an ordered `[[rule]]` list in config, each matching one of
  `match_calendar` / `match_title` / `match_location` (regexps) with an action:

  ```toml
  [[rule]]
  match_title = "(?i)soccer|practice"
  action      = "tag"
  tag         = "kids"

  [[rule]]
  match_calendar = "Work"
  action         = "redact"     # occurrence shows as "Busy"; the stored event keeps its detail
  ```

**Views** are named predicates over owner and tags with a stable meaning:

```toml
[[view]]
name      = "no-work"
predicate = "not tag:work"

[[view]]
name      = "brandon-kids"
predicate = "owner:brandon and tag:kids"
```

Inspect the result: `concordiad occurrences [--owner X] [--tag Y] [--view Z] [--days N]`.

## Web view

Open `http://tranquility:8080/` for the planning screen: upcoming events grouped
by day, one colour per person, with filter chips for each person, each
configured view, and the tags in view. Chips toggle a query param and update
the page in place (htmx, no page reload). Set `[serve] timezone` to the wall
tablet's zone.

The page updates itself over SSE within a couple of seconds of a sync, keeping
the active filters. Overlapping events are flagged, with a per-day count.

## Connecting a client

The daemon serves read-only CalDAV and ICS on `[server] listen`.

**CalDAV** (DAVx5, KOrganizer, iOS/macOS Calendar): point the client at

```
http://tranquility:8080/dav/principal/
```

It discovers one collection per person (`.../cal/p-<name>/`) and one per view
(`.../cal/v-<view>/`). Writes are refused. There is no `sync-collection` yet, so
clients do periodic full pulls; set the refresh interval to a few minutes.

**ICS subscription** (anything that takes a URL):

```
http://tranquility:8080/feeds/all.ics          every person, merged
http://tranquility:8080/feeds/p-<name>.ics      one person
http://tranquility:8080/feeds/v-<view>.ics      one view
```

Set `[serve] summary_prefix = true` if a merged view needs `[owner]` on each
event to tell people apart.
