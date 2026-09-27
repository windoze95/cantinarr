package request

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

var requesterTagsCanary = flag.String("requester-tags-canary", "", "private manifest for owned disposable requester tag instances")

// Opt-in proof against real providers. The named canary must have no download
// clients or indexers; only the selected fixture author's books may be present.
func TestLiveDisposableRequesterTags(t *testing.T) {
	if *requesterTagsCanary == "" {
		t.Skip("pass -requester-tags-canary with disposable service credentials")
	}
	raw, err := os.ReadFile(*requesterTagsCanary)
	if err != nil {
		t.Fatal(err)
	}
	var configs map[string]catalogCanaryInstance
	if json.Unmarshal(raw, &configs) != nil {
		t.Fatal("invalid manifest")
	}
	for _, kind := range []string{"lidarr", "chaptarr"} {
		t.Run(kind, func(t *testing.T) {
			cfg, ok := configs[kind]
			if !ok {
				t.Skip("provider not selected")
			}
			target, e := url.Parse(cfg.URL)
			if e != nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" || !strings.HasPrefix(cfg.Container, "cantinarr-677-") {
				t.Fatal("requires owned loopback canary")
			}
			nativeHTTP := &http.Client{Transport: httpx.Internal()}
			read := func(path string, out any) {
				t.Helper()
				req, _ := http.NewRequest("GET", cfg.URL+path, nil)
				req.Header.Set("X-Api-Key", cfg.Key)
				resp, e := nativeHTTP.Do(req)
				if e != nil {
					t.Fatal("native read failed")
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(out) != nil {
					t.Fatalf("native read %s HTTP %d", path, resp.StatusCode)
				}
			}
			for _, collection := range []string{"indexer", "downloadclient"} {
				var list []any
				read("/api/v1/"+collection, &list)
				if len(list) != 0 {
					t.Fatal("canary must not download media")
				}
			}
			var version map[string]any
			read("/api/v1/system/status", &version)
			t.Logf("native %s %v", kind, version["version"])
			child, parent, foreign, media, format := "album", "artist", "1b022e01-4da6-387b-8658-8678046e4cef", "music", ""
			if kind == "chaptarr" {
				child, parent, foreign, media, format = "book", "author", "hc:242987", "book", "both"
			}
			var children []map[string]any
			read("/api/v1/"+child, &children)
			for _, item := range children {
				if kind == "lidarr" {
					if item["foreignAlbumId"] != foreign {
						t.Fatal("unexpected native music fixture")
					}
				} else if item["authorId"] != float64(1) {
					t.Fatal("unexpected native book fixture")
				}
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			proxy.Transport = httpx.Internal()
			var outage atomic.Bool
			var mediaWrites atomic.Int64
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" && (strings.HasPrefix(r.URL.Path, "/api/v1/"+child) || r.URL.Path == "/api/v1/command") {
					mediaWrites.Add(1)
				}
				if outage.Load() && r.URL.Path == "/api/v1/tag" {
					w.WriteHeader(503)
					return
				}
				proxy.ServeHTTP(w, r)
			}))
			defer gateway.Close()
			cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{0x37}, 32))
			dbPath := filepath.Join(t.TempDir(), "tags.db")
			database, e := db.Open(dbPath)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { database.Close() }()
			store := instance.NewStore(database, cipher)
			inst := &instance.Instance{ServiceType: kind, Name: "Tags canary", URL: gateway.URL, APIKey: cfg.Key, TagRequests: true}
			if e = store.Create(inst); e != nil {
				t.Fatal(e)
			}
			users := []int64{}
			for _, name := range []string{"native-tag-reader", "native-tag-second"} {
				res, e := database.Exec(`INSERT INTO users(username,password_hash,role) VALUES(?,'','user')`, name)
				if e != nil {
					t.Fatal(e)
				}
				uid, _ := res.LastInsertId()
				users = append(users, uid)
				if e = store.SetUserDefault(uid, kind, inst.ID); e != nil {
					t.Fatal(e)
				}
			}
			s := NewService(database, instance.NewRegistry(store), nil, nil)
			requireApproval(t, s)
			admin := createTestAdmin(t, s)
			req := &CreateRequest{MediaType: media, ForeignID: foreign, BookFormat: format, Title: "Native tags fixture", InstanceID: inst.ID}
			out, e := s.CreateMediaRequest(users[0], req)
			if e != nil {
				t.Fatal(e)
			}
			requestIDs := []int64{out.RequestID}
			if kind == "chaptarr" {
				req.BookFormat = "ebook"
			}
			second, e := s.CreateMediaRequest(users[1], req)
			if e != nil {
				t.Fatal(e)
			}
			if kind == "chaptarr" {
				if second.RequestID != out.RequestID {
					t.Fatal("expected shared book")
				}
			} else {
				requestIDs = append(requestIDs, second.RequestID)
			}
			for _, rid := range requestIDs {
				if _, e = s.ApproveRequest(admin, rid, nil); e != nil {
					t.Fatal(e)
				}
			}
			s.SweepDispatch(context.Background())
			for _, rid := range requestIDs {
				states, _ := s.deliveryStates(rid)
				for _, state := range states {
					if state.State != "complete" {
						t.Fatalf("native delivery incomplete: %+v", states)
					}
				}
			}
			var parentID int
			database.QueryRow(`SELECT book_record_id FROM request_dispatch WHERE request_id=? ORDER BY format LIMIT 1`, out.RequestID).Scan(&parentID)
			if parentID <= 0 {
				t.Fatal("native delivery omitted child binding")
			}
			var delivered map[string]any
			read(fmt.Sprintf("/api/v1/%s/%d", child, parentID), &delivered)
			parentID = int(delivered[parent+"Id"].(float64))
			parentPath := fmt.Sprintf("/api/v1/%s/%d", parent, parentID)
			var before map[string]any
			read(parentPath, &before)
			// Tag outage must not undo or repeat the already delivered requests.
			beforeMediaWrites := mediaWrites.Load()
			outage.Store(true)
			s.SweepRequesterTags(context.Background())
			var retrying int
			database.QueryRow(`SELECT COUNT(*) FROM request_tag_jobs WHERE state='retrying'`).Scan(&retrying)
			want := 2
			if kind == "chaptarr" {
				want = 3
			}
			if retrying != want {
				t.Fatalf("want %d durable retries, got %d", want, retrying)
			}
			database.Close()
			database, e = db.Open(dbPath)
			if e != nil {
				t.Fatal(e)
			}
			s = NewService(database, instance.NewRegistry(instance.NewStore(database, cipher)), nil, nil)
			outage.Store(false)
			database.Exec(`UPDATE request_tag_jobs SET next_attempt_at=0`)
			s.SweepRequesterTags(context.Background())
			if kind == "lidarr" {
				// A new album triggers asynchronous RefreshArtist. An immediate
				// successful tag read can be overwritten by its older snapshot.
				// Wait for native completion, then retry only unfinished jobs.
				deadline := time.Now().Add(2 * time.Minute)
				for {
					var commands []struct {
						Name   string `json:"name"`
						Status string `json:"status"`
					}
					read("/api/v1/command", &commands)
					busy := false
					for _, command := range commands {
						if (command.Name == "RefreshArtist" || command.Name == "BulkRefreshArtist") && command.Status != "completed" && command.Status != "failed" && command.Status != "aborted" && command.Status != "cancelled" {
							busy = true
						}
					}
					if !busy {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("native artist refresh did not settle")
					}
					time.Sleep(time.Second)
				}
				database.Exec(`UPDATE request_tag_jobs SET next_attempt_at=0 WHERE state='retrying'`)
				s.SweepRequesterTags(context.Background())
			}
			var applied int
			database.QueryRow(`SELECT COUNT(*) FROM request_tag_jobs WHERE state='applied'`).Scan(&applied)
			if applied != want {
				rows, _ := database.Query(`SELECT state,message FROM request_tag_jobs`)
				defer rows.Close()
				for rows.Next() {
					var state, message string
					rows.Scan(&state, &message)
					t.Logf("tag %s: %s", state, message)
				}
				t.Fatalf("want %d applied native receipts, got %d", want, applied)
			}
			if mediaWrites.Load() != beforeMediaWrites {
				t.Fatal("tag retry resubmitted media")
			}
			var after map[string]any
			read(parentPath, &after)
			var allParents []map[string]any
			read("/api/v1/"+parent, &allParents)
			listed := false
			for _, item := range allParents {
				if item["id"] == float64(parentID) {
					listed = true
					for _, key := range []string{"tags", "ebookTags", "audiobookTags"} {
						if !reflect.DeepEqual(item[key], after[key]) {
							t.Fatalf("native list and detail disagree on %s: %v / %v", key, item[key], after[key])
						}
					}
				}
			}
			if !listed {
				t.Fatal("native parent missing from library list")
			}
			for _, key := range []string{"monitored", "path", "rootFolderPath", "qualityProfileId", "metadataProfileId", "monitorNewItems", "ebookMonitored", "audiobookMonitored", "ebookMonitorNewItems", "audiobookMonitorNewItems", "ebookQualityProfileId", "audiobookQualityProfileId", "ebookMetadataProfileId", "audiobookMetadataProfileId", "ebookRootFolderPath", "audiobookRootFolderPath", "syncMonitoredAcrossFormats", "ebookSettingsManuallyOverridden", "audiobookSettingsManuallyOverridden"} {
				if !reflect.DeepEqual(before[key], after[key]) {
					t.Fatalf("native setting changed: %s: %v -> %v", key, before[key], after[key])
				}
			}
			for _, key := range []string{"tags", "ebookTags", "audiobookTags"} {
				if tags, ok := before[key].([]any); ok {
					afterTags, _ := after[key].([]any)
					for _, tag := range tags {
						found := false
						for _, got := range afterTags {
							found = found || tag == got
						}
						if !found {
							t.Fatalf("removed existing %s tag", key)
						}
					}
				}
			}
			var tags []map[string]any
			read("/api/v1/tag", &tags)
			for _, uid := range users {
				label := fmt.Sprintf("cantinarr-%d-", uid)
				tagID := float64(0)
				for _, tag := range tags {
					if strings.HasPrefix(tag["label"].(string), label) {
						tagID = tag["id"].(float64)
					}
				}
				if tagID == 0 {
					t.Fatal("requester tag missing")
				}
				key := "tags"
				if kind == "chaptarr" {
					key = "ebookTags"
				}
				found := false
				for _, tag := range after[key].([]any) {
					found = found || tag == tagID
				}
				if !found {
					t.Fatalf("requester %d tag missing on parent", uid)
				}
			}
			s.SweepRequesterTags(context.Background())
			t.Logf("PASS: %d requester/format receipts, native list/detail tags agree after refresh, settings preserved, database restart recovered, no media replay", applied)
		})
	}
}
