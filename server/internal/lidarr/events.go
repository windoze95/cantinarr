package lidarr

// Lidarr's event vocabulary is verified against its open source
// (github.com/Lidarr/Lidarr): the webhook import event serializes as
// "Download" (WebhookEventType) and the history enum's import events as
// "trackFileImported" and "downloadImported" (EntityHistoryEventType). This
// file is the single home for the names Cantinarr accepts as "a music import
// completed", shared by the webhook receiver and the queue poller's
// import-history catch-up so the two witnesses can never disagree about what
// counts as an import.

// importEventTypes are the normalized event names announcing a completed
// import. The set stays deliberately generous — the verified names plus the
// wider Servarr-lineage spellings — because an unrecognized name degrades to
// the queue witness (an alert delayed), while a wrongly recognized one would
// announce an album that never arrived. Rename, retag and delete are
// deliberately NOT import events: announcing "your album is ready" because a
// file was renamed would be worse than announcing it 30 seconds late.
var importEventTypes = map[string]struct{}{
	"download": {}, "releaseimport": {}, "trackfileimport": {}, "trackfileimported": {},
	"albumimport": {}, "albumimported": {}, "downloadimported": {}, "downloadfolderimported": {},
}

// NormalizeEventType folds an arr event name to lowercase letters so casing
// and word separators cannot cause a miss. Absurdly long input is rejected
// outright rather than normalized.
func NormalizeEventType(s string) string {
	if len(s) > 64 {
		return ""
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		}
	}
	return string(out)
}

// IsImportEventType reports whether a raw Lidarr event name announces a
// completed import, folding case and separators first.
func IsImportEventType(s string) bool {
	_, ok := importEventTypes[NormalizeEventType(s)]
	return ok
}
