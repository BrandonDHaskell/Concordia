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
