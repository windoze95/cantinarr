package tdarr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadContractAndNormalization(t *testing.T) {
	var pies atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "TEST_KEY" {
			t.Error("missing API key")
		}
		switch r.URL.Path {
		case "/base/api/v2/status":
			io.WriteString(w, `{"status":"good","version":"2.62.01"}`)
		case "/base/api/v2/get-nodes":
			if r.Method != "GET" {
				t.Error("node read must be GET")
			}
			io.WriteString(w, `{"n":{"nodeName":"Node","nodePaused":true,"config":{"apiKey":"NEVER_RETURN"},"workers":{
			"a":{"file":"C:\\media\\one.mkv","workerType":"transcodegpu","percentage":42.5,"fps":123,"ETA":"0:02:00","status":"Transcoding","isFlowWorker":true},
			"b":{"file":"/media/one.mkv","workerType":"healthcheckcpu"},
			"c":{"idle":true,"file":"old.mkv"},
			"d":{"file":"unknown.mkv","workerType":"future","percentage":101,"fps":-1}}}}`)
		case "/base/api/v2/cruddb":
			var request struct {
				Data struct {
					Collection, Mode, DocID string
					Obj                     map[string]any
				}
			}
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&request) != nil {
				t.Error("invalid read body")
			}
			if len(request.Data.Obj) != 0 {
				t.Error("read contains update fields")
			}
			switch request.Data.Collection {
			case "LibrarySettingsJSONDB":
				if request.Data.Mode != "getAll" {
					t.Error("library mutation")
				}
				io.WriteString(w, `[{"_id":"lib","name":"Movies","folder":"SECRET_CONFIG"},{"_id":"lib2","name":"Movies"}]`)
			case "StatisticsJSONDB":
				if request.Data.Mode != "getById" || request.Data.DocID != "statistics" {
					t.Error("stats mutation")
				}
				io.WriteString(w, `{"totalFileCount":10,"table1Count":3,"table2Count":4,"table3Count":2}`)
			default:
				t.Error("unexpected collection")
			}
		case "/base/api/v2/stats/get-pies":
			pies.Add(1)
			var request struct {
				Data struct {
					LibraryID string `json:"libraryId"`
				}
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Data.LibraryID != "lib" {
				t.Error("wrong library scope")
			}
			io.WriteString(w, `{"pieStats":{"totalFiles":10,"status":{"transcode":[{"name":"Not required","value":4},{"name":"Future state","value":1}],"healthcheck":[]}}}`)
		default:
			t.Errorf("unexpected upstream operation: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL+"/base/", "TEST_KEY")
	ctx := context.Background()
	if err := c.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	activity, err := c.Activity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workers := activity.Nodes[0].Workers
	if !activity.Nodes[0].Paused || len(workers) != 3 || workers[0].Compute != "GPU" || *workers[0].Progress != 42.5 || !workers[0].Flow || workers[1].Kind != "Health check" || workers[1].Progress != nil || workers[2].Progress != nil || workers[2].FPS != nil {
		t.Fatalf("wrong normalization: %+v", workers)
	}
	encoded, _ := json.Marshal(activity)
	if strings.Contains(string(encoded), "NEVER_RETURN") || strings.Contains(string(encoded), "config") {
		t.Fatal("raw node config leaked")
	}
	libs, err := c.Libraries(ctx)
	if err != nil || len(libs.Items) != 2 {
		t.Fatalf("distinct libraries were lost: %v %v", libs, err)
	}
	stats, err := c.Stats(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalFiles != 10 || stats.Transcodes[1].Label != "Success / not required" || stats.HealthChecks[0].Value != nil || !strings.Contains(stats.Note, "awaiting acceptance") {
		t.Fatalf("wrong global counts: %+v", stats)
	}
	stats, err = c.Stats(ctx, "lib")
	if err != nil || stats.Transcodes[1].Label != "Future state" {
		t.Fatalf("unknown state lost: %v %v", stats, err)
	}
	if pies.Load() != 1 {
		t.Fatal("per-library read not cached")
	}
	if _, err := c.Stats(ctx, "other"); !errors.Is(err, ErrLibraryNotFound) || pies.Load() != 1 {
		t.Fatal("unknown library was read")
	}
}

func TestFailuresNeverBecomeEmptySuccessOrLeakSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"auth", "TEST_KEY http://private-host", 401}, {"forbidden", "TEST_KEY", 403},
		{"redirect", "TEST_KEY", 302}, {"missing", "", 404}, {"busy", "TEST_KEY", 429},
		{"offline", "TEST_KEY", 503}, {"html", "<html>login</html>", 200},
		{"null", "null", 200}, {"wrong object", `{"error":"TEST_KEY"}`, 200},
		{"missing workers", `{"n":{"nodeName":"node"}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://private-host/?key=TEST_KEY")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			_, err := NewClient(srv.URL, "TEST_KEY").Activity(context.Background())
			if err == nil || strings.Contains(err.Error(), "TEST_KEY") || strings.Contains(err.Error(), "private-host") || strings.Contains(err.Error(), srv.URL) {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
}

func TestCacheExpiryCoalescingCancellationAndRecovery(t *testing.T) {
	var reads atomic.Int32
	var fail atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := reads.Add(1)
		if n == 1 {
			close(entered)
			<-release
		}
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	var tick atomic.Int64
	c.now = func() time.Time { return time.Unix(tick.Load(), 0) }
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := c.Activity(ctx); first <- err }()
	<-entered
	cancel()
	if <-first == nil {
		t.Fatal("cancelled caller succeeded")
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := c.Activity(context.Background())
			if err != nil || a == nil || len(a.Nodes) != 0 {
				t.Errorf("shared read failed: %v", err)
			}
		}()
	}
	close(release)
	wg.Wait()
	if reads.Load() != 1 {
		t.Fatalf("concurrent reads = %d", reads.Load())
	}
	tick.Store(6)
	fail.Store(true)
	if _, err := c.Activity(context.Background()); err == nil {
		t.Fatal("expired cache hid outage")
	}
	fail.Store(false)
	if _, err := c.Activity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 3 {
		t.Fatalf("failure was cached: reads=%d", reads.Load())
	}
}

func TestMissingStatsAreUnknownNotZero(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"totalFileCount":-1}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		_, err := NewClient(srv.URL, "").Stats(context.Background(), "")
		srv.Close()
		if err == nil {
			t.Errorf("accepted incomplete statistics %s", body)
		}
	}
}

// Optional live contract check against the disposable acceptance service. No
// writes occur here; sample-library setup belongs to the acceptance harness.
func TestLiveTdarrReadContract(t *testing.T) {
	addr := os.Getenv("CANTINARR_TEST_TDARR_URL")
	if addr == "" {
		t.Skip("set CANTINARR_TEST_TDARR_URL for disposable-service acceptance")
	}
	c := NewClient(addr, os.Getenv("CANTINARR_TEST_TDARR_KEY"))
	ctx := context.Background()
	if err := c.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := c.Activity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	libs, err := c.Libraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Stats(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, lib := range libs.Items {
		if _, err := c.Stats(ctx, lib.ID); err != nil {
			t.Fatal(err)
		}
	}
	workers := 0
	for _, n := range a.Nodes {
		workers += len(n.Workers)
	}
	t.Logf("live read: nodes=%d active_workers=%d libraries=%d files=%d", len(a.Nodes), workers, len(libs.Items), s.TotalFiles)
}
