package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// These tests drive the two mutations that act on media the *arr already
// IMPORTED. The risk they cover is not "did an HTTP call happen" but "did
// exactly the right set of remote records change, in the right order": deleting
// somebody's files and standing releases down is irreversible, and the fixture
// throughout is the live case from issue #814 (Futurama season 11, nine files
// imported 2026-07-21, thirteen days before E1 airs).

// --- fake Sonarr ---

// sonarrFileFake is a Sonarr stand-in for the imported-file mutations: it
// serves the four reads they make, accepts the two writes, and records every
// request in order so a test can assert not just which calls happened but which
// happened first.
type sonarrFileFake struct {
	t        *testing.T
	rec      *callRecorder
	series   []map[string]any
	episodes []map[string]any
	files    []map[string]any
	history  []map[string]any
	// globalHistory answers /api/v3/history, the paged whole-library log the
	// per-series endpoint exists to replace.
	globalHistory []map[string]any

	// historyStatus, deleteFails, failedStatus, policyStatus and commandStatus
	// let a test fail exactly one remote operation, which is how partial-success
	// reporting is proved.
	historyStatus  int
	deleteFails    map[int]bool
	failedStatus   int
	policyStatus   int
	commandStatus  int
	autoRedownload bool

	// libraryMu guards the episode and file fixtures, which a successful delete
	// mutates. The repair reads the season again to decide what to replace, so a
	// fake that kept serving the file it had just deleted would answer "nothing
	// to search" for a season it had emptied itself — and every assertion about
	// the second half of the fix would be measuring the fixture, not the code.
	libraryMu sync.Mutex

	// commands captures every POST /api/v3/command body (the search dispatch).
	// The httptest handler runs on another goroutine than the assertions, so
	// it is guarded exactly like callRecorder is.
	commandMu sync.Mutex
	commands  []map[string]any
}

func (f *sonarrFileFake) commandsSeen() []map[string]any {
	f.commandMu.Lock()
	defer f.commandMu.Unlock()
	return append([]map[string]any(nil), f.commands...)
}

func (f *sonarrFileFake) start() *httptest.Server {
	f.t.Helper()
	if f.rec == nil {
		f.rec = &callRecorder{}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// record() drains the body, so keep a copy for the handler below.
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		f.rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series":
			_ = json.NewEncoder(w).Encode(matchingRecords(f.series, "tvdbId", r.URL.Query().Get("tvdbId")))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v3/series/"):
			for _, s := range f.series {
				if fmt.Sprintf("%v", s["id"]) == fmt.Sprintf("%d", pathTailInt(f.t, r.URL.Path)) {
					_ = json.NewEncoder(w).Encode(s)
					return
				}
			}
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/episode":
			_ = json.NewEncoder(w).Encode(f.episodesFor(r.URL.Query().Get("seasonNumber")))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/episodefile":
			_ = json.NewEncoder(w).Encode(f.fileRecords())
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/history":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"totalRecords": len(f.globalHistory), "records": f.globalHistory,
			})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v3/episodefile/"):
			id := pathTailInt(f.t, r.URL.Path)
			if f.deleteFails[id] {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			f.forgetFile(id)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/history/series":
			if f.historyStatus != 0 {
				w.WriteHeader(f.historyStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(f.history)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v3/history/failed/"):
			if f.failedStatus != 0 {
				w.WriteHeader(f.failedStatus)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/config/downloadclient":
			if f.policyStatus != 0 {
				w.WriteHeader(f.policyStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"autoRedownloadFailed": f.autoRedownload})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				f.t.Errorf("decode command %q: %v", raw, err)
			}
			f.commandMu.Lock()
			f.commands = append(f.commands, payload)
			f.commandMu.Unlock()
			if f.commandStatus != 0 {
				w.WriteHeader(f.commandStatus)
				return
			}
			w.WriteHeader(http.StatusCreated)
		default:
			f.t.Errorf("unexpected sonarr request %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	f.t.Cleanup(server.Close)
	return server
}

// episodesFor narrows the fixture the way Sonarr narrows /api/v3/episode, so a
// caller that forgets the season filter cannot accidentally pass.
func (f *sonarrFileFake) episodesFor(season string) []map[string]any {
	f.libraryMu.Lock()
	defer f.libraryMu.Unlock()
	return copyRecords(matchingRecords(f.episodes, "seasonNumber", season))
}

func (f *sonarrFileFake) fileRecords() []map[string]any {
	f.libraryMu.Lock()
	defer f.libraryMu.Unlock()
	return copyRecords(f.files)
}

// forgetFile applies to the fixture what a successful DELETE applies to the
// library: the file record is gone, and the episode that held it now holds
// nothing. That second half is what makes the season read as "aired and
// missing" on the repair's own re-read.
func (f *sonarrFileFake) forgetFile(id int) {
	f.libraryMu.Lock()
	defer f.libraryMu.Unlock()
	for _, ep := range f.episodes {
		if fmt.Sprintf("%v", ep["episodeFileId"]) == fmt.Sprintf("%d", id) {
			ep["hasFile"] = false
			ep["episodeFileId"] = 0
		}
	}
	remaining := make([]map[string]any, 0, len(f.files))
	for _, file := range f.files {
		if fmt.Sprintf("%v", file["id"]) != fmt.Sprintf("%d", id) {
			remaining = append(remaining, file)
		}
	}
	f.files = remaining
}

// copyRecords hands the handler its own maps to encode, so a record being
// serialised on one goroutine is never the record a delete is rewriting on
// another.
func copyRecords(records []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		clone := make(map[string]any, len(rec))
		for key, value := range rec {
			clone[key] = value
		}
		out = append(out, clone)
	}
	return out
}

// matchingRecords narrows a fixture on one field the way the *arr narrows on
// the matching query parameter; an empty value means the parameter was absent
// and every record is returned.
func matchingRecords(records []map[string]any, field, value string) []map[string]any {
	if value == "" {
		return records
	}
	out := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		if fmt.Sprintf("%v", rec[field]) == value {
			out = append(out, rec)
		}
	}
	return out
}

func pathTailInt(t *testing.T, path string) int {
	t.Helper()
	idx := strings.LastIndex(path, "/")
	var id int
	if _, err := fmt.Sscanf(path[idx+1:], "%d", &id); err != nil {
		t.Fatalf("path %q has no numeric id: %v", path, err)
	}
	return id
}

// futuramaSeries is the library record every fixture here resolves through:
// bridge-less, so seriesByTMDB matches on the TMDB id from GET /api/v3/series.
func futuramaSeries() []map[string]any {
	return []map[string]any{
		{"id": 9, "title": "Other Show", "tmdbId": 111, "tvdbId": 999},
		{"id": 28, "title": "Futurama", "tmdbId": 615, "tvdbId": 73871},
	}
}

// futuramaS11Episodes is the live season: E1+E2 double premiere on
// 2026-08-04, then weekly to 2026-09-29; E1..E9 hold files, E10 does not.
// Episode ids are 1100+n and file ids 5100+n so a mixed-up mapping shows up as
// a wrong number rather than an off-by-one that still passes.
func futuramaS11Episodes() []map[string]any {
	airs := []string{
		"2026-08-04T00:00:00Z", "2026-08-04T00:23:00Z", "2026-08-11T00:00:00Z",
		"2026-08-18T00:00:00Z", "2026-08-25T00:00:00Z", "2026-09-01T00:00:00Z",
		"2026-09-08T00:00:00Z", "2026-09-15T00:00:00Z", "2026-09-22T00:00:00Z",
		"2026-09-29T00:00:00Z",
	}
	titles := []string{
		"Beef", "The Cure for Boredom", "How the West Was 1010001",
		"Attack of the Clothes", "Ship Happens", "Lord Nibbler in the Nothingverse",
		"Love at First Scam", "Cold Warriors II", "The Bots and the Bees II", "Finale II",
	}
	out := make([]map[string]any, 0, 10)
	for i := 0; i < 10; i++ {
		ep := map[string]any{
			"id": 1100 + i + 1, "seriesId": 28, "seasonNumber": 11, "episodeNumber": i + 1,
			"title": titles[i], "airDateUtc": airs[i], "monitored": true,
		}
		if i < 9 {
			ep["hasFile"] = true
			ep["episodeFileId"] = 5100 + i + 1
		} else {
			ep["hasFile"] = false
			ep["episodeFileId"] = 0
		}
		out = append(out, ep)
	}
	return out
}

// futuramaFileImportedAt is when all nine files landed — one batch, thirteen
// days before E1 airs.
const futuramaFileImportedAt = "2026-07-21T09:30:00Z"

// futuramaProposedAt is when the repair was proposed: after those files landed,
// so the staleness gate recognises every one of them as a file the diagnosis
// actually saw.
var futuramaProposedAt = time.Date(2026, 8, 3, 14, 5, 0, 0, time.UTC)

// futuramaS11FileRecords are the file records behind those episodes. The
// episode says only THAT it holds a file; only this record says when the file
// arrived, which is what the staleness gate turns on.
func futuramaS11FileRecords() []map[string]any {
	return futuramaS11FileRecordsImportedAt(map[int]string{})
}

// futuramaS11FileRecordsImportedAt lets one file carry a different import time
// (or none at all, with an empty string) so a single episode can be made to
// look replaced.
func futuramaS11FileRecordsImportedAt(overrides map[int]string) []map[string]any {
	out := make([]map[string]any, 0, 9)
	for i := 1; i <= 9; i++ {
		id := 5100 + i
		rec := map[string]any{
			"id": id, "seriesId": 28, "seasonNumber": 11,
			"relativePath": fmt.Sprintf("Season 11/Futurama - S11E%02d.mkv", i),
			"sceneName":    fmt.Sprintf("Futurama.S11E%02d.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM", i),
			"quality":      map[string]any{"quality": map[string]any{"name": "WEBDL-1080p"}},
		}
		dateAdded, overridden := overrides[id]
		if !overridden {
			dateAdded = futuramaFileImportedAt
		}
		if dateAdded != "" {
			rec["dateAdded"] = dateAdded
		}
		out = append(out, rec)
	}
	return out
}

// perEpisodeGrabHistory is the production shape: nine separate RSS grabs, nine
// download ids, each imported onto its own episode. Newest first, as Sonarr
// sends it.
func perEpisodeGrabHistory(episodes int) []map[string]any {
	var out []map[string]any
	for i := episodes; i >= 1; i-- {
		out = append(out,
			map[string]any{
				"id": int64(9000 + i), "seriesId": 28, "episodeId": 1100 + i,
				"eventType": "downloadFolderImported", "downloadId": fmt.Sprintf("SABnzbd_nzo_%02d", i),
				"sourceTitle": fmt.Sprintf("Futurama.S11E%02d.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM", i),
			},
			map[string]any{
				"id": int64(8000 + i), "seriesId": 28, "episodeId": 1100 + i,
				"eventType": "grabbed", "downloadId": fmt.Sprintf("SABnzbd_nzo_%02d", i),
				"sourceTitle": fmt.Sprintf("Futurama.S11E%02d.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM", i),
			},
		)
	}
	return out
}

// --- a season caught mid-run ---
//
// The live fixture above is a season where NOTHING has aired yet, which is the
// moment issue #814 was captured. The repair resolves "aired" from time.Now()
// at the instant it runs, so everything about what it searches needs a season
// that straddles now — and anchored to the real clock, because a fixed date
// quietly changes meaning as the calendar moves past it.

// midFlightSeasonEpisodes is that season: E01 and E02 are out, E03..E05 are
// not, and all five hold a file that landed before any of them aired.
func midFlightSeasonEpisodes() []map[string]any {
	now := time.Now().UTC()
	airs := []time.Duration{-48 * time.Hour, -24 * time.Hour, 24 * time.Hour, 48 * time.Hour, 72 * time.Hour}
	out := make([]map[string]any, 0, len(airs))
	for i, offset := range airs {
		out = append(out, map[string]any{
			"id": 1100 + i + 1, "seriesId": 28, "seasonNumber": 11, "episodeNumber": i + 1,
			"title": fmt.Sprintf("Episode %d", i+1), "airDateUtc": now.Add(offset).Format(time.RFC3339),
			"monitored": true, "hasFile": true, "episodeFileId": 5100 + i + 1,
		})
	}
	return out
}

// midFlightFileRecords are the files behind them: one batch, imported a month
// ago — before the first episode aired and long before the fix was proposed, so
// the staleness gate spares none of them.
func midFlightFileRecords() []map[string]any {
	imported := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	out := make([]map[string]any, 0, 5)
	for i := 1; i <= 5; i++ {
		out = append(out, map[string]any{
			"id": 5100 + i, "seriesId": 28, "seasonNumber": 11, "dateAdded": imported,
			"relativePath": fmt.Sprintf("Season 11/Futurama - S11E%02d.mkv", i),
			"sceneName":    fmt.Sprintf("Futurama.S11E%02d.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM", i),
		})
	}
	return out
}

// midFlightProposedAt is "the fix was proposed a moment ago".
func midFlightProposedAt() time.Time { return time.Now().UTC() }

// midFlightFake wires the three together with the production history shape.
func midFlightFake(t *testing.T) *sonarrFileFake {
	t.Helper()
	return &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: midFlightSeasonEpisodes(), files: midFlightFileRecords(),
		history: perEpisodeGrabHistory(5),
	}
}

