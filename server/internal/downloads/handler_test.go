package downloads

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
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

// --- Deluge fake ---

type delugeRPC struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

// delugeFake is a Deluge web UI already connected to its daemon: it issues
// a session cookie on auth.login, refuses calls without it, and answers the
// daemon methods with canned JSON.
type delugeFake struct {
	t        *testing.T
	torrents string // JSON object for core.get_torrents_status
	status   string // JSON object for core.get_session_status

	mu    sync.Mutex
	calls []delugeRPC
}

const delugeSessionID = "fake-deluge-session"

func (f *delugeFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json" {
			f.t.Errorf("deluge path = %s, want /json", r.URL.Path)
		}
		var call delugeRPC
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			f.t.Errorf("deluge decode request: %v", err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, call)
		torrents, status := f.torrents, f.status
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if call.Method == "auth.login" {
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: delugeSessionID, Path: "/"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
			return
		}
		if cookie, err := r.Cookie("_session_id"); err != nil || cookie.Value != delugeSessionID {
			_, _ = io.WriteString(w, `{"result":null,"error":{"message":"Not authenticated","code":1},"id":1}`)
			return
		}
		switch call.Method {
		case "web.connected":
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
		case "core.get_torrents_status":
			if torrents == "" {
				torrents = "{}"
			}
			_, _ = io.WriteString(w, `{"result":`+torrents+`,"error":null,"id":1}`)
		case "core.get_session_status":
			if status == "" {
				status = "{}"
			}
			_, _ = io.WriteString(w, `{"result":`+status+`,"error":null,"id":1}`)
		case "core.remove_torrent":
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
		default: // core.pause_torrent, core.resume_torrent
			_, _ = io.WriteString(w, `{"result":null,"error":null,"id":1}`)
		}
	}
}

func (f *delugeFake) callsOf(method string) []delugeRPC {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []delugeRPC
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// delugeHashes decodes the single list parameter of a pause/resume call.
func delugeHashes(t *testing.T, call delugeRPC) []string {
	t.Helper()
	if len(call.Params) != 1 {
		t.Fatalf("%s params = %d, want exactly one (the hash list)", call.Method, len(call.Params))
	}
	var hashes []string
	if err := json.Unmarshal(call.Params[0], &hashes); err != nil {
		t.Fatalf("%s param is not a list: %s", call.Method, call.Params[0])
	}
	return hashes
}

func TestSnapshotDelugeNormalization(t *testing.T) {
	fake := &delugeFake{
		t: t,
		torrents: `{
			"deadbeef01": {"name":"Fedora","state":"Downloading","progress":75.0,"total_size":1000000,"total_done":750000,"download_payload_rate":250000,"eta":12,"is_finished":false,"message":"OK","time_added":1752500000.0,"label":"linux"},
			"deadbeef02": {"name":"Done","state":"Seeding","progress":100.0,"total_size":5000,"total_done":5000,"download_payload_rate":0,"eta":0,"is_finished":true,"message":"OK","time_added":1752500000.0,"completed_time":1752600000},
			"deadbeef03": {"name":"Paused","state":"Paused","progress":0.0,"total_size":800,"total_done":0,"download_payload_rate":0,"eta":-1,"is_finished":false,"message":"","time_added":1752500001.0}
		}`,
		status: `{"payload_download_rate":314159.0,"payload_upload_rate":0}`,
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "deluge", srv.URL, "", "", "deluge-pass")

	view, err := Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if view.SpeedBPS != 314159 {
		t.Errorf("SpeedBPS = %d, want 314159", view.SpeedBPS)
	}
	// Finished torrents belong to /history, not the queue.
	if len(view.Items) != 2 {
		t.Fatalf("items = %d, want 2 (finished torrent must be excluded)", len(view.Items))
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
		Status:        "Downloading", // Deluge's own state name
		Category:      "linux",       // Label plugin label
	}
	if got != want {
		t.Errorf("item[0] = %+v, want %+v", got, want)
	}
	paused := view.Items[1]
	if paused.Status != "Paused" || paused.ETASeconds != 0 || paused.Category != "" || paused.SizeLeftBytes != 800 {
		t.Errorf("paused item = %+v, want status Paused, ETA 0 (negative sentinel), empty category, 800 left", paused)
	}
	if view.Paused {
		t.Error("Paused = true, want false while one torrent downloads")
	}
	// Login happened once and every daemon call carried the session.
	if logins := fake.callsOf("auth.login"); len(logins) != 1 {
		t.Errorf("auth.login calls = %d, want 1", len(logins))
	}
	if statusCalls := fake.callsOf("core.get_torrents_status"); len(statusCalls) != 1 {
		t.Errorf("core.get_torrents_status calls = %d, want 1", len(statusCalls))
	}
}

func TestSnapshotDelugeAllPausedMarksQueuePaused(t *testing.T) {
	fake := &delugeFake{
		t: t,
		torrents: `{
			"deadbeef03": {"name":"Paused","state":"Paused","progress":0.0,"total_size":800,"total_done":0,"download_payload_rate":0,"eta":0,"is_finished":false,"message":"","time_added":1752500001.0}
		}`,
		status: `{"payload_download_rate":0,"payload_upload_rate":0}`,
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "deluge", srv.URL, "", "", "deluge-pass")

	view, err := Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !view.Paused {
		t.Error("Paused = false, want true when every queued torrent is paused")
	}

	// A Queued torrent is Deluge scheduling it, not a user pause.
	fake.mu.Lock()
	fake.torrents = `{
		"deadbeef03": {"name":"Paused","state":"Paused","progress":0.0,"total_size":800,"total_done":0,"is_finished":false},
		"deadbeef04": {"name":"Queued","state":"Queued","progress":0.0,"total_size":800,"total_done":0,"is_finished":false}
	}`
	fake.mu.Unlock()
	view, err = Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if view.Paused {
		t.Error("Paused = true, want false while a torrent is Queued")
	}
}

func TestSnapshotDelugeEmptyQueue(t *testing.T) {
	fake := &delugeFake{t: t, torrents: `{}`, status: `{"payload_download_rate":0}`}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "deluge", srv.URL, "", "", "deluge-pass")
	rec := e.do(t, "GET", "/downloads/"+inst.ID+"/queue")
	view := decodeView(t, rec)
	if view.Items == nil || len(view.Items) != 0 || view.Paused {
		t.Errorf("view = %+v, want an empty, unpaused items list (never null)", view)
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("body = %s, want items serialized as []", rec.Body.String())
	}
}

