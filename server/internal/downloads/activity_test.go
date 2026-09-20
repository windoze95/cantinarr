package downloads

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
)

type activityEnv struct {
	db       *sql.DB
	h        *Handler
	settings *serversettings.Service
	policy   *contentpolicy.Service
}

func newActivityEnv(t *testing.T) *activityEnv {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	store := instance.NewStore(d, cipher)
	e := &activityEnv{db: d, settings: serversettings.NewService(d, nil), policy: contentpolicy.New(d, nil, nil)}
	e.h = NewHandler(store, instance.NewRegistry(store))
	e.h.ConfigureActivity(d, e.policy, e.settings, nil)
	for _, q := range []string{"INSERT INTO users(id,username,password_hash,role) VALUES (1,'admin','','admin')", "INSERT INTO users(id,username,password_hash,role) VALUES (2,'reader','','user')", "INSERT INTO users(id,username,password_hash,role) VALUES (3,'other','','user')"} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *activityEnv) add(t *testing.T, kind, rawURL string) instance.Instance {
	t.Helper()
	i := instance.Instance{ServiceType: kind, Name: kind, URL: rawURL, APIKey: "fixture-key"}
	if err := e.h.store.Create(&i); err != nil {
		t.Fatal(err)
	}
	return i
}

func (e *activityEnv) get(t *testing.T, user int64, path string) (*Activity, *httptest.ResponseRecorder) {
	t.Helper()
	role := "user"
	if user == 1 {
		role = "admin"
	}
	r := httptest.NewRequest("GET", path, nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: user, Role: role}))
	w := httptest.NewRecorder()
	if strings.Contains(path, "summary") {
		e.h.GetSummary(w, r)
	} else {
		e.h.GetActivity(w, r)
	}
	var a Activity
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
	}
	return &a, w
}

func rec(s string) record {
	var m record
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic(err)
	}
	return m
}
func definition(kind, host string) record {
	return rec(fmt.Sprintf(`{"id":1,"name":"Client","implementation":%q,"fields":[{"name":"host","value":%q},{"name":"port","value":80}]}`, kind, host))
}

func cacheSource(e *activityEnv, inst instance.Instance, snapshot sourceSnapshot) {
	snapshot.at = time.Now().UTC()
	e.h.activity.mu.Lock()
	defer e.h.activity.mu.Unlock()
	e.h.activity.cache[inst.ID] = cachedSource{sourceKey(inst), snapshot}
}

func movieRow(id, tmdb int, download string, size, left int) activityRow {
	return activityRow{queue: rec(fmt.Sprintf(`{"id":%d,"movieId":%d,"downloadClient":"Client","downloadId":%q,"size":%d,"sizeleft":%d,"status":"downloading","title":"PRIVATE.release.filename"}`, id, id, download, size, left)), parent: rec(fmt.Sprintf(`{"id":%d,"tmdbId":%d,"title":"Movie %d","year":2026,"certification":"G","genres":[],"images":[{"remoteUrl":"https://images.example/poster.jpg"}]}`, id, tmdb, id)), identityKnown: true, detailsKnown: true}
}

