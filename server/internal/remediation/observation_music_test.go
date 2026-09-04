package remediation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// musicFileState drives the fake Lidarr's trackfile + import-history responses
// so tests can flip the exact-file witness between "missing" and "imported".
type musicFileState struct {
	fileID           int // 0 = no file on disk
	importDownloadID string
	importDate       time.Time
}

func setupMusicObservationService(t *testing.T, state *musicFileState) (*Service, *fakeNotifier) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/queue":
			fmt.Fprint(w, `{"totalRecords":0,"records":[]}`)
		case "/api/v1/trackfile":
			if r.URL.Query().Get("albumId") != "123" {
				fmt.Fprint(w, `[]`)
				return
			}
			if state.fileID == 0 {
				fmt.Fprint(w, `[]`)
			} else {
				fmt.Fprintf(w, `[{"id":%d,"albumId":123,"path":"/music/track01.flac"}]`, state.fileID)
			}
		case "/api/v1/album":
			// Library records for report resolution: one album per foreign id —
			// music has no format axis, so no ambiguity states exist.
			fmt.Fprint(w, `[
				{"id":123,"artistId":456,"foreignAlbumId":"fa-1","title":"Example Album"},
				{"id":310,"artistId":456,"foreignAlbumId":"fa-2","title":"Second Album"}
			]`)
		case "/api/v1/history":
			if state.importDownloadID == "" {
				fmt.Fprint(w, `{"page":1,"pageSize":20,"totalRecords":0,"records":[]}`)
			} else {
				// Lidarr emits "trackFileImported" (its event 3) for a completed
				// import.
				fmt.Fprintf(w, `{"page":1,"pageSize":20,"totalRecords":1,"records":[{"id":88,"eventType":"trackFileImported","downloadId":%q,"date":%q,"artistId":456,"albumId":123,"album":{"id":123,"title":"Example Album"}}]}`,
					state.importDownloadID, state.importDate.Format(time.RFC3339))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{0x37}, 32))
	store := instance.NewStore(database, cipher)
	if err := store.Create(&instance.Instance{
		ID: "lidarr-observe", ServiceType: "lidarr", Name: "Music", URL: server.URL, APIKey: "key",
	}); err != nil {
		t.Fatal(err)
	}
	notifier := &fakeNotifier{}
	return NewService(database, instance.NewRegistry(store), nil, notifier), notifier
}

// setupMusicReportService seeds a reporter holding the lidarr assignment that
// IS the music access model.
func setupMusicReportService(t *testing.T, state *musicFileState) (*Service, int64) {
	t.Helper()
	svc, _ := setupMusicObservationService(t, state)
	reporterID := seedUser(t, svc.db, "music-reporter")
	if _, err := svc.db.Exec(
		"INSERT INTO user_default_instances (user_id, service_type, instance_id) VALUES (?, 'lidarr', 'lidarr-observe')",
		reporterID,
	); err != nil {
		t.Fatalf("assign lidarr instance: %v", err)
	}
	return svc, reporterID
}

func observedMusicProblem(downloadID string, queueID int) arr.QueueObservation {
	signal := arr.QueueSignal{
		TrackedDownloadStatus: "error", TrackedDownloadState: "importPending",
		ErrorMessage: "The download is stalled with no connections", Size: 100, SizeLeft: 100,
	}
	return arr.QueueObservation{
		DownloadID: downloadID,
		Media:      arr.QueueMediaContext{QueueID: queueID, Title: "Example Album", AuthorID: 456, BookID: 123},
		Signal:     signal, Diagnosis: arr.Diagnose(signal),
	}
}

// A lidarr snapshot is a first-class observation source: a problem queue row
// creates one music issue carrying the durable Lidarr artist/album ids on the
// generic identity columns.
func TestLidarrSnapshotCreatesMusicIssueWithDurableIdentity(t *testing.T) {
	svc, _ := setupMusicObservationService(t, &musicFileState{})
	enableAutoDispatch(t, svc, 5)

	base := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	if err := svc.observeQueueSnapshot("lidarr", "lidarr-observe", []arr.QueueObservation{observedMusicProblem("download-1", 9)}, base); err != nil {
		t.Fatal(err)
	}
	issues, _, err := svc.ListIssues("", 0)
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues=%+v err=%v", issues, err)
	}
	issue, err := svc.GetIssue(issues[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.MediaType != "music" || issue.AuthorID != 456 || issue.BookID != 123 ||
		issue.ArrQueueID != 9 || issue.DownloadID != "download-1" || issue.Status != IssueObserving {
		t.Fatalf("music issue identity = %+v", issue)
	}

	// The same incident on the next poll reconciles instead of duplicating.
	if err := svc.observeQueueSnapshot("lidarr", "lidarr-observe", []arr.QueueObservation{observedMusicProblem("download-1", 9)}, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if again, _, err := svc.ListIssues("", 0); err != nil || len(again) != 1 {
		t.Fatalf("post-reconcile issues=%+v err=%v", again, err)
	}
}

func TestCreateUserIssueMusicResolvesDurableIdentity(t *testing.T) {
	svc, reporterID := setupMusicReportService(t, &musicFileState{})

	created, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: "lidarr-observe", MediaType: "music", ForeignID: "fa-1",
		Category: CategoryBadCopy, Reason: "Track two is silent.",
	})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := svc.GetIssue(created.IssueID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.MediaType != "music" || issue.AuthorID != 456 || issue.BookID != 123 ||
		issue.TmdbID != 0 || issue.Title != "Example Album" {
		t.Fatalf("music report identity = %+v", issue)
	}

	duplicate, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: "lidarr-observe", MediaType: "music", ForeignID: "fa-1",
		Category: CategoryBadCopy, Reason: "Still silent.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.IssueID != created.IssueID {
		t.Fatalf("same album report did not dedupe: first=%d duplicate=%d", created.IssueID, duplicate.IssueID)
	}
}

