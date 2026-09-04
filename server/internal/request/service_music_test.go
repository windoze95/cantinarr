package request

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// newLidarrMusicTestService builds a Service wired to a fake Lidarr at
// lidarrURL, with one non-admin user granted that instance (the pin IS the
// grant — Lidarr is grant-only like Chaptarr).
func newLidarrMusicTestService(t *testing.T, lidarrURL string) (*Service, int64, string) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	res, err := database.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ('listener', '', 'user')",
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	uid, _ := res.LastInsertId()

	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{
		ServiceType: "lidarr",
		Name:        "Music",
		URL:         lidarrURL,
		APIKey:      "key",
	}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create lidarr instance: %v", err)
	}
	if err := store.SetUserDefault(uid, "lidarr", inst.ID); err != nil {
		t.Fatalf("grant lidarr: %v", err)
	}

	return NewService(database, instance.NewRegistry(store), nil, nil), uid, inst.ID
}

func TestMusicRequestRejectsBlankForeignIDAndFormats(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	res, _ := database.Exec("INSERT INTO users (username, password_hash, role) VALUES ('listener', '', 'user')")
	uid, _ := res.LastInsertId()
	svc := NewService(database, nil, nil, nil)

	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: " \t ", Title: "Blue"}); err == nil || err.Error() != "foreign_id is required for music requests" {
		t.Fatalf("blank foreign_id error = %v", err)
	}
	// Music has no format axis; a client sending one is confused and must
	// hear so rather than have the value silently dropped.
	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: "mb-1", Title: "Blue", BookFormat: "ebook"}); err == nil || !strings.Contains(err.Error(), "no book_format") {
		t.Fatalf("book_format on music error = %v", err)
	}
}

// fakeLidarr is a minimal programmable Lidarr for add-path tests.
type fakeLidarr struct {
	albums       string // JSON for GET /api/v1/album
	lookupByTerm map[string]string
	monitorCalls []string
	commandCalls []string
	addCalls     []string
	addResponse  string
	queue        string
}

func (f *fakeLidarr) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/album":
			_, _ = w.Write([]byte(f.albums))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/album/lookup":
			term := r.URL.Query().Get("term")
			if body, ok := f.lookupByTerm[term]; ok {
				_, _ = w.Write([]byte(body))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/album/monitor":
			body, _ := readAll(r)
			f.monitorCalls = append(f.monitorCalls, body)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/command":
			body, _ := readAll(r)
			f.commandCalls = append(f.commandCalls, body)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/album":
			body, _ := readAll(r)
			f.addCalls = append(f.addCalls, body)
			if f.addResponse == "" {
				http.Error(w, "unexpected add", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(f.addResponse))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/queue":
			if f.queue == "" {
				_, _ = w.Write([]byte(`{"totalRecords":0,"records":[]}`))
				return
			}
			_, _ = w.Write([]byte(f.queue))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":9,"name":"Lossless"},{"id":3,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/metadataprofile":
			_, _ = w.Write([]byte(`[{"id":11,"name":"None"},{"id":2,"name":"Standard"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Music","path":"/music","accessible":true,"defaultQualityProfileId":3,"defaultMetadataProfileId":2}]`))
		default:
			http.NotFound(w, r)
		}
	}
}

func readAll(r *http.Request) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			return sb.String(), nil
		}
	}
}

func TestMusicRequestCompleteAlbumIsAvailableWithoutMutation(t *testing.T) {
	fake := &fakeLidarr{albums: `[{"id":7,"title":"Blue Album","foreignAlbumId":"mb-1","monitored":true,"statistics":{"trackFileCount":10,"trackCount":10}}]`}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	svc, uid, instID := newLidarrMusicTestService(t, server.URL)

	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: "mb-1", Title: "Blue Album"})
	if err != nil {
		t.Fatalf("CreateMediaRequest error = %v", err)
	}
	if resp.Status != StatusAvailable || resp.InstanceID != instID {
		t.Fatalf("resp = %+v", resp)
	}
	if len(fake.monitorCalls)+len(fake.addCalls)+len(fake.commandCalls) != 0 {
		t.Fatalf("available album mutated the library: monitor=%v add=%v cmd=%v", fake.monitorCalls, fake.addCalls, fake.commandCalls)
	}
}

