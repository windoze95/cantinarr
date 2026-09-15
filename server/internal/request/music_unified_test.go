package request

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/musicdiscovery"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const musicA = "11111111-1111-4111-8111-111111111111"
const musicB = "22222222-2222-4222-8222-222222222222"
const musicC = "33333333-3333-4333-8333-333333333333"

func TestNativeMusicHTTPAcknowledgesBeforeStalledProviders(t *testing.T) {
	for _, approval := range []bool{false, true} {
		t.Run(fmt.Sprint(approval), func(t *testing.T) {
			release := make(chan struct{})
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); <-release; http.Error(w, "stalled", 503) }))
			defer upstream.Close()
			defer close(release)
			s, uid, id := newLidarrMusicTestService(t, upstream.URL)
			if approval {
				requireApproval(t, s)
			}
			request := httptest.NewRequest("POST", "/api/requests", strings.NewReader(fmt.Sprintf(`{"media_type":"music","foreign_id":%q,"title":"Single","instance_id":%q}`, musicA, id)))
			request = request.WithContext(context.WithValue(request.Context(), auth.ClaimsKey, &auth.Claims{UserID: uid, Role: auth.RoleUser}))
			response := httptest.NewRecorder()
			start := time.Now()
			NewHandler(s).Create(response, request)
			if time.Since(start) >= time.Second || response.Code != 201 && response.Code != 200 {
				t.Fatalf("ack %v: %d %s", time.Since(start), response.Code, response.Body.String())
			}
			var out CreateResponse
			if json.Unmarshal(response.Body.Bytes(), &out) != nil || out.RequestID == 0 || len(out.Delivery) != 1 {
				t.Fatalf("missing durable receipt: %s", response.Body.String())
			}
			if calls.Load() != 0 {
				t.Fatal("intake contacted a provider")
			}
			want := "queued"
			if approval {
				want = "approval"
			}
			if out.Delivery[0].State != want {
				t.Fatal(out.Delivery)
			}
			start = time.Now()
			saved, err := s.DeliveryStatus(uid, "music", musicA, id, nil, false)
			if err != nil || saved.RequestID != out.RequestID || time.Since(start) >= time.Second || calls.Load() != 0 {
				t.Fatalf("saved read waited on Lidarr: %+v %v", saved, err)
			}
		})
	}
}