// episodeSearchIDs returns the episode ids of the single search the repair
// dispatched, or nil when it dispatched none. Anything other than one
// EpisodeSearch fails the test: a SeasonSearch would re-grab the whole season,
// including the episodes that are the reason this fix exists.
func episodeSearchIDs(t *testing.T, fake *sonarrFileFake) []int {
	t.Helper()
	commands := fake.commandsSeen()
	if len(commands) == 0 {
		return nil
	}
	if len(commands) != 1 {
		t.Fatalf("the repair dispatched %d commands: %+v", len(commands), commands)
	}
	if commands[0]["name"] != "EpisodeSearch" {
		t.Fatalf("command = %+v, want EpisodeSearch", commands[0])
	}
	return commandEpisodeIDs(t, commands[0])
}

func intPtr(v int) *int { return &v }

// --- DeleteMediaFilesHelper, TV ---

// TestDeleteMediaFilesDeletesExactlyTheRequestedEpisodes pins the blast radius:
// the two episodes named lose their files and nothing else is touched, and the
// grab that delivered each one is the release marked failed.
func TestDeleteMediaFilesDeletesExactlyTheRequestedEpisodes(t *testing.T) {
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: futuramaS11Episodes(), files: futuramaS11FileRecords(),
		history: perEpisodeGrabHistory(9), autoRedownload: true,
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{3, 7}, true, futuramaProposedAt)
	if err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}

	deletes := requestsMatching(fake.rec, http.MethodDelete, "/api/v3/episodefile/")
	if want := []string{"/api/v3/episodefile/5103", "/api/v3/episodefile/5107"}; !equalStrings(deletes, want) {
		t.Fatalf("deleted files = %v, want exactly %v", deletes, want)
	}
	posts := requestsMatching(fake.rec, http.MethodPost, "/api/v3/history/failed/")
	if want := []string{"/api/v3/history/failed/8003", "/api/v3/history/failed/8007"}; !equalStrings(posts, want) {
		t.Fatalf("blocklisted grabs = %v, want exactly %v", posts, want)
	}
	for _, want := range []string{"Deleted 2 files", "S11E03, S11E07", "Blocklisted 2 release(s)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("outcome missing %q:\n%s", want, text)
		}
	}
}

// TestDeleteMediaFilesCollapsesOneSeasonPackIntoOneBlocklist is the single most
// important behaviour in this file. Ten episodes that arrived inside ONE
// download are one release.
//
// Sonarr's history is per EPISODE, so a season pack writes ten grabbed rows and
// ten imported rows — all carrying the one download id. Walking that history
// per episode would mark the same release failed ten times: ten entries in the
// admin's blocklist for a release that was grabbed once.
func TestDeleteMediaFilesCollapsesOneSeasonPackIntoOneBlocklist(t *testing.T) {
	episodes := futuramaS11Episodes()
	// The whole season came from one pack, so give E10 a file too.
	episodes[9]["hasFile"] = true
	episodes[9]["episodeFileId"] = 5110

	const (
		packDownloadID = "SABnzbd_nzo_pack"
		packTitle      = "Futurama.S11.COMPLETE.1080p.DSNP.WEB-DL.DDP5.1.H.264-CM"
	)
	var history []map[string]any
	for i := 10; i >= 1; i-- {
		history = append(history, map[string]any{
			"id": int64(9000 + i), "seriesId": 28, "episodeId": 1100 + i,
			"eventType": "downloadFolderImported", "downloadId": packDownloadID,
			"sourceTitle": packTitle,
		})
	}
	// One grabbed row per episode of the pack, newest first — the shape Sonarr
	// actually records, and the reason deduping by download id is load-bearing.
	for i := 10; i >= 1; i-- {
		history = append(history, map[string]any{
			"id": int64(8000 + i), "seriesId": 28, "episodeId": 1100 + i,
			"eventType": "grabbed", "downloadId": packDownloadID,
			"sourceTitle": packTitle,
		})
	}

	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: episodes, files: futuramaS11FileRecords(),
		history: history, autoRedownload: true,
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, true, futuramaProposedAt)
	if err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}

	if deletes := requestsMatching(fake.rec, http.MethodDelete, "/api/v3/episodefile/"); len(deletes) != 10 {
		t.Fatalf("a ten-episode pack deleted %d file(s): %v", len(deletes), deletes)
	}
	// 8010 is the newest of the pack's ten grabbed rows, which all record the
	// same single grab; the walk takes the first one history offers.
	posts := requestsMatching(fake.rec, http.MethodPost, "/api/v3/history/failed/")
	if want := []string{"/api/v3/history/failed/8010"}; !equalStrings(posts, want) {
		t.Fatalf("one season pack blocklisted %v, want exactly %v — one download is one release", posts, want)
	}
	if !strings.Contains(text, "Blocklisted 1 release(s)") {
		t.Fatalf("outcome should name one release:\n%s", text)
	}
}