func TestActivityCorrelatesAllClientsWithoutConfusingControlIDs(t *testing.T) {
	for _, kind := range DownloadClientTypes() {
		t.Run(kind, func(t *testing.T) {
			e := newActivityEnv(t)
			arr := e.add(t, "radarr", "http://arr.invalid")
			client := e.add(t, kind, "http://client.invalid")
			implementation := kind
			if kind == "rutorrent" {
				implementation = "rTorrent"
			}
			control, tracking := "job-1", "job-1"
			if kind == "nzbget" {
				control, tracking = "42", "drone-alias"
			}
			cacheSource(e, client, sourceSnapshot{queue: &QueueView{Items: []QueueItem{{ID: control, CorrelationID: tracking, Name: "PRIVATE.release.filename", SizeBytes: 100, SizeLeftBytes: 50, Status: "paused"}}}})
			cacheSource(e, arr, sourceSnapshot{definitions: []record{definition(implementation, "client.invalid")}, rows: []activityRow{movieRow(1, 100, tracking, 100, 60)}})
			a, w := e.get(t, 1, "/api/downloads/activity")
			if w.Code != 200 || a.Count == nil || *a.Count != 1 || len(a.Groups) != 1 {
				t.Fatalf("%s", w.Body.String())
			}
			if a.Jobs[0].Control == nil || a.Jobs[0].Control.ItemID != control || a.Jobs[0].Progress != 50 {
				t.Fatalf("job = %+v", a.Jobs[0])
			}
			a, w = e.get(t, 2, "/api/downloads/activity")
			if a.Count == nil || *a.Count != 1 || a.Jobs[0].Control != nil {
				t.Fatalf("%s", w.Body.String())
			}
			for _, secret := range []string{"PRIVATE", "client.invalid", "item_id", "control", "drone-alias", "fixture-key"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatalf("requester leaked %q", secret)
				}
			}
		})
	}
}

func TestActivityCountsUniqueJobsAcrossLibrariesAndClients(t *testing.T) {
	e := newActivityEnv(t)
	a1 := e.add(t, "radarr", "http://arr1.invalid")
	a2 := e.add(t, "radarr", "http://arr2.invalid")
	c1 := e.add(t, "sabnzbd", "http://client1.invalid")
	c2 := e.add(t, "sabnzbd", "http://client2.invalid")
	for _, c := range []instance.Instance{c1, c2} {
		cacheSource(e, c, sourceSnapshot{queue: &QueueView{Items: []QueueItem{{ID: "same-id", SizeBytes: 100, SizeLeftBytes: 50, Status: "downloading"}, {ID: "complete", SizeBytes: 100, SizeLeftBytes: 0, Status: "completed"}}}})
	}
	cacheSource(e, a1, sourceSnapshot{definitions: []record{definition("sabnzbd", "client1.invalid")}, rows: []activityRow{movieRow(1, 100, "same-id", 100, 50)}})
	cacheSource(e, a2, sourceSnapshot{definitions: []record{definition("sabnzbd", "client1.invalid")}, rows: []activityRow{movieRow(1, 100, "same-id", 100, 50)}})
	a, w := e.get(t, 1, "/api/downloads/activity")
	if a.Count == nil || *a.Count != 2 || len(a.Groups) != 3 {
		t.Fatalf("%s", w.Body.String())
	}
	if a.Groups[0].ID == a.Groups[1].ID || a.Groups[0].JobIDs[0] != a.Groups[1].JobIDs[0] {
		t.Fatal("library identities or shared job lost")
	}
}