func TestDelugeItemActions(t *testing.T) {
	fake := &delugeFake{t: t}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "deluge", srv.URL, "", "", "deluge-pass")
	base := "/downloads/" + inst.ID

	rec := e.do(t, "POST", base+"/queue/deadbeef01/pause")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pause status = %d (body %s)", rec.Code, rec.Body.String())
	}
	pauses := fake.callsOf("core.pause_torrent")
	if len(pauses) != 1 {
		t.Fatalf("core.pause_torrent calls = %d, want 1", len(pauses))
	}
	if hashes := delugeHashes(t, pauses[0]); len(hashes) != 1 || hashes[0] != "deadbeef01" {
		t.Errorf("core.pause_torrent hashes = %v, want [deadbeef01]", hashes)
	}

	rec = e.do(t, "POST", base+"/queue/deadbeef01/resume")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resume status = %d (body %s)", rec.Code, rec.Body.String())
	}
	resumes := fake.callsOf("core.resume_torrent")
	if len(resumes) != 1 {
		t.Fatalf("core.resume_torrent calls = %d, want 1", len(resumes))
	}
	if hashes := delugeHashes(t, resumes[0]); len(hashes) != 1 || hashes[0] != "deadbeef01" {
		t.Errorf("core.resume_torrent hashes = %v, want [deadbeef01]", hashes)
	}

	rec = e.do(t, "DELETE", base+"/queue/deadbeef01")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d (body %s)", rec.Code, rec.Body.String())
	}
	removes := fake.callsOf("core.remove_torrent")
	if len(removes) != 1 || len(removes[0].Params) != 2 ||
		string(removes[0].Params[0]) != `"deadbeef01"` || string(removes[0].Params[1]) != `false` {
		t.Fatalf("core.remove_torrent calls = %+v, want one call [deadbeef01, false]", removes)
	}

	rec = e.do(t, "DELETE", base+"/queue/deadbeef01?deleteData=true")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete-with-data status = %d (body %s)", rec.Code, rec.Body.String())
	}
	removes = fake.callsOf("core.remove_torrent")
	if len(removes) != 2 || string(removes[1].Params[1]) != `true` {
		t.Fatalf("core.remove_torrent calls = %+v, want a second call with remove_data true", removes)
	}
}

