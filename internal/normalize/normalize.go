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
	"strings"
	"unicode"

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
	case model.ProviderGraph:
		return Graph(raw, cal)
	default:
		return model.Event{}, fmt.Errorf("normalize: no normalizer for provider %q", providerKind)
	}
}

func redact(e *model.Event) {
	e.Summary = RedactedSummary
	e.Location = ""
	e.Raw = nil
}

// tagSlug normalizes a provider label into a tag: lowercase, with runs of
// non-alphanumerics collapsed to a single hyphen. "Work Stuff" -> "work-stuff".
func tagSlug(s string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			hyphen = false
			continue
		}
		if b.Len() > 0 && !hyphen {
			b.WriteByte('-')
			hyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// addTag appends slug(tag) to tags if non-empty and not already present.
func addTag(tags []string, tag string) []string {
	s := tagSlug(tag)
	if s == "" {
		return tags
	}
	for _, t := range tags {
		if t == s {
			return tags
		}
	}
	return append(tags, s)
}
