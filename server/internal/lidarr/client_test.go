package lidarr

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestLookupAlbumUsesExactAuthenticatedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/album/lookup" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("term"); got != "lidarr:mbid-1234" {
			t.Errorf("term = %q", got)
		}
		if got := r.Header.Get("X-Api-Key"); got != "music-key" {
			t.Errorf("X-Api-Key = %q", got)
		}
		_, _ = w.Write([]byte(`[{"title":"Blue Album","foreignAlbumId":"mbid-1234","remoteCover":"https://covers.example/blue.jpg","artist":{"artistName":"Weezer","foreignArtistId":"artist-9"}}]`))
	}))
	t.Cleanup(server.Close)

	results, err := NewClient(server.URL, "music-key").LookupAlbum("lidarr:mbid-1234")
	if err != nil {
		t.Fatalf("LookupAlbum() error = %v", err)
	}
	if len(results) != 1 || results[0].ForeignAlbumID != "mbid-1234" || results[0].RemoteCover == "" {
		t.Fatalf("results = %#v", results)
	}
	if results[0].Artist == nil || results[0].Artist.ForeignArtistID != "artist-9" {
		t.Fatalf("nested artist = %#v", results[0].Artist)
	}
}

func TestLookupArtistEscapesTerm(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/artist/lookup" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if raw := r.URL.RawQuery; raw != "term="+url.QueryEscape("sigur rós & friends") {
			t.Errorf("raw query = %q", raw)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(server.Close)

	if _, err := NewClient(server.URL, "k").LookupArtist("sigur rós & friends"); err != nil {
		t.Fatalf("LookupArtist() error = %v", err)
	}
}

// TestAddAlbumPinsWirePayload pins the add body Lidarr's AddAlbumService
// validates: the nested artist with profiles/root, artist monitor scope
// "none", monitorNewItems "none", and the album-level search flag. A request
// for one album must never subscribe the whole discography.
func TestAddAlbumPinsWirePayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/album" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode add payload: %v", err)
		}
		if payload["foreignAlbumId"] != "mbid-1234" || payload["monitored"] != true || payload["anyReleaseOk"] != true {
			t.Errorf("album fields = %v", payload)
		}
		addOptions, _ := payload["addOptions"].(map[string]any)
		if addOptions["searchForNewAlbum"] != true {
			t.Errorf("addOptions = %v", addOptions)
		}
		artist, _ := payload["artist"].(map[string]any)
		if artist["foreignArtistId"] != "artist-9" || artist["qualityProfileId"] != float64(3) ||
			artist["metadataProfileId"] != float64(2) || artist["rootFolderPath"] != "/music" ||
			artist["monitored"] != true || artist["monitorNewItems"] != "none" {
			t.Errorf("artist fields = %v", artist)
		}
		artistOptions, _ := artist["addOptions"].(map[string]any)
		// The monitor option must be ABSENT: "none" would unmonitor the
		// artist itself (verified live), stranding the album outside wanted.
		if _, present := artistOptions["monitor"]; present {
			t.Errorf("artist addOptions carried a monitor option: %v", artistOptions)
		}
		if artistOptions["searchForMissingAlbums"] != false {
			t.Errorf("artist addOptions = %v", artistOptions)
		}
		_, _ = w.Write([]byte(`{"id":77,"title":"Blue Album","foreignAlbumId":"mbid-1234","monitored":true}`))
	}))
	t.Cleanup(server.Close)

	req := AddAlbumRequest{ForeignAlbumID: "mbid-1234", Title: "Blue Album", Monitored: true, AnyReleaseOk: true}
	req.AddOptions.SearchForNewAlbum = true
	req.Artist = AddArtistRequest{
		ArtistName:        "Weezer",
		ForeignArtistID:   "artist-9",
		QualityProfileID:  3,
		MetadataProfileID: 2,
		RootFolderPath:    "/music",
		Monitored:         true,
		MonitorNewItems:   "none",
	}
	created, err := NewClient(server.URL, "k").AddAlbum(req)
	if err != nil {
		t.Fatalf("AddAlbum() error = %v", err)
	}
	if created.ID != 77 || created.ForeignAlbumID != "mbid-1234" {
		t.Fatalf("created = %#v", created)
	}
}

