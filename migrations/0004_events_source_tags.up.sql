-- Source-label tags (Google colorId, Outlook categories) are assigned in
-- normalize and must survive to the rules pass, which re-loads events from
-- this table. Stored as a JSON array of strings.
ALTER TABLE events ADD COLUMN source_tags TEXT NOT NULL DEFAULT '';
