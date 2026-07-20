package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	requestsvc "github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// This file drives whole tool bodies through ExecuteTool with realistic
// argument payloads and asserts the CAPTURED downstream effect: the exact
// upstream arr request (method/path/body) via httptest fakes. RBAC and the
// authorization matrix are covered in tools_test.go; instance binding for
// search/grab is covered in release_capability_test.go. Here the risk under
// test is argument mapping: an authorized call must mutate exactly the target
// its arguments name, and nothing else.

// upstreamCall is one recorded request an arr fake received.
type upstreamCall struct {
	Method string
	URI    string // path + raw query
	Body   string
}

// callRecorder captures upstream requests race-safely (the httptest handler
// runs on a different goroutine than the assertions).
type callRecorder struct {
	mu    sync.Mutex
	calls []upstreamCall
}

func (r *callRecorder) record(req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, upstreamCall{Method: req.Method, URI: req.URL.RequestURI(), Body: string(body)})
}

func (r *callRecorder) all() []upstreamCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]upstreamCall(nil), r.calls...)
}

// mutations returns only the write-shaped calls (POST/PUT/DELETE), in order.
func (r *callRecorder) mutations() []upstreamCall {
	var out []upstreamCall
	for _, call := range r.all() {
		if call.Method != http.MethodGet {
			out = append(out, call)
		}
	}
	return out
}

func decodeBody(t *testing.T, body string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode upstream body %q: %v", body, err)
	}
	return decoded
}

// newDefaultInstanceToolServer builds a ToolServer whose registry has one
// default instance per given service type, mirroring release_capability_test.
func newDefaultInstanceToolServer(t *testing.T, urls map[string]string) *ToolServer {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x37}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	for serviceType, url := range urls {
		inst := &instance.Instance{ServiceType: serviceType, Name: serviceType, URL: url, APIKey: "key", IsDefault: true}
		if err := store.Create(inst); err != nil {
			t.Fatalf("create %s instance: %v", serviceType, err)
		}
	}
	return NewToolServer(nil, nil, instance.NewRegistry(store), nil)
}

func adminCallContext() CallContext {
	return CallContext{Role: auth.RoleAdmin, TrustedInternal: true}
}

// assertRejectedWith asserts a tool call failed with the given preflight
// message. ExecuteTool deliberately flattens error types through
// secrets.RedactError at its trust boundary (typed MutationNotStarted /
// PartialMutation classification is consumed by the remediation executor,
// which invokes the shared helpers directly — see arr_mutations_test.go), so
// at this boundary the message plus the recorder's proof of zero upstream
// traffic is the pre-dispatch evidence.
func assertRejectedWith(t *testing.T, err error, wantSubstring string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a preflight rejection, got success")
	}
	if !strings.Contains(err.Error(), wantSubstring) {
		t.Fatalf("rejection = %v, want message containing %q", err, wantSubstring)
	}
}

// --- request_media ---

