// Package provider fetches calendar changes from a remote source. It is the
// only layer that knows about sync tokens, delta links, OAuth, or provider
// HTTP shapes; that vocabulary must not leak past internal/normalize.
//
// Every implementation is read-only. No implementation may issue a write to a
// remote calendar.
package provider

import (
	"context"

	"github.com/bhaskell/Concordia/internal/model"
)

// RawEvent is an unparsed provider payload plus enough identity to normalize
// and store it. Body is the provider's own representation (JSON for Google and
// Graph, an iCalendar object for CalDAV).
type RawEvent struct {
	RemoteID string
	ETag     string
	Body     []byte
}

// Delta is the result of one incremental sync of one calendar.
type Delta struct {
	// Changed holds events that were created or modified since the token.
	Changed []RawEvent
	// Deleted holds provider-local event IDs that were removed or cancelled.
	Deleted []string
	// FullResync reports that the supplied token was invalid: the provider
	// re-fetched the whole window, so Changed is the complete current state
	// and any stored event for this calendar not present here is stale.
	FullResync bool
}

// Provider fetches changes for a single remote calendar. An implementation is
// bound to one account's credentials and may sync any calendar in that account.
type Provider interface {
	// Sync returns changes to cal since token. An empty token means a full
	// window sync. The returned string is the next token to persist; it must
	// be stored in the same transaction as the data it describes.
	Sync(ctx context.Context, cal model.Calendar, token string) (Delta, string, error)
}
