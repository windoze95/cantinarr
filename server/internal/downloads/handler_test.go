package downloads

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// --- test environment ---

type env struct {
	store    *instance.Store
	registry *instance.Registry
	handler  *Handler
	router   chi.Router
}

// newEnv builds a handler over an in-memory instance store and mounts it on
// the same route patterns the API router uses (without auth middleware, which
// is covered elsewhere).
func newEnv(t *testing.T) *env {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	registry := instance.NewRegistry(store)
	handler := NewHandler(store, registry)

	router := chi.NewRouter()
	router.Get("/downloads/{instanceID}/queue", handler.GetQueue)
	router.Post("/downloads/{instanceID}/queue/{itemID}/pause", handler.PauseItem)
	router.Post("/downloads/{instanceID}/queue/{itemID}/resume", handler.ResumeItem)
	router.Delete("/downloads/{instanceID}/queue/{itemID}", handler.DeleteItem)
	router.Post("/downloads/{instanceID}/pause", handler.PauseAll)
	router.Post("/downloads/{instanceID}/resume", handler.ResumeAll)
	router.Get("/downloads/{instanceID}/history", handler.GetHistory)

	return &env{store: store, registry: registry, handler: handler, router: router}
}

func (e *env) mkInstance(t *testing.T, serviceType, baseURL, apiKey, username, password string) instance.Instance {
	t.Helper()
	inst := &instance.Instance{
		ServiceType: serviceType,
		Name:        serviceType + " test",
		URL:         baseURL,
		APIKey:      apiKey,
		Username:    username,
		Password:    password,
	}
	if err := e.store.Create(inst); err != nil {
		t.Fatalf("create %s instance: %v", serviceType, err)
	}
	return *inst
}

func (e *env) do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func decodeView(t *testing.T, rec *httptest.ResponseRecorder) QueueView {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var view QueueView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode queue view: %v", err)
	}
	return view
}

func decodeHistory(t *testing.T, rec *httptest.ResponseRecorder) []historyItem {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp historyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	return resp.Items
}

// --- SABnzbd fake ---

type sabFake struct {
	t       *testing.T
	apiKey  string
	queue   string
	history string

	mu    sync.Mutex
	calls []url.Values
}

func (f *sabFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" {
			f.t.Errorf("sabnzbd path = %s, want /api", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("output") != "json" {
			f.t.Errorf("sabnzbd output = %q, want json", q.Get("output"))
		}
		if q.Get("apikey") != f.apiKey {
			f.t.Errorf("sabnzbd apikey = %q, want %q", q.Get("apikey"), f.apiKey)
		}
		f.mu.Lock()
		f.calls = append(f.calls, q)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case q.Get("mode") == "queue" && q.Get("name") == "":
			_, _ = io.WriteString(w, f.queue)
		case q.Get("mode") == "history":
			_, _ = io.WriteString(w, f.history)
		default:
			_, _ = io.WriteString(w, `{"status": true}`)
		}
	}
}

func (f *sabFake) lastCall(t *testing.T) url.Values {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("sabnzbd fake received no calls")
	}
	return f.calls[len(f.calls)-1]
}

// --- qBittorrent fake ---

type qbitForm struct {
	path string
	form url.Values
}

type qbitFake struct {
	t        *testing.T
	username string
	password string
	torrents string
	transfer string

	mu      sync.Mutex
	sid     string
	logins  int
	actions []qbitForm
}

func (f *qbitFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			_ = r.ParseForm()
			if r.PostForm.Get("username") != f.username || r.PostForm.Get("password") != f.password {
				_, _ = io.WriteString(w, "Fails.")
				return
			}
			f.mu.Lock()
			f.logins++
			f.sid = "sid-" + strings.Repeat("x", f.logins)
			sid := f.sid
			f.mu.Unlock()
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: sid})
			_, _ = io.WriteString(w, "Ok.")
			return
		}

		cookie, err := r.Cookie("SID")
		f.mu.Lock()
		valid := err == nil && cookie.Value == f.sid
		f.mu.Unlock()
		if !valid {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		switch r.URL.Path {
		case "/api/v2/torrents/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, f.torrents)
		case "/api/v2/transfer/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, f.transfer)
		case "/api/v2/torrents/stop", "/api/v2/torrents/start",
			"/api/v2/torrents/pause", "/api/v2/torrents/resume",
			"/api/v2/torrents/delete":
			_ = r.ParseForm()
			f.mu.Lock()
			f.actions = append(f.actions, qbitForm{path: r.URL.Path, form: r.PostForm})
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func (f *qbitFake) lastAction(t *testing.T) qbitForm {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.actions) == 0 {
		t.Fatal("qbittorrent fake received no action calls")
	}
	return f.actions[len(f.actions)-1]
}

// --- NZBGet fake ---

type rpcCall struct {
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
}

type nzbgetFake struct {
	t        *testing.T
	username string
	password string
	groups   string
	status   string
	history  string

	mu    sync.Mutex
	calls []rpcCall
}

func (f *nzbgetFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jsonrpc" {
			f.t.Errorf("nzbget path = %s, want /jsonrpc", r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != f.username || pass != f.password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var call rpcCall
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			f.t.Errorf("nzbget decode request: %v", err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, call)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch call.Method {
		case "listgroups":
			_, _ = io.WriteString(w, `{"result": `+f.groups+`}`)
		case "status":
			_, _ = io.WriteString(w, `{"result": `+f.status+`}`)
		case "history":
			_, _ = io.WriteString(w, `{"result": `+f.history+`}`)
		default: // editqueue, pausedownload, resumedownload
			_, _ = io.WriteString(w, `{"result": true}`)
		}
	}
}

func (f *nzbgetFake) lastCall(t *testing.T) rpcCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("nzbget fake received no calls")
	}
	return f.calls[len(f.calls)-1]
}

