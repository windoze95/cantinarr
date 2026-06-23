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
