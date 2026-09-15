package request

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// This explicitly opted-in journey mutates disposable local services. The
// manifest must point at empty instances with writable root folders, profiles,
// and no indexers or download clients. It is never used by the default suite.
var catalogCanary = flag.String("catalog-canary", "", "private JSON manifest for disposable loopback Chaptarr/Lidarr instances")

type catalogCanaryInstance struct {
	URL, Key, Container string
	// Explicitly seeded, unmonitored copies of the selected native book. This
	// exercises real monitoring/search writes without claiming a new metadata import.
	ExistingBookFixture bool
}

func TestLiveDisposableCatalogDelivery(t *testing.T) {
	if *catalogCanary == "" {
		t.Skip("pass -catalog-canary with disposable service credentials")
	}
	body, err := os.ReadFile(*catalogCanary)
	if err != nil {
		t.Fatal(err)
	}
	var configs map[string]catalogCanaryInstance
	if json.Unmarshal(body, &configs) != nil {
		t.Fatal("invalid canary manifest")
	}
	for _, serviceType := range []string{"lidarr", "chaptarr"} {
		t.Run(serviceType, func(t *testing.T) {
			cfg, present := configs[serviceType]
			if !present {
				t.Skip("service not included in disposable manifest")
			}
			target, err := url.Parse(cfg.URL)
			if err != nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" || cfg.Key == "" {
				t.Fatal("canary must use a disposable loopback service")
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			proxy.Transport = httpx.Internal()
			var outage atomic.Bool
			outage.Store(serviceType == "lidarr")
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if outage.Load() {
					w.Header().Set("Retry-After", "600")
					w.WriteHeader(503)
					return
				}
				proxy.ServeHTTP(w, r)
			}))
			defer gateway.Close()
			cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{0x37}, 32))
			databasePath := filepath.Join(t.TempDir(), "requests.db")
			database, err := db.Open(databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { database.Close() }()
			store := instance.NewStore(database, cipher)
			inst := &instance.Instance{ServiceType: serviceType, Name: "Disposable canary", URL: gateway.URL, APIKey: cfg.Key}
			if err = store.Create(inst); err != nil {
				t.Fatal(err)
			}
			result, err := database.Exec(`INSERT INTO users(username,password_hash,role) VALUES('canary','','user')`)
			if err != nil {
				t.Fatal(err)
			}
			uid, _ := result.LastInsertId()
			if err = store.SetUserDefault(uid, serviceType, inst.ID); err != nil {
				t.Fatal(err)
			}
			s := NewService(database, instance.NewRegistry(store), nil, nil)
			req := &CreateRequest{MediaType: "music", Title: "Nevermind", InstanceID: inst.ID, ForeignID: "1b022e01-4da6-387b-8658-8678046e4cef"}
			if serviceType == "chaptarr" {
				req = &CreateRequest{MediaType: "book", Title: "The Subtle Art of Not Giving a Fuck", BookFormat: "both", InstanceID: inst.ID, ForeignID: "gr:48297245", SearchTerm: "The Subtle Art of Not Giving a Fuck"}
			}
			// Refuse to run against a populated library, even on loopback.
			outage.Store(false)
			if serviceType == "chaptarr" {
				client, _, _ := s.resolveChaptarr(uid, inst.ID)
				books, e := client.GetAllBooks()
				if e != nil {
					t.Fatal("canary book library is not readable")
				}
				if cfg.ExistingBookFixture {
					if len(books) != 2 {
						t.Fatal("expected exactly two unmonitored native fixture records")
					}
					for _, book := range books {
						if book.ForeignBookID != req.ForeignID || book.Monitored || book.Statistics.BookFileCount > 0 {
							t.Fatal("book library does not contain the expected unmonitored fixture")
						}
					}
				} else if len(books) != 0 {
					t.Fatal("canary book library is not empty or readable")
				}
			} else {
				client, _, _ := s.resolveLidarr(uid, inst.ID)
				albums, e := client.GetAllAlbums()
				if e != nil || len(albums) != 0 {
					t.Fatal("canary music library is not empty or readable")
				}
			}
			outage.Store(serviceType == "lidarr")
			started := time.Now()
			out, err := s.CreateMediaRequest(uid, req)
			{
				t.Logf("native acknowledgement: %s", time.Since(started))
				if time.Since(started) > time.Second {
					t.Fatal("native acknowledgement exceeded one second")
				}
			}
			if err != nil || out.RequestID == 0 {
				t.Fatalf("request was not saved: %v", err)
			}
			s.SweepDispatch(context.Background())
			states, err := s.deliveryStates(out.RequestID)
			if err != nil || len(states) == 0 {
				t.Fatal("delivery intent lost")
			}
			if serviceType == "lidarr" {
				if states[0].State != "retry" || states[0].NextAttemptAt == nil {
					t.Fatalf("outage was not retained: %+v", states)
				}
				// Reopen the actual database and rebuild both service and registry.
				database.Close()
				database, err = db.Open(databasePath)
				if err != nil {
					t.Fatal(err)
				}
				s = NewService(database, instance.NewRegistry(instance.NewStore(database, cipher)), nil, nil)
				outage.Store(false)
				database.Exec(`UPDATE request_dispatch SET next_attempt_at=0`)
				s.SweepDispatch(context.Background())
				states, _ = s.deliveryStates(out.RequestID)
				if states[0].State != "complete" {
					t.Fatalf("live Lidarr delivery did not complete: %+v", states)
				}
				client, _, _ := s.resolveLidarr(uid, inst.ID)
				albums, e := client.GetAllAlbums()
				if e != nil {
					t.Fatal(e)
				}
				matches := 0
				for _, album := range albums {
					if album.ForeignAlbumID == req.ForeignID {
						matches++
						if !album.Monitored {
							t.Fatal("requested release is not monitored")
						}
					} else if album.Monitored {
						t.Fatalf("sibling release was monitored: %s", album.ForeignAlbumID)
					}
				}
				if matches != 1 {
					t.Fatalf("expected one exact live album, got %d", matches)
				}
				t.Log("saved through HTTP 503, reopened database, delivered one exact release group to live Lidarr with every sibling unmonitored")
			} else {
				deadline := time.Now().Add(5 * time.Minute)
				previous := ""
				for {
					s.SweepParkedBookRequests()
					states, _ = s.deliveryStates(out.RequestID)
					ready := len(states) == 2
					for _, state := range states {
						ready = ready && state.State == "complete"
					}
					if ready {
						break
					}
					for _, state := range states {
						if state.State == "attention" {
							t.Fatalf("native delivery requires attention: %+v", states)
						}
					}
					if time.Now().After(deadline) {
						t.Fatalf("native delivery did not complete: %+v", states)
					}
					snapshot, _ := json.Marshal(states)
					if string(snapshot) != previous {
						t.Logf("native delivery still waiting: %+v", states)
						previous = string(snapshot)
					}
					time.Sleep(5 * time.Second)
					s.SweepDispatch(context.Background())
				}
				client, _, _ := s.resolveChaptarr(uid, inst.ID)
				books, err := client.GetAllBooks()
				if err != nil {
					t.Fatal(err)
				}
				formats := map[string]int{}
				for _, book := range books {
					if book.Monitored {
						if book.ForeignBookID != req.ForeignID {
							t.Fatalf("different title was targeted: %s", book.ForeignBookID)
						}
						formats[book.MediaType]++
					}
				}
				if formats["ebook"] != 1 || formats["audiobook"] != 1 {
					t.Fatalf("expected one exact record per format: %v", formats)
				}
				t.Log("delivered one eBook and one audiobook for gr:48297245 to real Chaptarr")
			}
		})
	}
}