func TestActivityUnconfiguredClientKeepsArrProgressAndUncertainMappingDisablesCount(t *testing.T) {
	e := newActivityEnv(t)
	arr := e.add(t, "radarr", "http://arr.invalid")
	ss := sourceSnapshot{definitions: []record{definition("sabnzbd", "not-configured.invalid")}, rows: []activityRow{movieRow(1, 100, "job", 100, 25)}}
	cacheSource(e, arr, ss)
	a, w := e.get(t, 2, "/api/downloads/activity")
	if a.Count == nil || *a.Count != 1 || a.Jobs[0].Progress != 75 || a.Jobs[0].Control != nil {
		t.Fatalf("%s", w.Body.String())
	}
	ss.definitions = nil
	cacheSource(e, arr, ss)
	a, w = e.get(t, 1, "/api/downloads/summary")
	if a.Count != nil || a.Complete || len(a.Groups) != 0 || len(a.Jobs) != 0 {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestActivityMyRequestsAndServerRestriction(t *testing.T) {
	e := newActivityEnv(t)
	arr := e.add(t, "radarr", "http://arr.invalid")
	cacheSource(e, arr, sourceSnapshot{definitions: []record{definition("sabnzbd", "client.invalid")}, rows: []activityRow{movieRow(1, 100, "one", 100, 25), movieRow(2, 200, "two", 300, 150)}})
	_, err := e.db.Exec(`INSERT INTO request_log(user_id,tmdb_id,media_type,title,instance_id) VALUES(2,100,'movie','Deliberately unrelated title',?)`, arr.ID)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := e.get(t, 2, "/api/downloads/activity?scope=all")
	mine, w := e.get(t, 2, "/api/downloads/activity?scope=mine")
	if all.Count == nil || *all.Count != 2 || mine.Count == nil || *mine.Count != 1 || mine.Groups[0].Title != "Movie 1" {
		t.Fatalf("%s", w.Body.String())
	}
	if _, err := e.settings.SetDownloadsUserScope("mine"); err != nil {
		t.Fatal(err)
	}
	forced, w := e.get(t, 2, "/api/downloads/activity?scope=all")
	if forced.Scope != "mine" || forced.Count == nil || *forced.Count != 1 {
		t.Fatalf("%s", w.Body.String())
	}
	admin, _ := e.get(t, 1, "/api/downloads/summary?scope=mine")
	if admin.Count == nil || *admin.Count != 2 {
		t.Fatal("admin count must be server-wide")
	}
}

func TestActivitySeasonPackOwnRequestMappingAndByteWeight(t *testing.T) {
	e := newActivityEnv(t)
	inst := e.add(t, "sonarr", "http://sonarr.invalid")
	var rows []activityRow
	for i := 1; i <= 3; i++ {
		download, size, left := "pack", 100, 50
		season := 7
		if i == 3 {
			download, size, left, season = "single", 300, 0, 8
			left = 75
		}
		row := activityRow{queue: rec(fmt.Sprintf(`{"id":%d,"seriesId":4,"downloadClient":"Client","downloadId":%q,"size":%d,"sizeleft":%d,"status":"downloading"}`, i, download, size, left)), parent: rec(`{"id":4,"tvdbId":900,"title":"Actual native show","certification":"TV-Y"}`), identityKnown: true, detailsKnown: true, children: []record{rec(fmt.Sprintf(`{"id":%d,"seriesId":4,"seasonNumber":%d,"episodeNumber":%d,"title":"Episode %d"}`, i, season, i, i))}}
		rows = append(rows, row)
	}
	cacheSource(e, inst, sourceSnapshot{rows: rows, definitions: []record{definition("sabnzbd", "client.invalid")}})
	result, err := e.db.Exec(`INSERT INTO request_log(user_id,tmdb_id,tvdb_id,media_type,title,instance_id) VALUES(2,10,900,'tv','Source show',?)`, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	_, err = e.db.Exec(`INSERT INTO request_tv_targets(request_id,snapshot,series_id) VALUES(?, ?,4)`, id, `{"match":{"tvdb_id":900},"source_seasons":[1],"target_seasons":[7],"pilot":true}`)
	if err != nil {
		t.Fatal(err)
	}
	a, w := e.get(t, 1, "/api/downloads/activity")
	if a.Count == nil || *a.Count != 2 || len(a.Groups) != 1 || len(a.Groups[0].Children) != 3 || a.Groups[0].Progress != 68.75 {
		t.Fatalf("%s", w.Body.String())
	}
	a, w = e.get(t, 2, "/api/downloads/activity?scope=mine")
	if a.Count == nil || *a.Count != 1 || len(a.Groups[0].Children) != 1 || a.Groups[0].Children[0].Episode != 1 {
		t.Fatalf("%s", w.Body.String())
	}
	rows[0].children = nil
	rows[0].detailsKnown = false
	cacheSource(e, inst, sourceSnapshot{rows: rows[:1], definitions: []record{definition("sabnzbd", "client.invalid")}})
	a, w = e.get(t, 2, "/api/downloads/activity?scope=mine")
	if a.Count != nil || a.Complete {
		t.Fatalf("unknown pack reported exact count: %s", w.Body.String())
	}
}

func TestActivityBookSubscriptionsAndAlbumJobs(t *testing.T) {
	e := newActivityEnv(t)
	book := e.add(t, "chaptarr", "http://books.invalid")
	music := e.add(t, "lidarr", "http://music.invalid")
	if err := e.h.store.SetUserGrants(2, map[string][]string{"chaptarr": {book.ID}, "lidarr": {music.ID}}); err != nil {
		t.Fatal(err)
	}
	var books []activityRow
	for i, format := range []string{"ebook", "audiobook"} {
		row := movieRow(i+1, 0, format, 100, 40)
		row.parent = rec(fmt.Sprintf(`{"id":%d,"title":"Book","foreignBookId":"verified-book","mediaType":%q}`, i+1, format))
		books = append(books, row)
	}
	cacheSource(e, book, sourceSnapshot{rows: books, definitions: []record{definition("sabnzbd", "client.invalid")}})
	album := movieRow(3, 0, "album", 200, 100)
	album.parent = rec(`{"id":3,"title":"Album","foreignAlbumId":"verified-album"}`)
	album.children = []record{rec(`{"id":1,"title":"First track","mediumNumber":1,"trackNumber":"1"}`), rec(`{"id":2,"title":"Second track","mediumNumber":2,"trackNumber":"1"}`)}
	cacheSource(e, music, sourceSnapshot{rows: []activityRow{album}, definitions: []record{definition("sabnzbd", "client.invalid")}})
	result, err := e.db.Exec(`INSERT INTO request_log(user_id,tmdb_id,foreign_id,media_type,title,book_format,instance_id) VALUES(3,0,'verified-book','book','Book','both',?)`, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	if _, err = e.db.Exec(`INSERT INTO book_request_waiters(request_id,user_id,book_format) VALUES(?,2,'audiobook')`, id); err != nil {
		t.Fatal(err)
	}
	a, w := e.get(t, 2, "/api/downloads/activity?scope=mine")
	if a.Count == nil || *a.Count != 1 || a.Groups[0].Format != "audiobook" {
		t.Fatalf("%s", w.Body.String())
	}
	a, w = e.get(t, 2, "/api/downloads/activity")
	if a.Count == nil || *a.Count != 3 {
		t.Fatalf("%s", w.Body.String())
	}
	if err := e.h.store.SetUserGrants(2, map[string][]string{"chaptarr": {}, "lidarr": {}}); err != nil {
		t.Fatal(err)
	}
	a, w = e.get(t, 2, "/api/downloads/activity")
	if a.Count == nil || *a.Count != 0 || len(a.Groups) > 0 {
		t.Fatalf("cached grant leaked: %s", w.Body.String())
	}
}

func TestActivityCanonicalRequestIdentities(t *testing.T) {
	book := instance.Instance{ID: "books", ServiceType: "chaptarr"}
	row := activityRow{parent: rec(`{"id":7,"foreignBookId":"canonical","mediaType":"ebook"}`)}
	requests := []savedRequest{{Media: "book", Instance: "books", Foreign: "old", Canonical: "canonical", Format: "ebook"}}
	if ok, _ := matchesRequests(requests, book, row); !ok {
		t.Fatal("canonical book identity lost")
	}
	row.parent["mediaType"] = ""
	if !uncertainRequestScope(requests, book, row) {
		t.Fatal("unknown canonical format looked absent")
	}
	requests[0].DeliveryNative = 8
	row.parent["mediaType"] = "ebook"
	if ok, _ := matchesRequests(requests, book, row); ok {
		t.Fatal("different native book record was conflated")
	}
	music := instance.Instance{ID: "music", ServiceType: "lidarr"}
	row.parent = rec(`{"id":3,"foreignAlbumId":"canonical-album"}`)
	requests = []savedRequest{{Media: "music", Instance: "music", Foreign: "old-album", Canonical: "canonical-album"}}
	if ok, _ := matchesRequests(requests, music, row); !ok {
		t.Fatal("canonical album identity lost")
	}
	music.ID = "other-music"
	if ok, _ := matchesRequests(requests, music, row); ok {
		t.Fatal("another library's request was matched")
	}
}

func TestActivityKidsFilteringAndUnknownIdentity(t *testing.T) {
	e := newActivityEnv(t)
	inst := e.add(t, "radarr", "http://arr.invalid")
	rows := []activityRow{movieRow(1, 100, "safe", 100, 50), movieRow(2, 200, "adult", 100, 50)}
	rows[1].parent["certification"] = "R"
	rows[1].parent["title"] = "Forbidden title"
	cacheSource(e, inst, sourceSnapshot{rows: rows, definitions: []record{definition("sabnzbd", "client.invalid")}})
	if err := e.policy.Store.Set(2, contentpolicy.Policy{RatingRegion: "US", MaxMovieRating: "G", MaxTVRating: "TV-Y", BlockUnrated: true}); err != nil {
		t.Fatal(err)
	}
	a, w := e.get(t, 2, "/api/downloads/activity")
	if a.Count == nil || *a.Count != 1 || strings.Contains(w.Body.String(), "Forbidden") {
		t.Fatalf("%s", w.Body.String())
	}
	rows[0].identityKnown = false
	cacheSource(e, inst, sourceSnapshot{rows: rows[:1], definitions: []record{definition("sabnzbd", "client.invalid")}})
	a, w = e.get(t, 2, "/api/downloads/activity")
	if a.Count != nil || a.Complete || len(a.Groups) > 0 {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestActivityCountingStatesAndRecovery(t *testing.T) {
	e := newActivityEnv(t)
	inst := e.add(t, "sabnzbd", "http://client.invalid")
	for status, want := range map[string]int{"Downloading": 1, "Paused": 1, "Queued": 1, "stalledDL": 1, "Failed": 1, "checkingDL": 1, "Completed": 0, "Seeding": 0, "Importing": 0, "Unpacking": 0} {
		cacheSource(e, inst, sourceSnapshot{queue: &QueueView{Items: []QueueItem{{ID: "one", Status: status, SizeBytes: 100, SizeLeftBytes: 50}}}})
		a, w := e.get(t, 1, "/api/downloads/summary")
		if a.Count == nil || *a.Count != want {
			t.Fatalf("%s: %s", status, w.Body.String())
		}
	}
	cacheSource(e, inst, sourceSnapshot{err: fmt.Errorf("private upstream error")})
	a, w := e.get(t, 1, "/api/downloads/summary")
	if a.Count != nil || a.Complete || strings.Contains(w.Body.String(), "private upstream") {
		t.Fatalf("%s", w.Body.String())
	}
	cacheSource(e, inst, sourceSnapshot{queue: &QueueView{Items: []QueueItem{}}})
	a, w = e.get(t, 1, "/api/downloads/summary")
	if a.Count == nil || *a.Count != 0 || !a.Complete {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestActivityAmbiguousAliasNeverSelectsAControl(t *testing.T) {
	e := newActivityEnv(t)
	arr := e.add(t, "radarr", "http://arr.invalid")
	client := e.add(t, "nzbget", "http://client.invalid")
	cacheSource(e, client, sourceSnapshot{queue: &QueueView{Items: []QueueItem{
		{ID: "42", CorrelationID: "alias", SizeBytes: 100, SizeLeftBytes: 50, Status: "PAUSED"},
		{ID: "43", CorrelationID: "alias", SizeBytes: 100, SizeLeftBytes: 50, Status: "PAUSED"},
	}}})
	cacheSource(e, arr, sourceSnapshot{definitions: []record{definition("nzbget", "client.invalid")}, rows: []activityRow{movieRow(1, 100, "alias", 100, 50)}})
	a, w := e.get(t, 1, "/api/downloads/activity")
	if a.Complete || a.Count != nil || len(a.Groups) != 2 {
		t.Fatalf("ambiguous alias: %s", w.Body.String())
	}
	for _, job := range a.Jobs {
		if job.ID == a.Groups[0].JobIDs[0] && job.Control != nil {
			t.Fatal("ambiguous content received a guessed control")
		}
	}
}

func TestActivityCompletedClientOverridesArrImportLag(t *testing.T) {
	e := newActivityEnv(t)
	arr := e.add(t, "radarr", "http://arr.invalid")
	client := e.add(t, "qbittorrent", "http://client.invalid")
	cacheSource(e, client, sourceSnapshot{queue: &QueueView{Items: []QueueItem{
		{ID: "hash", SizeBytes: 100, SizeLeftBytes: 0, Status: "pausedUP"},
	}}})
	cacheSource(e, arr, sourceSnapshot{definitions: []record{definition("qbittorrent", "client.invalid")}, rows: []activityRow{movieRow(1, 100, "HASH", 100, 50)}})
	for _, user := range []int64{1, 2} {
		a, w := e.get(t, user, "/api/downloads/activity")
		if a.Count == nil || *a.Count != 0 || len(a.Groups) != 0 {
			t.Fatalf("finished client counted as downloading: %s", w.Body.String())
		}
	}
}

func TestActivityUnverifiedClientIdentityCannotProduceAnExactCount(t *testing.T) {
	for _, scenario := range []string{"another address", "missing client job"} {
		t.Run(scenario, func(t *testing.T) {
			e := newActivityEnv(t)
			arr := e.add(t, "radarr", "http://arr.invalid")
			client := e.add(t, "sabnzbd", "http://client.invalid")
			host := "client.invalid"
			queue := &QueueView{Items: []QueueItem{}}
			if scenario == "another address" {
				host = "unverified-alias.invalid"
				queue.Items = []QueueItem{{ID: "one", Status: "downloading", SizeBytes: 100, SizeLeftBytes: 50}}
			}
			cacheSource(e, client, sourceSnapshot{queue: queue})
			cacheSource(e, arr, sourceSnapshot{definitions: []record{definition("sabnzbd", host)}, rows: []activityRow{movieRow(1, 100, "one", 100, 50)}})
			for _, user := range []int64{1, 2} {
				a, w := e.get(t, user, "/api/downloads/activity")
				if a.Complete || a.Count != nil || len(a.Groups) == 0 || a.Jobs[0].Progress != 50 {
					t.Fatalf("uncertain source agreement looked exact: %s", w.Body.String())
				}
				for _, job := range a.Jobs {
					if job.ID == a.Groups[0].JobIDs[0] && job.Control != nil {
						t.Fatal("unverified content gained client controls")
					}
				}
			}
		})
	}
}

func TestActivityInvalidatedReadCannotPopulateCache(t *testing.T) {
	e := newActivityEnv(t)
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		fmt.Fprint(w, `{"totalRecords":0,"records":[]}`)
	}))
	defer upstream.Close()
	e.add(t, "radarr", upstream.URL)
	finished := make(chan *httptest.ResponseRecorder)
	go func() { _, w := e.get(t, 2, "/api/downloads/activity"); finished <- w }()
	<-entered
	e.h.InvalidateActivity()
	close(release)
	if w := <-finished; w.Code != 503 {
		t.Fatalf("superseded source returned %d", w.Code)
	}
	e.h.activity.mu.Lock()
	defer e.h.activity.mu.Unlock()
	if len(e.h.activity.cache) != 0 {
		t.Fatal("superseded source repopulated the cache")
	}
}

func TestActivityMalformedQueueIsNotConfirmedZero(t *testing.T) {
	for _, body := range []string{
		`{"records":[]}`,
		`{"totalRecords":2,"records":[]}`,
		`{"totalRecords":1,"records":[{"id":1,"status":"downloading","size":100}]}`,
	} {
		e := newActivityEnv(t)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		e.add(t, "radarr", upstream.URL)
		a, w := e.get(t, 2, "/api/downloads/summary")
		upstream.Close()
		if w.Code != 200 || a.Complete || a.Count != nil || len(a.Sources) != 1 || a.Sources[0].Available {
			t.Fatalf("malformed queue looked empty: %s", w.Body.String())
		}
	}
}

func TestActivitySettingsPreserveOtherPreferencesAndRequireAdmin(t *testing.T) {
	e := newActivityEnv(t)
	if _, err := e.db.Exec(`INSERT INTO settings(key,value) VALUES ('server_settings','{"external_url":"https://media.example","discovery_source":"tmdb_popular"}')`); err != nil {
		t.Fatal(err)
	}
	changes := 0
	for _, tc := range []struct {
		role, body string
		want       int
	}{
		{"user", `{"user_scope":"mine"}`, 403},
		{"admin", `{"user_scope":"invalid"}`, 400},
		{"admin", `{"user_scope":"all"} {}`, 400},
		{"admin", `{"user_scope":"mine"}`, 200},
	} {
		r := httptest.NewRequest("PUT", "/api/admin/downloads/settings", strings.NewReader(tc.body))
		r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: 1, Role: tc.role}))
		w := httptest.NewRecorder()
		e.h.ActivitySettings(func() { changes++ })(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
	saved, err := e.settings.Read()
	if err != nil || saved.DownloadsUserScope != "mine" || saved.ExternalURL != "https://media.example" || saved.DiscoverySource != "tmdb_popular" || changes != 1 {
		t.Fatalf("settings were not preserved: %+v, %v; changes=%d", saved, err, changes)
	}
}

func TestActivityLiveReadCoalescingAndPolicyChange(t *testing.T) {
	e := newActivityEnv(t)
	var reads atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/queue":
			reads.Add(1)
			once.Do(func() { close(entered) })
			<-release
			fmt.Fprint(w, `{"totalRecords":1,"records":[{"id":1,"movieId":1,"downloadId":"one","downloadClient":"Client","status":"downloading","size":100,"sizeleft":50}]}`)
		case "/api/v3/movie/1":
			fmt.Fprint(w, `{"id":1,"tmdbId":100,"title":"Movie","certification":"G"}`)
		case "/api/v3/downloadclient":
			json.NewEncoder(w).Encode([]record{definition("sabnzbd", "client.invalid")})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	inst := e.add(t, "radarr", upstream.URL)
	var wg sync.WaitGroup
	results := make(chan sourceSnapshot, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- e.h.activity.snapshot(context.Background(), inst) }()
	}
	<-entered
	close(release)
	wg.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("queue fetched %d times", reads.Load())
	}
	// A cached read still applies the current policy, including a new restriction.
	if err := e.policy.Store.Set(2, contentpolicy.Policy{RatingRegion: "US", MaxMovieRating: "G", MaxTVRating: "TV-Y", BlockedMovieGenres: []int{18}}); err != nil {
		t.Fatal(err)
	}
	a, w := e.get(t, 2, "/api/downloads/activity")
	if w.Code != 200 || a.Count == nil || *a.Count != 1 {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestActivityRejectsAccessChangedDuringRead(t *testing.T) {
	for _, change := range []string{"policy", "grants", "scope", "role"} {
		t.Run(change, func(t *testing.T) {
			e := newActivityEnv(t)
			entered := make(chan struct{})
			release := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				fmt.Fprint(w, `{"totalRecords":0,"records":[]}`)
			}))
			defer upstream.Close()
			inst := e.add(t, "radarr", upstream.URL)
			if err := e.h.store.SetUserGrants(2, map[string][]string{"radarr": {inst.ID}}); err != nil {
				t.Fatal(err)
			}
			finished := make(chan *httptest.ResponseRecorder)
			go func() { _, w := e.get(t, 2, "/api/downloads/activity"); finished <- w }()
			<-entered
			switch change {
			case "policy":
				if err := e.policy.Store.Set(2, contentpolicy.Policy{RatingRegion: "US", MaxMovieRating: "G", MaxTVRating: "TV-Y"}); err != nil {
					t.Fatal(err)
				}
			case "grants":
				other := e.add(t, "radarr", "http://other.invalid")
				if err := e.h.store.SetUserGrants(2, map[string][]string{"radarr": {other.ID}}); err != nil {
					t.Fatal(err)
				}
			case "scope":
				if _, err := e.settings.SetDownloadsUserScope("mine"); err != nil {
					t.Fatal(err)
				}
			case "role":
				if _, err := e.db.Exec("UPDATE users SET role='admin' WHERE id=2"); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			w := <-finished
			if w.Code != 503 {
				t.Fatalf("stale authorization returned %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