// TestRequestMediaRoutesToCallersInstanceAndLogsAttribution proves the highest
// blast-radius requester tool maps its arguments to the exact Radarr add the
// CALLING user's granted instance receives, and attributes the request row to
// that user — while the global default instance sees no traffic at all.
func TestRequestMediaRoutesToCallersInstanceAndLogsAttribution(t *testing.T) {
	personal := &callRecorder{}
	personalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/lookup":
			_, _ = w.Write([]byte(`[{"title":"Fight Club","tmdbId":550,"year":1999}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Any"},{"id":7,"name":"HD-1080p"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/rootfolder":
			_, _ = w.Write([]byte(`[{"id":1,"path":"/movies"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/movie":
			personal.record(r)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected personal radarr request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer personalServer.Close()

	shared := &callRecorder{}
	sharedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shared.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer sharedServer.Close()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	res, err := database.Exec("INSERT INTO users (username, password_hash, role) VALUES ('requester', '', 'user')")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	uid, _ := res.LastInsertId()

	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x21}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	sharedInst := &instance.Instance{ServiceType: "radarr", Name: "Shared", URL: sharedServer.URL, APIKey: "shared", IsDefault: true}
	personalInst := &instance.Instance{ServiceType: "radarr", Name: "Personal", URL: personalServer.URL, APIKey: "personal"}
	for _, inst := range []*instance.Instance{sharedInst, personalInst} {
		if err := store.Create(inst); err != nil {
			t.Fatalf("create instance %s: %v", inst.Name, err)
		}
	}
	if err := store.SetUserDefault(uid, "radarr", personalInst.ID); err != nil {
		t.Fatalf("grant personal instance: %v", err)
	}

	registry := instance.NewRegistry(store)
	service := requestsvc.NewService(database, registry, nil, nil)
	allowQualityChoice := true
	if err := service.SetUserSettings(uid, requestsvc.UserSettingsDTO{AllowQualityChoice: &allowQualityChoice}); err != nil {
		t.Fatalf("allow quality choice: %v", err)
	}
	server := NewToolServer(nil, service, registry, nil)
	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) {
		return auth.RoleUser, nil
	})
	callCtx := CallContext{UserID: uid, Role: auth.RoleUser, DeviceID: "device-1", Reauthorize: true}

	options, err := server.ExecuteTool(
		context.Background(),
		"get_request_options",
		json.RawMessage(`{"media_type":"movie"}`),
		callCtx,
	)
	if err != nil {
		t.Fatalf("get_request_options: %v", err)
	}
	if !strings.Contains(options.Text, `"can_choose_quality":true`) ||
		!strings.Contains(options.Text, `{"id":7,"name":"HD-1080p"}`) {
		t.Fatalf("request options = %q, want allowed profile 7", options.Text)
	}

	result, err := server.ExecuteTool(
		context.Background(),
		"request_media",
		json.RawMessage(`{"tmdb_id":550,"media_type":"movie","quality_profile_id":7}`),
		callCtx,
	)
	if err != nil {
		t.Fatalf("request_media: %v", err)
	}
	if !strings.Contains(result.Text, `"status":"requested"`) || !strings.Contains(result.Text, "Fight Club") {
		t.Fatalf("request result = %q, want requested status with canonical title", result.Text)
	}

	adds := personal.all()
	if len(adds) != 1 {
		t.Fatalf("personal instance adds = %+v, want exactly one", adds)
	}
	if adds[0].Method != http.MethodPost || adds[0].URI != "/api/v3/movie" {
		t.Fatalf("add call = %s %s, want POST /api/v3/movie", adds[0].Method, adds[0].URI)
	}
	body := decodeBody(t, adds[0].Body)
	if body["tmdbId"] != float64(550) || body["title"] != "Fight Club" || body["year"] != float64(1999) {
		t.Errorf("add identity = %v/%v/%v, want 550/Fight Club/1999", body["tmdbId"], body["title"], body["year"])
	}
	if body["monitored"] != true {
		t.Errorf("monitored = %v, want true", body["monitored"])
	}
	if body["qualityProfileId"] != float64(7) {
		t.Errorf("qualityProfileId = %v, want requested profile 7", body["qualityProfileId"])
	}
	addOptions, _ := body["addOptions"].(map[string]any)
	if addOptions["searchForMovie"] != true {
		t.Errorf("addOptions = %#v, want searchForMovie true", body["addOptions"])
	}
	if got := shared.all(); len(got) != 0 {
		t.Fatalf("global default instance received %d request(s): %+v", len(got), got)
	}

	var loggedUser int64
	var status string
	if err := database.QueryRow(
		"SELECT user_id, status FROM request_log WHERE tmdb_id = 550 AND media_type = 'movie'",
	).Scan(&loggedUser, &status); err != nil {
		t.Fatalf("read request_log: %v", err)
	}
	if loggedUser != uid || status != "requested" {
		t.Errorf("request_log row = user %d status %s, want user %d requested", loggedUser, status, uid)
	}

	// Wrong-argument rejection: an unknown media type is refused before any
	// upstream traffic, as a benign tool result the model can correct.
	result, err = server.ExecuteTool(
		context.Background(),
		"request_media",
		json.RawMessage(`{"tmdb_id":550,"media_type":"album"}`),
		callCtx,
	)
	if err != nil {
		t.Fatalf("request_media wrong media_type: %v", err)
	}
	if !strings.HasPrefix(result.Text, "Request failed:") || !strings.Contains(result.Text, "unsupported media type") {
		t.Fatalf("wrong media_type result = %q", result.Text)
	}
	if got := personal.all(); len(got) != 1 {
		t.Fatalf("rejected request still reached the arr: %+v", got)
	}
}

// --- instance scoping (extends release_capability_test's binding coverage) ---

// TestArrToolsRefuseUnknownInstanceWithoutDefaultFallback pins that a call
// bound to an instance id the caller cannot access is refused outright: the
// configured default instance must never quietly serve it instead.
func TestArrToolsRefuseUnknownInstanceWithoutDefaultFallback(t *testing.T) {
	recorder := &callRecorder{}
	defaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer defaultServer.Close()

	server := newDefaultInstanceToolServer(t, map[string]string{
		"radarr": defaultServer.URL, "sonarr": defaultServer.URL, "chaptarr": defaultServer.URL,
	})
	callCtx := adminCallContext()
	callCtx.InstanceID = "someone-elses-instance"

	tests := []struct {
		tool  string
		input string
	}{
		{tool: "get_queue", input: `{"media_type":"movie"}`},
		{tool: "get_queue", input: `{"media_type":"tv"}`},
		{tool: "get_queue", input: `{"media_type":"book"}`},
		{tool: "get_library", input: `{"media_type":"movie"}`},
		{tool: "get_history", input: `{"media_type":"tv"}`},
		{tool: "search_releases", input: `{"media_type":"movie","tmdb_id":42}`},
	}
	for _, tt := range tests {
		t.Run(tt.tool+" "+tt.input, func(t *testing.T) {
			result, err := server.ExecuteTool(context.Background(), tt.tool, json.RawMessage(tt.input), callCtx)
			if err != nil {
				t.Fatalf("%s: %v", tt.tool, err)
			}
			if !strings.Contains(result.Text, "is not configured") {
				t.Fatalf("unknown instance result = %q, want a not-configured refusal", result.Text)
			}
		})
	}

	// grab_release fails as a definitive pre-dispatch mutation error.
	grabInput, err := json.Marshal(map[string]any{
		"media_type": "movie", "guid": releaseGUIDReference("raw"), "indexer_id": 1, "tmdb_id": 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.ExecuteTool(context.Background(), "grab_release", json.RawMessage(grabInput), callCtx)
	assertRejectedWith(t, err, "Radarr is not configured for the selected instance")

	if got := recorder.all(); len(got) != 0 {
		t.Fatalf("default instance served %d request(s) for an inaccessible instance id: %+v", len(got), got)
	}
}

// --- remove_queue_item ---

func TestRemoveQueueItemDispatchesExactDeletePerService(t *testing.T) {
	tests := []struct {
		mediaType   string
		serviceType string
		wantURI     string
	}{
		{mediaType: "movie", serviceType: "radarr", wantURI: "/api/v3/queue/42?removeFromClient=true&blocklist=true&skipRedownload=false&changeCategory=false"},
		{mediaType: "tv", serviceType: "sonarr", wantURI: "/api/v3/queue/42?removeFromClient=true&blocklist=true&skipRedownload=false&changeCategory=false"},
		{mediaType: "book", serviceType: "chaptarr", wantURI: "/api/v1/queue/42?removeFromClient=true&blocklist=true&skipRedownload=false&changeCategory=false"},
	}
	for _, tt := range tests {
		t.Run(tt.mediaType, func(t *testing.T) {
			recorder := &callRecorder{}
			arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recorder.record(r)
				w.WriteHeader(http.StatusOK)
			}))
			defer arrServer.Close()

			server := newDefaultInstanceToolServer(t, map[string]string{tt.serviceType: arrServer.URL})
			result, err := server.ExecuteTool(
				context.Background(),
				"remove_queue_item",
				json.RawMessage(`{"queue_id":42,"media_type":"`+tt.mediaType+`","blocklist":true}`),
				adminCallContext(),
			)
			if err != nil {
				t.Fatalf("remove_queue_item: %v", err)
			}
			if !strings.Contains(result.Text, "Removed queue item 42") || !strings.Contains(result.Text, "blocklisted") {
				t.Fatalf("result = %q", result.Text)
			}
			calls := recorder.all()
			if len(calls) != 1 || calls[0].Method != http.MethodDelete || calls[0].URI != tt.wantURI {
				t.Fatalf("upstream calls = %+v, want exactly DELETE %s", calls, tt.wantURI)
			}
		})
	}

	t.Run("invalid media_type never reaches the arr", func(t *testing.T) {
		recorder := &callRecorder{}
		arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder.record(r)
			w.WriteHeader(http.StatusOK)
		}))
		defer arrServer.Close()

		server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})
		_, err := server.ExecuteTool(
			context.Background(),
			"remove_queue_item",
			json.RawMessage(`{"queue_id":42,"media_type":"album"}`),
			adminCallContext(),
		)
		assertRejectedWith(t, err, `media_type must be "movie", "tv", or "book"`)
		if got := recorder.all(); len(got) != 0 {
			t.Fatalf("rejected removal still reached the arr: %+v", got)
		}
	})
}

