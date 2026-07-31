package request

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeRadarr simulates the Radarr endpoints the movie request flow touches and
// records what was written, mirroring fakeSonarr in service_monitor_test.go.
type fakeRadarr struct {
	libraryJSON string // GET /api/v3/movie?tmdbId= body; "" means empty library
	lookupJSON  string // GET /api/v3/movie/lookup body
	queueJSON   string // records array for GET /api/v3/queue; "" means empty

	addBody    map[string]any
	editorBody map[string]any
	commands   []map[string]any
}

func (f *fakeRadarr) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie":
			body := f.libraryJSON
			if body == "" {
				body = "[]"
			}
			_, _ = w.Write([]byte(body))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/lookup":
			_, _ = w.Write([]byte(f.lookupJSON))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Any"},{"id":7,"name":"HD-1080p"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/movies"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/movie":
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &f.addBody)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/movie/editor":
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &f.editorBody)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			var cmd map[string]any
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &cmd)
			f.commands = append(f.commands, cmd)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/queue":
			records := f.queueJSON
			if records == "" {
				records = "[]"
			}
			_, _ = w.Write([]byte(`{"records":` + records + `}`))
		default:
			t.Errorf("unexpected radarr request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newFakeRadarrServer(t *testing.T, f *fakeRadarr) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return srv
}

// TestCreateMovieRequestAddsToRadarr covers the auto-approve movie path end to
// end: the add payload must carry the canonical lookup title/year, the
// effective per-user quality profile (an admin-set default applies even though
// the user may not choose quality — the request's own choice is ignored), the
// first root folder, and searchForMovie so the grab starts immediately.
func TestCreateMovieRequestAddsToRadarr(t *testing.T) {
	f := &fakeRadarr{
		lookupJSON: `[{"title":"Fight Club","tmdbId":550,"year":1999}]`,
	}
	srv := newFakeRadarrServer(t, f)

	s, uid := newHistoryTestService(t, srv.URL, "", "")
	profile := 7
	if err := s.SetUserSettings(uid, UserSettingsDTO{QualityProfileRadarr: &profile}); err != nil {
		t.Fatalf("SetUserSettings: %v", err)
	}

	resp, err := s.CreateMediaRequest(uid, &CreateRequest{
		TmdbID:    550,
		MediaType: "movie",
		Title:     "fight club (client title)",
		// Quality choice is not allowed out of the box, so this must be ignored
		// in favor of the admin-set per-user default (7).
		QualityProfileID: 1,
	})
	if err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if !resp.Success || resp.Status != StatusRequested {
		t.Fatalf("response = %+v, want success + requested", resp)
	}
	if resp.Title != "Fight Club" {
		t.Fatalf("title = %q, want the canonical lookup title", resp.Title)
	}

	if f.addBody == nil {
		t.Fatal("AddMovie was not called")
	}
	if f.addBody["title"] != "Fight Club" || f.addBody["tmdbId"] != float64(550) || f.addBody["year"] != float64(1999) {
		t.Errorf("add identity = %v/%v/%v, want Fight Club/550/1999",
			f.addBody["title"], f.addBody["tmdbId"], f.addBody["year"])
	}
	if got := f.addBody["qualityProfileId"]; got != float64(7) {
		t.Errorf("qualityProfileId = %v, want 7 (admin-set per-user default)", got)
	}
	if got := f.addBody["rootFolderPath"]; got != "/movies" {
		t.Errorf("rootFolderPath = %v, want /movies", got)
	}
	if f.addBody["monitored"] != true {
		t.Errorf("monitored = %v, want true", f.addBody["monitored"])
	}
	addOptions, _ := f.addBody["addOptions"].(map[string]any)
	if addOptions["searchForMovie"] != true {
		t.Errorf("addOptions = %#v, want searchForMovie true", f.addBody["addOptions"])
	}
	if len(f.commands) != 0 {
		t.Errorf("commands = %v, want none (search rides on addOptions)", f.commands)
	}

	// The fulfilled request is logged with the canonical title + status.
	var title, status string
	if err := s.db.QueryRow(
		"SELECT title, status FROM request_log WHERE user_id = ? AND tmdb_id = 550 AND media_type = 'movie'", uid,
	).Scan(&title, &status); err != nil {
		t.Fatalf("read request_log: %v", err)
	}
	if title != "Fight Club" || status != StatusRequested {
		t.Errorf("logged row = %s/%s, want Fight Club/requested", title, status)
	}
}