// --- Transmission fake ---

type transRPC struct {
	Method    string                 `json:"method"`
	Arguments map[string]interface{} `json:"arguments"`
}

type transFake struct {
	t        *testing.T
	torrents string // JSON array for the torrent-get "torrents" field
	stats    string

	mu    sync.Mutex
	calls []transRPC
}

const transSessionID = "fake-session-id-123"

func (f *transFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transmission/rpc" {
			f.t.Errorf("transmission path = %s, want /transmission/rpc", r.URL.Path)
		}
		if r.Header.Get("X-Transmission-Session-Id") != transSessionID {
			w.Header().Set("X-Transmission-Session-Id", transSessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		var call transRPC
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			f.t.Errorf("transmission decode request: %v", err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, call)
		torrents, stats := f.torrents, f.stats
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch call.Method {
		case "torrent-get":
			_, _ = io.WriteString(w, `{"result":"success","arguments":{"torrents":`+torrents+`}}`)
		case "session-stats":
			_, _ = io.WriteString(w, `{"result":"success","arguments":`+stats+`}`)
		default: // torrent-stop, torrent-start, torrent-remove
			_, _ = io.WriteString(w, `{"result":"success"}`)
		}
	}
}

func (f *transFake) callsOf(method string) []transRPC {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []transRPC
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// --- Snapshot normalization ---

func TestSnapshotSabnzbdNormalization(t *testing.T) {
	fake := &sabFake{
		t:      t,
		apiKey: "sab-key",
		queue: `{"queue":{
			"paused": false,
			"kbpersec": "2048.00",
			"speed": "2.0 M",
			"slots": [
				{"nzo_id":"SABnzbd_nzo_p86tE","filename":"Andor.S02E03.1080p.WEB","mb":"1400.00","mbleft":"350.00","percentage":"75","timeleft":"0:07:30","status":"Downloading","cat":"tv"},
				{"nzo_id":"SABnzbd_nzo_zq9xY","filename":"Rogue.One.2016.2160p","mb":"8192.00","mbleft":"8192.00","percentage":"0","timeleft":"","status":"Queued","cat":"*"}
			]
		}}`,
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "sabnzbd", srv.URL, "sab-key", "", "")

	view, err := Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if view.Paused {
		t.Error("Paused = true, want false")
	}
	if view.SpeedBPS != 2048*1024 {
		t.Errorf("SpeedBPS = %d, want %d", view.SpeedBPS, 2048*1024)
	}
	if len(view.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(view.Items))
	}
	got := view.Items[0]
	want := QueueItem{
		ID:            "SABnzbd_nzo_p86tE",
		Name:          "Andor.S02E03.1080p.WEB",
		SizeBytes:     1400 * 1024 * 1024,
		SizeLeftBytes: 350 * 1024 * 1024,
		Progress:      75,
		SpeedBPS:      0, // SABnzbd has no per-item speed
		ETASeconds:    450,
		Status:        "Downloading",
		Category:      "tv",
	}
	if got != want {
		t.Errorf("item[0] = %+v, want %+v", got, want)
	}
	// SABnzbd's "*" wildcard category normalizes to empty, and a blank
	// timeleft normalizes to ETA 0.
	if view.Items[1].Category != "" {
		t.Errorf("item[1].Category = %q, want \"\" (from \"*\")", view.Items[1].Category)
	}
	if view.Items[1].ETASeconds != 0 {
		t.Errorf("item[1].ETASeconds = %d, want 0", view.Items[1].ETASeconds)
	}
}

func TestSnapshotQbittorrentNormalization(t *testing.T) {
	fake := &qbitFake{
		t:        t,
		username: "admin",
		password: "qbit-pass",
		torrents: `[
			{"name":"ubuntu-live","hash":"aaa111","size":1000000,"progress":0.25,"dlspeed":52428,"eta":3600,"state":"downloading","category":"movies","completion_on":0},
			{"name":"done-torrent","hash":"bbb222","size":2000000,"progress":1.0,"dlspeed":0,"eta":8640000,"state":"uploading","category":"tv","completion_on":1752700000},
			{"name":"stalled","hash":"ccc333","size":2000000,"progress":0.5,"dlspeed":0,"eta":8640000,"state":"stalledDL","category":"","completion_on":0}
		]`,
		transfer: `{"dl_info_speed":123456,"up_info_speed":0,"dl_rate_limit":0}`,
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "qbittorrent", srv.URL, "", "admin", "qbit-pass")

	view, err := Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if view.SpeedBPS != 123456 {
		t.Errorf("SpeedBPS = %d, want 123456", view.SpeedBPS)
	}
	// Completed torrents (progress >= 1) belong to /history, not the queue.
	if len(view.Items) != 2 {
		t.Fatalf("items = %d, want 2 (completed torrent must be excluded)", len(view.Items))
	}
	got := view.Items[0]
	want := QueueItem{
		ID:            "aaa111",
		Name:          "ubuntu-live",
		SizeBytes:     1000000,
		SizeLeftBytes: 750000,
		Progress:      25,
		SpeedBPS:      52428,
		ETASeconds:    3600,
		Status:        "downloading",
		Category:      "movies",
	}
	if got != want {
		t.Errorf("item[0] = %+v, want %+v", got, want)
	}
	// qBittorrent's 8640000 "infinite" ETA sentinel normalizes to 0.
	if view.Items[1].ETASeconds != 0 {
		t.Errorf("item[1].ETASeconds = %d, want 0 (8640000 sentinel)", view.Items[1].ETASeconds)
	}
	if view.Items[1].SizeLeftBytes != 1000000 {
		t.Errorf("item[1].SizeLeftBytes = %d, want 1000000", view.Items[1].SizeLeftBytes)
	}
	// stalledDL counts as active, so the queue is not paused.
	if view.Paused {
		t.Error("Paused = true, want false")
	}
}