// --- remediate_queue_item ---

func newRemediationRadarr(t *testing.T, recorder *callRecorder, queueJSON string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/queue":
			_, _ = w.Write([]byte(queueJSON))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v3/queue/"):
			recorder.record(r)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			recorder.record(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected radarr request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

const remediationMovieQueueJSON = `{"totalRecords":1,"records":[{"id":42,"movieId":7,"downloadId":"dl-1","protocol":"usenet","title":"Stuck.Release","movie":{"id":7,"title":"Scoped Movie","year":2026,"tmdbId":550}}]}`

func TestRemediateQueueItemBlocklistSearchRemovesThenSearchesSameMovie(t *testing.T) {
	recorder := &callRecorder{}
	arrServer := newRemediationRadarr(t, recorder, remediationMovieQueueJSON)

	server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})
	result, err := server.ExecuteTool(
		context.Background(),
		"remediate_queue_item",
		json.RawMessage(`{"queue_id":42,"media_type":"movie","action":"blocklist_search"}`),
		adminCallContext(),
	)
	if err != nil {
		t.Fatalf("remediate_queue_item: %v", err)
	}
	if !strings.Contains(result.Text, "Removed and blocklisted queue item 42") || !strings.Contains(result.Text, "Scoped Movie") {
		t.Fatalf("result = %q", result.Text)
	}

	mutations := recorder.mutations()
	if len(mutations) != 2 {
		t.Fatalf("mutations = %+v, want remove followed by search", mutations)
	}
	if mutations[0].Method != http.MethodDelete ||
		mutations[0].URI != "/api/v3/queue/42?removeFromClient=true&blocklist=true&skipRedownload=false&changeCategory=false" {
		t.Fatalf("first mutation = %+v, want blocklisting DELETE of queue 42", mutations[0])
	}
	command := decodeBody(t, mutations[1].Body)
	movieIDs, _ := command["movieIds"].([]any)
	if command["name"] != "MoviesSearch" || len(movieIDs) != 1 || movieIDs[0] != float64(7) {
		t.Fatalf("replacement search command = %v, want MoviesSearch for movie 7", command)
	}
}

