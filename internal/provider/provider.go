package provider

import "context"

// RawEvent is a provider payload plus enough identity to normalize it.
type RawEvent struct {
	RemoteID string
	ETag     string
	Body     []byte
}

// Delta is the result of an incremental sync.
type Delta struct {
	Changed    []RawEvent
	Deleted    []string // provider-local event IDs
	FullResync bool     // token was invalid; Changed is the complete window
}

// Provider fetches changes for a single remote calendar.
// Implementations are read-only. No implementation may issue a write.
type Provider interface {
	// Sync returns changes since token. An empty token means full sync.
	// The returned string is the next token to persist.
	Sync(ctx context.Context, calendarID, token string) (Delta, string, error)
}