func TestMusicRequestMonitorsUnmonitoredRecordInPlace(t *testing.T) {
	fake := &fakeLidarr{albums: `[{"id":7,"title":"Blue Album","foreignAlbumId":"mb-1","monitored":false,"statistics":{"trackFileCount":0,"trackCount":10}}]`}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	svc, uid, _ := newLidarrMusicTestService(t, server.URL)

	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: "mb-1", Title: "Blue Album"})
	if err != nil {
		t.Fatalf("CreateMediaRequest error = %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %q, want requested", resp.Status)
	}
	if len(fake.monitorCalls) != 1 || !strings.Contains(fake.monitorCalls[0], `"albumIds":[7]`) {
		t.Fatalf("monitor calls = %v", fake.monitorCalls)
	}
	if len(fake.commandCalls) != 1 || !strings.Contains(fake.commandCalls[0], "AlbumSearch") {
		t.Fatalf("command calls = %v", fake.commandCalls)
	}
	if len(fake.addCalls) != 0 {
		t.Fatalf("existing record was re-added: %v", fake.addCalls)
	}
}

func TestMusicRequestPartialAlbumReadsPartial(t *testing.T) {
	fake := &fakeLidarr{albums: `[{"id":7,"title":"Blue Album","foreignAlbumId":"mb-1","monitored":true,"statistics":{"trackFileCount":3,"trackCount":10}}]`}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	svc, uid, _ := newLidarrMusicTestService(t, server.URL)

	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: "mb-1", Title: "Blue Album"})
	if err != nil {
		t.Fatalf("CreateMediaRequest error = %v", err)
	}
	if resp.Status != StatusPartial {
		t.Fatalf("status = %q, want partial", resp.Status)
	}
}

// TestMusicRequestAddsNewAlbumWithRootFolderDefaults pins the whole add path:
// the exact-id lookup term, the nested-artist payload built from the root
// folder's defaults, the never-subscribe-the-discography monitor scope, and
// the hidden "None" metadata profile being skipped when a folder's default
// dangles.
func TestMusicRequestAddsNewAlbumWithRootFolderDefaults(t *testing.T) {
	fake := &fakeLidarr{
		albums: `[]`,
		lookupByTerm: map[string]string{
			"lidarr:mb-1": `[{"title":"Blue Album","foreignAlbumId":"mb-1","artist":{"artistName":"Weezer","foreignArtistId":"artist-9"}}]`,
		},
		addResponse: `{"id":77,"title":"Blue Album","foreignAlbumId":"mb-1","monitored":true}`,
	}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	svc, uid, _ := newLidarrMusicTestService(t, server.URL)

	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: "mb-1", Title: "Blue Album"})
	if err != nil {
		t.Fatalf("CreateMediaRequest error = %v", err)
	}
	if resp.Status != StatusRequested || resp.Title != "Blue Album" {
		t.Fatalf("resp = %+v", resp)
	}
	if len(fake.addCalls) != 1 {
		t.Fatalf("add calls = %v", fake.addCalls)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(fake.addCalls[0]), &payload); err != nil {
		t.Fatalf("decode add payload: %v", err)
	}
	artist, _ := payload["artist"].(map[string]any)
	if artist["foreignArtistId"] != "artist-9" {
		t.Fatalf("artist = %v", artist)
	}
	// Root folder defaults: quality 3 and metadata 2 come from the folder row,
	// not from first-profile order (quality profile 9 lists first).
	if artist["qualityProfileId"] != float64(3) || artist["metadataProfileId"] != float64(2) || artist["rootFolderPath"] != "/music" {
		t.Fatalf("artist config = %v", artist)
	}
	if artist["monitorNewItems"] != "none" {
		t.Fatalf("monitorNewItems = %v", artist["monitorNewItems"])
	}
	artistOptions, _ := artist["addOptions"].(map[string]any)
	// The monitor option must be absent: "none" would unmonitor the artist
	// itself (verified live), whose albums then never count as wanted.
	if _, present := artistOptions["monitor"]; present {
		t.Fatalf("artist addOptions carried a monitor option: %v", artistOptions)
	}
	if artistOptions["searchForMissingAlbums"] != false {
		t.Fatalf("artist addOptions = %v", artistOptions)
	}
	options, _ := payload["addOptions"].(map[string]any)
	if options["searchForNewAlbum"] != true {
		t.Fatalf("album addOptions = %v", options)
	}
}