// TestDelugeQueueActionsOnlyTouchUnfinishedTorrents pins the same guardrail
// Transmission has: pause/resume-all never reaches seeding torrents, and an
// empty visible queue sends nothing (Deluge would read an empty list as
// "every torrent").
func TestDelugeQueueActionsOnlyTouchUnfinishedTorrents(t *testing.T) {
	fake := &delugeFake{t: t, torrents: `{
		"incomplete-1": {"name":"A","state":"Downloading","progress":50.0,"total_size":10,"total_done":5,"is_finished":false},
		"seeding-1": {"name":"B","state":"Seeding","progress":100.0,"total_size":10,"total_done":10,"is_finished":true,"completed_time":1752600000}
	}`, status: `{"payload_download_rate":0}`}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "deluge", srv.URL, "", "", "deluge-pass")
	base := "/downloads/" + inst.ID

	rec := e.do(t, "POST", base+"/pause")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pause-all status = %d (body %s)", rec.Code, rec.Body.String())
	}
	pauses := fake.callsOf("core.pause_torrent")
	if len(pauses) != 1 {
		t.Fatalf("core.pause_torrent calls = %d, want 1", len(pauses))
	}
	if hashes := delugeHashes(t, pauses[0]); len(hashes) != 1 || hashes[0] != "incomplete-1" {
		t.Fatalf("core.pause_torrent hashes = %v, want only the unfinished torrent", hashes)
	}

	rec = e.do(t, "POST", base+"/resume")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resume-all status = %d (body %s)", rec.Code, rec.Body.String())
	}
	resumes := fake.callsOf("core.resume_torrent")
	if len(resumes) != 1 {
		t.Fatalf("core.resume_torrent calls = %d, want 1", len(resumes))
	}
	if hashes := delugeHashes(t, resumes[0]); len(hashes) != 1 || hashes[0] != "incomplete-1" {
		t.Fatalf("core.resume_torrent hashes = %v, want only the unfinished torrent", hashes)
	}

	// Everything finished: pause-all succeeds without issuing any pause.
	fake.mu.Lock()
	fake.torrents = `{
		"seeding-1": {"name":"B","state":"Seeding","progress":100.0,"total_size":10,"total_done":10,"is_finished":true}
	}`
	fake.mu.Unlock()
	rec = e.do(t, "POST", base+"/pause")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("pause-all (empty queue) status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if got := len(fake.callsOf("core.pause_torrent")); got != 1 {
		t.Fatalf("core.pause_torrent calls = %d, want still 1 (no pause of every torrent)", got)
	}
}

func TestDelugeHistoryFinishedOnly(t *testing.T) {
	fake := &delugeFake{t: t, torrents: `{
		"h1": {"name":"incomplete","state":"Downloading","progress":50.0,"total_size":10,"total_done":5,"is_finished":false,"time_added":1752700000.0},
		"h2": {"name":"older-done","state":"Seeding","progress":100.0,"total_size":100,"total_done":100,"is_finished":true,"message":"OK","time_added":1752300000.0,"completed_time":1752400000,"label":"linux"},
		"h3": {"name":"newer-done","state":"Error","progress":100.0,"total_size":200,"total_done":200,"is_finished":true,"message":"No data found","time_added":1752300000.0,"completed_time":1752600000},
		"h4": {"name":"legacy-done","state":"Seeding","progress":100.0,"total_size":300,"total_done":300,"is_finished":true,"message":"OK","time_added":1752500000.0}
	}`, status: `{"payload_download_rate":0}`}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "deluge", srv.URL, "", "", "deluge-pass")

	items := decodeHistory(t, e.do(t, "GET", "/downloads/"+inst.ID+"/history"))
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3 (incomplete excluded)", len(items))
	}
	got := items[0]
	if got.Name != "newer-done" || got.Status != "Error" || got.Error != "No data found" ||
		got.CompletedAt != time.Unix(1752600000, 0).UTC().Format(time.RFC3339) {
		t.Errorf("item[0] = %+v", got)
	}
	// Deluge 1.3 reports no completed_time; the time added orders it instead.
	if items[1].Name != "legacy-done" || items[1].CompletedAt != time.Unix(1752500000, 0).UTC().Format(time.RFC3339) {
		t.Errorf("item[1] = %+v, want legacy-done dated by time_added", items[1])
	}
	if items[2].Name != "older-done" || items[2].Status != "Seeding" ||
		items[2].Error != "" || items[2].Category != "linux" {
		t.Errorf("item[2] = %+v", items[2])
	}

	limited := decodeHistory(t, e.do(t, "GET", "/downloads/"+inst.ID+"/history?limit=1"))
	if len(limited) != 1 || limited[0].Name != "newer-done" {
		t.Errorf("limited = %+v, want only newer-done", limited)
	}
}

