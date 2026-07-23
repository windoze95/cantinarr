package request

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// newHistoryTestService builds a Service whose user has Radarr, Sonarr and
// Chaptarr instances pointed at the given fake servers (any empty URL skips
// that service), so GetRequests' live-status overlay resolves real digests.
func newHistoryTestService(t *testing.T, radarrURL, sonarrURL, chaptarrURL string) (*Service, int64) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	res, err := database.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ('requester', '', 'user')",
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
	for serviceType, url := range map[string]string{
		"radarr": radarrURL, "sonarr": sonarrURL, "chaptarr": chaptarrURL,
	} {
		if url == "" {
			continue
		}
		inst := &instance.Instance{ServiceType: serviceType, Name: serviceType, URL: url, APIKey: "key"}
		if err := store.Create(inst); err != nil {
			t.Fatalf("create %s instance: %v", serviceType, err)
		}
		if err := store.SetUserDefault(uid, serviceType, inst.ID); err != nil {
			t.Fatalf("grant %s: %v", serviceType, err)
		}
	}

	return NewService(database, instance.NewRegistry(store), nil, nil), uid
}

func jsonServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func statusOf(t *testing.T, requests []RequestLog, title string) string {
	t.Helper()
	for _, r := range requests {
		if r.Title == title {
			return r.Status
		}
	}
	t.Fatalf("no history row titled %q in %+v", title, requests)
	return ""
}

// TestGetRequestsOverlaysLiveStatus covers the history overlay: stored
// statuses are recomputed from the live libraries (a fulfilled request reads
// available, a title deleted from the arr reads unavailable), while pending
// stays pending and denied only upgrades when the title has since landed.
func TestGetRequestsOverlaysLiveStatus(t *testing.T) {
	radarrSrv := jsonServer(t, map[string]string{
		"/api/v3/movie": `[
			{"id":1,"tmdbId":100,"title":"Imported","hasFile":true,"monitored":true},
			{"id":2,"tmdbId":101,"title":"Still Missing","hasFile":false,"monitored":true},
			{"id":3,"tmdbId":103,"title":"Landed Anyway","hasFile":true,"monitored":true}
		]`,
	})
	// Series 500: 2 of 9 episodes on disk across seasons -> partial, even
	// though its only monitored season is complete (the percentOfEpisodes trap).
	sonarrSrv := jsonServer(t, map[string]string{
		"/api/v3/series": `[
			{"id":7,"tvdbId":500,"title":"Gappy Show","monitored":true,"seasons":[
				{"seasonNumber":1,"monitored":false,"statistics":{"episodeFileCount":0,"episodeCount":0,"totalEpisodeCount":7}},
				{"seasonNumber":2,"monitored":true,"statistics":{"episodeFileCount":2,"episodeCount":2,"totalEpisodeCount":2}}
			],"statistics":{"episodeFileCount":2,"episodeCount":2,"totalEpisodeCount":9,"percentOfEpisodes":100}}
		]`,
	})

	s, uid := newHistoryTestService(t, radarrSrv.URL, sonarrSrv.URL, "")

	seed := []struct {
		title  string
		tmdbID int
		tvdbID int
		media  string
		status string
	}{
		{"Imported", 100, 0, "movie", StatusRequested},        // file landed -> available
		{"Still Missing", 101, 0, "movie", StatusRequested},   // monitored, no file -> requested
		{"Deleted In Arr", 102, 0, "movie", StatusAvailable},  // gone from library -> unavailable
		{"Landed Anyway", 103, 0, "movie", StatusDenied},      // denied but present -> available
		{"Denied Absent", 104, 0, "movie", StatusDenied},      // denied and absent -> stays denied
		{"Awaiting Approval", 105, 0, "movie", StatusPending}, // pending always stays
		{"Gappy Show", 200, 500, "tv", StatusRequested},       // 2/9 episodes -> partial
	}
	for _, row := range seed {
		r := &resolvedRequest{userID: uid, tmdbID: row.tmdbID, tvdbID: row.tvdbID, mediaType: row.media}
		if _, err := s.insertRequest(r, row.title, row.status); err != nil {
			t.Fatalf("seed %s: %v", row.title, err)
		}
	}

	requests, err := s.GetRequests(uid)
	if err != nil {
		t.Fatalf("GetRequests: %v", err)
	}
	want := map[string]string{
		"Imported":          StatusAvailable,
		"Still Missing":     StatusRequested,
		"Deleted In Arr":    StatusUnavailable,
		"Landed Anyway":     StatusAvailable,
		"Denied Absent":     StatusDenied,
		"Awaiting Approval": StatusPending,
		"Gappy Show":        StatusPartial,
	}
	for title, wantStatus := range want {
		if got := statusOf(t, requests, title); got != wantStatus {
			t.Errorf("%s = %s, want %s", title, got, wantStatus)
		}
	}
}