// TestDeleteMediaFilesBlocklistsEveryDownloadInTheProductionShape is the other
// half of the same rule. Nine files that arrived as nine separate RSS grabs are
// nine releases, and collapsing them would leave eight bad releases free to be
// grabbed again.
func TestDeleteMediaFilesBlocklistsEveryDownloadInTheProductionShape(t *testing.T) {
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: futuramaS11Episodes(), files: futuramaS11FileRecords(),
		history: perEpisodeGrabHistory(9), autoRedownload: true,
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2, 3, 4, 5, 6, 7, 8, 9}, true, futuramaProposedAt)
	if err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}

	posts := requestsMatching(fake.rec, http.MethodPost, "/api/v3/history/failed/")
	want := make([]string, 0, 9)
	for i := 1; i <= 9; i++ {
		want = append(want, fmt.Sprintf("/api/v3/history/failed/%d", 8000+i))
	}
	if !equalStrings(posts, want) {
		t.Fatalf("nine separate grabs blocklisted %v, want %v", posts, want)
	}
	if !strings.Contains(text, "Blocklisted 9 release(s)") {
		t.Fatalf("outcome should name nine releases:\n%s", text)
	}
}

// TestDeleteMediaFilesPicksTheNewestGrabForADownload proves the walk takes the
// FIRST grab it meets for a download id. History arrives newest-first, so an
// older repeat of the same download must not be the record marked failed.
func TestDeleteMediaFilesPicksTheNewestGrabForADownload(t *testing.T) {
	history := []map[string]any{
		{"id": int64(9003), "seriesId": 28, "episodeId": 1103, "eventType": "downloadFolderImported", "downloadId": "dl-3", "sourceTitle": "Newest.Import"},
		{"id": int64(8500), "seriesId": 28, "episodeId": 1103, "eventType": "grabbed", "downloadId": "dl-3", "sourceTitle": "Newest.Grab"},
		{"id": int64(8100), "seriesId": 28, "episodeId": 1103, "eventType": "grabbed", "downloadId": "dl-3", "sourceTitle": "Older.Grab"},
	}
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: futuramaS11Episodes(), files: futuramaS11FileRecords(), history: history, autoRedownload: true}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{3}, true, futuramaProposedAt)
	if err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}
	posts := requestsMatching(fake.rec, http.MethodPost, "/api/v3/history/failed/")
	if want := []string{"/api/v3/history/failed/8500"}; !equalStrings(posts, want) {
		t.Fatalf("blocklisted %v, want the newest grab %v", posts, want)
	}
	if !strings.Contains(text, "Newest.Grab") || strings.Contains(text, "Older.Grab") {
		t.Fatalf("outcome named the wrong release:\n%s", text)
	}
}

// TestDeleteMediaFilesPicksTheNewestImportForAnEpisode is the same rule one
// hop earlier: an episode re-imported from a second download must trace to the
// download that delivered the file being deleted now, not the one it replaced.
func TestDeleteMediaFilesPicksTheNewestImportForAnEpisode(t *testing.T) {
	history := []map[string]any{
		{"id": int64(9500), "seriesId": 28, "episodeId": 1103, "eventType": "downloadFolderImported", "downloadId": "dl-current", "sourceTitle": "Current.Import"},
		{"id": int64(9400), "seriesId": 28, "episodeId": 1103, "eventType": "downloadFolderImported", "downloadId": "dl-superseded", "sourceTitle": "Superseded.Import"},
		{"id": int64(8500), "seriesId": 28, "episodeId": 1103, "eventType": "grabbed", "downloadId": "dl-current", "sourceTitle": "Current.Grab"},
		{"id": int64(8400), "seriesId": 28, "episodeId": 1103, "eventType": "grabbed", "downloadId": "dl-superseded", "sourceTitle": "Superseded.Grab"},
	}
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: futuramaS11Episodes(), files: futuramaS11FileRecords(), history: history, autoRedownload: true}
	server := fake.start()

	if _, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{3}, true, futuramaProposedAt); err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}
	posts := requestsMatching(fake.rec, http.MethodPost, "/api/v3/history/failed/")
	if want := []string{"/api/v3/history/failed/8500"}; !equalStrings(posts, want) {
		t.Fatalf("blocklisted %v, want the grab behind the CURRENT file %v", posts, want)
	}
}

// TestDeleteMediaFilesDeletesEveryFileBeforeBlocklistingAnything pins the
// ordering the file's comment calls load-bearing: a replacement search fired
// while the old file is still on disk is rejected as "not an upgrade", so the
// fix would report success and change nothing.
func TestDeleteMediaFilesDeletesEveryFileBeforeBlocklistingAnything(t *testing.T) {
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: futuramaS11Episodes(), files: futuramaS11FileRecords(),
		history: perEpisodeGrabHistory(9), autoRedownload: true,
	}
	server := fake.start()

	if _, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2, 3}, true, futuramaProposedAt); err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}

	lastDelete, firstBlocklist := -1, -1
	for i, call := range fake.rec.all() {
		switch {
		case call.Method == http.MethodDelete && strings.HasPrefix(call.URI, "/api/v3/episodefile/"):
			lastDelete = i
		case call.Method == http.MethodPost && strings.HasPrefix(call.URI, "/api/v3/history/failed/"):
			if firstBlocklist < 0 {
				firstBlocklist = i
			}
		}
	}
	if lastDelete < 0 || firstBlocklist < 0 {
		t.Fatalf("expected both deletes and blocklists, got %+v", fake.rec.all())
	}
	if firstBlocklist < lastDelete {
		t.Fatalf("blocklist at call %d ran before the last delete at call %d — a replacement found while the old file is on disk is rejected as not an upgrade:\n%+v",
			firstBlocklist, lastDelete, fake.rec.all())
	}
}

// TestDeleteMediaFilesSkipsMissingAndFilelessEpisodes proves the two benign
// mismatches are reported, not raised: an episode number the season does not
// have, and one that holds no file. Both are ordinary when a fix was proposed
// minutes ago and the library moved since.
func TestDeleteMediaFilesSkipsMissingAndFilelessEpisodes(t *testing.T) {
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: futuramaS11Episodes(), files: futuramaS11FileRecords(),
		history: perEpisodeGrabHistory(9), autoRedownload: true,
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{3, 10, 42}, false, futuramaProposedAt)
	if err != nil {
		t.Fatalf("skipped episodes must not be an error: %v", err)
	}
	if deletes := requestsMatching(fake.rec, http.MethodDelete, "/api/v3/episodefile/"); !equalStrings(deletes, []string{"/api/v3/episodefile/5103"}) {
		t.Fatalf("deleted %v, want only the one episode that had a file", deletes)
	}
	for _, want := range []string{"Left alone:", "S11E10 (no file)", "S11E42 (not in this season)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("outcome missing %q:\n%s", want, text)
		}
	}
}

// --- the staleness gate ---

// stalenessEpisode is one episode of a purpose-built season: when it airs, and
// when the file it currently holds arrived. Those two instants plus the moment
// the fix was proposed are the entire input to the gate.
type stalenessEpisode struct {
	number     int
	airsAt     string // RFC3339; "" leaves the episode unscheduled
	importedAt string // RFC3339; "" gives the file no dateAdded at all
}