func TestRemediateQueueItemChangeCategoryKeepsDownloadInClient(t *testing.T) {
	recorder := &callRecorder{}
	arrServer := newRemediationRadarr(t, recorder, remediationMovieQueueJSON)

	server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})
	result, err := server.ExecuteTool(
		context.Background(),
		"remediate_queue_item",
		json.RawMessage(`{"queue_id":42,"media_type":"movie","action":"change_category"}`),
		adminCallContext(),
	)
	if err != nil {
		t.Fatalf("remediate_queue_item: %v", err)
	}
	if !strings.Contains(result.Text, "post-import category") {
		t.Fatalf("result = %q", result.Text)
	}
	mutations := recorder.mutations()
	if len(mutations) != 1 || mutations[0].Method != http.MethodDelete ||
		mutations[0].URI != "/api/v3/queue/42?removeFromClient=false&blocklist=false&skipRedownload=false&changeCategory=true" {
		t.Fatalf("mutations = %+v, want a single change-category DELETE", mutations)
	}
}

func TestRemediateQueueItemTVBlocklistSearchTargetsExactEpisode(t *testing.T) {
	recorder := &callRecorder{}
	arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/queue":
			_, _ = w.Write([]byte(`{"totalRecords":1,"records":[{"id":8,"seriesId":3,"episodeId":55,"downloadId":"dl-8","protocol":"torrent","series":{"id":3,"title":"Scoped Show","tmdbId":42},"episode":{"id":55,"seasonNumber":2,"episodeNumber":7}}]}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v3/queue/"):
			recorder.record(r)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			recorder.record(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected sonarr request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer arrServer.Close()

	server := newDefaultInstanceToolServer(t, map[string]string{"sonarr": arrServer.URL})
	if _, err := server.ExecuteTool(
		context.Background(),
		"remediate_queue_item",
		json.RawMessage(`{"queue_id":8,"media_type":"tv","action":"blocklist_search"}`),
		adminCallContext(),
	); err != nil {
		t.Fatalf("remediate_queue_item: %v", err)
	}

	mutations := recorder.mutations()
	if len(mutations) != 2 {
		t.Fatalf("mutations = %+v, want remove followed by episode search", mutations)
	}
	if mutations[0].URI != "/api/v3/queue/8?removeFromClient=true&blocklist=true&skipRedownload=false&changeCategory=false" {
		t.Fatalf("first mutation = %+v", mutations[0])
	}
	command := decodeBody(t, mutations[1].Body)
	episodeIDs, _ := command["episodeIds"].([]any)
	if command["name"] != "EpisodeSearch" || len(episodeIDs) != 1 || episodeIDs[0] != float64(55) {
		t.Fatalf("replacement search command = %v, want EpisodeSearch for episode 55", command)
	}
}

func TestRemediateQueueItemRejectsUnknownTargetsBeforeMutating(t *testing.T) {
	recorder := &callRecorder{}
	arrServer := newRemediationRadarr(t, recorder, remediationMovieQueueJSON)
	server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})

	t.Run("unknown action", func(t *testing.T) {
		_, err := server.ExecuteTool(
			context.Background(),
			"remediate_queue_item",
			json.RawMessage(`{"queue_id":42,"media_type":"movie","action":"nuke"}`),
			adminCallContext(),
		)
		assertRejectedWith(t, err, `action must be "remove", "blocklist_search", or "change_category"`)
	})

	t.Run("missing queue item", func(t *testing.T) {
		_, err := server.ExecuteTool(
			context.Background(),
			"remediate_queue_item",
			json.RawMessage(`{"queue_id":99,"media_type":"movie","action":"remove"}`),
			adminCallContext(),
		)
		assertRejectedWith(t, err, "no movie queue item with id 99")
	})

	if got := recorder.mutations(); len(got) != 0 {
		t.Fatalf("rejected remediations still mutated the arr: %+v", got)
	}
}

// --- execute_manual_import ---

func TestExecuteManualImportImportsCleanCandidatesAndSkipsPermanentRejections(t *testing.T) {
	recorder := &callRecorder{}
	arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/queue":
			_, _ = w.Write([]byte(`{"totalRecords":1,"records":[{"id":42,"movieId":7,"downloadId":"dl-1","protocol":"torrent","title":"Stuck.Release","movie":{"id":7,"title":"Scoped Movie","year":2026,"tmdbId":550}}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/manualimport":
			if got := r.URL.Query().Get("downloadId"); got != "dl-1" {
				t.Errorf("manual import candidates downloadId = %q, want dl-1", got)
			}
			_, _ = w.Write([]byte(`[
				{"name":"good.mkv","path":"/downloads/good.mkv","folderName":"Scoped.Movie.2026","movie":{"id":7},"quality":{"quality":{"name":"WEBDL-1080p"}},"languages":[{"id":1,"name":"English"}],"releaseGroup":"GRP","downloadId":"dl-1","rejections":[]},
				{"name":"sample.mkv","path":"/downloads/sample.mkv","movie":{"id":7},"downloadId":"dl-1","rejections":[{"reason":"Sample file","type":"permanent"}]}
			]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			recorder.record(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected radarr request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer arrServer.Close()

	server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})
	result, err := server.ExecuteTool(
		context.Background(),
		"execute_manual_import",
		json.RawMessage(`{"queue_id":42,"media_type":"movie"}`),
		adminCallContext(),
	)
	if err != nil {
		t.Fatalf("execute_manual_import: %v", err)
	}
	if !strings.Contains(result.Text, "Sent 1 file(s) to import (mode: copy)") ||
		!strings.Contains(result.Text, "Skipped 1 file(s)") ||
		!strings.Contains(result.Text, "sample.mkv") {
		t.Fatalf("result = %q", result.Text)
	}

	mutations := recorder.mutations()
	if len(mutations) != 1 {
		t.Fatalf("mutations = %+v, want a single ManualImport command", mutations)
	}
	command := decodeBody(t, mutations[0].Body)
	if command["name"] != "ManualImport" || command["importMode"] != "copy" {
		t.Fatalf("command = %v, want ManualImport in copy mode (torrent protocol)", command)
	}
	files, _ := command["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("imported files = %v, want only the clean candidate", files)
	}
	file, _ := files[0].(map[string]any)
	if file["path"] != "/downloads/good.mkv" || file["movieId"] != float64(7) || file["downloadId"] != "dl-1" {
		t.Fatalf("imported file = %v", file)
	}
	quality, _ := file["quality"].(map[string]any)
	inner, _ := quality["quality"].(map[string]any)
	if inner["name"] != "WEBDL-1080p" {
		t.Fatalf("quality was not passed back verbatim: %v", file["quality"])
	}
}

// --- trigger_search ---

func TestTriggerSearchDispatchesScopedCommands(t *testing.T) {
	t.Run("movie resolves tmdb to library movie", func(t *testing.T) {
		recorder := &callRecorder{}
		arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie":
				if got := r.URL.Query().Get("tmdbId"); got != "550" {
					t.Errorf("movie lookup tmdbId = %q, want 550", got)
				}
				_, _ = w.Write([]byte(`[{"id":7,"title":"Scoped Movie","year":2026,"tmdbId":550}]`))
			case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
				recorder.record(r)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{}`))
			default:
				t.Errorf("unexpected radarr request %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		defer arrServer.Close()

		server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})
		result, err := server.ExecuteTool(
			context.Background(),
			"trigger_search",
			json.RawMessage(`{"media_type":"movie","tmdb_id":550}`),
			adminCallContext(),
		)
		if err != nil {
			t.Fatalf("trigger_search: %v", err)
		}
		if !strings.Contains(result.Text, "Search started for Scoped Movie (2026)") {
			t.Fatalf("result = %q", result.Text)
		}
		mutations := recorder.mutations()
		if len(mutations) != 1 {
			t.Fatalf("mutations = %+v", mutations)
		}
		command := decodeBody(t, mutations[0].Body)
		movieIDs, _ := command["movieIds"].([]any)
		if command["name"] != "MoviesSearch" || len(movieIDs) != 1 || movieIDs[0] != float64(7) {
			t.Fatalf("command = %v, want MoviesSearch for movie 7", command)
		}
	})

	t.Run("tv season scopes SeasonSearch to the resolved series", func(t *testing.T) {
		recorder := &callRecorder{}
		arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series":
				_, _ = w.Write([]byte(`[{"id":3,"title":"Scoped Show","tmdbId":42,"tvdbId":4242}]`))
			case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
				recorder.record(r)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{}`))
			default:
				t.Errorf("unexpected sonarr request %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		defer arrServer.Close()

		server := newDefaultInstanceToolServer(t, map[string]string{"sonarr": arrServer.URL})
		if _, err := server.ExecuteTool(
			context.Background(),
			"trigger_search",
			json.RawMessage(`{"media_type":"tv","tmdb_id":42,"season_number":2}`),
			adminCallContext(),
		); err != nil {
			t.Fatalf("trigger_search: %v", err)
		}
		mutations := recorder.mutations()
		if len(mutations) != 1 {
			t.Fatalf("mutations = %+v", mutations)
		}
		command := decodeBody(t, mutations[0].Body)
		if command["name"] != "SeasonSearch" || command["seriesId"] != float64(3) || command["seasonNumber"] != float64(2) {
			t.Fatalf("command = %v, want SeasonSearch series 3 season 2", command)
		}
	})

	t.Run("tv episode resolves to the exact episode id", func(t *testing.T) {
		recorder := &callRecorder{}
		arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series":
				_, _ = w.Write([]byte(`[{"id":3,"title":"Scoped Show","tmdbId":42,"tvdbId":4242}]`))
			case r.Method == http.MethodGet && r.URL.Path == "/api/v3/episode":
				if got := r.URL.RawQuery; got != "seriesId=3&seasonNumber=2" {
					t.Errorf("episode query = %q", got)
				}
				_, _ = w.Write([]byte(`[{"id":54,"seasonNumber":2,"episodeNumber":6},{"id":55,"seasonNumber":2,"episodeNumber":7}]`))
			case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
				recorder.record(r)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{}`))
			default:
				t.Errorf("unexpected sonarr request %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		defer arrServer.Close()

		server := newDefaultInstanceToolServer(t, map[string]string{"sonarr": arrServer.URL})
		if _, err := server.ExecuteTool(
			context.Background(),
			"trigger_search",
			json.RawMessage(`{"media_type":"tv","tmdb_id":42,"season_number":2,"episode_number":7}`),
			adminCallContext(),
		); err != nil {
			t.Fatalf("trigger_search: %v", err)
		}
		mutations := recorder.mutations()
		if len(mutations) != 1 {
			t.Fatalf("mutations = %+v", mutations)
		}
		command := decodeBody(t, mutations[0].Body)
		episodeIDs, _ := command["episodeIds"].([]any)
		if command["name"] != "EpisodeSearch" || len(episodeIDs) != 1 || episodeIDs[0] != float64(55) {
			t.Fatalf("command = %v, want EpisodeSearch for episode 55 only", command)
		}
	})

	t.Run("book id and author id map to their own commands", func(t *testing.T) {
		recorder := &callRecorder{}
		arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPost && r.URL.Path == "/api/v1/command" {
				recorder.record(r)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			t.Errorf("unexpected chaptarr request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}))
		defer arrServer.Close()

		server := newDefaultInstanceToolServer(t, map[string]string{"chaptarr": arrServer.URL})
		if _, err := server.ExecuteTool(
			context.Background(),
			"trigger_search",
			json.RawMessage(`{"media_type":"book","book_id":9}`),
			adminCallContext(),
		); err != nil {
			t.Fatalf("trigger_search book_id: %v", err)
		}
		if _, err := server.ExecuteTool(
			context.Background(),
			"trigger_search",
			json.RawMessage(`{"media_type":"book","author_id":4}`),
			adminCallContext(),
		); err != nil {
			t.Fatalf("trigger_search author_id: %v", err)
		}
		mutations := recorder.mutations()
		if len(mutations) != 2 {
			t.Fatalf("mutations = %+v", mutations)
		}
		bookCommand := decodeBody(t, mutations[0].Body)
		bookIDs, _ := bookCommand["bookIds"].([]any)
		if bookCommand["name"] != "BookSearch" || len(bookIDs) != 1 || bookIDs[0] != float64(9) {
			t.Fatalf("book command = %v, want BookSearch for book 9", bookCommand)
		}
		authorCommand := decodeBody(t, mutations[1].Body)
		if authorCommand["name"] != "AuthorSearch" || authorCommand["authorId"] != float64(4) {
			t.Fatalf("author command = %v, want AuthorSearch for author 4", authorCommand)
		}
	})

	t.Run("book without book_id or author_id is refused pre-dispatch", func(t *testing.T) {
		recorder := &callRecorder{}
		arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder.record(r)
			w.WriteHeader(http.StatusCreated)
		}))
		defer arrServer.Close()

		server := newDefaultInstanceToolServer(t, map[string]string{"chaptarr": arrServer.URL})
		_, err := server.ExecuteTool(
			context.Background(),
			"trigger_search",
			json.RawMessage(`{"media_type":"book"}`),
			adminCallContext(),
		)
		assertRejectedWith(t, err, "trigger_search for a book requires author_id or book_id")
		if got := recorder.all(); len(got) != 0 {
			t.Fatalf("rejected search still reached the arr: %+v", got)
		}
	})

	t.Run("movie not in library never dispatches a command", func(t *testing.T) {
		recorder := &callRecorder{}
		arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			recorder.record(r)
			w.WriteHeader(http.StatusCreated)
		}))
		defer arrServer.Close()

		server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})
		_, err := server.ExecuteTool(
			context.Background(),
			"trigger_search",
			json.RawMessage(`{"media_type":"movie","tmdb_id":550}`),
			adminCallContext(),
		)
		assertRejectedWith(t, err, "this movie is not in the library yet")
		if got := recorder.all(); len(got) != 0 {
			t.Fatalf("not-in-library search still dispatched: %+v", got)
		}
	})
}