func TestSnapshotQbittorrentAllPausedMarksQueuePaused(t *testing.T) {
	fake := &qbitFake{
		t:        t,
		username: "admin",
		password: "qbit-pass",
		torrents: `[
			{"name":"a","hash":"aaa","size":100,"progress":0.5,"dlspeed":0,"eta":-1,"state":"pausedDL","category":"","completion_on":0},
			{"name":"b","hash":"bbb","size":100,"progress":0.5,"dlspeed":0,"eta":0,"state":"stoppedDL","category":"","completion_on":0}
		]`,
		transfer: `{"dl_info_speed":0,"up_info_speed":0,"dl_rate_limit":0}`,
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "qbittorrent", srv.URL, "", "admin", "qbit-pass")

	view, err := Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !view.Paused {
		t.Error("Paused = false, want true when every queued torrent is paused/stopped")
	}
	// A negative ETA (qBittorrent 5.x paused torrents) normalizes to 0.
	if view.Items[0].ETASeconds != 0 {
		t.Errorf("item[0].ETASeconds = %d, want 0", view.Items[0].ETASeconds)
	}
}

func TestSnapshotNzbgetNormalization(t *testing.T) {
	fake := &nzbgetFake{
		t:        t,
		username: "nzbget",
		password: "tegbzn6789",
		groups: `[
			{"NZBID":42,"NZBName":"Show.S01E01","FileSizeLo":1073741824,"FileSizeHi":0,"FileSizeMB":1024,"RemainingSizeLo":268435456,"RemainingSizeHi":0,"RemainingSizeMB":256,"Status":"DOWNLOADING","Category":"tv"},
			{"NZBID":43,"NZBName":"Big.Movie.2160p","FileSizeLo":705032704,"FileSizeHi":1,"FileSizeMB":4768,"RemainingSizeLo":705032704,"RemainingSizeHi":1,"RemainingSizeMB":4768,"Status":"QUEUED","Category":"movies"}
		]`,
		status: `{"DownloadRate":10485760,"DownloadPaused":false,"RemainingSizeLo":0,"RemainingSizeHi":0,"RemainingSizeMB":0}`,
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "nzbget", srv.URL, "", "nzbget", "tegbzn6789")

	view, err := Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if view.Paused {
		t.Error("Paused = true, want false")
	}
	if view.SpeedBPS != 10485760 {
		t.Errorf("SpeedBPS = %d, want 10485760", view.SpeedBPS)
	}
	if len(view.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(view.Items))
	}
	got := view.Items[0]
	want := QueueItem{
		ID:            "42", // NZBID is stringified for the unified ID field
		Name:          "Show.S01E01",
		SizeBytes:     1073741824,
		SizeLeftBytes: 268435456,
		Progress:      75,
		SpeedBPS:      0,  // NZBGet has no per-item speed
		ETASeconds:    25, // 268435456 / 10485760, integer division
		Status:        "DOWNLOADING",
		Category:      "tv",
	}
	if got != want {
		t.Errorf("item[0] = %+v, want %+v", got, want)
	}
	// The 32-bit Lo/Hi pair reassembles sizes beyond 4 GiB.
	if view.Items[1].SizeBytes != 5000000000 {
		t.Errorf("item[1].SizeBytes = %d, want 5000000000 (Lo/Hi reassembly)", view.Items[1].SizeBytes)
	}
}

func TestSnapshotTransmissionNormalization(t *testing.T) {
	fake := &transFake{
		t: t,
		torrents: `[
			{"id":1,"hashString":"deadbeef01","name":"Fedora","totalSize":1000000,"leftUntilDone":250000,"percentDone":0.75,"rateDownload":250000,"eta":12,"status":4,"error":0,"errorString":"","labels":["linux","iso"],"doneDate":0},
			{"id":2,"hashString":"deadbeef02","name":"Done","totalSize":5000,"leftUntilDone":0,"percentDone":1.0,"rateDownload":0,"eta":-1,"status":6,"error":0,"errorString":"","labels":[],"doneDate":1752600000},
			{"id":3,"hashString":"deadbeef03","name":"Stopped","totalSize":800,"leftUntilDone":800,"percentDone":0,"rateDownload":0,"eta":-2,"status":0,"error":0,"errorString":"","labels":[],"doneDate":0}
		]`,
		stats: `{"downloadSpeed":314159,"uploadSpeed":0}`,
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "transmission", srv.URL, "", "trans", "trans-pass")

	view, err := Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if view.SpeedBPS != 314159 {
		t.Errorf("SpeedBPS = %d, want 314159", view.SpeedBPS)
	}
	// Completed torrents (percentDone >= 1) belong to /history, not the queue.
	if len(view.Items) != 2 {
		t.Fatalf("items = %d, want 2 (completed torrent must be excluded)", len(view.Items))
	}
	got := view.Items[0]
	want := QueueItem{
		ID:            "deadbeef01",
		Name:          "Fedora",
		SizeBytes:     1000000,
		SizeLeftBytes: 250000,
		Progress:      75,
		SpeedBPS:      250000,
		ETASeconds:    12,
		Status:        "downloading", // numeric status 4 mapped to a string
		Category:      "linux",       // first label becomes the category
	}
	if got != want {
		t.Errorf("item[0] = %+v, want %+v", got, want)
	}
	stopped := view.Items[1]
	if stopped.Status != "stopped" || stopped.ETASeconds != 0 || stopped.Category != "" {
		t.Errorf("stopped item = %+v, want status stopped, ETA 0 (negative sentinel), empty category", stopped)
	}
	// One downloading torrent means the queue is not paused.
	if view.Paused {
		t.Error("Paused = true, want false")
	}
}

