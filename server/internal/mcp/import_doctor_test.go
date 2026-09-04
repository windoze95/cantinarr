package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// diagnose_queue is the one surface the agent actually follows, so provenance
// has to reach it there. A stalled release the service picked up on its own,
// for a movie the library already holds, must render the abandon fix — and the
// same release, if a search produced it, must not.
func TestDiagnoseQueueUsesGrabProvenanceForAStuckUpgrade(t *testing.T) {
	const queueJSON = `{"totalRecords":1,"records":[{"id":42,"movieId":7,"downloadId":"dl-1","protocol":"torrent","title":"Stuck.Release","trackedDownloadStatus":"error","errorMessage":"The download is stalled with no connections","movie":{"id":7,"title":"Scoped Movie","year":2026,"tmdbId":550,"movieFileId":622}}]}`

	cases := []struct {
		name         string
		historyJSON  string
		wantAction   string
		unwantAction string
	}{
		{
			name:         "picked up on its own, copy already in the library",
			historyJSON:  `{"totalRecords":1,"records":[{"id":1,"movieId":7,"eventType":"grabbed","downloadId":"dl-1","data":{"releaseSource":"Rss"}}]}`,
			wantAction:   "blocklist_only",
			unwantAction: `"action": "blocklist_search"`,
		},
		{
			name:         "a search produced it, so the service decides",
			historyJSON:  `{"totalRecords":1,"records":[{"id":1,"movieId":7,"eventType":"grabbed","downloadId":"dl-1","data":{"releaseSource":"UserInvokedSearch"}}]}`,
			wantAction:   "blocklist_search",
			unwantAction: `"action": "blocklist_only"`,
		},
		{
			name:         "provenance unknown never assumes nobody asked",
			historyJSON:  `{"totalRecords":0,"records":[]}`,
			wantAction:   "blocklist_search",
			unwantAction: `"action": "blocklist_only"`,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v3/queue":
					_, _ = w.Write([]byte(queueJSON))
				case "/api/v3/history":
					if r.URL.Query().Get("eventType") != "1" {
						t.Errorf("provenance lookup asked for eventType=%q, want the grabbed events", r.URL.Query().Get("eventType"))
					}
					_, _ = w.Write([]byte(tt.historyJSON))
				default:
					t.Errorf("unexpected radarr request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer arrServer.Close()

			server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})
			result, err := server.ExecuteTool(
				context.Background(),
				"diagnose_queue",
				json.RawMessage(`{"media_type":"movie"}`),
				adminCallContext(),
			)
			if err != nil {
				t.Fatalf("diagnose_queue: %v", err)
			}
			if !strings.Contains(result.Text, tt.wantAction) {
				t.Fatalf("diagnosis missing %q:\n%s", tt.wantAction, result.Text)
			}
			if strings.Contains(result.Text, tt.unwantAction) {
				t.Fatalf("diagnosis rendered %q:\n%s", tt.unwantAction, result.Text)
			}
		})
	}
}

// The Import Doctor's music arm: a stuck Lidarr download is diagnosed with the
// same verbs as the other services, the "→ next:" line names media_type music
// so the fix tools route to Lidarr, and rescan renders the artist_id resolve
// hint (albums carry no TMDB id). Before this arm existed, media_type=music
// scanned nothing and media_type=all silently skipped the Lidarr queue.
func TestDiagnoseQueueScansTheLidarrQueue(t *testing.T) {
	const queueJSON = `{"totalRecords":2,"records":[
		{"id":42,"artistId":3,"albumId":77,"downloadId":"dl-1","protocol":"torrent","title":"Stuck.Album","status":"completed","trackedDownloadStatus":"warning","trackedDownloadState":"importPending","statusMessages":[{"title":"Stuck.Album","messages":["No files found are eligible for import in /downloads/Stuck.Album"]}],"artist":{"id":3,"artistName":"Scoped Artist"},"album":{"id":77,"title":"Scoped Album"}},
		{"id":43,"artistId":4,"albumId":78,"downloadId":"dl-2","protocol":"usenet","title":"Healthy.Album","status":"downloading","trackedDownloadStatus":"ok","trackedDownloadState":"downloading","artist":{"id":4,"artistName":"Other Artist"},"album":{"id":78,"title":"Other Album"}}]}`
	lidarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/queue" {
			t.Errorf("unexpected lidarr request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(queueJSON))
	}))
	defer lidarrServer.Close()
	server := newDefaultInstanceToolServer(t, map[string]string{"lidarr": lidarrServer.URL})

	for _, mediaType := range []string{"music", "all"} {
		result, err := server.ExecuteTool(context.Background(), "diagnose_queue",
			json.RawMessage(`{"media_type":"`+mediaType+`"}`), adminCallContext())
		if err != nil {
			t.Fatalf("diagnose_queue %s: %v", mediaType, err)
		}
		if !strings.Contains(result.Text, "[queue 42] (music) Scoped Artist — Scoped Album") {
			t.Fatalf("media_type=%s did not diagnose the stuck album:\n%s", mediaType, result.Text)
		}
		if !strings.Contains(result.Text, `get_manual_import_candidates {"queue_id": 42, "media_type": "music"}`) {
			t.Fatalf("media_type=%s next call does not route to the music arm:\n%s", mediaType, result.Text)
		}
		if strings.Contains(result.Text, "queue 43") || !strings.Contains(result.Text, "1 other item(s) are healthy") {
			t.Fatalf("media_type=%s miscounted the healthy album:\n%s", mediaType, result.Text)
		}
	}

	// Narrowed to an album that is not in the queue, the stuck one is out of
	// scope and the read says what it looked at rather than reporting a clean
	// queue.
	scoped, err := server.ExecuteTool(context.Background(), "diagnose_queue",
		json.RawMessage(`{"media_type":"music","album_id":99}`), adminCallContext())
	if err != nil {
		t.Fatalf("scoped diagnose_queue: %v", err)
	}
	if strings.Contains(scoped.Text, "queue 42") || !strings.Contains(scoped.Text, "Nothing for this album is in the queue") {
		t.Fatalf("album scope leaked or went unnamed:\n%s", scoped.Text)
	}

	if got := nextCalls("music", 42, 0, []string{arr.ActionRescan}); !strings.HasPrefix(got, "rescan_media") ||
		!strings.Contains(got, "artist_id") || strings.Contains(got, "tmdb_id") {
		t.Fatalf("music rescan hint = %q, want the artist_id resolve hint", got)
	}
}

