# Provider credential setup

Per-provider steps to get Concordia read access to a calendar account. All
scopes are read-only; Concordia never writes upstream.

## Google

### 1. Create an OAuth client

1. In the Google Cloud console, create (or reuse) a project for household
   infrastructure.
2. APIs & Services > Library: enable **Google Calendar API**.
3. APIs & Services > OAuth consent screen: user type **External**, publishing
   status can stay **Testing**. Add each household Google account as a test
   user. Add the scope `.../auth/calendar.readonly`.
4. APIs & Services > Credentials > Create credentials > OAuth client ID, type
   **Desktop app**. Download the JSON (`client_secret_*.json`). It contains an
   `installed` object with `client_id` and `client_secret`.

The same client is shared by every Google account in the household.

### 2. Install the client secret

**Production (tranquility):**

```
sudo install -d -m 0755 /etc/concordia
sudo systemd-creds encrypt client_secret_XXXX.json /etc/concordia/google_oauth.cred
sudo rm client_secret_XXXX.json
```

The unit file already has
`LoadCredentialEncrypted=google_oauth:/etc/concordia/google_oauth.cred`, so at
runtime the decrypted secret appears at `$CREDENTIALS_DIRECTORY/google_oauth`
and nowhere on disk.

**Dev checkout:** drop the file next to the config and point at it:

```toml
[google]
credential_file = "google_oauth.json"
```

### 3. Authorize each account

```
concordiad auth google --account Gmail
```

`--account` takes the `name` (or `credential_ref`) from a `[[account]]` block
with `provider = "google"`. The command:

1. prints an authorization URL (add `--open` to launch a browser),
2. runs a loopback listener on `127.0.0.1` to catch the redirect,
3. exchanges the code (with PKCE) and writes the token to
   `<state-dir>/tokens/<credential_ref>.json` with 0600 perms,
4. lists the account's calendars and records them in the database.

If you run this over SSH without a local browser, forward the loopback port the
command prints, or run it on a workstation that shares the config and state
directory.

The refresh token is rewritten in place whenever it rotates. Keep the `tokens/`
directory out of any backup set.

### 4. Run a sync

```
concordiad sync-once --account Gmail            # all of the account's calendars
concordiad sync-once --account Gmail --calendar brandon@example.com
```

`sync-once` pulls the delta, normalizes and stores events, and rebuilds
occurrences for the rolling window, all in one transaction per calendar. It
advances the stored sync token, so the next run only sees changes. The daemon
will do this on a schedule in a later milestone.

### Notes and traps

- `events.list` with `syncToken`. A 410 means the token is dead: Concordia
  clears it and does a full window resync automatically.
- Cancelled events arrive in the delta with status `cancelled` and are applied
  as tombstones.
- Recurring-event overrides arrive as separate items with `recurringEventId`
  and `originalStartTime`; they are stored as-is and resolved during expansion.

## iCloud

Not yet implemented (milestone M1 was deferred).

## Microsoft Graph

Not yet implemented (milestone M3).