func TestSetAlbumMonitoredUsesPut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/album/monitor" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			AlbumIDs  []int `json:"albumIds"`
			Monitored bool  `json:"monitored"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode monitor payload: %v", err)
		}
		if len(payload.AlbumIDs) != 2 || payload.AlbumIDs[0] != 4 || payload.AlbumIDs[1] != 9 || !payload.Monitored {
			t.Errorf("payload = %+v", payload)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	if err := NewClient(server.URL, "k").SetAlbumMonitored([]int{4, 9}, true); err != nil {
		t.Fatalf("SetAlbumMonitored() error = %v", err)
	}
}

func TestGetQueueDetailedPinsParamsAndCompleteness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("includeArtist") != "true" || q.Get("includeAlbum") != "true" || q.Get("includeUnknownArtistItems") != "true" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"totalRecords":2,"records":[
			{"id":1,"artistId":5,"albumId":9,"title":"A","status":"downloading","trackFileCount":12,"trackHasFileCount":3},
			{"id":2,"artistId":5,"albumId":10,"title":"B","status":"queued"}
		]}`))
	}))
	t.Cleanup(server.Close)

	items, err := NewClient(server.URL, "k").GetQueueDetailed()
	if err != nil {
		t.Fatalf("GetQueueDetailed() error = %v", err)
	}
	if len(items) != 2 || items[0].AlbumID != 9 || items[0].TrackFileCount != 12 || items[0].TrackHasFileCount != 3 {
		t.Fatalf("items = %#v", items)
	}
}

func TestGetQueueDetailedRejectsTruncatedSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"totalRecords":5,"records":[{"id":1}]}`))
	}))
	t.Cleanup(server.Close)

	if _, err := NewClient(server.URL, "k").GetQueueDetailed(); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("GetQueueDetailed() error = %v, want incompleteness refusal", err)
	}
}

func TestGetQueueDetailedRejectsDuplicateIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"totalRecords":2,"records":[{"id":7},{"id":7}]}`))
	}))
	t.Cleanup(server.Close)

	if _, err := NewClient(server.URL, "k").GetQueueDetailed(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("GetQueueDetailed() error = %v, want duplicate refusal", err)
	}
}

func TestRemoveQueueItemPinsFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/queue/42" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("removeFromClient") != "true" || q.Get("blocklist") != "true" || q.Get("skipRedownload") != "true" || q.Get("changeCategory") != "false" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	if err := NewClient(server.URL, "k").RemoveQueueItem(42, true, true, true, false); err != nil {
		t.Fatalf("RemoveQueueItem() error = %v", err)
	}
}

// TestImportHistorySinceWindows pins the windowing contract shared with the
// hub's catch-up: eventType=3 (trackFileImported), completeness proven either
// by reaching a record at/before the boundary or by holding every record.
func TestImportHistorySinceWindows(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	older := now.Add(-2 * time.Hour).Format(time.RFC3339)
	newer := now.Add(-10 * time.Minute).Format(time.RFC3339)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("eventType"); got != "3" {
			t.Errorf("eventType = %q", got)
		}
		_, _ = w.Write([]byte(`{"totalRecords":9,"records":[
			{"id":2,"eventType":"trackFileImported","albumId":9,"date":"` + newer + `"},
			{"id":1,"eventType":"trackFileImported","albumId":7,"date":"` + older + `"}
		]}`))
	}))
	t.Cleanup(server.Close)

	inWindow, complete, err := NewClient(server.URL, "k").GetImportHistorySince(now.Add(-time.Hour), 50)
	if err != nil {
		t.Fatalf("GetImportHistorySince() error = %v", err)
	}
	if len(inWindow) != 1 || inWindow[0].AlbumID != 9 {
		t.Fatalf("inWindow = %#v", inWindow)
	}
	if !complete {
		t.Fatal("window reached a pre-boundary record but reported incomplete")
	}
}

func TestUpgradeDeleteHistorySinceUsesEventTypeFive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("eventType"); got != "5" {
			t.Errorf("eventType = %q", got)
		}
		_, _ = w.Write([]byte(`{"totalRecords":0,"records":[]}`))
	}))
	t.Cleanup(server.Close)

	if _, _, err := NewClient(server.URL, "k").GetUpgradeDeleteHistorySince(time.Now().Add(-time.Hour), 50); err != nil {
		t.Fatalf("GetUpgradeDeleteHistorySince() error = %v", err)
	}
}

func TestGetWantedMissingPinsParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/wanted/missing" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("page") != "2" || q.Get("pageSize") != "25" || q.Get("includeArtist") != "true" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"page":2,"pageSize":25,"totalRecords":1,"records":[{"id":9,"title":"Missing","foreignAlbumId":"mb-1","artist":{"artistName":"Someone"}}]}`))
	}))
	t.Cleanup(server.Close)

	page, err := NewClient(server.URL, "k").GetWantedMissing(2, 25)
	if err != nil {
		t.Fatalf("GetWantedMissing() error = %v", err)
	}
	if page.TotalRecords != 1 || len(page.Records) != 1 || page.Records[0].Artist == nil {
		t.Fatalf("page = %#v", page)
	}
}