// TestMusicAddSkipsNoneMetadataProfile pins the trap: Lidarr ships a hidden
// "None" metadata profile meant for import-list exclusion; selecting it would
// hydrate no albums. A root folder whose default names it — and a profile
// list where it sorts first — must both fall through to the first real
// profile.
func TestMusicAddSkipsNoneMetadataProfile(t *testing.T) {
	fake := &fakeLidarr{
		albums: `[]`,
		lookupByTerm: map[string]string{
			"lidarr:mb-3": `[{"title":"OK Computer","foreignAlbumId":"mb-3","artist":{"artistName":"Radiohead","foreignArtistId":"artist-1"}}]`,
		},
		addResponse: `{"id":90,"title":"OK Computer","foreignAlbumId":"mb-3","monitored":true}`,
	}
	base := fake.handler(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/rootfolder" {
			// The folder's metadata default dangles onto the hidden profile.
			_, _ = w.Write([]byte(`[{"id":1,"name":"Music","path":"/music","accessible":true,"defaultQualityProfileId":0,"defaultMetadataProfileId":11}]`))
			return
		}
		base(w, r)
	}))
	t.Cleanup(server.Close)
	svc, uid, _ := newLidarrMusicTestService(t, server.URL)

	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: "mb-3", Title: "OK Computer"}); err != nil {
		t.Fatalf("CreateMediaRequest error = %v", err)
	}
	if len(fake.addCalls) != 1 {
		t.Fatalf("add calls = %d", len(fake.addCalls))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(fake.addCalls[0]), &payload); err != nil {
		t.Fatalf("decode add payload: %v", err)
	}
	artist, _ := payload["artist"].(map[string]any)
	// Metadata profile 2 ("Standard"), never 11 ("None"); quality falls to the
	// first profile (9) because the folder's quality default is unset.
	if artist["metadataProfileId"] != float64(2) || artist["qualityProfileId"] != float64(9) {
		t.Fatalf("artist config = %v", artist)
	}
}

func TestMusicRequestParksWhenMetadataUnresolved(t *testing.T) {
	fake := &fakeLidarr{albums: `[]`, lookupByTerm: map[string]string{}}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	svc, uid, instID := newLidarrMusicTestService(t, server.URL)

	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: "mb-unknown", Title: "Ghost Album", SearchTerm: "ghost album"})
	if err != nil {
		t.Fatalf("CreateMediaRequest error = %v", err)
	}
	if resp.Status != StatusPending || resp.Message != musicParkedMessage {
		t.Fatalf("resp = %+v", resp)
	}
	// The parked row records that its add already ran and failed, so the
	// approval queue can say so instead of inviting a blind Approve.
	pending, err := svc.ListPending()
	if err != nil {
		t.Fatalf("ListPending error = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	row := pending[0]
	if row.MediaType != "music" || row.ForeignID != "mb-unknown" || row.InstanceID != instID {
		t.Fatalf("pending row = %+v", row)
	}
	if row.AddFailureReason != bookAddFailureMetadataUnresolved {
		t.Fatalf("add failure = %q", row.AddFailureReason)
	}
	if row.BookFormat != "" {
		t.Fatalf("music row grew a book_format %q", row.BookFormat)
	}
}

func TestMusicApprovalReplaysAddAndStampsRecordID(t *testing.T) {
	fake := &fakeLidarr{
		albums: `[]`,
		lookupByTerm: map[string]string{
			"lidarr:mb-2": `[{"title":"Pinkerton","foreignAlbumId":"mb-2","artist":{"artistName":"Weezer","foreignArtistId":"artist-9"}}]`,
		},
		addResponse: `{"id":88,"title":"Pinkerton","foreignAlbumId":"mb-2","monitored":true}`,
	}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	svc, uid, instID := newLidarrMusicTestService(t, server.URL)

	// Force the approval path.
	if err := svc.SetGlobalSettings(GlobalSettings{RequireApproval: true, DefaultSeasonScope: SeasonScopeAll}); err != nil {
		t.Fatalf("SetGlobalSettings: %v", err)
	}
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: "mb-2", Title: "Pinkerton"})
	if err != nil {
		t.Fatalf("create error = %v", err)
	}
	if resp.Status != StatusPending {
		t.Fatalf("create status = %q", resp.Status)
	}
	if len(fake.addCalls) != 0 {
		t.Fatal("approval-required create mutated the library")
	}

	pending, err := svc.ListPending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	approved, err := svc.ApproveRequest(0, pending[0].ID, nil)
	if err != nil {
		t.Fatalf("ApproveRequest error = %v", err)
	}
	if approved.Status != StatusRequested {
		t.Fatalf("approved status = %q", approved.Status)
	}
	if len(fake.addCalls) != 1 {
		t.Fatalf("add calls after approval = %d", len(fake.addCalls))
	}

	// The fulfilled row carries the Lidarr record id in the reused
	// book_record_id column and no book_format.
	var status string
	var recordID int
	var format any
	if err := svc.db.QueryRow(
		"SELECT status, COALESCE(book_record_id, 0), book_format FROM request_log WHERE id = ?", pending[0].ID,
	).Scan(&status, &recordID, &format); err != nil {
		t.Fatalf("read fulfilled row: %v", err)
	}
	if status != StatusRequested || recordID != 88 || format != nil {
		t.Fatalf("fulfilled row = status %q record %d format %v (instance %s)", status, recordID, format, instID)
	}
}