// TestSnapshotDelugeErrorTorrentStaysQueued pins the is_finished rule: Deluge
// reports progress 100 for any torrent in the Error state, so an unfinished
// failed download must still show in the queue, never slip into history.
func TestSnapshotDelugeErrorTorrentStaysQueued(t *testing.T) {
	fake := &delugeFake{t: t, torrents: `{
		"failed-1": {"name":"Failed","state":"Error","progress":100.0,"total_size":1000,"total_done":10,"is_finished":false,"message":"Tracker: file not found","time_added":1752500000.0}
	}`, status: `{"payload_download_rate":0}`}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "deluge", srv.URL, "", "", "deluge-pass")

	view, err := Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(view.Items) != 1 || view.Items[0].Status != "Error" || view.Items[0].SizeLeftBytes != 990 || view.Items[0].Progress != 1 {
		t.Fatalf("items = %+v, want the errored torrent queued with status Error, 990 bytes left, and progress 1 from its bytes", view.Items)
	}
	if view.Paused {
		t.Error("Paused = true, want false: an errored torrent is not a user pause")
	}
	items := decodeHistory(t, e.do(t, "GET", "/downloads/"+inst.ID+"/history"))
	if len(items) != 0 {
		t.Errorf("history = %+v, want empty", items)
	}
}

// --- ruTorrent fake ---

var rtMethodRe = regexp.MustCompile(`<methodName>([^<]+)</methodName>`)

// rtFake serves ruTorrent's httprpc endpoint: raw XML-RPC reads with canned
// rTorrent answers, and the form command protocol. rows is the XML of the
// d.multicall2 result rows.
type rtFake struct {
	t        *testing.T
	rows     string
	rate     int64
	commands string // body answered to form commands

	mu     sync.Mutex
	bodies []string // every XML-RPC request body, in order
	forms  []string // every form command body, in order
}

func rtRow(hash, name, label string, size, left, rate int64, open, active, state, complete, hashing int, message string, finished int64, addtime string) string {
	return `<value><array><data>` +
		`<value><string>` + hash + `</string></value><value><string>` + name + `</string></value><value><string>` + label + `</string></value>` +
		`<value><i8>` + strconv.FormatInt(size, 10) + `</i8></value><value><i8>` + strconv.FormatInt(left, 10) + `</i8></value>` +
		`<value><i8>` + strconv.FormatInt(size-left, 10) + `</i8></value><value><i8>` + strconv.FormatInt(rate, 10) + `</i8></value>` +
		`<value><i8>` + strconv.Itoa(open) + `</i8></value><value><i8>` + strconv.Itoa(active) + `</i8></value><value><i8>` + strconv.Itoa(state) + `</i8></value>` +
		`<value><i8>` + strconv.Itoa(complete) + `</i8></value><value><i8>` + strconv.Itoa(hashing) + `</i8></value>` +
		`<value><string>` + message + `</string></value><value><i8>` + strconv.FormatInt(finished, 10) + `</i8></value><value><string>` + addtime + `</string></value>` +
		`</data></array></value>`
}

