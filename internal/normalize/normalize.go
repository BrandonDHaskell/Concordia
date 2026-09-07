// Package normalize converts provider payloads into model.Event. Provider
// quirks are absorbed here (Google's dateTime vs date split, its recurrence
// line array) and provider vocabulary does not leak past this package.
//
// Redaction happens here: for a calendar marked redact, the summary and
// location are replaced and the raw payload is dropped before the event is
// returned, so plaintext detail never reaches disk.
package normalize

import (
	"fmt"

	"github.com/bhaskell/Concordia/internal/model"
	"github.com/bhaskell/Concordia/internal/provider"
)

// RedactedSummary is what a redacted event's summary becomes: a busy block with
// its time intact but no detail.
const RedactedSummary = "Busy"

// Event normalizes one raw event from the named provider.
func Event(providerKind string, raw provider.RawEvent, cal model.Calendar) (model.Event, error) {
	switch providerKind {
	case model.ProviderGoogle:
		return Google(raw, cal)
	default:
		return model.Event{}, fmt.Errorf("normalize: no normalizer for provider %q", providerKind)
	}
}

func redact(e *model.Event) {
	e.Summary = RedactedSummary
	e.Location = ""
	e.Raw = nil
}