// stalenessFixture turns those into the episode and file records Sonarr would
// serve, keeping the id scheme every other fixture here uses.
func stalenessFixture(eps []stalenessEpisode) (episodes, files []map[string]any) {
	for _, ep := range eps {
		episode := map[string]any{
			"id": 1100 + ep.number, "seriesId": 28, "seasonNumber": 11, "episodeNumber": ep.number,
			"title": fmt.Sprintf("Episode %d", ep.number), "monitored": true,
			"hasFile": true, "episodeFileId": 5100 + ep.number,
		}
		if ep.airsAt != "" {
			episode["airDateUtc"] = ep.airsAt
		}
		episodes = append(episodes, episode)

		file := map[string]any{"id": 5100 + ep.number, "seriesId": 28, "seasonNumber": 11}
		if ep.importedAt != "" {
			file["dateAdded"] = ep.importedAt
		}
		files = append(files, file)
	}
	return episodes, files
}

// TestDeleteMediaFilesSparesAFileThatCouldBeGenuine is the destructive-action
// guard. A proposal waits on a human, and the service keeps working: once the
// episode airs, an upgrade can replace the impossible file with the real
// release. Approving the old proposal then deletes the RIGHT file and
// blocklists the RIGHT release — the exact opposite of the repair.
func TestDeleteMediaFilesSparesAFileThatCouldBeGenuine(t *testing.T) {
	episodes, files := stalenessFixture([]stalenessEpisode{
		// Aired, then re-imported six hours later: this could be the real thing.
		{number: 1, airsAt: "2026-08-04T00:00:00Z", importedAt: "2026-08-04T06:00:00Z"},
		// Still the file the diagnosis looked at.
		{number: 2, airsAt: "2026-08-04T00:23:00Z", importedAt: futuramaFileImportedAt},
	})
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: episodes, files: files,
		history: perEpisodeGrabHistory(2), autoRedownload: true,
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2}, true, futuramaProposedAt)
	if err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}
	if deletes := requestsMatching(fake.rec, http.MethodDelete, "/api/v3/episodefile/"); !equalStrings(deletes, []string{"/api/v3/episodefile/5102"}) {
		t.Fatalf("deleted %v — a file that arrived after the diagnosis and after the episode aired must be spared", deletes)
	}
	if !strings.Contains(text, "S11E01 (a different file arrived after this fix was proposed)") {
		t.Fatalf("outcome does not name the spared episode:\n%s", text)
	}
	// Its release must not be stood down either: the file is still there, and
	// blocklisting the grab behind it would ban the release that fixed things.
	posts := requestsMatching(fake.rec, http.MethodPost, "/api/v3/history/failed/")
	if !equalStrings(posts, []string{"/api/v3/history/failed/8002"}) {
		t.Fatalf("blocklisted %v — the spared episode's release must be left alone", posts)
	}
}

// TestDeleteMediaFilesStillDeletesAPreAirReplacement is the other half of the
// same rule, and the reason the gate is not simply "newer than the diagnosis".
// A replacement that STILL landed before its episode aired is exactly as
// impossible as the one it replaced; sparing it would leave the season broken
// and make the agent re-propose the identical fix forever.
func TestDeleteMediaFilesStillDeletesAPreAirReplacement(t *testing.T) {
	episodes, files := stalenessFixture([]stalenessEpisode{
		// Imported hours after the fix was proposed, but still before it airs.
		{number: 1, airsAt: "2026-08-04T00:00:00Z", importedAt: "2026-08-03T20:00:00Z"},
	})
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: episodes, files: files,
		history: perEpisodeGrabHistory(1), autoRedownload: true,
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1}, true, futuramaProposedAt)
	if err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}
	if deletes := requestsMatching(fake.rec, http.MethodDelete, "/api/v3/episodefile/"); !equalStrings(deletes, []string{"/api/v3/episodefile/5101"}) {
		t.Fatalf("deleted %v — a replacement that itself arrived pre-air is still impossible content", deletes)
	}
	if strings.Contains(text, "a different file arrived") {
		t.Fatalf("a pre-air replacement was spared:\n%s", text)
	}
	if posts := requestsMatching(fake.rec, http.MethodPost, "/api/v3/history/failed/"); !equalStrings(posts, []string{"/api/v3/history/failed/8001"}) {
		t.Fatalf("blocklisted %v, want the grab behind the deleted file", posts)
	}
}

// TestDeleteMediaFilesGateToleratesClockSkew: Cantinarr stamps the proposal and
// the *arr stamps the import, from two different clocks. A minute of drift must
// not read as a replaced file — while three minutes, past the slack, must.
func TestDeleteMediaFilesGateToleratesClockSkew(t *testing.T) {
	episodes, files := stalenessFixture([]stalenessEpisode{
		// Both aired days ago, so only the slack decides these two.
		{number: 1, airsAt: "2026-08-01T00:00:00Z", importedAt: "2026-08-03T14:06:00Z"},
		{number: 2, airsAt: "2026-08-01T00:00:00Z", importedAt: "2026-08-03T14:08:00Z"},
	})
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: episodes, files: files,
		history: perEpisodeGrabHistory(2), autoRedownload: true,
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2}, false, futuramaProposedAt)
	if err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}
	if deletes := requestsMatching(fake.rec, http.MethodDelete, "/api/v3/episodefile/"); !equalStrings(deletes, []string{"/api/v3/episodefile/5101"}) {
		t.Fatalf("deleted %v — one minute of clock drift is not a replaced file, three minutes is", deletes)
	}
	if !strings.Contains(text, "S11E02 (a different file arrived after this fix was proposed)") {
		t.Fatalf("outcome does not name the file that really did arrive later:\n%s", text)
	}
}

// TestDeleteMediaFilesGateFailsOpen: this gate spares a file that MIGHT be
// genuine; it never demands proof that a file is fake. Without a timestamp to
// reason from, an approved repair must still happen rather than become a silent
// no-op.
func TestDeleteMediaFilesGateFailsOpen(t *testing.T) {
	cases := []struct {
		name       string
		importedAt string
		proposedAt time.Time
	}{
		{
			// Sonarr returned the file without a dateAdded.
			name: "no import stamp to compare", importedAt: "", proposedAt: futuramaProposedAt,
		},
		{
			// The caller had no proposal time — an interactive admin call.
			name: "no proposal time to compare against", importedAt: "2026-08-04T06:00:00Z", proposedAt: time.Time{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			episodes, files := stalenessFixture([]stalenessEpisode{
				{number: 1, airsAt: "2026-08-04T00:00:00Z", importedAt: tc.importedAt},
			})
			fake := &sonarrFileFake{
				t: t, series: futuramaSeries(), episodes: episodes, files: files,
				history: perEpisodeGrabHistory(1), autoRedownload: true,
			}
			server := fake.start()

			text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
				"tv", 615, intPtr(11), []int{1}, false, tc.proposedAt)
			if err != nil {
				t.Fatalf("DeleteMediaFilesHelper: %v", err)
			}
			if deletes := requestsMatching(fake.rec, http.MethodDelete, "/api/v3/episodefile/"); !equalStrings(deletes, []string{"/api/v3/episodefile/5101"}) {
				t.Fatalf("deleted %v — a missing timestamp must not turn an approved repair into a no-op", deletes)
			}
			if strings.Contains(text, "a different file arrived") {
				t.Fatalf("the gate fired without the evidence to fire on:\n%s", text)
			}
		})
	}
}

// TestDeleteMediaFilesWithNothingDeletableIsBenign proves a no-op is classified
// as a preflight outcome, not a failure: nothing was mutated, and the approval
// service must be able to record that definitively.
func TestDeleteMediaFilesWithNothingDeletableIsBenign(t *testing.T) {
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: futuramaS11Episodes(), files: futuramaS11FileRecords()}
	server := fake.start()

	_, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{10, 42}, true, futuramaProposedAt)
	if err == nil {
		t.Fatal("a delete that found nothing returned success")
	}
	classified, ok := err.(interface{ MutationNotStarted() bool })
	if !ok || !classified.MutationNotStarted() {
		t.Fatalf("nothing-to-delete is not a preflight outcome: %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "nothing to delete") {
		t.Fatalf("unhelpful no-op message: %v", err)
	}
	if calls := fake.rec.mutations(); len(calls) != 0 {
		t.Fatalf("a no-op sent %d write(s) upstream: %+v", len(calls), calls)
	}
}