func (f *rtFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/plugins/httprpc/action.php" {
			f.t.Errorf("rutorrent path = %s, want the httprpc endpoint", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
			f.mu.Lock()
			f.forms = append(f.forms, string(body))
			answer := f.commands
			f.mu.Unlock()
			if answer == "" {
				answer = "[0]"
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, answer)
			return
		}
		f.mu.Lock()
		f.bodies = append(f.bodies, string(body))
		rows, rate := f.rows, f.rate
		f.mu.Unlock()
		method := ""
		if m := rtMethodRe.FindStringSubmatch(string(body)); m != nil {
			method = m[1]
		}
		w.Header().Set("Content-Type", "text/xml")
		switch method {
		case "system.client_version":
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><params><param><value><string>0.16.21</string></value></param></params></methodResponse>`)
		case "throttle.global_down.rate":
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><params><param><value><i8>`+strconv.FormatInt(rate, 10)+`</i8></value></param></params></methodResponse>`)
		case "d.multicall2":
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><params><param><value><array><data>`+rows+`</data></array></value></param></params></methodResponse>`)
		default:
			_, _ = io.WriteString(w, `<?xml version="1.0"?><methodResponse><fault><value><struct><member><name>faultCode</name><value><i4>-507</i4></value></member><member><name>faultString</name><value><string>Command "`+method+`" is not allowed for untrusted connections.</string></value></member></struct></value></fault></methodResponse>`)
		}
	}
}

// methods lists the XML-RPC methods called, in order.
func (f *rtFake) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, b := range f.bodies {
		if m := rtMethodRe.FindStringSubmatch(b); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func (f *rtFake) formsSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.forms...)
}

func TestSnapshotRutorrentNormalization(t *testing.T) {
	fake := &rtFake{t: t, rate: 314159, rows: strings.Join([]string{
		rtRow("AAA1", "Fedora", "linux", 1000000, 250000, 250000, 1, 1, 1, 0, 0, "", 0, "1752500000\n"),
		rtRow("BBB2", "Done", "", 5000, 0, 0, 1, 1, 1, 1, 0, "", 1752600000, "1752500000\n"),
		rtRow("CCC3", "Paused", "", 800, 800, 0, 1, 0, 1, 0, 0, "", 0, ""),
		rtRow("DDD4", "Stopped", "", 900, 900, 0, 1, 0, 0, 0, 0, "", 0, ""),
		rtRow("EEE5", "Closed", "", 700, 700, 0, 0, 0, 1, 0, 0, "", 0, ""),
		rtRow("FFF6", "Checking", "", 600, 600, 0, 1, 1, 1, 0, 1, "", 0, ""),
	}, "")}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "rutorrent", srv.URL, "", "", "")

	view, err := Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if view.SpeedBPS != 314159 {
		t.Errorf("SpeedBPS = %d, want 314159", view.SpeedBPS)
	}
	if len(view.Items) != 5 {
		t.Fatalf("items = %d, want 5 (finished download must be excluded)", len(view.Items))
	}
	got := view.Items[0]
	want := QueueItem{
		ID:            "AAA1",
		Name:          "Fedora",
		SizeBytes:     1000000,
		SizeLeftBytes: 250000,
		Progress:      75,
		SpeedBPS:      250000,
		ETASeconds:    1, // 250000 bytes left at 250000 B/s
		Status:        "downloading",
		Category:      "linux",
	}
	if got != want {
		t.Errorf("item[0] = %+v, want %+v", got, want)
	}
	statuses := map[string]string{}
	for _, item := range view.Items {
		statuses[item.Name] = item.Status
	}
	wantStatuses := map[string]string{"Fedora": "downloading", "Paused": "paused", "Stopped": "stopped", "Closed": "stopped", "Checking": "checking"}
	for name, status := range wantStatuses {
		if statuses[name] != status {
			t.Errorf("status of %s = %q, want %q", name, statuses[name], status)
		}
	}
	if view.Paused {
		t.Error("Paused = true, want false while a download is active")
	}
	if got := fake.methods(); strings.Join(got, ",") != "d.multicall2,throttle.global_down.rate" {
		t.Errorf("methods = %v, want the multicall then the global rate", got)
	}
}

func TestSnapshotRutorrentAllHaltedMarksQueuePaused(t *testing.T) {
	fake := &rtFake{t: t, rows: rtRow("CCC3", "Paused", "", 800, 800, 0, 1, 0, 1, 0, 0, "", 0, "") +
		rtRow("DDD4", "Stopped", "", 900, 900, 0, 1, 0, 0, 0, 0, "", 0, "")}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "rutorrent", srv.URL, "", "", "")
	view, err := Snapshot(e.registry, inst)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !view.Paused {
		t.Error("Paused = false, want true when every unfinished download is paused or stopped")
	}
}

func TestRutorrentItemActions(t *testing.T) {
	fake := &rtFake{t: t}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "rutorrent", srv.URL, "", "", "")
	base := "/downloads/" + inst.ID

	if rec := e.do(t, "POST", base+"/queue/AAA1/pause"); rec.Code != http.StatusNoContent {
		t.Fatalf("pause status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "POST", base+"/queue/AAA1/resume"); rec.Code != http.StatusNoContent {
		t.Fatalf("resume status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "DELETE", base+"/queue/AAA1"); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "DELETE", base+"/queue/AAA1?deleteData=true"); rec.Code != http.StatusNoContent {
		t.Fatalf("delete-with-data status = %d (body %s)", rec.Code, rec.Body.String())
	}
	want := []string{"hash=AAA1&mode=pause", "hash=AAA1&mode=start", "hash=AAA1&mode=remove", "hash=AAA1&mode=removewithdata&v=1"}
	if got := fake.formsSeen(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("form commands =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := len(fake.methods()); n != 0 {
		t.Errorf("XML-RPC calls = %d, want 0: mutations must ride ruTorrent's trusted form commands", n)
	}

	// ruTorrent keeps the torrent when it cannot work out the files.
	fake.mu.Lock()
	fake.commands = "false"
	fake.mu.Unlock()
	rec := e.do(t, "DELETE", base+"/queue/AAA1?deleteData=true")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "kept the torrent") {
		t.Fatalf("delete-with-data on a refusing erasedata = %d %s, want 502 explaining the torrent was kept", rec.Code, rec.Body.String())
	}
}

// TestRutorrentQueueActionsOnlyTouchUnfinishedDownloads pins the guardrail:
// pause/resume-all list the unfinished downloads and send exactly those,
// never the seeding one, and send nothing when none is unfinished.
func TestRutorrentQueueActionsOnlyTouchUnfinishedDownloads(t *testing.T) {
	fake := &rtFake{t: t, rows: rtRow("INC1", "A", "", 10, 5, 0, 1, 1, 1, 0, 0, "", 0, "") +
		rtRow("INC2", "B", "", 10, 5, 0, 1, 0, 0, 0, 0, "", 0, "") +
		rtRow("SEED1", "C", "", 10, 0, 0, 1, 1, 1, 1, 0, "", 1752600000, "")}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "rutorrent", srv.URL, "", "", "")
	base := "/downloads/" + inst.ID

	if rec := e.do(t, "POST", base+"/pause"); rec.Code != http.StatusNoContent {
		t.Fatalf("pause-all status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "POST", base+"/resume"); rec.Code != http.StatusNoContent {
		t.Fatalf("resume-all status = %d (body %s)", rec.Code, rec.Body.String())
	}
	want := []string{"hash=INC1&hash=INC2&mode=pause", "hash=INC1&hash=INC2&mode=start"}
	if got := fake.formsSeen(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("form commands =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	fake.mu.Lock()
	fake.rows = rtRow("SEED1", "C", "", 10, 0, 0, 1, 1, 1, 1, 0, "", 1752600000, "")
	fake.mu.Unlock()
	if rec := e.do(t, "POST", base+"/pause"); rec.Code != http.StatusNoContent {
		t.Fatalf("pause-all (nothing unfinished) status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if got := len(fake.formsSeen()); got != 2 {
		t.Errorf("form commands = %d, want still 2 (nothing sent for an all-finished list)", got)
	}
}

func TestRutorrentHistoryCompletedOnly(t *testing.T) {
	fake := &rtFake{t: t, rows: strings.Join([]string{
		rtRow("H1", "incomplete", "", 10, 5, 0, 1, 1, 1, 0, 0, "", 0, ""),
		rtRow("H2", "older-done", "linux", 100, 0, 0, 1, 1, 1, 1, 0, "Tracker: [Timeout was reached]", 1752400000, ""),
		rtRow("H3", "newer-done", "", 200, 0, 0, 1, 0, 0, 1, 0, "Hash check on download completion found bad chunks", 1752600000, ""),
		rtRow("H4", "loaded-complete", "", 300, 0, 0, 1, 1, 1, 1, 0, "", 0, "1752500000\n"),
	}, "")}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	e := newEnv(t)
	inst := e.mkInstance(t, "rutorrent", srv.URL, "", "", "")

	items := decodeHistory(t, e.do(t, "GET", "/downloads/"+inst.ID+"/history"))
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3 (incomplete excluded)", len(items))
	}
	if items[0].Name != "newer-done" || items[0].Status != "stopped" || items[0].Error != "Hash check on download completion found bad chunks" ||
		items[0].CompletedAt != time.Unix(1752600000, 0).UTC().Format(time.RFC3339) {
		t.Errorf("item[0] = %+v", items[0])
	}
	// A download that was complete when loaded has no finished stamp; its
	// add time orders it instead.
	if items[1].Name != "loaded-complete" || items[1].CompletedAt != time.Unix(1752500000, 0).UTC().Format(time.RFC3339) {
		t.Errorf("item[1] = %+v", items[1])
	}
	// Tracker chatter is not an error.
	if items[2].Name != "older-done" || items[2].Status != "seeding" || items[2].Error != "" || items[2].Category != "linux" {
		t.Errorf("item[2] = %+v", items[2])
	}
}