func TestSnapshotTransmissionAllStoppedMarksQueuePaused(t *testing.T) {
	fake := &transFake{
		t: t,
		torrents: `[
			{"id":3,"hashString":"deadbeef03","name":"Stopped","totalSize":800,"leftUntilDone":800,"percentDone":0,"rateDownload":0,"eta":-2,"status":0,"error":0,"errorString":"","labels":[],"doneDate":0}
		]`,
		stats: `{"downloadSpeed":0,"uploadSpeed":0}`,
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "transmission", srv.URL, "", "", "")

	view, err := Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !view.Paused {
		t.Error("Paused = false, want true when every queued torrent is stopped")
	}
}

func TestSnapshotEmptyQueues(t *testing.T) {
	sab := &sabFake{t: t, apiKey: "k", queue: `{"queue":{"paused":false,"kbpersec":"0.00","slots":[]}}`}
	sabSrv := httptest.NewServer(sab.handler())
	t.Cleanup(sabSrv.Close)

	qbit := &qbitFake{t: t, username: "u", password: "p", torrents: `[]`, transfer: `{"dl_info_speed":0}`}
	qbitSrv := httptest.NewServer(qbit.handler())
	t.Cleanup(qbitSrv.Close)

	nzb := &nzbgetFake{t: t, username: "u", password: "p", groups: `[]`,
		status: `{"DownloadRate":0,"DownloadPaused":false}`}
	nzbSrv := httptest.NewServer(nzb.handler())
	t.Cleanup(nzbSrv.Close)

	trans := &transFake{t: t, torrents: `[]`, stats: `{"downloadSpeed":0,"uploadSpeed":0}`}
	transSrv := httptest.NewServer(trans.handler())
	t.Cleanup(transSrv.Close)

	e := newEnv(t)
	cases := []struct {
		serviceType string
		inst        instance.Instance
	}{
		{"sabnzbd", e.mkInstance(t, "sabnzbd", sabSrv.URL, "k", "", "")},
		{"qbittorrent", e.mkInstance(t, "qbittorrent", qbitSrv.URL, "", "u", "p")},
		{"nzbget", e.mkInstance(t, "nzbget", nzbSrv.URL, "", "u", "p")},
		{"transmission", e.mkInstance(t, "transmission", transSrv.URL, "", "", "")},
	}
	for _, tc := range cases {
		t.Run(tc.serviceType, func(t *testing.T) {
			view, err := Snapshot(e.registry, tc.inst)
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			// Items must be a non-nil empty slice so the JSON payload is []
			// rather than null.
			if view.Items == nil || len(view.Items) != 0 {
				t.Errorf("Items = %#v, want non-nil empty slice", view.Items)
			}
			// An empty queue is never reported as paused for the torrent
			// backends (paused is derived from item states).
			if tc.serviceType != "sabnzbd" && view.Paused {
				t.Error("Paused = true, want false for an empty queue")
			}
		})
	}
}

func TestSnapshotRejectsNonDownloadInstance(t *testing.T) {
	e := newEnv(t)
	inst := e.mkInstance(t, "radarr", "http://radarr.invalid", "key", "", "")
	if _, err := Snapshot(e.registry, inst); err == nil {
		t.Fatal("Snapshot accepted a radarr instance")
	}
}