// TestGetRequestsKeepsStoredStatusWhenArrUnreachable pins the fail-open rule:
// when the user's arr source can't be resolved, history rows keep their stored
// statuses instead of all collapsing to unavailable.
func TestGetRequestsKeepsStoredStatusWhenArrUnreachable(t *testing.T) {
	s, uid := newHistoryTestService(t, "", "", "") // no instances at all
	r := &resolvedRequest{userID: uid, tmdbID: 100, mediaType: "movie"}
	if _, err := s.insertRequest(r, "Frozen", StatusRequested); err != nil {
		t.Fatalf("seed: %v", err)
	}
	requests, err := s.GetRequests(uid)
	if err != nil {
		t.Fatalf("GetRequests: %v", err)
	}
	if got := statusOf(t, requests, "Frozen"); got != StatusRequested {
		t.Errorf("status without arr source = %s, want stored requested", got)
	}
}

// TestGetRequestsBookRowsUsePinnedLiveProjection covers book history rows: a
// requested format whose file landed reads available, one of two reads partial,
// and a canonical title absent from its pinned instance reads unavailable.
func TestGetRequestsBookRowsUsePinnedLiveProjection(t *testing.T) {
	chaptarrSrv := jsonServer(t, map[string]string{
		"/api/v1/book": `[
			{"id":1,"title":"Read Me","foreignBookId":"book-1","monitored":true,"mediaType":"ebook",
			 "author":{"authorName":"A"},"statistics":{"bookFileCount":1}},
			{"id":2,"title":"Half Here","foreignBookId":"book-2","monitored":true,"mediaType":"ebook",
			 "author":{"authorName":"B"},"statistics":{"bookFileCount":1}},
			{"id":3,"title":"Half Here","foreignBookId":"book-2","monitored":true,"mediaType":"audiobook",
			 "author":{"authorName":"B"},"statistics":{"bookFileCount":0}},
			{"id":4,"title":"On the Way","foreignBookId":"book-3","monitored":true,"mediaType":"audiobook",
			 "author":{"authorName":"C"},"statistics":{"bookFileCount":0}},
			{"id":5,"title":"Being Watched","foreignBookId":"book-4","monitored":true,"grabbed":true,"mediaType":"ebook",
			 "author":{"authorName":"D"},"statistics":{"bookFileCount":0}},
			{"id":6,"title":"Unknown Format","foreignBookId":"book-5","monitored":true,"mediaType":"paperback",
			 "author":{"authorName":"E"},"statistics":{"bookFileCount":0}}
		]`,
		"/api/v1/queue":   `{"totalRecords":1,"records":[{"bookId":4,"status":"downloading","trackedDownloadStatus":"ok"}]}`,
		"/api/v1/command": `[]`,
	})
	s, uid := newHistoryTestService(t, "", "", chaptarrSrv.URL)
	_, instanceID, err := s.resolveChaptarr(uid, "")
	if err != nil || instanceID == "" {
		t.Fatalf("resolve Chaptarr: id=%q err=%v", instanceID, err)
	}

	seed := []struct {
		title     string
		foreignID string
		format    string
		status    string
	}{
		{"Read Me", "book-1", BookFormatEbook, StatusRequested},        // downloaded -> available
		{"Half Here", "book-2", "both", StatusRequested},               // one of two -> partial
		{"On the Way", "book-3", BookFormatAudiobook, StatusRequested}, // healthy queue -> downloading
		{"Being Watched", "book-4", BookFormatEbook, StatusAvailable},  // monitored, no file -> requested
		{"Unknown Format", "book-5", BookFormatEbook, StatusRequested}, // physical does not satisfy ebook
		{"Not Matched", "book-9", BookFormatEbook, StatusRequested},    // absent -> unavailable
	}
	for _, row := range seed {
		r := &resolvedRequest{userID: uid, mediaType: "book", foreignID: row.foreignID, bookFormat: row.format, instanceID: instanceID}
		if _, err := s.insertRequest(r, row.title, row.status); err != nil {
			t.Fatalf("seed %s: %v", row.title, err)
		}
	}
	legacy := &resolvedRequest{userID: uid, mediaType: "book", foreignID: "book-1", bookFormat: BookFormatEbook}
	if _, err := s.insertRequest(legacy, "Legacy Unscoped", StatusRequested); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	requests, err := s.GetRequests(uid)
	if err != nil {
		t.Fatalf("GetRequests: %v", err)
	}
	want := map[string]string{
		"Read Me":         StatusAvailable,
		"Half Here":       StatusPartial,
		"On the Way":      StatusDownloading,
		"Being Watched":   StatusRequested,
		"Unknown Format":  StatusUnavailable,
		"Not Matched":     StatusUnavailable,
		"Legacy Unscoped": StatusRequested,
	}
	for title, wantStatus := range want {
		if got := statusOf(t, requests, title); got != wantStatus {
			t.Errorf("%s = %s, want %s", title, got, wantStatus)
		}
	}
	for _, request := range requests {
		if request.Title == "Read Me" && !request.StatusKnown {
			t.Fatalf("resolved history = %+v, want status_known true", request)
		}
	}
}