func TestCreateUserIssueMusicFailsClosedOnUnresolvableReport(t *testing.T) {
	svc, reporterID := setupMusicReportService(t, &musicFileState{})

	if _, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: "lidarr-observe", MediaType: "music", ForeignID: "fa-unknown",
		Category: CategoryBadCopy,
	}); err == nil || !strings.Contains(err.Error(), "no library album matches") {
		t.Fatalf("unresolvable album report error = %v", err)
	}
	// A music report never takes a book format.
	if _, err := svc.CreateUserIssue(reporterID, &CreateIssueRequest{
		InstanceID: "lidarr-observe", MediaType: "music", ForeignID: "fa-1",
		BookFormat: "ebook", Category: CategoryBadCopy,
	}); err == nil || !strings.Contains(err.Error(), "no book_format") {
		t.Fatalf("book_format on music error = %v", err)
	}
}

// Music action params validate on the artist/album vocabulary and bind to the
// issue's generic identity columns exactly as books do.
func TestMusicIssueWithDurableIdentityScopesTitleActions(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	res, err := database.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title, instance_id, download_id, arr_queue_id, author_id, book_id)
		 VALUES ('auto', 'open', 'music', 0, 'Album', 'lidarr-1', 'download-1', 77, 456, 123)`,
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	for _, tc := range []struct {
		kind   ActionKind
		params string
		ok     bool
	}{
		{ActionTriggerSearch, `{"media_type":"music","album_id":123}`, true},
		{ActionTriggerSearch, `{"media_type":"music","artist_id":456}`, true},
		{ActionTriggerSearch, `{"media_type":"music","album_id":999}`, false},
		{ActionTriggerSearch, `{"media_type":"music","artist_id":999}`, false},
		{ActionRescan, `{"media_type":"music","artist_id":456}`, true},
		{ActionRescan, `{"media_type":"music","artist_id":999}`, false},
		{ActionDeleteMediaFiles, `{"media_type":"music","album_id":123,"blocklist":true}`, true},
		{ActionDeleteMediaFiles, `{"media_type":"music","album_id":999,"blocklist":true}`, false},
		{ActionGrabRelease, `{"media_type":"music","guid":"g","indexer_id":1,"queue_id_to_replace":77,"release_title":"Album.Release","size":1,"protocol":"usenet","indexer":"Test"}`, true},
	} {
		canonical, err := validateActionParams(tc.kind, json.RawMessage(tc.params))
		if err != nil {
			t.Fatalf("validate %s params (%s): %v", tc.kind, tc.params, err)
		}
		err = validateActionScopeWith(database, issueID, tc.kind, canonical)
		if tc.ok && err != nil {
			t.Errorf("exact-scope %s rejected: %v (params %s)", tc.kind, err, tc.params)
		}
		if !tc.ok && err == nil {
			t.Errorf("out-of-scope %s accepted: %s", tc.kind, tc.params)
		}
	}
}

// The music param vocabulary is enforced both ways: music refuses book/tmdb
// keys, and the video/book arms refuse the music keys.
func TestMusicActionParamVocabulary(t *testing.T) {
	rejects := []struct {
		kind    ActionKind
		params  string
		message string
	}{
		{ActionTriggerSearch, `{"media_type":"music","tmdb_id":42}`, "must not set tmdb_id"},
		{ActionTriggerSearch, `{"media_type":"music","book_id":123}`, "apply only to media_type book"},
		{ActionTriggerSearch, `{"media_type":"music"}`, "requires a positive artist_id or album_id"},
		{ActionTriggerSearch, `{"media_type":"movie","tmdb_id":42,"album_id":1}`, "apply only to media_type music"},
		{ActionTriggerSearch, `{"media_type":"book","book_id":9,"artist_id":1}`, "apply only to media_type music"},
		{ActionDeleteMediaFiles, `{"media_type":"music"}`, "requires the issue's album_id"},
		{ActionDeleteMediaFiles, `{"media_type":"music","album_id":1,"tmdb_id":5}`, "take only album_id and blocklist"},
		{ActionDeleteMediaFiles, `{"media_type":"movie","tmdb_id":5,"album_id":1}`, "applies only to media_type music"},
		{ActionRescan, `{"media_type":"music"}`, "requires a positive artist_id"},
		{ActionRescan, `{"media_type":"music","tmdb_id":5,"artist_id":1}`, "must not set tmdb_id"},
		{ActionRescan, `{"media_type":"movie","tmdb_id":5,"artist_id":1}`, "applies only to media_type music"},
	}
	for _, tc := range rejects {
		if _, err := validateActionParams(tc.kind, json.RawMessage(tc.params)); err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("%s %s error = %v, want %q", tc.kind, tc.params, err, tc.message)
		}
	}
}