// TestSnapshotFailureIsPerInstance pins the degradation contract: multiple
// configured clients never cross-wire, and an unreachable backend fails only
// its own instance's snapshot — the healthy client's snapshot is unaffected.
func TestSnapshotFailureIsPerInstance(t *testing.T) {
	sab := &sabFake{t: t, apiKey: "sab-key", queue: `{"queue":{"paused":false,"kbpersec":"1.00","slots":[
		{"nzo_id":"NZO1","filename":"Sab.Item","mb":"1.00","mbleft":"1.00","percentage":"0","timeleft":"","status":"Queued","cat":""}
	]}}`}
	sabSrv := httptest.NewServer(sab.handler())
	t.Cleanup(sabSrv.Close)

	trans := &transFake{t: t, torrents: `[
		{"id":1,"hashString":"hash-t","name":"Trans.Item","totalSize":10,"leftUntilDone":10,"percentDone":0,"rateDownload":0,"eta":-1,"status":4,"labels":[],"doneDate":0}
	]`, stats: `{"downloadSpeed":0}`}
	transSrv := httptest.NewServer(trans.handler())

	e := newEnv(t)
	sabInst := e.mkInstance(t, "sabnzbd", sabSrv.URL, "sab-key", "", "")
	transInst := e.mkInstance(t, "transmission", transSrv.URL, "", "", "")

	// Both healthy: each snapshot returns its own backend's items.
	sabView, err := Snapshot(e.registry, sabInst)
	if err != nil {
		t.Fatalf("sab Snapshot: %v", err)
	}
	transView, err := Snapshot(e.registry, transInst)
	if err != nil {
		t.Fatalf("transmission Snapshot: %v", err)
	}
	if len(sabView.Items) != 1 || sabView.Items[0].Name != "Sab.Item" {
		t.Errorf("sab items = %+v, want the SABnzbd item", sabView.Items)
	}
	if len(transView.Items) != 1 || transView.Items[0].Name != "Trans.Item" {
		t.Errorf("transmission items = %+v, want the Transmission item", transView.Items)
	}

	// Kill Transmission: its snapshot errors, SABnzbd's still succeeds.
	transSrv.Close()
	if _, err := Snapshot(e.registry, transInst); err == nil {
		t.Fatal("Snapshot of unreachable transmission succeeded")
	}
	if _, err := Snapshot(e.registry, sabInst); err != nil {
		t.Fatalf("sab Snapshot after sibling died: %v", err)
	}
}

// --- queue endpoint ---

func TestGetQueueEndpointAndErrors(t *testing.T) {
	sab := &sabFake{t: t, apiKey: "sab-key", queue: `{"queue":{"paused":true,"kbpersec":"0.00","slots":[]}}`}
	sabSrv := httptest.NewServer(sab.handler())
	t.Cleanup(sabSrv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "sabnzbd", sabSrv.URL, "sab-key", "", "")

	view := decodeView(t, e.do(t, "GET", "/downloads/"+inst.ID+"/queue"))
	if !view.Paused || len(view.Items) != 0 {
		t.Errorf("view = %+v, want paused empty queue", view)
	}

	// Unknown instance → 404.
	if rec := e.do(t, "GET", "/downloads/no-such-instance/queue"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown instance status = %d, want 404", rec.Code)
	}

	// Non-download-client instance → 400.
	radarrInst := e.mkInstance(t, "radarr", "http://radarr.invalid", "key", "", "")
	if rec := e.do(t, "GET", "/downloads/"+radarrInst.ID+"/queue"); rec.Code != http.StatusBadRequest {
		t.Errorf("radarr instance status = %d, want 400", rec.Code)
	}
}

// TestGetQueueUnreachableBackendReturns502 pins the degradation surface: an
// unreachable backend errors the whole snapshot as a 502, and the error body
// never leaks the instance API key (SABnzbd embeds it in the request URL).
func TestGetQueueUnreachableBackendReturns502(t *testing.T) {
	const secret = "SAB_API_KEY_SENTINEL"
	deadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadSrv.Close()

	e := newEnv(t)
	inst := e.mkInstance(t, "sabnzbd", deadSrv.URL, secret, "", "")

	rec := e.do(t, "GET", "/downloads/"+inst.ID+"/queue")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("502 body leaked the API key: %s", rec.Body.String())
	}
}

// TestGetQueueGarbageBackendReturns502 pins that a backend replying 200 with
// non-JSON garbage fails the snapshot instead of returning a bogus queue.
func TestGetQueueGarbageBackendReturns502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>login page</html>")
	}))
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "sabnzbd", srv.URL, "key", "", "")

	if rec := e.do(t, "GET", "/downloads/"+inst.ID+"/queue"); rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

// --- destructive item/queue actions ---

func TestSabnzbdActions(t *testing.T) {
	fake := &sabFake{t: t, apiKey: "sab-key"}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "sabnzbd", srv.URL, "sab-key", "", "")
	base := "/downloads/" + inst.ID

	steps := []struct {
		name   string
		method string
		path   string
		want   map[string]string
	}{
		{"pause item", "POST", base + "/queue/SABnzbd_nzo_1/pause",
			map[string]string{"mode": "queue", "name": "pause", "value": "SABnzbd_nzo_1"}},
		{"resume item", "POST", base + "/queue/SABnzbd_nzo_1/resume",
			map[string]string{"mode": "queue", "name": "resume", "value": "SABnzbd_nzo_1"}},
		{"delete item keeps files", "DELETE", base + "/queue/SABnzbd_nzo_1",
			map[string]string{"mode": "queue", "name": "delete", "value": "SABnzbd_nzo_1", "del_files": "0"}},
		{"delete item removes files", "DELETE", base + "/queue/SABnzbd_nzo_1?deleteData=true",
			map[string]string{"mode": "queue", "name": "delete", "value": "SABnzbd_nzo_1", "del_files": "1"}},
		{"pause all", "POST", base + "/pause", map[string]string{"mode": "pause"}},
		{"resume all", "POST", base + "/resume", map[string]string{"mode": "resume"}},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			rec := e.do(t, step.method, step.path)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
			}
			got := fake.lastCall(t)
			for key, want := range step.want {
				if got.Get(key) != want {
					t.Errorf("query %s = %q, want %q (full query %v)", key, got.Get(key), want, got)
				}
			}
		})
	}
}

