package arr

import (
	"slices"
	"testing"
)

// msg is a shorthand for building a single-line status message group.
func msg(lines ...string) []StatusMessage {
	return []StatusMessage{{Title: "", Messages: lines}}
}

func TestDiagnose(t *testing.T) {
	cases := []struct {
		name        string
		sig         QueueSignal
		wantProblem string
		wantSev     string
		wantActions []string
	}{
		{
			name: "thexem unconfirmed mapping needs manual input",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages: msg("This show has individual episode mappings on TheXEM and " +
					"requires manual input to determine the correct episodes."),
			},
			wantProblem: "TheXEM mapping needs confirmation",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionManualImport, ActionForceImport},
		},
		{
			name: "no files eligible for import",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("No files found are eligible for import in /downloads/Some.Release"),
			},
			wantProblem: "Nothing importable in the folder",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionManualImport, ActionBlocklistSearch},
		},
		{
			name: "found archive must be extracted",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Found archive file, might need to be extracted"),
			},
			wantProblem: "Release is an unextracted archive",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionChangeCategory, ActionManualImport},
		},
		{
			name: "sample file",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Sample"),
			},
			wantProblem: "File looks like a sample",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionForceImport, ActionBlocklistSearch},
		},
		{
			name: "unable to determine sample beats generic sample rule",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Unable to determine if file is a sample"),
			},
			wantProblem: "Could not verify sample",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionForceImport, ActionBlocklistSearch},
		},
		{
			name: "not an upgrade for existing",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages: msg("Not an upgrade for existing episode file(s). " +
					"Existing: WEBDL-1080p. New: HDTV-720p."),
			},
			wantProblem: "Not an upgrade",
			wantSev:     SeverityInfo,
			wantActions: []string{ActionRemove, ActionForceImport},
		},
		{
			// Verified Sonarr UpgradeSpecification.cs string.
			name: "not a custom format upgrade",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages: msg("Not a Custom Format upgrade for existing episode file(s). " +
					"New: [WEB] (0) do not improve on Existing: [WEB] (5)"),
			},
			wantProblem: "Not a Custom Format upgrade",
			wantSev:     SeverityInfo,
			wantActions: []string{ActionRemove, ActionForceImport},
		},
		{
			// Verified Radarr UpgradeSpecification.cs string (movie variant).
			name: "not a custom format upgrade (movie)",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages: msg("Not a Custom Format upgrade for existing movie file(s). " +
					"New: [WEB] (0) do not improve on Existing: [WEB] (5)"),
			},
			wantProblem: "Not a Custom Format upgrade",
			wantSev:     SeverityInfo,
			wantActions: []string{ActionRemove, ActionForceImport},
		},
		{
			// Verified ImportDecisionMaker.cs string (both services).
			name: "unable to parse file",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Unable to parse file"),
			},
			wantProblem: "Couldn't parse the file",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionManualImport, ActionBlocklistSearch},
		},
		{
			// Verified DownloadedEpisodesImportService.cs / DownloadedMovieImportService.cs.
			name: "invalid video file applefork",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Invalid video file, filename starts with '._'"),
			},
			wantProblem: "Invalid video file",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionManualImport, ActionBlocklistSearch},
		},
		{
			// Verified unsupported-extension string; must beat the broader
			// "invalid video file" rule (both share that prefix).
			name: "unsupported extension beats invalid video file",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Invalid video file, unsupported extension: '.mkv.exe'"),
			},
			wantProblem: "Unsupported file type",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionBlocklistSearch, ActionManualImport},
		},
		{
			// Verified against a real Sonarr 4.0.16 instance.
			name: "one or more episodes not imported",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("One or more episodes expected in this release were not imported or missing from the release"),
			},
			wantProblem: "Release contents don't match",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionManualImport, ActionBlocklistSearch},
		},
		{
			// Verified Radarr CompletedDownloadService.cs status message; the
			// "expected in this release were not imported" substring matches it.
			name: "one or more movies not imported",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("One or more movies expected in this release were not imported or missing"),
			},
			wantProblem: "Release contents don't match",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionManualImport, ActionBlocklistSearch},
		},
		{
			// Real Sonarr 4.0.16 line that co-occurs with the partial-import
			// message above.
			name: "episode was not found in the grabbed release",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Episode 5 was not found in the grabbed release"),
			},
			wantProblem: "Release contents don't match",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionManualImport, ActionBlocklistSearch},
		},
		{
			name: "invalid season or episode mapping",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Invalid season or episode"),
			},
			wantProblem: "Episode mapping problem",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionManualImport},
		},
		{
			// Verified AlreadyImportedSpecification.cs (Sonarr) — covered by the
			// existing "already imported" rule.
			name: "episode file already imported at",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Episode file already imported at 1/2/2024 3:04 PM"),
			},
			wantProblem: "Already imported",
			wantSev:     SeverityInfo,
			wantActions: []string{ActionRemove},
		},
		{
			// Verified Radarr AlreadyImportedSpecification.cs.
			name: "movie file already imported at",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Movie file already imported at 1/2/2024 3:04 PM"),
			},
			wantProblem: "Already imported",
			wantSev:     SeverityInfo,
			wantActions: []string{ActionRemove},
		},
		{
			// Verified Sonarr CompletedDownloadService.cs warn message.
			name: "matched to series by id",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Found matching series via grab history, but release was matched to series by ID. Automatic import is not possible. See the FAQ for details."),
			},
			wantProblem: "Matched by ID — needs manual import",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionManualImport},
		},
		{
			// Verified Radarr CompletedDownloadService.cs warn message.
			name: "matched to movie by id",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Found matching movie via grab history, but release was matched to movie by ID. Manual Import required."),
			},
			wantProblem: "Matched by ID — needs manual import",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionManualImport},
		},
		{
			// Verified MonitoredEpisodeSpecification.cs (Sonarr).
			name: "series is not monitored",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Series is not monitored"),
			},
			wantProblem: "Unmonitored",
			wantSev:     SeverityInfo,
			wantActions: []string{ActionNone},
		},
		{
			// Verified CompletedDownloadService.cs remote-path-mapping string.
			// Must beat the broad "does not exist"/"permission" path rules.
			name: "remote path mapping not a valid local path",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("[/data/incomplete/Some.Release] is not a valid local path. You may need a Remote Path Mapping."),
			},
			wantProblem: "Remote path mapping",
			wantSev:     SeverityError,
			wantActions: []string{ActionRescan},
		},
		{
			// Verified DownloadClientCheck en.json string. Surfaces on an error
			// status; nothing to fix per item (config-level).
			name: "download client unreachable",
			sig: QueueSignal{
				TrackedDownloadStatus: "error",
				TrackedDownloadState:  "downloading",
				ErrorMessage:          "Unable to communicate with qBittorrent.",
			},
			wantProblem: "Download client unreachable",
			wantSev:     SeverityError,
			wantActions: []string{ActionNone},
		},
		{
			name: "dangerous executable wins over everything",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages: []StatusMessage{{
					Title:    "Some.Release",
					Messages: []string{"Caution: Found executable file", "Sample"},
				}},
			},
			wantProblem: "Dangerous file in release",
			wantSev:     SeverityError,
			wantActions: []string{ActionBlocklistSearch},
		},
		{
			name: "stalled with no connections",
			sig: QueueSignal{
				TrackedDownloadStatus: "error",
				TrackedDownloadState:  "downloading",
				ErrorMessage:          "The download is stalled with no connections",
				Protocol:              "torrent",
			},
			wantProblem: "Download stalled",
			wantSev:     SeverityError,
			wantActions: []string{ActionBlocklistSearch},
		},
		{
			name: "qbittorrent reporting an error",
			sig: QueueSignal{
				TrackedDownloadStatus: "error",
				TrackedDownloadState:  "downloading",
				ErrorMessage:          "qBittorrent is reporting an error",
				Protocol:              "torrent",
			},
			wantProblem: "Download client error",
			wantSev:     SeverityError,
			wantActions: []string{ActionBlocklistSearch},
		},
		{
			// Real Sonarr 4.0.16: the client error surfaced on an item whose
			// trackedDownloadStatus was still "ok" (not "error"). The classifier
			// must inspect errorMessage regardless of status so this is not
			// misclassified to the generic "Import blocked" fallback.
			name: "magnet cannot resolve with status still ok",
			sig: QueueSignal{
				TrackedDownloadStatus: "ok",
				TrackedDownloadState:  "downloading",
				ErrorMessage:          "qBittorrent cannot resolve magnet link with DHT disabled",
				Protocol:              "torrent",
			},
			wantProblem: "Download client can't fetch the magnet",
			wantSev:     SeverityError,
			wantActions: []string{ActionBlocklistSearch},
		},
		{
			name: "import pending stuck with no messages",
			sig: QueueSignal{
				TrackedDownloadStatus: "ok",
				TrackedDownloadState:  "importPending",
			},
			wantProblem: "Waiting to import",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionProcess, ActionManualImport},
		},
		{
			name: "failed download",
			sig: QueueSignal{
				TrackedDownloadStatus: "ok",
				TrackedDownloadState:  "failed",
			},
			wantProblem: "Download failed",
			wantSev:     SeverityError,
			wantActions: []string{ActionBlocklistSearch},
		},
		{
			name: "healthy downloading item",
			sig: QueueSignal{
				TrackedDownloadStatus: "ok",
				TrackedDownloadState:  "downloading",
				Protocol:              "usenet",
			},
			wantProblem: "",
			wantSev:     SeverityOK,
			wantActions: []string{ActionNone},
		},
		{
			name: "healthy importing item with empty status status",
			sig: QueueSignal{
				TrackedDownloadStatus: "",
				TrackedDownloadState:  "importing",
			},
			wantProblem: "",
			wantSev:     SeverityOK,
			wantActions: []string{ActionNone},
		},
		{
			name: "warning with unrecognized message falls back",
			sig: QueueSignal{
				TrackedDownloadStatus: "warning",
				TrackedDownloadState:  "importBlocked",
				StatusMessages:        msg("Some brand new reason Sonarr invented last week"),
			},
			wantProblem: "Import blocked",
			wantSev:     SeverityWarning,
			wantActions: []string{ActionManualImport, ActionBlocklistSearch},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Diagnose(tc.sig)
			if got.Problem != tc.wantProblem {
				t.Errorf("Problem = %q, want %q", got.Problem, tc.wantProblem)
			}
			if got.Severity != tc.wantSev {
				t.Errorf("Severity = %q, want %q", got.Severity, tc.wantSev)
			}
			if !slices.Equal(got.SuggestedActions, tc.wantActions) {
				t.Errorf("SuggestedActions = %v, want %v", got.SuggestedActions, tc.wantActions)
			}
			if tc.wantSev != SeverityOK && got.Transparency == "" {
				t.Errorf("expected a transparency sentence for a non-ok diagnosis")
			}
		})
	}
}

func TestDiagnoseFirstMatchWins(t *testing.T) {
	// "No files found are eligible" should not be shadowed by the broader
	// "sample" rule even though both could plausibly co-occur; the eligible
	// rule is earlier and more specific.
	sig := QueueSignal{
		TrackedDownloadStatus: "warning",
		TrackedDownloadState:  "importBlocked",
		StatusMessages: []StatusMessage{{
			Messages: []string{
				"No files found are eligible for import in /downloads/X",
				"Sample",
			},
		}},
	}
	got := Diagnose(sig)
	if got.Problem != "Nothing importable in the folder" {
		t.Fatalf("first-match-wins broken: got %q", got.Problem)
	}
}