// --- rescan_media ---

func TestRescanMediaMovieRescansThenRunsImportPass(t *testing.T) {
	recorder := &callRecorder{}
	arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie":
			_, _ = w.Write([]byte(`[{"id":7,"title":"Scoped Movie","year":2026,"tmdbId":550}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			recorder.record(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected radarr request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer arrServer.Close()

	server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})
	result, err := server.ExecuteTool(
		context.Background(),
		"rescan_media",
		json.RawMessage(`{"media_type":"movie","tmdb_id":550}`),
		adminCallContext(),
	)
	if err != nil {
		t.Fatalf("rescan_media: %v", err)
	}
	if !strings.Contains(result.Text, "Rescanning Scoped Movie (2026)") {
		t.Fatalf("result = %q", result.Text)
	}
	mutations := recorder.mutations()
	if len(mutations) != 2 {
		t.Fatalf("mutations = %+v, want rescan then import pass", mutations)
	}
	rescan := decodeBody(t, mutations[0].Body)
	movieIDs, _ := rescan["movieIds"].([]any)
	if rescan["name"] != "RescanMovie" || len(movieIDs) != 1 || movieIDs[0] != float64(7) {
		t.Fatalf("first command = %v, want RescanMovie for movie 7", rescan)
	}
	if importPass := decodeBody(t, mutations[1].Body); importPass["name"] != "ProcessMonitoredDownloads" {
		t.Fatalf("second command = %v, want ProcessMonitoredDownloads", importPass)
	}
}

func TestRescanMediaReportsPartialMutationWhenImportPassFails(t *testing.T) {
	arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie":
			_, _ = w.Write([]byte(`[{"id":7,"title":"Scoped Movie","year":2026,"tmdbId":550}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "ProcessMonitoredDownloads") {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer arrServer.Close()

	server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})
	_, err := server.ExecuteTool(
		context.Background(),
		"rescan_media",
		json.RawMessage(`{"media_type":"movie","tmdb_id":550}`),
		adminCallContext(),
	)
	if err == nil {
		t.Fatal("half-completed rescan reported success")
	}
	// The typed PartialMutationError is flattened by RedactError at the tool
	// boundary; the message must still say the rescan happened and the import
	// pass did not, so the model cannot describe this as a clean failure.
	if !strings.Contains(err.Error(), "a rescan of movie 7 was started") ||
		!strings.Contains(err.Error(), "starting the monitored-download import pass failed") {
		t.Fatalf("partial-mutation message = %v", err)
	}
}

func TestRescanMediaBookRequiresAuthorID(t *testing.T) {
	recorder := &callRecorder{}
	arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r)
		w.WriteHeader(http.StatusCreated)
	}))
	defer arrServer.Close()

	server := newDefaultInstanceToolServer(t, map[string]string{"chaptarr": arrServer.URL})
	_, err := server.ExecuteTool(
		context.Background(),
		"rescan_media",
		json.RawMessage(`{"media_type":"book"}`),
		adminCallContext(),
	)
	assertRejectedWith(t, err, "rescan for a book requires author_id")
	if got := recorder.all(); len(got) != 0 {
		t.Fatalf("rejected rescan still reached the arr: %+v", got)
	}
}