func TestQbittorrentActions(t *testing.T) {
	fake := &qbitFake{t: t, username: "admin", password: "qbit-pass"}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "qbittorrent", srv.URL, "", "admin", "qbit-pass")
	base := "/downloads/" + inst.ID

	steps := []struct {
		name     string
		method   string
		path     string
		wantPath string
		wantForm map[string]string
	}{
		// The client targets the qBittorrent 5.x stop/start names first;
		// the 4.x pause/resume fallback is covered in the qbittorrent package.
		{"pause item", "POST", base + "/queue/aaa111/pause",
			"/api/v2/torrents/stop", map[string]string{"hashes": "aaa111"}},
		{"resume item", "POST", base + "/queue/aaa111/resume",
			"/api/v2/torrents/start", map[string]string{"hashes": "aaa111"}},
		{"delete item keeps files", "DELETE", base + "/queue/aaa111",
			"/api/v2/torrents/delete", map[string]string{"hashes": "aaa111", "deleteFiles": "false"}},
		{"delete item removes files", "DELETE", base + "/queue/aaa111?deleteData=true",
			"/api/v2/torrents/delete", map[string]string{"hashes": "aaa111", "deleteFiles": "true"}},
		{"pause all", "POST", base + "/pause",
			"/api/v2/torrents/stop", map[string]string{"hashes": "all"}},
		{"resume all", "POST", base + "/resume",
			"/api/v2/torrents/start", map[string]string{"hashes": "all"}},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			rec := e.do(t, step.method, step.path)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
			}
			got := fake.lastAction(t)
			if got.path != step.wantPath {
				t.Errorf("upstream path = %s, want %s", got.path, step.wantPath)
			}
			for key, want := range step.wantForm {
				if got.form.Get(key) != want {
					t.Errorf("form %s = %q, want %q", key, got.form.Get(key), want)
				}
			}
		})
	}
}

func TestNzbgetActions(t *testing.T) {
	fake := &nzbgetFake{t: t, username: "nzbget", password: "pass"}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "nzbget", srv.URL, "", "nzbget", "pass")
	base := "/downloads/" + inst.ID

	assertEditQueue := func(t *testing.T, got rpcCall, command string) {
		t.Helper()
		if got.Method != "editqueue" {
			t.Fatalf("method = %s, want editqueue", got.Method)
		}
		// Modern 3-parameter signature: [Command, Param, IDs].
		if len(got.Params) != 3 || got.Params[0] != command || got.Params[1] != "" {
			t.Fatalf("params = %v, want [%s \"\" [42]]", got.Params, command)
		}
		ids, ok := got.Params[2].([]interface{})
		if !ok || len(ids) != 1 || ids[0] != float64(42) {
			t.Fatalf("ids = %v, want [42]", got.Params[2])
		}
	}

	rec := e.do(t, "POST", base+"/queue/42/pause")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pause status = %d (body %s)", rec.Code, rec.Body.String())
	}
	assertEditQueue(t, fake.lastCall(t), "GroupPause")

	rec = e.do(t, "POST", base+"/queue/42/resume")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resume status = %d (body %s)", rec.Code, rec.Body.String())
	}
	assertEditQueue(t, fake.lastCall(t), "GroupResume")

	// NZBGet's dialect has no remove-data flag: ?deleteData=true still maps
	// to a plain GroupDelete with no extra parameter.
	rec = e.do(t, "DELETE", base+"/queue/42?deleteData=true")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d (body %s)", rec.Code, rec.Body.String())
	}
	assertEditQueue(t, fake.lastCall(t), "GroupDelete")

	rec = e.do(t, "POST", base+"/pause")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pause-all status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if got := fake.lastCall(t); got.Method != "pausedownload" {
		t.Errorf("pause-all method = %s, want pausedownload", got.Method)
	}

	rec = e.do(t, "POST", base+"/resume")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resume-all status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if got := fake.lastCall(t); got.Method != "resumedownload" {
		t.Errorf("resume-all method = %s, want resumedownload", got.Method)
	}

	// A non-numeric item ID is rejected before any RPC is issued.
	fake.mu.Lock()
	before := len(fake.calls)
	fake.mu.Unlock()
	rec = e.do(t, "POST", base+"/queue/not-a-number/pause")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-numeric id status = %d, want 400", rec.Code)
	}
	fake.mu.Lock()
	after := len(fake.calls)
	fake.mu.Unlock()
	if after != before {
		t.Errorf("non-numeric id reached the backend (%d new calls)", after-before)
	}
}