// TestRootFoldersCarryDefaults pins the Lidarr-specific add-config source: a
// root folder names the quality/metadata profiles new artists should get.
func TestRootFoldersCarryDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/rootfolder" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"id":1,"name":"Music","path":"/music","accessible":true,"freeSpace":42,"defaultQualityProfileId":3,"defaultMetadataProfileId":2,"defaultMonitorOption":"none"}]`))
	}))
	t.Cleanup(server.Close)

	folders, err := NewClient(server.URL, "k").GetRootFolders()
	if err != nil {
		t.Fatalf("GetRootFolders() error = %v", err)
	}
	if len(folders) != 1 || folders[0].DefaultQualityProfileID != 3 || folders[0].DefaultMetadataProfileID != 2 || !folders[0].Accessible {
		t.Fatalf("folders = %#v", folders)
	}
}

func TestGetTrackFilesForArtistUsesFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/trackfile" || r.URL.Query().Get("artistId") != "12" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`[{"id":1,"albumId":9,"path":"/music/a.flac","size":1000,"dateAdded":"2026-08-30T10:00:00Z"}]`))
	}))
	t.Cleanup(server.Close)

	files, err := NewClient(server.URL, "k").GetTrackFilesForArtist(12)
	if err != nil {
		t.Fatalf("GetTrackFilesForArtist() error = %v", err)
	}
	if len(files) != 1 || files[0].AlbumID != 9 || files[0].DateAdded == nil {
		t.Fatalf("files = %#v", files)
	}
}

func TestCommandPayloads(t *testing.T) {
	var got []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/command" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		got = append(got, payload)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "k")
	if err := client.TriggerAlbumSearch([]int{4}); err != nil {
		t.Fatalf("TriggerAlbumSearch() error = %v", err)
	}
	if err := client.TriggerArtistSearch(7); err != nil {
		t.Fatalf("TriggerArtistSearch() error = %v", err)
	}
	if err := client.RescanArtist(7); err != nil {
		t.Fatalf("RescanArtist() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("commands sent = %d", len(got))
	}
	if got[0]["name"] != "AlbumSearch" {
		t.Errorf("album search payload = %v", got[0])
	}
	if ids, _ := got[0]["albumIds"].([]any); len(ids) != 1 || ids[0] != float64(4) {
		t.Errorf("albumIds = %v", got[0]["albumIds"])
	}
	if got[1]["name"] != "ArtistSearch" || got[1]["artistId"] != float64(7) {
		t.Errorf("artist search payload = %v", got[1])
	}
	// Lidarr has no per-artist rescan command; RescanFolders scoped by
	// artistId is the equivalent.
	if got[2]["name"] != "RescanFolders" || got[2]["artistId"] != float64(7) {
		t.Errorf("rescan payload = %v", got[2])
	}
}

func TestGetAlbumTreats404AsGone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	album, err := NewClient(server.URL, "k").GetAlbum(9)
	if err != nil || album != nil {
		t.Fatalf("GetAlbum() = %#v, %v; want nil, nil", album, err)
	}
}

func TestGetAlbumsForArtistRefiltersResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("artistId") != "5" {
			t.Errorf("artistId = %q", r.URL.Query().Get("artistId"))
		}
		_, _ = w.Write([]byte(`[{"id":1,"artistId":5,"title":"Mine"},{"id":2,"artistId":6,"title":"Someone else's"}]`))
	}))
	t.Cleanup(server.Close)

	albums, err := NewClient(server.URL, "k").GetAlbumsForArtist(5)
	if err != nil {
		t.Fatalf("GetAlbumsForArtist() error = %v", err)
	}
	if len(albums) != 1 || albums[0].Title != "Mine" {
		t.Fatalf("albums = %#v", albums)
	}
}

// TestTransportErrorOmitsHost pins the topology-privacy property shared with
// the other arr clients: transport failures embed the full request URL, and
// these errors surface to requesters through music-request failures — so the
// client must summarize them host-free.
func TestTransportErrorOmitsHost(t *testing.T) {
	dnsFailure := &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "lidarr-internal"}}
	c := NewClient("http://lidarr-internal:8686", "key")
	c.httpClient = &http.Client{Transport: failingTransport{dnsFailure}}

	if _, err := c.LookupArtist("boards of canada"); err == nil {
		t.Fatal("LookupArtist succeeded against a failing transport")
	} else if msg := err.Error(); strings.Contains(msg, "lidarr-internal") || strings.Contains(msg, "8686") {
		t.Errorf("LookupArtist error %q names the host", msg)
	}
}

func TestUpstreamErrorBodyNeverLeaks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`secret path /music/private and signed-token`))
	}))
	t.Cleanup(server.Close)

	_, err := NewClient(server.URL, "key").GetArtists()
	if err == nil {
		t.Fatal("GetArtists() error = nil")
	}
	if msg := err.Error(); strings.Contains(msg, "/music/private") || strings.Contains(msg, "signed-token") {
		t.Fatalf("error leaked upstream body: %q", msg)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedRequests.Add(1)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	if _, err := NewClient(source.URL, "lidarr-secret").GetArtists(); err == nil {
		t.Fatal("GetArtists() error = nil, want redirect refusal")
	}
	if redirectedRequests.Load() != 0 {
		t.Fatalf("client followed a redirect %d time(s), leaking the API key", redirectedRequests.Load())
	}
}