// --- grab_release (TV episode scope; movie scope covered in release_capability_test.go) ---

func TestGrabReleaseTVEpisodeFreshSearchDispatchesExactRelease(t *testing.T) {
	const rawGUID = "https://indexer.invalid/download/tv-episode-capability?token=tv-sentinel"
	recorder := &callRecorder{}
	arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series":
			_, _ = w.Write([]byte(`[{"id":3,"title":"Scoped Show","tmdbId":42,"tvdbId":4242}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/episode":
			_, _ = w.Write([]byte(`[{"id":55,"seasonNumber":2,"episodeNumber":7}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/release":
			recorder.record(r)
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"guid": rawGUID, "indexerId": 9, "indexer": "Scoped Indexer",
				"title": "Scoped.Show.S02E07.1080p", "size": 4096, "protocol": "usenet",
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/release":
			recorder.record(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected sonarr request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer arrServer.Close()

	server := newDefaultInstanceToolServer(t, map[string]string{"sonarr": arrServer.URL})
	input, err := json.Marshal(map[string]any{
		"media_type": "tv", "guid": releaseGUIDReference(rawGUID), "indexer_id": 9,
		"tmdb_id": 42, "season_number": 2, "episode_number": 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := server.ExecuteTool(context.Background(), "grab_release", json.RawMessage(input), adminCallContext())
	if err != nil {
		t.Fatalf("grab_release: %v", err)
	}
	if !strings.Contains(result.Text, "Release sent to the download client") {
		t.Fatalf("result = %q", result.Text)
	}

	calls := recorder.all()
	if len(calls) != 2 {
		t.Fatalf("release traffic = %+v, want a fresh episode search then one grab", calls)
	}
	if calls[0].Method != http.MethodGet || calls[0].URI != "/api/v3/release?episodeId=55" {
		t.Fatalf("fresh search = %+v, want GET /api/v3/release?episodeId=55", calls[0])
	}
	grab := decodeBody(t, calls[1].Body)
	if grab["guid"] != rawGUID || grab["indexerId"] != float64(9) {
		t.Fatalf("grab body = %v, want the fresh raw capability for indexer 9", grab)
	}
}

func TestGrabReleaseRejectsMismatchedScopeArguments(t *testing.T) {
	recorder := &callRecorder{}
	arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer arrServer.Close()

	server := newDefaultInstanceToolServer(t, map[string]string{
		"radarr": arrServer.URL, "sonarr": arrServer.URL, "chaptarr": arrServer.URL,
	})

	tests := []struct {
		name       string
		input      map[string]any
		wantReject string
	}{
		{
			name:       "raw guid instead of one-way reference",
			input:      map[string]any{"media_type": "movie", "guid": "https://indexer.invalid/raw", "indexer_id": 9, "tmdb_id": 42},
			wantReject: "guid must be the exact one-way release reference",
		},
		{
			name:       "movie scope polluted with season_number",
			input:      map[string]any{"media_type": "movie", "guid": releaseGUIDReference("raw"), "indexer_id": 9, "tmdb_id": 42, "season_number": 2},
			wantReject: "movie grab_release requires only a positive tmdb_id",
		},
		{
			name:       "tv scope missing season_number",
			input:      map[string]any{"media_type": "tv", "guid": releaseGUIDReference("raw"), "indexer_id": 9, "tmdb_id": 42},
			wantReject: "TV grab_release requires a positive tmdb_id, a non-negative season_number",
		},
		{
			name:       "book scope polluted with tmdb_id",
			input:      map[string]any{"media_type": "book", "guid": releaseGUIDReference("raw"), "indexer_id": 9, "book_id": 5, "tmdb_id": 42},
			wantReject: "book grab_release requires only a positive book_id",
		},
		{
			name:       "missing indexer_id",
			input:      map[string]any{"media_type": "movie", "guid": releaseGUIDReference("raw"), "tmdb_id": 42},
			wantReject: "indexer_id must be a positive id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			_, err = server.ExecuteTool(context.Background(), "grab_release", json.RawMessage(input), adminCallContext())
			assertRejectedWith(t, err, tt.wantReject)
		})
	}
	if got := recorder.all(); len(got) != 0 {
		t.Fatalf("rejected grabs still reached the arr: %+v", got)
	}
}

// --- get_queue (read-only response shaping + exact-scope verification) ---

func TestGetQueueRendersProgressErrorsAndExactScopeVerification(t *testing.T) {
	arrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/queue" {
			_, _ = w.Write([]byte(`{"totalRecords":1,"records":[{
				"id":42,"movieId":7,"downloadId":"dl-1","title":"Stuck.Release","status":"downloading",
				"size":2000,"sizeleft":500,"timeleft":"00:12:00","protocol":"usenet","downloadClient":"sab",
				"trackedDownloadStatus":"warning","trackedDownloadState":"importPending",
				"errorMessage":"import blocked",
				"statusMessages":[{"title":"Stuck.Release","messages":["No files found are eligible for import"]}],
				"movie":{"id":7,"title":"Scoped Movie","year":2026,"tmdbId":550}
			}]}`))
			return
		}
		t.Errorf("unexpected radarr request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer arrServer.Close()

	server := newDefaultInstanceToolServer(t, map[string]string{"radarr": arrServer.URL})
	result, err := server.ExecuteTool(
		context.Background(),
		"get_queue",
		json.RawMessage(`{"media_type":"movie"}`),
		adminCallContext(),
	)
	if err != nil {
		t.Fatalf("get_queue: %v", err)
	}
	for _, want := range []string{
		"Movie queue (1 items):",
		"[queue 42] Scoped Movie (2026) — downloading",
		"75.0% done",
		"00:12:00 left",
		"usenet",
		"via sab",
		"[warning/importPending]",
		"error: import blocked",
		"issue: No files found are eligible for import",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("queue rendering missing %q:\n%s", want, result.Text)
		}
	}
	if result.Verification != nil {
		t.Fatalf("unscoped queue read produced verification: %+v", result.Verification)
	}

	// Exact scope on the live target: verification proves presence.
	result, err = server.ExecuteTool(
		context.Background(),
		"get_queue",
		json.RawMessage(`{"media_type":"movie","queue_id":42}`),
		adminCallContext(),
	)
	if err != nil {
		t.Fatalf("scoped get_queue: %v", err)
	}
	if result.Verification == nil || result.Verification.Kind != VerificationQueueTarget ||
		!result.Verification.ExactScope || !result.Verification.TargetPresent {
		t.Fatalf("scoped verification = %+v", result.Verification)
	}

	// Exact scope on a vanished target: empty render plus a definitive absent
	// verification (the signal remediation uses to close auto-detected issues).
	result, err = server.ExecuteTool(
		context.Background(),
		"get_queue",
		json.RawMessage(`{"media_type":"movie","queue_id":41}`),
		adminCallContext(),
	)
	if err != nil {
		t.Fatalf("absent-target get_queue: %v", err)
	}
	if !strings.Contains(result.Text, "Movie queue: empty.") {
		t.Fatalf("absent-target render = %q", result.Text)
	}
	if result.Verification == nil || !result.Verification.ExactScope || result.Verification.TargetPresent {
		t.Fatalf("absent-target verification = %+v", result.Verification)
	}
}

// TestGetQueueParseFailureIsAnError pins that malformed tool input surfaces as
// an error instead of an unscoped default read.
func TestGetQueueParseFailureIsAnError(t *testing.T) {
	server := NewToolServer(nil, nil, nil, nil)
	_, err := server.ExecuteTool(
		context.Background(),
		"get_queue",
		json.RawMessage(`{"media_type":42}`),
		adminCallContext(),
	)
	if err == nil || !strings.Contains(err.Error(), "parse input") {
		t.Fatalf("malformed input error = %v", err)
	}
	if errors.Is(err, ErrToolAuthorization) {
		t.Fatalf("parse failure misclassified as authorization failure: %v", err)
	}
}