func TestTransmissionItemActions(t *testing.T) {
	fake := &transFake{t: t}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "transmission", srv.URL, "", "", "")
	base := "/downloads/" + inst.ID

	rec := e.do(t, "POST", base+"/queue/deadbeef01/pause")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pause status = %d (body %s)", rec.Code, rec.Body.String())
	}
	stops := fake.callsOf("torrent-stop")
	if len(stops) != 1 {
		t.Fatalf("torrent-stop calls = %d, want 1", len(stops))
	}
	if ids, _ := stops[0].Arguments["ids"].([]interface{}); len(ids) != 1 || ids[0] != "deadbeef01" {
		t.Errorf("torrent-stop ids = %v, want [deadbeef01]", stops[0].Arguments["ids"])
	}

	rec = e.do(t, "POST", base+"/queue/deadbeef01/resume")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resume status = %d (body %s)", rec.Code, rec.Body.String())
	}
	starts := fake.callsOf("torrent-start")
	if len(starts) != 1 {
		t.Fatalf("torrent-start calls = %d, want 1", len(starts))
	}
	if ids, _ := starts[0].Arguments["ids"].([]interface{}); len(ids) != 1 || ids[0] != "deadbeef01" {
		t.Errorf("torrent-start ids = %v, want [deadbeef01]", starts[0].Arguments["ids"])
	}

	rec = e.do(t, "DELETE", base+"/queue/deadbeef01")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d (body %s)", rec.Code, rec.Body.String())
	}
	removes := fake.callsOf("torrent-remove")
	if len(removes) != 1 {
		t.Fatalf("torrent-remove calls = %d, want 1", len(removes))
	}
	if removes[0].Arguments["delete-local-data"] != false {
		t.Errorf("delete-local-data = %v, want false", removes[0].Arguments["delete-local-data"])
	}

	rec = e.do(t, "DELETE", base+"/queue/deadbeef01?deleteData=true")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete-with-data status = %d (body %s)", rec.Code, rec.Body.String())
	}
	removes = fake.callsOf("torrent-remove")
	if len(removes) != 2 {
		t.Fatalf("torrent-remove calls = %d, want 2", len(removes))
	}
	last := removes[1]
	if last.Arguments["delete-local-data"] != true {
		t.Errorf("delete-local-data = %v, want true", last.Arguments["delete-local-data"])
	}
	if ids, _ := last.Arguments["ids"].([]interface{}); len(ids) != 1 || ids[0] != "deadbeef01" {
		t.Errorf("torrent-remove ids = %v, want [deadbeef01]", last.Arguments["ids"])
	}
}

// TestTransmissionQueueActionsOnlyTouchIncompleteTorrents pins the guardrail
// in transmissionQueueAction: pause/resume-all must never send a bare (all
// torrents) command — completed/seeding torrents stay untouched, and when the
// visible queue is empty no torrent-stop/start is issued at all.
func TestTransmissionQueueActionsOnlyTouchIncompleteTorrents(t *testing.T) {
	fake := &transFake{t: t, torrents: `[
		{"id":1,"hashString":"incomplete-1","name":"A","totalSize":10,"leftUntilDone":5,"percentDone":0.5,"rateDownload":0,"eta":-1,"status":4,"labels":[],"doneDate":0},
		{"id":2,"hashString":"seeding-1","name":"B","totalSize":10,"leftUntilDone":0,"percentDone":1.0,"rateDownload":0,"eta":-1,"status":6,"labels":[],"doneDate":1752600000}
	]`, stats: `{"downloadSpeed":0}`}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "transmission", srv.URL, "", "", "")
	base := "/downloads/" + inst.ID

	rec := e.do(t, "POST", base+"/pause")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pause-all status = %d (body %s)", rec.Code, rec.Body.String())
	}
	stops := fake.callsOf("torrent-stop")
	if len(stops) != 1 {
		t.Fatalf("torrent-stop calls = %d, want 1", len(stops))
	}
	ids, _ := stops[0].Arguments["ids"].([]interface{})
	if len(ids) != 1 || ids[0] != "incomplete-1" {
		t.Fatalf("torrent-stop ids = %v, want only the incomplete torrent", stops[0].Arguments["ids"])
	}

	rec = e.do(t, "POST", base+"/resume")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resume-all status = %d (body %s)", rec.Code, rec.Body.String())
	}
	starts := fake.callsOf("torrent-start")
	if len(starts) != 1 {
		t.Fatalf("torrent-start calls = %d, want 1", len(starts))
	}
	ids, _ = starts[0].Arguments["ids"].([]interface{})
	if len(ids) != 1 || ids[0] != "incomplete-1" {
		t.Fatalf("torrent-start ids = %v, want only the incomplete torrent", starts[0].Arguments["ids"])
	}

	// All torrents complete: pause-all succeeds without issuing any stop.
	fake.mu.Lock()
	fake.torrents = `[
		{"id":2,"hashString":"seeding-1","name":"B","totalSize":10,"leftUntilDone":0,"percentDone":1.0,"rateDownload":0,"eta":-1,"status":6,"labels":[],"doneDate":1752600000}
	]`
	fake.mu.Unlock()
	rec = e.do(t, "POST", base+"/pause")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pause-all (empty queue) status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if got := len(fake.callsOf("torrent-stop")); got != 1 {
		t.Fatalf("torrent-stop calls = %d, want still 1 (no bare stop-all)", got)
	}
}

// --- history ---

func TestSabnzbdHistory(t *testing.T) {
	fake := &sabFake{t: t, apiKey: "sab-key", history: `{"history":{"slots":[
		{"name":"Old.Show","status":"Completed","fail_message":"","bytes":734003200.0,"size":"700 MB","completed":1752500000,"category":"tv"},
		{"name":"Bad.Nzb","status":"Failed","fail_message":"Aborted, cannot be completed","bytes":0,"size":"","completed":0,"category":"movies"}
	]}}`}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "sabnzbd", srv.URL, "sab-key", "", "")

	items := decodeHistory(t, e.do(t, "GET", "/downloads/"+inst.ID+"/history?limit=5"))
	if got := fake.lastCall(t); got.Get("mode") != "history" || got.Get("limit") != "5" {
		t.Errorf("upstream query = %v, want mode=history limit=5", got)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	wantCompleted := time.Unix(1752500000, 0).UTC().Format(time.RFC3339)
	got := items[0]
	if got.Name != "Old.Show" || got.Status != "Completed" || got.SizeBytes != 734003200 ||
		got.CompletedAt != wantCompleted || got.Category != "tv" || got.Error != "" {
		t.Errorf("item[0] = %+v", got)
	}
	// Zero completed timestamp stays empty; fail_message maps to error.
	if items[1].CompletedAt != "" || items[1].Error != "Aborted, cannot be completed" {
		t.Errorf("item[1] = %+v", items[1])
	}
}

