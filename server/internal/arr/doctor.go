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

// QueueMediaContext is the stable media identity the arr already returned with
// a detailed queue item. Auto-remediation carries it into the issue instead of
// throwing it away, so the agent is bound to the exact instance, queue row, and
// movie/episode that triggered the incident.
type QueueMediaContext struct {
	QueueID       int
	Title         string
	TmdbID        int
	TvdbID        int
	SeasonNumber  int
	EpisodeNumber int
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
		// "Invalid video file, unsupported extension: '{extension}'" — more
		// specific than the filename-prefix invalid-video rule below, so it
		// comes first (both share the "invalid video file" prefix).
		prefix:       "invalid video file, unsupported extension",
		problem:      "Unsupported file type",
		transparency: "The file has an extension the service doesn't import as video. It's likely the wrong file (or needs unpacking) — remove and re-search for a proper release.",
		severity:     SeverityWarning,
		actions:      []string{ActionBlocklistSearch, ActionManualImport},
	},
	{
		// "Invalid video file, filename starts with '._'" (macOS AppleDouble).
		prefix:       "invalid video file",
		problem:      "Invalid video file",
		transparency: "The service flagged this as not a valid video file (for example a macOS resource-fork \"._\" file). Inspect the candidates and import the real file manually, or remove and re-search.",
		severity:     SeverityWarning,
		actions:      []string{ActionManualImport, ActionBlocklistSearch},
	},
	{
		// "Unable to parse file" (import-time parse failure).
		prefix:       "unable to parse file",
		problem:      "Couldn't parse the file",
		transparency: "The service couldn't parse the file name to figure out what it is, so it wasn't imported. Map it yourself with a manual import, or remove and re-search for a cleaner release.",
		severity:     SeverityWarning,
		actions:      []string{ActionManualImport, ActionBlocklistSearch},
	},
	{
		// Sonarr: "One or more episodes expected in this release were not
		// imported or missing from the release". Radarr's analog says "movies".
		// Verified against a real Sonarr 4.0.16 instance. The substring
		// "expected in this release were not imported" matches both services.
		prefix:       "expected in this release were not imported",
		problem:      "Release contents don't match",
		transparency: "Sonarr/Radarr expected files this release didn't contain, so it wasn't fully imported. Review the candidate files and import what's there manually, or remove and re-search for a complete release.",
		severity:     SeverityWarning,
		actions:      []string{ActionManualImport, ActionBlocklistSearch},
	},
	{
		// Co-occurs with the above on a real Sonarr instance: a specific
		// episode "was not found in the grabbed release". Same fix.
		prefix:       "was not found in the grabbed release",
		problem:      "Release contents don't match",
		transparency: "An episode this grab was supposed to include wasn't in the release, so it wasn't fully imported. Review the candidate files and import what's there manually, or remove and re-search for a complete release.",
		severity:     SeverityWarning,
		actions:      []string{ActionManualImport, ActionBlocklistSearch},
	},
	{
		// "Invalid season or episode" — Sonarr couldn't map the file to a
		// season/episode. Map it yourself via a manual import.
		prefix:       "invalid season or episode",
		problem:      "Episode mapping problem",
		transparency: "The service couldn't work out which season/episode this file is, so it wasn't imported. Map it yourself with a manual import.",
		severity:     SeverityWarning,
		actions:      []string{ActionManualImport},
	},
	{
		prefix:       "not an upgrade for existing",
		problem:      "Not an upgrade",
		transparency: "This release isn't better than the file you already have, so it wasn't imported.",
		severity:     SeverityInfo,
		actions:      []string{ActionRemove, ActionForceImport},
	},
	{
		// "Not a Custom Format upgrade for existing {episode,movie} file(s)..."
		// A distinct string from the plain "not an upgrade" rule above.
		prefix:       "not a custom format upgrade for existing",
		problem:      "Not a Custom Format upgrade",
		transparency: "This release doesn't improve on your existing file's Custom Format score, so it wasn't imported. Clear it, or force the import if you want it anyway.",
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
		// Sonarr: "...release was matched to series by ID. Automatic import is
		// not possible...". Radarr's analog says "matched to movie by ID".
		// Verified against a real Sonarr 4.0.16 instance: automatic import is
		// genuinely blocked, so this is a warning that needs a manual import.
		prefix:       "matched to series by id",
		problem:      "Matched by ID — needs manual import",
		transparency: "The release was matched to the series by its download-client ID rather than by its name, so the service won't import it automatically. Import it manually.",
		severity:     SeverityWarning,
		actions:      []string{ActionManualImport},
	},
	{
		prefix:       "matched to movie by id",
		problem:      "Matched by ID — needs manual import",
		transparency: "The release was matched to the movie by its download-client ID rather than by its name, so the service won't import it automatically. Import it manually.",
		severity:     SeverityWarning,
		actions:      []string{ActionManualImport},
	},
	{
		// Grab/search-side rejections ("Series/Episode/Movie is not monitored").
		// Surfaced for transparency; nothing to fix on the item itself.
		prefix:       "is not monitored",
		problem:      "Unmonitored",
		transparency: "This item is unmonitored, so the service won't grab or import it. Monitor it first if you want it.",
		severity:     SeverityInfo,
		actions:      []string{ActionNone},
	},
	{
		prefix:       "not enough free space",
		problem:      "Not enough free space",
		transparency: "The library drive is below its free-space floor. Free up space, then rescan to retry.",
		severity:     SeverityError,
		actions:      []string{ActionRescan},
	},
	{
		// "[{path}] is not a valid local path. You may need a Remote Path
		// Mapping..." — the download client reported a path the service can't
		// reach. Confirm with get_arr_health (remote path mapping), then rescan.
		prefix:       "is not a valid local path. you may need a remote path mapping",
		problem:      "Remote path mapping",
		transparency: "The download client reported a path the service can't reach on disk — a remote-path-mapping problem. Run get_arr_health to confirm the mapping, fix it, then rescan to retry.",
		severity:     SeverityError,
		actions:      []string{ActionRescan},
	},
	{
		prefix:       "permission",
		problem:      "Path or permissions error",
		transparency: "A permissions problem or wrong remote-path mapping blocked the import. Run get_arr_health to confirm the config, fix access to the path, then rescan to retry.",
		severity:     SeverityError,
		actions:      []string{ActionRescan},
	},
	{
		prefix:       "does not exist",
		problem:      "Path not accessible",
		transparency: "The download path doesn't exist or isn't accessible to the service — usually a remote-path mapping issue. Run get_arr_health to confirm, fix the path, then rescan to retry.",
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
		// "qBittorrent cannot resolve magnet link with DHT disabled" — the
		// torrent client can't fetch the magnet's metadata, so this download
		// will never start. Blocklist and re-search; a usenet or non-magnet
		// release sidesteps it.
		prefix:       "cannot resolve magnet",
		problem:      "Download client can't fetch the magnet",
		transparency: "Your torrent client can't resolve this magnet link (often DHT is disabled), so the download will never start. Remove and blocklist it, then re-search — a usenet or non-magnet release avoids this. Run get_arr_health to check the client.",
		severity:     SeverityError,
		actions:      []string{ActionBlocklistSearch},
	},
	{
		// "Unable to communicate with {downloadClient}..." — the service can't
		// reach the download client at all. This is a config/connectivity
		// problem, not the release's fault, so there's nothing to fix per item;
		// get_arr_health surfaces the root cause.
		prefix:       "unable to communicate with",
		problem:      "Download client unreachable",
		transparency: "The service can't reach your download client, so nothing in the queue can progress. Run get_arr_health to confirm, then fix the client connection — this isn't a problem with the release itself.",
		severity:     SeverityError,
		actions:      []string{ActionNone},
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
//  2. any non-empty errorMessage matching the error table -> classify it even
//     when the status is still "ok" (e.g. qBittorrent magnet errors surface on
//     an otherwise-downloading item).
//  3. a warning/blocked/failed-pending item with statusMessages -> match each
//     line against the verbatim rejection table.
//  4. trackedDownloadState == importPending with no messages -> stuck pending.
//  5. trackedDownloadState == failed -> remove + blocklist + re-search.
//  6. otherwise healthy.
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

	// 2. A recognized errorMessage on an otherwise-ok item (the status hasn't
	// flipped to "error" yet). Checked before healthy() so these aren't
	// misclassified into the generic "Import blocked" fallback.
	if sig.ErrorMessage != "" {
		if d, ok := matchError(sig.ErrorMessage); ok {
			return d
		}
	}

	// 3. Import rejections surfaced as statusMessages.
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