// TestCreateMovieRequestUnknownProfileFallsBack pins the profile guard: an
// effective profile id Radarr doesn't actually have must fall back to Radarr's
// first profile rather than sending a broken add.
func TestCreateMovieRequestUnknownProfileFallsBack(t *testing.T) {
	f := &fakeRadarr{
		lookupJSON: `[{"title":"Fight Club","tmdbId":550,"year":1999}]`,
	}
	srv := newFakeRadarrServer(t, f)

	s, uid := newHistoryTestService(t, srv.URL, "", "")
	profile := 99 // not in the fake's [1, 7]
	if err := s.SetUserSettings(uid, UserSettingsDTO{QualityProfileRadarr: &profile}); err != nil {
		t.Fatalf("SetUserSettings: %v", err)
	}

	if _, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Fight Club"}); err != nil {
		t.Fatalf("CreateMediaRequest: %v", err)
	}
	if got := f.addBody["qualityProfileId"]; got != float64(1) {
		t.Errorf("qualityProfileId = %v, want 1 (first profile fallback)", got)
	}
}

// TestCreateMovieRequestExistingMovie covers the three in-library outcomes: a
// movie with a file is simply available, a monitored movie without a file is
// already being worked on (no writes), and an unmonitored one must be revived
// (monitor + search) instead of reporting "requested" while nothing would ever
// happen.
func TestCreateMovieRequestExistingMovie(t *testing.T) {
	t.Run("has file reads available with no writes", func(t *testing.T) {
		f := &fakeRadarr{
			libraryJSON: `[{"id":12,"tmdbId":550,"title":"Fight Club","hasFile":true,"monitored":true}]`,
		}
		srv := newFakeRadarrServer(t, f)
		s, uid := newHistoryTestService(t, srv.URL, "", "")

		resp, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Fight Club"})
		if err != nil {
			t.Fatalf("CreateMediaRequest: %v", err)
		}
		if resp.Status != StatusAvailable || resp.Title != "Fight Club" {
			t.Errorf("response = %+v, want available/Fight Club", resp)
		}
		if f.addBody != nil || f.editorBody != nil || len(f.commands) != 0 {
			t.Errorf("unexpected writes: add=%v editor=%v commands=%v", f.addBody, f.editorBody, f.commands)
		}
	})

	t.Run("monitored without file reads requested with no writes", func(t *testing.T) {
		f := &fakeRadarr{
			libraryJSON: `[{"id":12,"tmdbId":550,"title":"Fight Club","hasFile":false,"monitored":true}]`,
		}
		srv := newFakeRadarrServer(t, f)
		s, uid := newHistoryTestService(t, srv.URL, "", "")

		resp, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Fight Club"})
		if err != nil {
			t.Fatalf("CreateMediaRequest: %v", err)
		}
		if resp.Status != StatusRequested {
			t.Errorf("status = %s, want requested", resp.Status)
		}
		if f.editorBody != nil || len(f.commands) != 0 {
			t.Errorf("unexpected writes for already-monitored movie: editor=%v commands=%v", f.editorBody, f.commands)
		}
	})

	t.Run("unmonitored is revived with monitor and search", func(t *testing.T) {
		f := &fakeRadarr{
			libraryJSON: `[{"id":12,"tmdbId":550,"title":"Fight Club","hasFile":false,"monitored":false}]`,
		}
		srv := newFakeRadarrServer(t, f)
		s, uid := newHistoryTestService(t, srv.URL, "", "")

		resp, err := s.CreateMediaRequest(uid, &CreateRequest{TmdbID: 550, MediaType: "movie", Title: "Fight Club"})
		if err != nil {
			t.Fatalf("CreateMediaRequest: %v", err)
		}
		if resp.Status != StatusRequested {
			t.Errorf("status = %s, want requested", resp.Status)
		}
		if f.addBody != nil {
			t.Errorf("AddMovie called for an in-library movie: %v", f.addBody)
		}
		ids, _ := f.editorBody["movieIds"].([]any)
		if f.editorBody["monitored"] != true || len(ids) != 1 || ids[0] != float64(12) {
			t.Errorf("editor body = %#v, want movieIds [12] monitored true", f.editorBody)
		}
		if len(f.commands) != 1 || f.commands[0]["name"] != "MoviesSearch" {
			t.Fatalf("commands = %v, want exactly one MoviesSearch", f.commands)
		}
		cmdIDs, _ := f.commands[0]["movieIds"].([]any)
		if len(cmdIDs) != 1 || cmdIDs[0] != float64(12) {
			t.Errorf("MoviesSearch movieIds = %v, want [12]", f.commands[0]["movieIds"])
		}
	})
}