// get_manual_import_candidates for music says which mapping a file lacks:
// Lidarr silently skips a manual-import file without artist, album, and track
// ids, so "not matched" has to be visible before execute_manual_import runs.
func TestGetManualImportCandidatesListsLidarrTrackMappings(t *testing.T) {
	const queueJSON = `{"totalRecords":1,"records":[{"id":42,"artistId":3,"albumId":77,"downloadId":"dl-1","protocol":"torrent","title":"Stuck.Album","status":"completed","trackedDownloadStatus":"warning","trackedDownloadState":"importPending","artist":{"id":3,"artistName":"Scoped Artist"},"album":{"id":77,"title":"Scoped Album"}}]}`
	const candidatesJSON = `[
		{"id":1,"path":"/downloads/Stuck.Album/01.flac","name":"01.flac","size":30000000,"artist":{"id":3},"album":{"id":77},"albumReleaseId":5,"tracks":[{"id":901}],"quality":{"quality":{"name":"FLAC"}},"rejections":[]},
		{"id":2,"path":"/downloads/Stuck.Album/cover.jpg.mp3","name":"cover.jpg.mp3","size":4000,"tracks":[],"quality":{"quality":{"name":"Unknown"}},"rejections":[{"reason":"Unable to parse file","type":"permanent"}]}]`
	lidarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/queue":
			_, _ = w.Write([]byte(queueJSON))
		case "/api/v1/manualimport":
			if r.URL.Query().Get("downloadId") != "dl-1" {
				t.Errorf("manualimport asked for downloadId=%q", r.URL.Query().Get("downloadId"))
			}
			_, _ = w.Write([]byte(candidatesJSON))
		default:
			t.Errorf("unexpected lidarr request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer lidarrServer.Close()
	server := newDefaultInstanceToolServer(t, map[string]string{"lidarr": lidarrServer.URL})

	result, err := server.ExecuteTool(context.Background(), "get_manual_import_candidates",
		json.RawMessage(`{"media_type":"music","queue_id":42}`), adminCallContext())
	if err != nil {
		t.Fatalf("get_manual_import_candidates: %v", err)
	}
	for _, want := range []string{
		"2 candidate file(s) for Scoped Artist — Scoped Album:",
		"maps to artist id 3, album id 77, 1 track(s)",
		"not matched to an album's tracks",
		"rejections: Unable to parse file (permanent)",
		"Use execute_manual_import to import these",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("candidates missing %q:\n%s", want, result.Text)
		}
	}

	// A queue id outside the album scope is absence within that scope, not a
	// candidate list for some other album.
	scoped, err := server.ExecuteTool(context.Background(), "get_manual_import_candidates",
		json.RawMessage(`{"media_type":"music","queue_id":42,"album_id":78}`), adminCallContext())
	if err != nil {
		t.Fatalf("scoped get_manual_import_candidates: %v", err)
	}
	if !strings.Contains(scoped.Text, "No music queue item with id 42 in this scope") {
		t.Fatalf("scope mismatch answered with candidates:\n%s", scoped.Text)
	}
}