func TestMusicApprovalRequiredCreateShortCircuitsOnLiveTruth(t *testing.T) {
	fake := &fakeLidarr{albums: `[{"id":7,"title":"Blue Album","foreignAlbumId":"mb-1","monitored":true,"statistics":{"trackFileCount":10,"trackCount":10}}]`}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	svc, uid, _ := newLidarrMusicTestService(t, server.URL)

	if err := svc.SetGlobalSettings(GlobalSettings{RequireApproval: true, DefaultSeasonScope: SeasonScopeAll}); err != nil {
		t.Fatalf("SetGlobalSettings: %v", err)
	}
	resp, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: "mb-1", Title: "Blue Album"})
	if err != nil {
		t.Fatalf("create error = %v", err)
	}
	if resp.Status != StatusAvailable {
		t.Fatalf("status = %q, want available (no pending row for an owned album)", resp.Status)
	}
	if n, _ := svc.PendingCount(); n != 0 {
		t.Fatalf("pending count = %d", n)
	}
}

func TestMusicStatusFollowsRekeyedRecord(t *testing.T) {
	// The request was logged under mb-old; the library has since re-keyed the
	// record to mb-new. The persisted record id must keep resolving live truth
	// and surface the canonical id so the client can re-address the album.
	fake := &fakeLidarr{albums: `[{"id":88,"title":"Pinkerton","foreignAlbumId":"mb-new","monitored":true,"statistics":{"trackFileCount":12,"trackCount":12}}]`}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	svc, uid, instID := newLidarrMusicTestService(t, server.URL)

	if _, err := svc.db.Exec(
		"INSERT INTO request_log (user_id, tmdb_id, foreign_id, book_record_id, instance_id, media_type, title, status) VALUES (?, 0, 'mb-old', 88, ?, 'music', 'Pinkerton', 'requested')",
		uid, instID,
	); err != nil {
		t.Fatalf("seed request row: %v", err)
	}

	resp, err := svc.GetUserMusicStatusForInstance(uid, "mb-old", "")
	if err != nil {
		t.Fatalf("GetUserMusicStatusForInstance error = %v", err)
	}
	if resp.Status != StatusAvailable {
		t.Fatalf("status = %q, want available via record id", resp.Status)
	}
	if resp.CanonicalForeignID != "mb-new" {
		t.Fatalf("canonical id = %q, want mb-new", resp.CanonicalForeignID)
	}
}

func TestMusicStatusUnrequestedAlbumReadsLiveLibrary(t *testing.T) {
	fake := &fakeLidarr{albums: `[{"id":7,"title":"Blue Album","foreignAlbumId":"mb-1","monitored":true,"statistics":{"trackFileCount":0,"trackCount":10}}]`}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	svc, uid, _ := newLidarrMusicTestService(t, server.URL)

	resp, err := svc.GetUserMusicStatusForInstance(uid, "mb-1", "")
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %q, want requested (monitored, no file)", resp.Status)
	}

	missing, err := svc.GetUserMusicStatusForInstance(uid, "mb-nope", "")
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	if missing.Status != StatusUnavailable {
		t.Fatalf("missing status = %q", missing.Status)
	}
}