func TestMusicNativeAndCatalogDoubleTapsSharePendingApproval(t *testing.T) {
	s, uid, id := newLidarrMusicTestService(t, "http://not-contacted.invalid")
	requireApproval(t, s)
	var wg sync.WaitGroup
	ids := make(chan int64, 20)
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := &CreateRequest{MediaType: "music", ForeignID: musicA, Title: "Same title", InstanceID: id}
			if i%2 == 0 {
				req.ForeignID = ""
				req.CatalogRef = &CatalogRef{Provider: "musicbrainz", ID: musicA}
			}
			out, err := s.CreateMediaRequest(uid, req)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- out.RequestID
		}(i)
	}
	wg.Wait()
	close(ids)
	var first int64
	for id := range ids {
		if first == 0 {
			first = id
		}
		if id != first {
			t.Fatal("duplicate pending work", id, first)
		}
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM request_log`).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	for _, ref := range []*CatalogRef{nil, {Provider: "musicbrainz", ID: musicA}} {
		saved, err := s.DeliveryStatus(uid, "music", musicA, id, ref, false)
		if err != nil || saved.RequestID != first || saved.Delivery[0].State != "approval" {
			t.Fatalf("legacy/native read diverged: %+v %v", saved, err)
		}
	}
}

func TestMusicPendingLegacyAndVerifiedAliasesPreserveIdentity(t *testing.T) {
	s, uid, id := newLidarrMusicTestService(t, "http://not-contacted.invalid")
	res, err := s.db.Exec(`INSERT INTO request_log(user_id,media_type,tmdb_id,foreign_id,instance_id,title,status) VALUES (?,'music',0,?,?,'Same title','pending')`, uid, musicA, id)
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := res.LastInsertId()
	before, err := s.DeliveryStatus(uid, "music", musicA, id, nil, false)
	if err != nil || before.RequestID != legacy || len(before.Delivery) != 1 || before.Delivery[0].State != "approval" {
		t.Fatalf("legacy saved read: %+v %v", before, err)
	}
	out, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", CatalogRef: &CatalogRef{Provider: "musicbrainz", ID: musicA}, InstanceID: id, Title: "Same title"})
	if err != nil || out.RequestID != legacy || out.Delivery[0].State != "approval" {
		t.Fatalf("legacy approval lost: %+v %v", out, err)
	}
	distinct, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: musicB, InstanceID: id, Title: "Same title"})
	if err != nil || distinct.RequestID == legacy {
		t.Fatal("same-title identities merged")
	}
	// Only server-verified canonical bindings connect aliases.
	s.db.Exec(`UPDATE request_dispatch SET canonical_foreign_id=? WHERE request_id=?`, musicC, legacy)
	canonical, err := s.DeliveryStatus(uid, "music", musicC, id, nil, false)
	if err != nil || canonical.RequestID != legacy {
		t.Fatalf("verified alias not followed: %+v %v", canonical, err)
	}
}

type unifiedCatalogStub struct {
	musicdiscovery.Catalog
	body    []byte
	err     error
	singles bool
}

func (c *unifiedCatalogStub) Search(_ context.Context, _ string, _ int, include ...bool) ([]byte, error) {
	c.singles = len(include) > 0 && include[0]
	return c.body, c.err
}

func TestUnifiedMusicSearchMatchesIDsKeepsOrderAndLibraryOnlyReleases(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"id":9,"foreignAlbumId":%q,"title":"Same title","monitored":true,"statistics":{"trackCount":2,"trackFileCount":2}},{"id":10,"foreignAlbumId":%q,"title":"Same title","albumType":"Single"}]`, musicB, musicC)
	}))
	defer upstream.Close()
	s, uid, id := newLidarrMusicTestService(t, upstream.URL)
	body, _ := json.Marshal(musicdiscovery.Page{Page: 1, Results: []musicdiscovery.Album{{ForeignID: musicA, Title: "Same title"}, {ForeignID: musicB, Title: "Same title"}}})
	catalog := &unifiedCatalogStub{body: body}
	s.MusicCatalog = catalog
	out, err := s.UnifiedMusicSearch(context.Background(), uid, "Same title", id, 1)
	if err != nil || len(out.Results) != 3 || !catalog.singles {
		t.Fatalf("unified results %+v %v", out, err)
	}
	if out.Results[0].ForeignID != musicA || out.Results[0].Status != StatusUnavailable || out.Results[1].ForeignID != musicB || out.Results[1].Status != StatusAvailable || out.Results[2].ForeignID != musicC || out.Results[2].RecordID != 10 {
		t.Fatalf("identity/order drift: %+v", out)
	}
	catalog.err = fmt.Errorf("outage")
	partial, err := s.UnifiedMusicSearch(context.Background(), uid, "Same title", id, 1)
	if err != nil || len(partial.Results) != 2 || len(partial.Warnings) != 1 {
		t.Fatalf("usable library results lost: %+v %v", partial, err)
	}
}

func TestMusicRevokedWhileLibraryLoadsPreventsEveryMutation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var mutations atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
		}
		if r.URL.Path == "/api/v1/album" {
			close(started)
			<-release
			fmt.Fprintf(w, `[{"id":7,"foreignAlbumId":%q,"title":"Single","monitored":false,"albumType":"Single"}]`, musicA)
			return
		}
		if r.URL.Path == "/api/v1/queue" {
			fmt.Fprint(w, `{"records":[]}`)
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()
	s, uid, id := newLidarrMusicTestService(t, upstream.URL)
	out, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "music", ForeignID: musicA, Title: "Single", InstanceID: id})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.SweepDispatch(context.Background()); close(done) }()
	<-started
	if _, err = s.db.Exec(`DELETE FROM user_default_instances WHERE user_id=?`, uid); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	if mutations.Load() != 0 {
		t.Fatal("revoked user changed Lidarr")
	}
	states, _ := s.deliveryStates(out.RequestID)
	if states[0].State != "attention" {
		t.Fatalf("revoked delivery: %+v", states)
	}
	if _, err = s.SavedMusicRequests(uid, id); err == nil {
		t.Fatal("revoked saved read was allowed")
	}
}