func TestGetTrackFilesForAlbumRefiltersResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/trackfile" || r.URL.Query().Get("albumId") != "9" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		// A wider answer than asked for: the client must re-filter.
		_, _ = w.Write([]byte(`[{"id":1,"albumId":9,"path":"/music/a.flac","size":1000},{"id":2,"albumId":10,"path":"/music/b.flac","size":1000}]`))
	}))
	t.Cleanup(server.Close)

	files, err := NewClient(server.URL, "k").GetTrackFilesForAlbum(9)
	if err != nil {
		t.Fatalf("GetTrackFilesForAlbum() error = %v", err)
	}
	if len(files) != 1 || files[0].ID != 1 {
		t.Fatalf("files = %#v, want only album 9's row", files)
	}
}

func TestGetCalendarQueriesWindowWithArtist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/v1/calendar" || q.Get("unmonitored") != "false" || q.Get("includeArtist") != "true" ||
			q.Get("start") == "" || q.Get("end") == "" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`[{"id":9,"title":"Fear Inoculum","releaseDate":"2026-09-05T00:00:00Z","artist":{"artistName":"Tool"}}]`))
	}))
	t.Cleanup(server.Close)

	albums, err := NewClient(server.URL, "k").GetCalendar(time.Now(), time.Now().Add(14*24*time.Hour))
	if err != nil {
		t.Fatalf("GetCalendar() error = %v", err)
	}
	if len(albums) != 1 || albums[0].Artist == nil || albums[0].Artist.ArtistName != "Tool" {
		t.Fatalf("albums = %#v", albums)
	}
}

func TestAlbumHistoryReadsPinTheirQueries(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		_, _ = w.Write([]byte(`{"page":1,"pageSize":30,"totalRecords":1,"records":[{"id":501,"eventType":"grabbed","albumId":9}]}`))
	}))
	t.Cleanup(server.Close)

	c := NewClient(server.URL, "k")
	if _, err := c.GetAlbumGrabs(9, 30); err != nil {
		t.Fatalf("GetAlbumGrabs() error = %v", err)
	}
	if _, err := c.GetImportHistory(9, "NZB-R", 30); err != nil {
		t.Fatalf("GetImportHistory() error = %v", err)
	}
	if len(paths) != 2 ||
		!strings.Contains(paths[0], "eventType=1") || !strings.Contains(paths[0], "albumId=9") ||
		!strings.Contains(paths[1], "eventType=3") || !strings.Contains(paths[1], "albumId=9") || !strings.Contains(paths[1], "downloadId=NZB-R") {
		t.Fatalf("paths = %v", paths)
	}
}

func TestGetImportHistoryRefusesOverflowingWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"page":1,"pageSize":30,"totalRecords":99,"records":[]}`))
	}))
	t.Cleanup(server.Close)

	if _, err := NewClient(server.URL, "k").GetImportHistory(9, "", 30); err == nil {
		t.Fatal("an overflowing import window must error, never truncate silently")
	}
}

func TestSettingsRawReadsAndConfigSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Lossless","extra":"must-round-trip"}]`))
		case "/api/v1/customformat":
			_, _ = w.Write([]byte(`[{"id":4,"name":"Vinyl Rip"}]`))
		case "/api/v1/indexer":
			_, _ = w.Write([]byte(`[{"id":2,"name":"Indexer A","enableRss":true}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	c := NewClient(server.URL, "k")
	profiles, err := c.GetQualityProfilesRaw()
	if err != nil || len(profiles) != 1 {
		t.Fatalf("GetQualityProfilesRaw() = %v, %v", profiles, err)
	}
	// Raw means verbatim: unknown fields survive for a future round-trip PUT.
	if !strings.Contains(string(profiles[0]), "must-round-trip") {
		t.Fatalf("profile lost fields: %s", profiles[0])
	}
	formats, err := c.GetCustomFormatsRaw()
	if err != nil || len(formats) != 1 {
		t.Fatalf("GetCustomFormatsRaw() = %v, %v", formats, err)
	}
	entries, err := c.GetConfigSummary("indexers")
	if err != nil || len(entries) != 1 {
		t.Fatalf("GetConfigSummary(indexers) = %v, %v", entries, err)
	}
}

func TestCustomFormats404MapsToSentinel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	if _, err := NewClient(server.URL, "k").GetCustomFormatsRaw(); !errors.Is(err, ErrCustomFormatsNotFound) {
		t.Fatalf("err = %v, want ErrCustomFormatsNotFound", err)
	}
}
