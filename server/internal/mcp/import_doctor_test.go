package mcp

import (
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// TestNextCalls verifies that each suggested-action verb maps to the exact next
// MCP tool call a weak agent can run verbatim.
func TestNextCalls(t *testing.T) {
	cases := []struct {
		name      string
		mediaType string
		queueID   int
		tmdbID    int
		verbs     []string
		want      string
	}{
		{
			name:      "manual_import renders candidates then execute",
			mediaType: "tv",
			queueID:   42,
			tmdbID:    0,
			verbs:     []string{arr.ActionManualImport, arr.ActionForceImport},
			want:      `get_manual_import_candidates {"queue_id": 42, "media_type": "tv"} then execute_manual_import {"queue_id": 42, "media_type": "tv"}`,
		},
		{
			name:      "force_import sets force true",
			mediaType: "movie",
			queueID:   7,
			tmdbID:    603,
			verbs:     []string{arr.ActionForceImport},
			want:      `execute_manual_import {"queue_id": 7, "media_type": "movie", "force": true}`,
		},
		{
			name:      "remove maps to remediate",
			mediaType: "movie",
			queueID:   9,
			verbs:     []string{arr.ActionRemove},
			want:      `remediate_queue_item {"queue_id": 9, "media_type": "movie", "action": "remove"}`,
		},
		{
			name:      "blocklist_search maps to remediate",
			mediaType: "tv",
			queueID:   3,
			verbs:     []string{arr.ActionBlocklistSearch},
			want:      `remediate_queue_item {"queue_id": 3, "media_type": "tv", "action": "blocklist_search"}`,
		},
		{
			name:      "change_category maps to remediate",
			mediaType: "movie",
			queueID:   5,
			verbs:     []string{arr.ActionChangeCategory},
			want:      `remediate_queue_item {"queue_id": 5, "media_type": "movie", "action": "change_category"}`,
		},
		{
			name:      "rescan with resolved tmdb renders rescan_media",
			mediaType: "movie",
			queueID:   11,
			tmdbID:    603,
			verbs:     []string{arr.ActionRescan},
			want:      `rescan_media {"tmdb_id": 603, "media_type": "movie"}`,
		},
		{
			name:      "process with resolved tmdb renders rescan_media",
			mediaType: "movie",
			queueID:   11,
			tmdbID:    27205,
			verbs:     []string{arr.ActionProcess, arr.ActionManualImport},
			want:      `rescan_media {"tmdb_id": 27205, "media_type": "movie"}`,
		},
		{
			name:      "none is not actionable",
			mediaType: "tv",
			queueID:   1,
			verbs:     []string{arr.ActionNone},
			want:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextCalls(tc.mediaType, tc.queueID, tc.tmdbID, tc.verbs)
			if got != tc.want {
				t.Errorf("nextCalls() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNextCallsRescanWithoutTmdbFallsBack verifies that when a tmdb_id cannot be
// resolved (Sonarr queue items carry only a TVDB id), the rescan path names the
// tool with a resolve hint instead of emitting a bogus call.
func TestNextCallsRescanWithoutTmdbFallsBack(t *testing.T) {
	got := nextCalls("tv", 8, 0, []string{arr.ActionRescan})
	if !strings.HasPrefix(got, "rescan_media") {
		t.Fatalf("expected rescan_media fallback, got %q", got)
	}
	if strings.Contains(got, `"tmdb_id": 0`) {
		t.Fatalf("must not emit a tmdb_id of 0: %q", got)
	}
	if !strings.Contains(got, "tmdb_id") {
		t.Fatalf("fallback should still tell the agent to resolve a tmdb_id: %q", got)
	}
}

// TestRenderHealthSectionSkipsOK verifies ok-type checks are hidden and non-ok
// checks render with type, message, source, and wiki URL.
func TestRenderHealthSectionSkipsOK(t *testing.T) {
	radarrChecks := []radarr.HealthCheck{
		{Source: "IndexerStatusCheck", Type: "ok", Message: "all good"},
		{Source: "DownloadClientCheck", Type: "error", Message: "Unable to communicate with qBittorrent.", WikiURL: "https://wiki.servarr.com/x"},
	}
	out := renderHealthSection("Radarr", radarrChecks)
	if strings.Contains(out, "all good") {
		t.Errorf("ok checks should be skipped: %q", out)
	}
	if !strings.Contains(out, "Unable to communicate with qBittorrent.") {
		t.Errorf("error check message missing: %q", out)
	}
	if !strings.Contains(out, "[error]") {
		t.Errorf("type should be rendered: %q", out)
	}
	if !strings.Contains(out, "DownloadClientCheck") {
		t.Errorf("source should be rendered: %q", out)
	}
	if !strings.Contains(out, "https://wiki.servarr.com/x") {
		t.Errorf("wiki url should be rendered: %q", out)
	}
}

// TestRenderHealthSectionAllOK verifies a clean service reports no problems.
func TestRenderHealthSectionAllOK(t *testing.T) {
	sonarrChecks := []sonarr.HealthCheck{
		{Source: "RootFolderCheck", Type: "ok", Message: "fine"},
	}
	out := renderHealthSection("Sonarr", sonarrChecks)
	if !strings.Contains(out, "no warnings or errors") {
		t.Errorf("expected clean health summary, got %q", out)
	}
}