// TestGetUserBookStatusOverlaysOwnership covers the book status endpoint's
// live overlay: a requested format whose file landed reads available (and the
// collapsed status follows), a pending request keeps showing pending, and a
// book with no matching library record keeps its stored state.
func TestGetUserBookStatusOverlaysOwnership(t *testing.T) {
	chaptarrSrv := jsonServer(t, map[string]string{
		"/api/v1/book": `[
			{"id":1,"title":"Read Me","foreignBookId":"book-1","monitored":true,"mediaType":"ebook",
			 "author":{"authorName":"A"},"statistics":{"bookFileCount":1}},
			{"id":2,"title":"Half Here","foreignBookId":"book-2","monitored":true,"mediaType":"ebook",
			 "author":{"authorName":"B"},"statistics":{"bookFileCount":1}},
			{"id":3,"title":"Half Here","foreignBookId":"book-2","monitored":true,"mediaType":"audiobook",
			 "author":{"authorName":"B"},"statistics":{"bookFileCount":0}},
			{"id":4,"title":"Pending But Here","foreignBookId":"book-3","monitored":true,"mediaType":"ebook",
			 "author":{"authorName":"C"},"statistics":{"bookFileCount":1}}
		]`,
		"/api/v1/queue":   `{"totalRecords":0,"records":[]}`,
		"/api/v1/command": `[]`,
	})
	s, uid := newHistoryTestService(t, "", "", chaptarrSrv.URL)

	seed := []struct {
		foreignID string
		format    string
		status    string
	}{
		{"book-1", BookFormatEbook, StatusRequested},
		{"book-2", "both", StatusRequested},
		{"book-3", BookFormatEbook, StatusPending},
		{"book-9", BookFormatEbook, StatusRequested},
	}
	for _, row := range seed {
		r := &resolvedRequest{userID: uid, mediaType: "book", foreignID: row.foreignID, bookFormat: row.format}
		if _, err := s.insertRequest(r, row.foreignID, row.status); err != nil {
			t.Fatalf("seed %s: %v", row.foreignID, err)
		}
	}

	cases := []struct {
		foreignID  string
		wantStatus string
		wantEbook  string
	}{
		{"book-1", StatusAvailable, StatusAvailable},
		{"book-2", StatusPartial, StatusAvailable},
		{"book-3", StatusAvailable, StatusAvailable}, // live truth outranks stale approval state
		{"book-9", StatusUnavailable, StatusUnavailable},
	}
	for _, c := range cases {
		st, err := s.GetUserBookStatus(uid, c.foreignID)
		if err != nil {
			t.Fatalf("GetUserBookStatus(%s): %v", c.foreignID, err)
		}
		if st.Status != c.wantStatus {
			t.Errorf("%s status = %s, want %s", c.foreignID, st.Status, c.wantStatus)
		}
		if got := st.BookFormats[BookFormatEbook]; got != c.wantEbook {
			t.Errorf("%s ebook format = %s, want %s", c.foreignID, got, c.wantEbook)
		}
	}
}
