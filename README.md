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
make run
```