func TestMusicRequestDeniedWithoutGrant(t *testing.T) {
	fake := &fakeLidarr{albums: `[]`}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	svc, _, _ := newLidarrMusicTestService(t, server.URL)

	// A second user with no grant: Lidarr is grant-only, so there is no
	// global-default fallback to leak the library through.
	res, err := svc.db.Exec("INSERT INTO users (username, password_hash, role) VALUES ('stranger', '', 'user')")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	strangerID, _ := res.LastInsertId()
	if _, err := svc.CreateMediaRequest(strangerID, &CreateRequest{MediaType: "music", ForeignID: "mb-1", Title: "Blue Album"}); err == nil || !strings.Contains(err.Error(), "not configured for you") {
		t.Fatalf("ungranted create error = %v", err)
	}
}

func TestMusicRequestExplicitForeignInstanceForbidden(t *testing.T) {
	fake := &fakeLidarr{albums: `[]`}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	svc, uid, _ := newLidarrMusicTestService(t, server.URL)

	if _, err := svc.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: "mb-1", Title: "Blue Album", InstanceID: "someone-elses"}); !errors.Is(err, ErrLidarrInstanceForbidden) {
		t.Fatalf("foreign instance error = %v, want ErrLidarrInstanceForbidden", err)
	}
}

// SearchAlbumsForUser resolves the user's own instance and shapes results for
// the AI tools: artist and year mapped, external covers preferred, arr-relative
// cover paths dropped, and each addressable row cached for the immediate
// display_media verification.
func TestSearchAlbumsForUserScopesAndCaches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/album/lookup" || r.URL.Query().Get("term") != "fear" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`[
			{"title":"Fear Inoculum","artist":{"artistName":"Tool"},"foreignAlbumId":"fa-1","releaseDate":"2019-08-30T00:00:00Z","overview":"Fifth.","images":[{"coverType":"cover","url":"/mediacover/1.jpg","remoteUrl":"https://covers.example.org/1.jpg"}]},
			{"title":"Relative Only","foreignAlbumId":"fa-2","remoteCover":"/MediaCover/Albums/2/cover.jpg"},
			{"title":"Unaddressable","foreignAlbumId":"  "}
		]`))
	}))
	t.Cleanup(srv.Close)

	svc, uid, _ := newLidarrMusicTestService(t, srv.URL)

	results, err := svc.SearchAlbumsForUser(uid, "fear")
	if err != nil {
		t.Fatalf("SearchAlbumsForUser: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v, want the two addressable rows", results)
	}
	first := results[0]
	if first.Title != "Fear Inoculum" || first.ArtistName != "Tool" || first.Year != 2019 ||
		first.ForeignAlbumID != "fa-1" || first.RemoteCover != "https://covers.example.org/1.jpg" {
		t.Fatalf("first = %#v", first)
	}
	// An arr-relative cover must be dropped, never handed to a client.
	if results[1].RemoteCover != "" {
		t.Fatalf("relative cover leaked: %q", results[1].RemoteCover)
	}

	cached, ok := svc.CachedAlbumByForeignID(uid, "fa-1")
	if !ok || cached.Title != "Fear Inoculum" {
		t.Fatalf("cache miss for a just-searched id: %#v %v", cached, ok)
	}
	if _, ok := svc.CachedAlbumByForeignID(uid, "fa-unknown"); ok {
		t.Fatal("an unsearched id must miss the cache (callers re-verify live)")
	}
}

func TestSearchAlbumsForUserWithoutAccessFailsClosed(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	res, _ := database.Exec("INSERT INTO users (username, password_hash, role) VALUES ('nobody', '', 'user')")
	uid, _ := res.LastInsertId()
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	svc := NewService(database, instance.NewRegistry(instance.NewStore(database, cipher)), nil, nil)

	if _, err := svc.SearchAlbumsForUser(uid, "anything"); !errors.Is(err, ErrNoLidarrAccess) {
		t.Fatalf("err = %v, want ErrNoLidarrAccess", err)
	}
}
