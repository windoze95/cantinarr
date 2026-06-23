// Package arr holds Radarr/Sonarr logic shared across the typed clients and the
// MCP tools. The Import Doctor classifier lives here so the same rules diagnose
// queue problems regardless of which service surfaced them.
package arr

import "strings"

// StatusMessage mirrors a Radarr/Sonarr queue item's statusMessages entry: a
// title plus one or more human-readable lines.
type StatusMessage struct {
	Title    string
	Messages []string
}

// ManualImportRejectionView is a service-neutral rejection reason, used when
// rendering manual-import candidates from either service.
type ManualImportRejectionView struct {
	Reason string
	Type   string
}

// QueueSignal is the service-neutral projection of a queue item that the
// classifier reasons over. Both sonarr.DetailedQueueItem and
// radarr.DetailedQueueItem map into it.
type QueueSignal struct {
	Status                string
	TrackedDownloadStatus string
	TrackedDownloadState  string
	ErrorMessage          string
	StatusMessages        []StatusMessage
	Protocol              string
}

// Diagnosis is the classifier's verdict for one queue item.
type Diagnosis struct {
	// Severity is one of: ok, info, warning, error.
	Severity string
	// Problem is a short label for the detected condition.
	Problem string
	// Transparency is a user-facing sentence explaining what happened.
	Transparency string
	// SuggestedActions are stable verb strings the caller can map to fix
	// tools: process, manual_import, force_import, remove, blocklist_search,
	// change_category, rescan, or none.
	SuggestedActions []string
}