// TestDeleteMediaFilesPartialFailureStillReportsWhatWasDeleted: a non-nil error
// tells the Executor the action mutated nothing. When two of three files are
// gone that is a lie, and the admin would be shown a failed fix that half
// happened.
func TestDeleteMediaFilesPartialFailureStillReportsWhatWasDeleted(t *testing.T) {
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: futuramaS11Episodes(), files: futuramaS11FileRecords(),
		history: perEpisodeGrabHistory(9), autoRedownload: true,
		deleteFails: map[int]bool{5102: true},
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2, 3}, true, futuramaProposedAt)
	if err != nil {
		t.Fatalf("partial success must report a nil error, got %v", err)
	}
	if !strings.Contains(text, "Deleted 2 files") || !strings.Contains(text, "S11E01, S11E03") {
		t.Fatalf("outcome does not name what actually went:\n%s", text)
	}
	if !strings.Contains(text, "Could not delete: S11E02") {
		t.Fatalf("outcome hides the failure:\n%s", text)
	}
	// The release behind the file that survived must NOT be stood down.
	posts := requestsMatching(fake.rec, http.MethodPost, "/api/v3/history/failed/")
	if want := []string{"/api/v3/history/failed/8001", "/api/v3/history/failed/8003"}; !equalStrings(posts, want) {
		t.Fatalf("blocklisted %v, want only the grabs whose files are actually gone %v", posts, want)
	}
}

// TestDeleteMediaFilesTotalFailureIsAnError is the other side of the same line:
// when nothing was deleted there is no partial success to protect, and the
// caller must be told the fix did not happen.
func TestDeleteMediaFilesTotalFailureIsAnError(t *testing.T) {
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: futuramaS11Episodes(), files: futuramaS11FileRecords(),
		deleteFails: map[int]bool{5101: true, 5103: true},
	}
	server := fake.start()

	_, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 3}, true, futuramaProposedAt)
	if err == nil {
		t.Fatal("every delete failed but the helper reported success")
	}
	if !strings.Contains(err.Error(), "could not delete any file") {
		t.Fatalf("total failure message = %v", err)
	}
	if posts := requestsMatching(fake.rec, http.MethodPost, "/api/v3/history/failed/"); len(posts) != 0 {
		t.Fatalf("nothing was deleted yet %v were blocklisted", posts)
	}
}

// TestDeleteMediaFilesReportsAnUnreadableHistoryWithoutFailing: the files are
// already gone, which is the irreversible half. A history read that fails must
// leave the admin with a to-do, not a failed fix.
func TestDeleteMediaFilesReportsAnUnreadableHistoryWithoutFailing(t *testing.T) {
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: futuramaS11Episodes(), files: futuramaS11FileRecords(),
		historyStatus: http.StatusInternalServerError, autoRedownload: true,
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2}, true, futuramaProposedAt)
	if err != nil {
		t.Fatalf("a blocklist read failure must not fail the whole action: %v", err)
	}
	if !strings.Contains(text, "Deleted 2 files") {
		t.Fatalf("deleted files must still be reported:\n%s", text)
	}
	if !strings.Contains(text, "could NOT be blocklisted") || !strings.Contains(text, "mark them failed in Sonarr") {
		t.Fatalf("outcome does not tell the admin what is left to do:\n%s", text)
	}
	// autoRedownloadFailed governs what a FAILED grab does. Nothing was marked
	// failed, so quoting it would promise a search that nothing will trigger.
	if strings.Contains(text, "redownload failed grabs") {
		t.Fatalf("outcome quotes the replacement policy after no release was stood down:\n%s", text)
	}
}

// TestDeleteMediaFilesReportsNoGrabRecord covers the history that reads fine
// but holds no grab for the deleted files: nothing can be blocklisted, and
// saying so beats silence. The instance's replacement policy stays unquoted for
// the same reason — it only governs grabs that were actually marked failed —
// and with nothing stood down, nothing was triggered to replace the files, so
// the repair's own search is the only one that will happen.
func TestDeleteMediaFilesReportsNoGrabRecord(t *testing.T) {
	fake := midFlightFake(t)
	fake.history = []map[string]any{}
	fake.autoRedownload = true
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2}, true, midFlightProposedAt())
	if err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}
	if !strings.Contains(text, "No grab record was found") {
		t.Fatalf("outcome should say why nothing was blocklisted:\n%s", text)
	}
	if posts := requestsMatching(fake.rec, http.MethodPost, "/api/v3/history/failed/"); len(posts) != 0 {
		t.Fatalf("blocklisted %v with no grab record to go on", posts)
	}
	if strings.Contains(text, "redownload failed grabs") {
		t.Fatalf("outcome promises a replacement search for a blocklist that never happened:\n%s", text)
	}
	for _, call := range fake.rec.all() {
		if strings.HasPrefix(call.URI, "/api/v3/config/downloadclient") {
			t.Fatalf("the replacement policy was read although nothing was stood down: %+v", fake.rec.all())
		}
	}
	if got := episodeSearchIDs(t, fake); !equalInts(got, []int{1101, 1102}) {
		t.Fatalf("searched %v — nothing was blocklisted, so nothing else is going to replace those files:\n%s", got, text)
	}
}

// TestDeleteMediaFilesReportsAFailedBlocklistWithoutPromisingAReplacement: the
// grab was found and the mark-failed call was rejected. Nothing is failed, so
// nothing will be redownloaded — and the admin has to be left with the to-do,
// not a promise.
func TestDeleteMediaFilesReportsAFailedBlocklistWithoutPromisingAReplacement(t *testing.T) {
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: futuramaS11Episodes(), files: futuramaS11FileRecords(),
		history: perEpisodeGrabHistory(9), failedStatus: http.StatusInternalServerError, autoRedownload: true,
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2}, true, futuramaProposedAt)
	if err != nil {
		t.Fatalf("a blocklist failure must not fail the whole action: %v", err)
	}
	if !strings.Contains(text, "Deleted 2 files") || !strings.Contains(text, "Could not blocklist:") {
		t.Fatalf("outcome does not report the half that happened and the half that did not:\n%s", text)
	}
	if strings.Contains(text, "redownload failed grabs") {
		t.Fatalf("outcome promises a replacement for grabs that were never marked failed:\n%s", text)
	}
}

// TestDeleteMediaFilesStatesTheInstancesReplacementPolicy: the repair replaces
// what it deleted unless the service is already doing it, and the instance's
// autoRedownloadFailed setting is the only thing that decides which. So the
// sentence and the dispatch have to agree — a text that says the service is
// looking for replacements, next to a search of our own, means one of them is
// lying to the admin reading the outcome.
func TestDeleteMediaFilesStatesTheInstancesReplacementPolicy(t *testing.T) {
	cases := []struct {
		name           string
		autoRedownload bool
		want           string
		unwanted       string
		wantSearched   []int
	}{
		{
			name: "policy on, the service replaces them itself", autoRedownload: true,
			want:         "This service is set to redownload failed grabs, so it is looking for replacements itself.",
			unwanted:     "does not redownload failed grabs on its own",
			wantSearched: nil,
		},
		{
			name: "policy off, so the repair finishes the job", autoRedownload: false,
			want:         "This service does not redownload failed grabs on its own.",
			unwanted:     "it is looking for replacements itself",
			wantSearched: []int{1101, 1102},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := midFlightFake(t)
			fake.autoRedownload = tc.autoRedownload
			server := fake.start()

			text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
				"tv", 615, intPtr(11), []int{1, 2, 3}, true, midFlightProposedAt())
			if err != nil {
				t.Fatalf("DeleteMediaFilesHelper: %v", err)
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("outcome missing %q:\n%s", tc.want, text)
			}
			if strings.Contains(text, tc.unwanted) {
				t.Fatalf("outcome states the opposite policy %q:\n%s", tc.unwanted, text)
			}
			if got := episodeSearchIDs(t, fake); !equalInts(got, tc.wantSearched) {
				t.Fatalf("searched episode ids %v, want %v — the policy sentence and the search must say the same thing:\n%s",
					got, tc.wantSearched, text)
			}
		})
	}
}

