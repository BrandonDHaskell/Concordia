// Package model holds Concordia's internal domain types. It imports nothing
// else from the project: every other package depends on model, not the reverse.
package model

import "time"

// Provider identifiers. These match the config file's provider strings.
const (
	ProviderICloud = "icloud"
	ProviderGoogle = "google"
	ProviderGraph  = "graph"
)

// Account is one provider login belonging to one person. Credentials are never
// stored on the struct; CredentialRef names an out-of-band secret (a token file
// under the state directory, keyed by this string).
type Account struct {
	ID            int64
	Person        string
	Provider      string
	DisplayName   string
	CredentialRef string
	Enabled       bool
}

// Calendar is one remote calendar within an account. SyncToken is the provider
// delta cursor; an empty SyncToken means the next sync must be a full window
// resync. LastSyncAt is the zero time until the first successful sync.
type Calendar struct {
	ID          int64
	AccountID   int64
	RemoteID    string
	DisplayName string
	SyncToken   string
	LastSyncAt  time.Time
	LastError   string
	Redact      bool
	Enabled     bool
}