func TestQbittorrentHistoryFiltersSortsAndLimits(t *testing.T) {
	fake := &qbitFake{t: t, username: "admin", password: "pass", torrents: `[
		{"name":"incomplete","hash":"h0","size":10,"progress":0.5,"state":"downloading","category":"","completion_on":0},
		{"name":"oldest","hash":"h1","size":100,"progress":1.0,"state":"uploading","category":"tv","completion_on":1752400000},
		{"name":"newest","hash":"h2","size":200,"progress":1.0,"state":"error","category":"movies","completion_on":1752600000},
		{"name":"middle","hash":"h3","size":300,"progress":1.0,"state":"missingFiles","category":"","completion_on":1752500000}
	]`}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "qbittorrent", srv.URL, "", "admin", "pass")

	items := decodeHistory(t, e.do(t, "GET", "/downloads/"+inst.ID+"/history?limit=2"))
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (limit applied)", len(items))
	}
	// Only completed torrents, newest first.
	if items[0].Name != "newest" || items[1].Name != "middle" {
		t.Errorf("order = [%s, %s], want [newest, middle]", items[0].Name, items[1].Name)
	}
	// error/missingFiles states surface as the error field.
	if items[0].Error != "error" || items[1].Error != "missingFiles" {
		t.Errorf("errors = [%q, %q], want [error, missingFiles]", items[0].Error, items[1].Error)
	}
	if items[0].CompletedAt != time.Unix(1752600000, 0).UTC().Format(time.RFC3339) {
		t.Errorf("CompletedAt = %s", items[0].CompletedAt)
	}
}

func TestNzbgetHistoryStatusMappingAndLimit(t *testing.T) {
	fake := &nzbgetFake{t: t, username: "u", password: "p", history: `[
		{"NZBID":7,"Name":"Success.Item","Status":"SUCCESS/ALL","FileSizeLo":52428800,"FileSizeHi":0,"FileSizeMB":50,"HistoryTime":1752500000,"Category":"tv"},
		{"NZBID":8,"Name":"Failed.Item","Status":"FAILURE/PAR","FileSizeLo":0,"FileSizeHi":0,"FileSizeMB":10,"HistoryTime":1752600000,"Category":""}
	]`}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "nzbget", srv.URL, "", "u", "p")

	items := decodeHistory(t, e.do(t, "GET", "/downloads/"+inst.ID+"/history"))
	// The history RPC asks for visible entries only (hidden=false).
	if got := fake.lastCall(t); got.Method != "history" || len(got.Params) != 1 || got.Params[0] != false {
		t.Errorf("upstream call = %+v, want history [false]", got)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	// Newest first; requester vocabulary: SUCCESS/* → Completed, FAILURE/* →
	// Failed with the raw status preserved as the error.
	if items[0].Name != "Failed.Item" || items[0].Status != "Failed" || items[0].Error != "FAILURE/PAR" {
		t.Errorf("item[0] = %+v", items[0])
	}
	if items[1].Name != "Success.Item" || items[1].Status != "Completed" || items[1].Error != "" {
		t.Errorf("item[1] = %+v", items[1])
	}
	if items[1].SizeBytes != 52428800 {
		t.Errorf("item[1].SizeBytes = %d, want 52428800", items[1].SizeBytes)
	}

	// limit=1 keeps only the most recent entry.
	items = decodeHistory(t, e.do(t, "GET", "/downloads/"+inst.ID+"/history?limit=1"))
	if len(items) != 1 || items[0].Name != "Failed.Item" {
		t.Fatalf("limited items = %+v, want only Failed.Item", items)
	}
}

func TestTransmissionHistoryCompletedOnly(t *testing.T) {
	fake := &transFake{t: t, torrents: `[
		{"id":1,"hashString":"h1","name":"incomplete","totalSize":10,"leftUntilDone":5,"percentDone":0.5,"status":4,"error":0,"errorString":"","labels":[],"doneDate":0},
		{"id":2,"hashString":"h2","name":"older-done","totalSize":100,"leftUntilDone":0,"percentDone":1.0,"status":6,"error":0,"errorString":"","labels":["linux"],"doneDate":1752400000},
		{"id":3,"hashString":"h3","name":"newer-done","totalSize":200,"leftUntilDone":0,"percentDone":1.0,"status":0,"error":3,"errorString":"No data found","labels":[],"doneDate":1752600000}
	]`, stats: `{"downloadSpeed":0}`}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "transmission", srv.URL, "", "", "")

	items := decodeHistory(t, e.do(t, "GET", "/downloads/"+inst.ID+"/history"))
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (incomplete excluded)", len(items))
	}
	got := items[0]
	if got.Name != "newer-done" || got.Status != "stopped" || got.Error != "No data found" ||
		got.CompletedAt != time.Unix(1752600000, 0).UTC().Format(time.RFC3339) {
		t.Errorf("item[0] = %+v", got)
	}
	if items[1].Name != "older-done" || items[1].Status != "seeding" ||
		items[1].Error != "" || items[1].Category != "linux" {
		t.Errorf("item[1] = %+v", items[1])
	}
}