// TestDeleteMediaFilesWithoutBlocklistStillReplacesWhatAired proves the
// files-only facet is genuinely files-only — no release is stood down, and
// neither the history nor the replacement policy is even read, because with
// nothing marked failed there is no failed-download handling to defer to. The
// replacement is still ours to do: the reporter's episode is missing either
// way.
func TestDeleteMediaFilesWithoutBlocklistStillReplacesWhatAired(t *testing.T) {
	fake := midFlightFake(t)
	fake.autoRedownload = true // would suppress the search if it were ever read
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2, 3}, false, midFlightProposedAt())
	if err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}
	for _, call := range fake.rec.all() {
		if strings.HasPrefix(call.URI, "/api/v3/history") || strings.HasPrefix(call.URI, "/api/v3/config/downloadclient") {
			t.Fatalf("files-only delete touched %s %s", call.Method, call.URI)
		}
	}
	if strings.Contains(text, "Blocklisted") || strings.Contains(text, "redownload failed grabs") {
		t.Fatalf("files-only outcome claims a blocklist happened:\n%s", text)
	}
	if got := episodeSearchIDs(t, fake); !equalInts(got, []int{1101, 1102}) {
		t.Fatalf("searched episode ids %v, want the aired-and-missing ones [1101 1102]:\n%s", got, text)
	}
	if !strings.Contains(text, "Searched the 2 aired episode(s) of Futurama season 11 that are missing a file: E01, E02.") {
		t.Fatalf("outcome does not report the replacement search:\n%s", text)
	}
}

// --- the folded repair: replacing what the deletion took away ---
//
// Deleting the wrong files and stopping there is half a fix: the reporter is
// left with exactly the missing episode they complained about. So the search is
// part of THIS action, not a second proposal — and the only thing that calls it
// off is the service having already dispatched one of its own.

// TestDeleteMediaFilesSearchesWhenTheReplacementPolicyCannotBeRead: the policy
// read is how the repair learns whether the service is already looking. When it
// fails, the repair finishes the job rather than assume it was done for it — a
// duplicate search costs one wasted indexer query, a skipped one costs the
// admin the episode they were promised.
func TestDeleteMediaFilesSearchesWhenTheReplacementPolicyCannotBeRead(t *testing.T) {
	fake := midFlightFake(t)
	fake.policyStatus = http.StatusInternalServerError
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2, 3}, true, midFlightProposedAt())
	if err != nil {
		t.Fatalf("an unreadable policy must not fail the repair: %v", err)
	}
	if !strings.Contains(text, "Blocklisted 3 release(s)") {
		t.Fatalf("outcome does not report the blocklist that did happen:\n%s", text)
	}
	// The policy is unknown, so the outcome must not claim it points either way.
	if strings.Contains(text, "redownload failed grabs") {
		t.Fatalf("outcome quotes a policy it could not read:\n%s", text)
	}
	read := false
	for _, call := range fake.rec.all() {
		if strings.HasPrefix(call.URI, "/api/v3/config/downloadclient") {
			read = true
		}
	}
	if !read {
		t.Fatalf("the policy was never read, so this test proves nothing: %+v", fake.rec.all())
	}
	if got := episodeSearchIDs(t, fake); !equalInts(got, []int{1101, 1102}) {
		t.Fatalf("searched %v — an unknown policy must not silently cancel the replacement:\n%s", got, text)
	}
}

// TestDeleteMediaFilesWithNothingAiredSearchesNothing is the live case at the
// moment of approval: on a season filled entirely ahead of its air dates, the
// correct number of releases to go looking for is zero. Searching an unaired
// episode is exactly how the library got into this state.
func TestDeleteMediaFilesWithNothingAiredSearchesNothing(t *testing.T) {
	now := time.Now().UTC()
	imported := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	episodes, files := stalenessFixture([]stalenessEpisode{
		{number: 1, airsAt: now.Add(24 * time.Hour).Format(time.RFC3339), importedAt: imported},
		{number: 2, airsAt: now.Add(48 * time.Hour).Format(time.RFC3339), importedAt: imported},
	})
	fake := &sonarrFileFake{
		t: t, series: futuramaSeries(), episodes: episodes, files: files,
		history: perEpisodeGrabHistory(2), autoRedownload: false,
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2}, true, midFlightProposedAt())
	if err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}
	if commands := fake.commandsSeen(); len(commands) != 0 {
		t.Fatalf("a season with nothing aired dispatched %+v", commands)
	}
	// The deletion still has to be reported for what it was.
	for _, want := range []string{
		"Deleted 2 files from Futurama season 11.",
		"Episodes: S11E01, S11E02.",
		"Blocklisted 2 release(s)",
		"no episode of Futurama season 11 has aired yet",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("outcome missing %q:\n%s", want, text)
		}
	}
}

// TestDeleteMediaFilesReportsAFailedSearchWithoutFailingTheRepair: the files
// are gone, which is the irreversible half. A search the service refuses to
// start leaves the admin with a to-do, not an action reported as a clean
// failure that mutated nothing — the Executor reads a non-nil error as exactly
// that, and here it would be a lie.
func TestDeleteMediaFilesReportsAFailedSearchWithoutFailingTheRepair(t *testing.T) {
	fake := midFlightFake(t)
	fake.commandStatus = http.StatusInternalServerError
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2, 3}, false, midFlightProposedAt())
	if err != nil {
		t.Fatalf("a failed search must not fail the whole repair, got %v", err)
	}
	for _, want := range []string{
		"Deleted 3 files from Futurama season 11.",
		"Episodes: S11E01, S11E02, S11E03.",
		"The replacement search could not be started",
		"an admin should search the aired episodes in Sonarr",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("outcome missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Searched the") {
		t.Fatalf("outcome claims a search that the service rejected:\n%s", text)
	}
	if commands := fake.commandsSeen(); len(commands) != 1 {
		t.Fatalf("the search was never attempted, so this test proves nothing: %+v", commands)
	}
}

// TestDeleteMediaFilesSearchesOnlyAfterEveryDeleteAndBlocklist pins the order
// the whole repair depends on. A replacement found while the old file is still
// on disk is rejected as "not an upgrade", and one found before the bad release
// is stood down can be that same release coming straight back — either way the
// fix would report success and change nothing.
func TestDeleteMediaFilesSearchesOnlyAfterEveryDeleteAndBlocklist(t *testing.T) {
	fake := midFlightFake(t)
	fake.autoRedownload = false
	server := fake.start()

	if _, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1, 2, 3}, true, midFlightProposedAt()); err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}

	lastDelete, lastBlocklist, firstSearch := -1, -1, -1
	for i, call := range fake.rec.all() {
		switch {
		case call.Method == http.MethodDelete && strings.HasPrefix(call.URI, "/api/v3/episodefile/"):
			lastDelete = i
		case call.Method == http.MethodPost && strings.HasPrefix(call.URI, "/api/v3/history/failed/"):
			lastBlocklist = i
		case call.Method == http.MethodPost && call.URI == "/api/v3/command":
			if firstSearch < 0 {
				firstSearch = i
			}
		}
	}
	if lastDelete < 0 || lastBlocklist < 0 || firstSearch < 0 {
		t.Fatalf("expected deletes, blocklists and a search, got %+v", fake.rec.all())
	}
	if firstSearch < lastDelete {
		t.Fatalf("the search at call %d ran before the last delete at call %d — a replacement found while the old file is on disk is rejected as not an upgrade:\n%+v",
			firstSearch, lastDelete, fake.rec.all())
	}
	if firstSearch < lastBlocklist {
		t.Fatalf("the search at call %d ran before the last blocklist at call %d — it can re-grab the very release being stood down:\n%+v",
			firstSearch, lastBlocklist, fake.rec.all())
	}
}

// TestDeleteMediaFilesRejectsUnsupportedShapes covers the preflight gates that
// must never reach an *arr.
func TestDeleteMediaFilesRejectsUnsupportedShapes(t *testing.T) {
	cases := []struct {
		name string
		run  func() (string, error)
		want string
	}{
		{
			name: "books have no file surface here",
			run: func() (string, error) {
				return DeleteMediaFilesHelper(nil, nil, nil, "book", 1, nil, nil, false, futuramaProposedAt)
			},
			want: `media_type "movie" or "tv"`,
		},
		{
			name: "tv without a season",
			run: func() (string, error) {
				return DeleteMediaFilesHelper(nil, nil, sonarr.NewClient("http://unused.invalid", "key"), "tv", 615, nil, []int{1}, false, futuramaProposedAt)
			},
			want: "requires a season",
		},
		{
			name: "sonarr not configured",
			run: func() (string, error) {
				return DeleteMediaFilesHelper(nil, nil, nil, "tv", 615, intPtr(11), []int{1}, false, futuramaProposedAt)
			},
			want: "Sonarr is not configured",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.run()
			if err == nil {
				t.Fatal("unsupported shape was accepted")
			}
			classified, ok := err.(interface{ MutationNotStarted() bool })
			if !ok || !classified.MutationNotStarted() {
				t.Fatalf("rejection is not a preflight outcome: %T %v", err, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("rejection = %v, want message containing %q", err, tc.want)
			}
		})
	}
}