// TestGetMovieStatusMatrix covers the availability computation for movies:
// file on disk -> available, queued download -> downloading with byte-derived
// progress, monitored without a file -> requested, and unmonitored or absent
// -> unavailable.
func TestGetMovieStatusMatrix(t *testing.T) {
	cases := []struct {
		name         string
		fake         *fakeRadarr
		wantStatus   string
		wantProgress float64
	}{
		{
			name:       "not in radarr",
			fake:       &fakeRadarr{},
			wantStatus: StatusUnavailable,
		},
		{
			name: "file on disk",
			fake: &fakeRadarr{
				libraryJSON: `[{"id":12,"tmdbId":550,"title":"Fight Club","hasFile":true,"monitored":true}]`,
			},
			wantStatus:   StatusAvailable,
			wantProgress: 1.0,
		},
		{
			name: "in download queue",
			fake: &fakeRadarr{
				libraryJSON: `[{"id":12,"tmdbId":550,"title":"Fight Club","hasFile":false,"monitored":true}]`,
				queueJSON:   `[{"movieId":12,"title":"Fight Club","status":"downloading","size":2000,"sizeleft":500}]`,
			},
			wantStatus:   StatusDownloading,
			wantProgress: 0.75,
		},
		{
			name: "monitored without file",
			fake: &fakeRadarr{
				libraryJSON: `[{"id":12,"tmdbId":550,"title":"Fight Club","hasFile":false,"monitored":true}]`,
			},
			wantStatus: StatusRequested,
		},
		{
			name: "unmonitored without file",
			fake: &fakeRadarr{
				libraryJSON: `[{"id":12,"tmdbId":550,"title":"Fight Club","hasFile":false,"monitored":false}]`,
			},
			wantStatus: StatusUnavailable,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newFakeRadarrServer(t, c.fake)
			s, uid := newHistoryTestService(t, srv.URL, "", "")
			st, err := s.getMovieStatus(uid, 550)
			if err != nil {
				t.Fatalf("getMovieStatus: %v", err)
			}
			if st.Status != c.wantStatus {
				t.Errorf("status = %s, want %s", st.Status, c.wantStatus)
			}
			if st.Progress != c.wantProgress {
				t.Errorf("progress = %v, want %v", st.Progress, c.wantProgress)
			}
		})
	}

	t.Run("no radarr source", func(t *testing.T) {
		s, uid := newHistoryTestService(t, "", "", "")
		st, err := s.getMovieStatus(uid, 550)
		if err != nil || st.Status != StatusUnavailable {
			t.Errorf("status = %+v err=%v, want unavailable", st, err)
		}
	})
}