// Severity levels.
const (
	SeverityOK      = "ok"
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Action verbs. These are stable across localizations and are what the fix
// tools key off of.
const (
	ActionNone            = "none"
	ActionProcess         = "process"
	ActionManualImport    = "manual_import"
	ActionForceImport     = "force_import"
	ActionRemove          = "remove"
	ActionBlocklistSearch = "blocklist_search"
	ActionChangeCategory  = "change_category"
	ActionRescan          = "rescan"
)

// messageRule matches a single statusMessages line by its stable English
// prefix (case-insensitive substring; the localized tail after {...}
// substitutions drifts and is ignored). The first rule that matches wins.
type messageRule struct {
	// prefix is the stable English fragment to look for, lower-cased.
	prefix       string
	problem      string
	transparency string
	severity     string
	actions      []string
}

// messageRules is the verbatim-prefix catalog, in first-match-wins order. More
// specific / more dangerous reasons come first so they are not shadowed by a
// broader rule.
var messageRules = []messageRule{
	{
		prefix:       "caution: found",
		problem:      "Dangerous file in release",
		transparency: "This release contains an executable or otherwise dangerous file — possible malware. Do not import it.",
		severity:     SeverityError,
		actions:      []string{ActionBlocklistSearch},
	},
	{
		prefix:       "individual episode mappings",
		problem:      "TheXEM mapping needs confirmation",
		transparency: "This show has individual episode mappings on TheXEM, so the download wasn't imported automatically. Confirm the episode and we'll force the import.",
		severity:     SeverityWarning,
		actions:      []string{ActionManualImport, ActionForceImport},
	},
	{
		prefix:       "found archive file",
		problem:      "Release is an unextracted archive",
		transparency: "This release is packed in an archive (RAR) and must be unpacked before it can be imported.",
		severity:     SeverityWarning,
		actions:      []string{ActionChangeCategory, ActionManualImport},
	},
	{
		prefix:       "no files found are eligible for import",
		problem:      "Nothing importable in the folder",
		transparency: "The download folder had nothing importable — likely all samples, an unextracted archive, or a path/permissions issue.",
		severity:     SeverityWarning,
		actions:      []string{ActionManualImport, ActionBlocklistSearch},
	},
	{
		prefix:       "unable to determine if file is a sample",
		problem:      "Could not verify sample",
		transparency: "The file's runtime couldn't be verified, so it might be a sample clip. Force the import if you know it's the full file.",
		severity:     SeverityWarning,
		actions:      []string{ActionForceImport, ActionBlocklistSearch},
	},
	{
		prefix:       "sample",
		problem:      "File looks like a sample",
		transparency: "The file looks like a sample clip rather than the full release.",
		severity:     SeverityWarning,
		actions:      []string{ActionForceImport, ActionBlocklistSearch},
	},
	{
		prefix:       "not an upgrade for existing",
		problem:      "Not an upgrade",
		transparency: "This release isn't better than the file you already have, so it wasn't imported.",
		severity:     SeverityInfo,
		actions:      []string{ActionRemove, ActionForceImport},
	},
	{
		prefix:       "already imported",
		problem:      "Already imported",
		transparency: "This exact download was already imported, so there's nothing to do but clear it.",
		severity:     SeverityInfo,
		actions:      []string{ActionRemove},
	},
	{
		prefix:       "not enough free space",
		problem:      "Not enough free space",
		transparency: "The library drive is below its free-space floor. Free up space, then rescan to retry.",
		severity:     SeverityError,
		actions:      []string{ActionRescan},
	},
	{
		prefix:       "permission",
		problem:      "Path or permissions error",
		transparency: "A permissions problem or wrong remote-path mapping blocked the import. Fix access to the path, then rescan to retry.",
		severity:     SeverityError,
		actions:      []string{ActionRescan},
	},
	{
		prefix:       "does not exist",
		problem:      "Path not accessible",
		transparency: "The download path doesn't exist or isn't accessible to the service — usually a remote-path mapping issue. Fix the path, then rescan to retry.",
		severity:     SeverityError,
		actions:      []string{ActionRescan},
	},
}

// errorRules matches the queue item's errorMessage (used when
// trackedDownloadStatus is error). First match wins.
var errorRules = []messageRule{
	{
		prefix:       "stalled",
		problem:      "Download stalled",
		transparency: "This torrent has no seeders and will never finish on its own.",
		severity:     SeverityError,
		actions:      []string{ActionBlocklistSearch},
	},
	{
		prefix:       "no connections",
		problem:      "Download stalled",
		transparency: "This torrent has no connections and will never finish on its own.",
		severity:     SeverityError,
		actions:      []string{ActionBlocklistSearch},
	},
	{
		prefix:       "qbittorrent is reporting an error",
		problem:      "Download client error",
		transparency: "Your torrent client errored on this download. It won't recover on its own.",
		severity:     SeverityError,
		actions:      []string{ActionBlocklistSearch},
	},
	{
		prefix:       "is reporting an error",
		problem:      "Download client error",
		transparency: "Your download client errored on this download. It won't recover on its own.",
		severity:     SeverityError,
		actions:      []string{ActionBlocklistSearch},
	},
}

// healthy reports whether the signal looks like a download progressing or
// completing normally, with no surfaced problem.
func (s QueueSignal) healthy() bool {
	tds := strings.ToLower(s.TrackedDownloadStatus)
	if tds != "" && tds != "ok" {
		return false
	}
	if s.ErrorMessage != "" {
		return false
	}
	for _, m := range s.StatusMessages {
		for _, line := range m.Messages {
			if strings.TrimSpace(line) != "" {
				return false
			}
		}
	}
	return true
}

// Diagnose classifies a single queue signal using first-match-wins rules.
//
// Order:
//  1. trackedDownloadStatus == error -> inspect errorMessage (client/stalled).
//  2. a warning/blocked/failed-pending item with statusMessages -> match each
//     line against the verbatim rejection table.
//  3. trackedDownloadState == importPending with no messages -> stuck pending.
//  4. trackedDownloadState == failed -> remove + blocklist + re-search.
//  5. otherwise healthy.
func Diagnose(sig QueueSignal) Diagnosis {
	state := strings.ToLower(sig.TrackedDownloadState)
	status := strings.ToLower(sig.TrackedDownloadStatus)

	// 1. Hard download-client / stalled errors.
	if status == "error" {
		if d, ok := matchError(sig.ErrorMessage); ok {
			return d
		}
		// An error status without a recognized message still warrants a remove.
		msg := sig.ErrorMessage
		transparency := "This download errored and won't recover on its own."
		if msg != "" {
			transparency = "This download errored (" + msg + ") and won't recover on its own."
		}
		return Diagnosis{
			Severity:         SeverityError,
			Problem:          "Download error",
			Transparency:     transparency,
			SuggestedActions: []string{ActionBlocklistSearch},
		}
	}

	// 2. Import rejections surfaced as statusMessages.
	if d, ok := matchMessages(sig.StatusMessages); ok {
		return d
	}

	// 3. Stuck waiting on the import pass.
	if state == "importpending" {
		return Diagnosis{
			Severity:         SeverityWarning,
			Problem:          "Waiting to import",
			Transparency:     "The download finished but hasn't been imported yet — the service hasn't run its import pass. Process it now to import it.",
			SuggestedActions: []string{ActionProcess, ActionManualImport},
		}
	}

	// 4. Failed download.
	if state == "failed" || state == "failedpending" {
		return Diagnosis{
			Severity:         SeverityError,
			Problem:          "Download failed",
			Transparency:     "This download failed. Remove and blocklist it so a fresh search grabs a different release.",
			SuggestedActions: []string{ActionBlocklistSearch},
		}
	}

	// 5. Anything else with no surfaced problem is healthy.
	if sig.healthy() {
		return Diagnosis{
			Severity:         SeverityOK,
			Problem:          "",
			Transparency:     "",
			SuggestedActions: []string{ActionNone},
		}
	}

	// Fallback: a warning with messages we don't recognize.
	return Diagnosis{
		Severity:         SeverityWarning,
		Problem:          "Import blocked",
		Transparency:     "The service couldn't import this automatically. Review the candidates and import manually, or remove and re-search.",
		SuggestedActions: []string{ActionManualImport, ActionBlocklistSearch},
	}
}

// matchMessages returns the first message rule that matches any status line.
func matchMessages(messages []StatusMessage) (Diagnosis, bool) {
	for _, group := range messages {
		// The group title is itself a status line in many *arr responses
		// (e.g. it carries the source title or the reason), so test it too.
		candidates := make([]string, 0, len(group.Messages)+1)
		if group.Title != "" {
			candidates = append(candidates, group.Title)
		}
		candidates = append(candidates, group.Messages...)
		for _, line := range candidates {
			if d, ok := matchRule(line, messageRules); ok {
				return d, true
			}
		}
	}
	return Diagnosis{}, false
}

// matchError returns the first error rule that matches the error message.
func matchError(errorMessage string) (Diagnosis, bool) {
	return matchRule(errorMessage, errorRules)
}

// matchRule scans rules in order and returns the first whose prefix is a
// case-insensitive substring of line.
func matchRule(line string, rules []messageRule) (Diagnosis, bool) {
	lower := strings.ToLower(line)
	if strings.TrimSpace(lower) == "" {
		return Diagnosis{}, false
	}
	for _, r := range rules {
		if strings.Contains(lower, r.prefix) {
			return Diagnosis{
				Severity:         r.severity,
				Problem:          r.problem,
				Transparency:     r.transparency,
				SuggestedActions: r.actions,
			}, true
		}
	}
	return Diagnosis{}, false
}