// TestDeleteMediaFilesForAnUnknownSeriesIsBenign: the series left the library
// between proposal and approval. Nothing was mutated, so nothing failed.
func TestDeleteMediaFilesForAnUnknownSeriesIsBenign(t *testing.T) {
	fake := &sonarrFileFake{t: t, series: []map[string]any{}, episodes: nil}
	server := fake.start()

	_, err := DeleteMediaFilesHelper(nil, nil, sonarr.NewClient(server.URL, "key"),
		"tv", 615, intPtr(11), []int{1}, true, futuramaProposedAt)
	if err == nil {
		t.Fatal("a series that is not in the library was accepted")
	}
	classified, ok := err.(interface{ MutationNotStarted() bool })
	if !ok || !classified.MutationNotStarted() {
		t.Fatalf("unknown series is not a preflight outcome: %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "not in the library") {
		t.Fatalf("unknown series message = %v", err)
	}
}

// --- DeleteMediaFilesHelper, movies ---

// radarrFileFake is the movie-side stand-in: the library record, the file
// behind it, the history the blocklist walks back through, the instance's
// replacement policy, and the command endpoint the repair's own search lands
// on.
type radarrFileFake struct {
	t   *testing.T
	rec *callRecorder

	movies  []map[string]any
	file    map[string]any
	history []map[string]any

	policyStatus   int
	autoRedownload bool

	commandMu sync.Mutex
	commands  []map[string]any
}

func (f *radarrFileFake) commandsSeen() []map[string]any {
	f.commandMu.Lock()
	defer f.commandMu.Unlock()
	return append([]map[string]any(nil), f.commands...)
}

func (f *radarrFileFake) start() *httptest.Server {
	f.t.Helper()
	if f.rec == nil {
		f.rec = &callRecorder{}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		f.rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie":
			_ = json.NewEncoder(w).Encode(matchingRecords(f.movies, "tmdbId", r.URL.Query().Get("tmdbId")))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v3/moviefile/"):
			_ = json.NewEncoder(w).Encode(f.file)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v3/moviefile/"):
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/history/movie":
			_ = json.NewEncoder(w).Encode(f.history)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v3/history/failed/"):
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/config/downloadclient":
			if f.policyStatus != 0 {
				w.WriteHeader(f.policyStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"autoRedownloadFailed": f.autoRedownload})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				f.t.Errorf("decode command %q: %v", raw, err)
			}
			f.commandMu.Lock()
			f.commands = append(f.commands, payload)
			f.commandMu.Unlock()
			w.WriteHeader(http.StatusCreated)
		default:
			f.t.Errorf("unexpected radarr request %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	f.t.Cleanup(server.Close)
	return server
}

// theThingMovie holds one imported file, and theThingFile records that it
// landed a month ago — long before the fix was proposed, so the staleness gate
// recognises it as the file the diagnosis actually looked at.
func theThingMovie() []map[string]any {
	return []map[string]any{
		{"id": 12, "title": "Other Movie", "tmdbId": 550, "hasFile": true, "movieFileId": 4412, "monitored": true},
		{"id": 77, "title": "The Thing", "tmdbId": 1091, "year": 1982, "hasFile": true, "movieFileId": 4477, "monitored": true},
	}
}

func theThingFile() map[string]any {
	return map[string]any{
		"id": 4477, "movieId": 77, "relativePath": "The Thing (1982).mkv",
		"dateAdded": time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func theThingHistory() []map[string]any {
	const title = "The.Thing.1982.1080p.BluRay.x264-GROUP"
	return []map[string]any{
		{"id": int64(9077), "movieId": 77, "eventType": "downloadFolderImported", "downloadId": "SABnzbd_nzo_movie", "sourceTitle": title},
		{"id": int64(8077), "movieId": 77, "eventType": "grabbed", "downloadId": "SABnzbd_nzo_movie", "sourceTitle": title},
	}
}

// TestDeleteMovieFileFinishesTheRepairUnlessTheServiceWill is the movie half of
// the folded repair. A movie has no air dates to reason about, so there is no
// set to narrow: the one film that just lost its file is the one to search for
// — unless marking the grab failed already handed that to the service.
func TestDeleteMovieFileFinishesTheRepairUnlessTheServiceWill(t *testing.T) {
	cases := []struct {
		name           string
		autoRedownload bool
		want           string
		unwanted       string
		wantSearch     bool
	}{
		{
			name: "policy off, so the repair searches for the replacement itself", autoRedownload: false,
			want: "Searched for a replacement.", unwanted: "it is looking for replacements itself", wantSearch: true,
		},
		{
			name: "policy on, the service already dispatched one", autoRedownload: true,
			want:     "This service is set to redownload failed grabs, so it is looking for replacements itself.",
			unwanted: "Searched for a replacement.", wantSearch: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &radarrFileFake{
				t: t, movies: theThingMovie(), file: theThingFile(), history: theThingHistory(),
				autoRedownload: tc.autoRedownload,
			}
			server := fake.start()

			text, err := DeleteMediaFilesHelper(nil, radarr.NewClient(server.URL, "key"), nil,
				"movie", 1091, nil, nil, true, midFlightProposedAt())
			if err != nil {
				t.Fatalf("DeleteMediaFilesHelper: %v", err)
			}
			if deletes := requestsMatching(fake.rec, http.MethodDelete, "/api/v3/moviefile/"); !equalStrings(deletes, []string{"/api/v3/moviefile/4477"}) {
				t.Fatalf("deleted %v, want only the file the named movie holds", deletes)
			}
			if posts := requestsMatching(fake.rec, http.MethodPost, "/api/v3/history/failed/"); !equalStrings(posts, []string{"/api/v3/history/failed/8077"}) {
				t.Fatalf("blocklisted %v, want the grab that delivered that file", posts)
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("outcome missing %q:\n%s", tc.want, text)
			}
			if strings.Contains(text, tc.unwanted) {
				t.Fatalf("outcome claims %q as well:\n%s", tc.unwanted, text)
			}

			commands := fake.commandsSeen()
			if !tc.wantSearch {
				if len(commands) != 0 {
					t.Fatalf("the service was already searching, yet the repair dispatched %+v", commands)
				}
				return
			}
			if len(commands) != 1 || commands[0]["name"] != "MoviesSearch" {
				t.Fatalf("dispatched %+v, want exactly one MoviesSearch", commands)
			}
			ids, ok := commands[0]["movieIds"].([]any)
			if !ok || len(ids) != 1 || ids[0].(float64) != 77 {
				t.Fatalf("MoviesSearch named %+v, want only the movie whose file was deleted", commands[0])
			}
		})
	}
}

// TestDeleteMovieFileSearchesWhenNothingWasBlocklisted: with no grab record
// there is nothing to mark failed, so no failed-download handling was triggered
// and nobody else is going to replace the file that was just deleted.
func TestDeleteMovieFileSearchesWhenNothingWasBlocklisted(t *testing.T) {
	fake := &radarrFileFake{
		t: t, movies: theThingMovie(), file: theThingFile(), history: []map[string]any{},
		autoRedownload: true,
	}
	server := fake.start()

	text, err := DeleteMediaFilesHelper(nil, radarr.NewClient(server.URL, "key"), nil,
		"movie", 1091, nil, nil, true, midFlightProposedAt())
	if err != nil {
		t.Fatalf("DeleteMediaFilesHelper: %v", err)
	}
	if !strings.Contains(text, "No grab record was found") {
		t.Fatalf("outcome should say why nothing was blocklisted:\n%s", text)
	}
	if commands := fake.commandsSeen(); len(commands) != 1 || commands[0]["name"] != "MoviesSearch" {
		t.Fatalf("dispatched %+v, want the repair's own MoviesSearch:\n%s", commands, text)
	}
	if !strings.Contains(text, "Searched for a replacement.") {
		t.Fatalf("outcome does not report the replacement search:\n%s", text)
	}
}

// --- triggerAiredEpisodeSearch, via TriggerSearchHelper(aired_only) ---

// airedOnlySeasonEpisodes builds a season around the real clock: the helper
// resolves "aired" at execution time from time.Now(), which is the whole point
// of the flag, so the fixture is anchored to now rather than to a fixed date.
func airedOnlySeasonEpisodes() []map[string]any {
	now := time.Now().UTC()
	aired := now.Add(-48 * time.Hour).Format(time.RFC3339)
	airedRecently := now.Add(-2 * time.Minute).Format(time.RFC3339)
	unaired := now.Add(72 * time.Hour).Format(time.RFC3339)
	return []map[string]any{
		{"id": 1101, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 1, "title": "Aired, missing", "airDateUtc": aired, "hasFile": false, "monitored": true},
		{"id": 1102, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 2, "title": "Aired, has a file", "airDateUtc": aired, "hasFile": true, "episodeFileId": 5102, "monitored": true},
		{"id": 1103, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 3, "title": "Not yet aired", "airDateUtc": unaired, "hasFile": false, "monitored": true},
		{"id": 1104, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 4, "title": "Unscheduled", "hasFile": false, "monitored": true},
		{"id": 1105, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 5, "title": "Just aired, missing", "airDateUtc": airedRecently, "hasFile": false, "monitored": true},
	}
}

// TestTriggerSearchAiredOnlySearchesOnlyAiredMissingEpisodes is the guard that
// keeps a repair from re-filling a season with content that does not exist:
// searching an unaired episode is exactly how the library got here.
func TestTriggerSearchAiredOnlySearchesOnlyAiredMissingEpisodes(t *testing.T) {
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: airedOnlySeasonEpisodes()}
	server := fake.start()

	text, err := TriggerSearchHelper(nil, nil, sonarr.NewClient(server.URL, "key"), nil, nil,
		"tv", 615, intPtr(11), nil, true, 0, nil, 0, nil)
	if err != nil {
		t.Fatalf("TriggerSearchHelper(aired_only): %v", err)
	}
	commands := fake.commandsSeen()
	if len(commands) != 1 {
		t.Fatalf("aired-only search dispatched %d command(s): %+v", len(commands), commands)
	}
	cmd := commands[0]
	if cmd["name"] != "EpisodeSearch" {
		t.Fatalf("command = %+v, want EpisodeSearch", cmd)
	}
	got := commandEpisodeIDs(t, cmd)
	if want := []int{1101, 1105}; !equalInts(got, want) {
		t.Fatalf("searched episode ids %v, want exactly the aired-and-missing ones %v (1102 has a file, 1103 has not aired, 1104 has no air date)", got, want)
	}
	if !strings.Contains(text, "E01, E05") {
		t.Fatalf("outcome does not name the searched episodes:\n%s", text)
	}
}

// TestTriggerSearchAiredOnlySkipsEpisodesWithNoAirDate isolates the third
// predicate: an episode the service carries no air date for is evidence of
// nothing, and must never be guessed into the aired set.
func TestTriggerSearchAiredOnlySkipsEpisodesWithNoAirDate(t *testing.T) {
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: []map[string]any{
		{"id": 1104, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 4, "title": "Unscheduled", "hasFile": false, "monitored": true},
		{"id": 1105, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 5, "title": "Unscheduled too", "hasFile": false, "monitored": true},
	}}
	server := fake.start()

	text, err := TriggerSearchHelper(nil, nil, sonarr.NewClient(server.URL, "key"), nil, nil,
		"tv", 615, intPtr(11), nil, true, 0, nil, 0, nil)
	if err != nil {
		t.Fatalf("TriggerSearchHelper(aired_only): %v", err)
	}
	if commands := fake.commandsSeen(); len(commands) != 0 {
		t.Fatalf("episodes with no air date were searched: %+v", commands)
	}
	if !strings.Contains(text, "Nothing to search") {
		t.Fatalf("outcome does not say nothing was searched:\n%s", text)
	}
}

// TestTriggerSearchAiredOnlyWithNothingAiredSearchesNothing is the live case at
// the moment of approval: on 2026-08-03 nothing in Futurama season 11 had
// aired. Searching an unaired episode is exactly how this library filled up
// with content that does not exist, so the only correct dispatch is none.
//
// An empty aired set is a completed action, not a refused one — "search
// whatever has aired" was answered truthfully — so the helper reports it with a
// nil error and says why it was empty.
func TestTriggerSearchAiredOnlyWithNothingAiredSearchesNothing(t *testing.T) {
	now := time.Now().UTC()
	episodes := []map[string]any{
		{"id": 1101, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 1, "title": "Beef", "airDateUtc": now.Add(10 * time.Hour).Format(time.RFC3339), "hasFile": false, "monitored": true},
		{"id": 1102, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 2, "title": "The Cure for Boredom", "airDateUtc": now.Add(11 * time.Hour).Format(time.RFC3339), "hasFile": false, "monitored": true},
	}
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: episodes}
	server := fake.start()

	text, err := TriggerSearchHelper(nil, nil, sonarr.NewClient(server.URL, "key"), nil, nil,
		"tv", 615, intPtr(11), nil, true, 0, nil, 0, nil)
	if err != nil {
		t.Fatalf("an empty aired set is not a failure: %v", err)
	}
	if commands := fake.commandsSeen(); len(commands) != 0 {
		t.Fatalf("a season with nothing aired dispatched %+v", commands)
	}
	for _, want := range []string{"Nothing to search", "has aired yet", "grab each one as it airs"} {
		if !strings.Contains(text, want) {
			t.Fatalf("outcome missing %q:\n%s", want, text)
		}
	}
}

// TestTriggerSearchAiredOnlyDistinguishesAiredButStillHeld covers the other
// empty-set reason. Every aired episode still holding a file usually means the
// delete this search was meant to follow has not happened, and telling the two
// apart is what stops an agent reading "nothing to search" as "season healthy".
func TestTriggerSearchAiredOnlyDistinguishesAiredButStillHeld(t *testing.T) {
	now := time.Now().UTC()
	aired := now.Add(-48 * time.Hour).Format(time.RFC3339)
	episodes := []map[string]any{
		{"id": 1101, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 1, "title": "Beef", "airDateUtc": aired, "hasFile": true, "episodeFileId": 5101, "monitored": true},
		{"id": 1102, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 2, "title": "The Cure for Boredom", "airDateUtc": aired, "hasFile": true, "episodeFileId": 5102, "monitored": true},
	}
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: episodes}
	server := fake.start()

	text, err := TriggerSearchHelper(nil, nil, sonarr.NewClient(server.URL, "key"), nil, nil,
		"tv", 615, intPtr(11), nil, true, 0, nil, 0, nil)
	if err != nil {
		t.Fatalf("TriggerSearchHelper(aired_only): %v", err)
	}
	if commands := fake.commandsSeen(); len(commands) != 0 {
		t.Fatalf("episodes that already hold files were searched: %+v", commands)
	}
	if !strings.Contains(text, "still have a file") || !strings.Contains(text, "deleted first") {
		t.Fatalf("outcome does not explain that the files are still there:\n%s", text)
	}
	if strings.Contains(text, "has aired yet") {
		t.Fatalf("outcome blames an unaired season when every episode had aired:\n%s", text)
	}
}

// TestTriggerSearchWithoutAiredOnlyStillSearchesTheWholeSeason pins that the
// new flag changed nothing for every existing caller: without it, one season
// search goes out exactly as before.
func TestTriggerSearchWithoutAiredOnlyStillSearchesTheWholeSeason(t *testing.T) {
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: airedOnlySeasonEpisodes()}
	server := fake.start()

	if _, err := TriggerSearchHelper(nil, nil, sonarr.NewClient(server.URL, "key"), nil, nil,
		"tv", 615, intPtr(11), nil, false, 0, nil, 0, nil); err != nil {
		t.Fatalf("TriggerSearchHelper: %v", err)
	}
	commands := fake.commandsSeen()
	if len(commands) != 1 || commands[0]["name"] != "SeasonSearch" {
		t.Fatalf("plain season search dispatched %+v", commands)
	}
	for _, call := range fake.rec.all() {
		if call.Method == http.MethodGet && strings.HasPrefix(call.URI, "/api/v3/episode?") {
			t.Fatalf("a plain season search read the episode list: %s", call.URI)
		}
	}
}

// --- shared assertions ---

// requestsMatching returns the paths of the recorded calls with this method
// whose path starts with prefix, in the order they arrived.
func requestsMatching(rec *callRecorder, method, prefix string) []string {
	var out []string
	for _, call := range rec.all() {
		path, _, _ := strings.Cut(call.URI, "?")
		if call.Method == method && strings.HasPrefix(path, prefix) {
			out = append(out, path)
		}
	}
	return out
}

func commandEpisodeIDs(t *testing.T, cmd map[string]any) []int {
	t.Helper()
	raw, ok := cmd["episodeIds"].([]any)
	if !ok {
		t.Fatalf("command has no episodeIds: %+v", cmd)
	}
	out := make([]int, 0, len(raw))
	for _, v := range raw {
		f, ok := v.(float64)
		if !ok {
			t.Fatalf("episodeIds holds a non-number: %+v", cmd)
		}
		out = append(out, int(f))
	}
	return out
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
