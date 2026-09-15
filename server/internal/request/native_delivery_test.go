package request

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

func TestNativeHTTPAcknowledgesAndReadsSavedStateWhileChaptarrStalls(t *testing.T) {
	for _, approval := range []bool{false, true} {
		for _, format := range []string{"ebook", "audiobook", "both"} {
			t.Run(fmt.Sprintf("%s/approval=%v", format, approval), func(t *testing.T) {
				blocked, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					once.Do(func() { close(blocked) })
					<-release
					w.WriteHeader(503)
				}))
				s, uid := newChaptarrBookTestService(t, upstream.URL)
				if approval {
					requireApproval(t, s)
				}
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(func() { cancel(); close(release); upstream.Close(); s.dispatchMu.Lock(); s.dispatchMu.Unlock() })
				h := NewHandler(s)
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: uid, Role: "user"}))
					if r.Method == "POST" {
						h.Create(w, r)
					} else {
						h.GetDelivery(w, r)
					}
				}))
				defer api.Close()
				client := &http.Client{Timeout: time.Second}
				s.StartDispatchMaintenance(ctx)
				payload := fmt.Sprintf(`{"media_type":"book","foreign_id":"gr:48297245","title":"The Subtle Art of Not Giving a Fuck","search_term":"Mark Manson","book_format":%q}`, format)
				post := func() CreateResponse {
					t.Helper()
					start := time.Now()
					resp, err := client.Post(api.URL, "application/json", strings.NewReader(payload))
					if err != nil {
						t.Fatalf("acknowledgement exceeded one second: %v", err)
					}
					defer resp.Body.Close()
					var out CreateResponse
					if json.NewDecoder(resp.Body).Decode(&out) != nil || resp.StatusCode != 200 || out.RequestID == 0 {
						t.Fatalf("unsaved receipt: %+v HTTP %d", out, resp.StatusCode)
					}
					t.Logf("acknowledged %s in %s", format, time.Since(start))
					return out
				}
				first := post()
				select {
				case <-blocked:
				case <-time.After(time.Second):
					t.Fatal("saved request did not wake worker")
				}
				if second := post(); second.RequestID != first.RequestID {
					t.Fatal("duplicate tap created another request")
				}
				for _, query := range []string{fmt.Sprintf("request_id=%d", first.RequestID), "media_type=book&foreign_id=gr:48297245&instance_id=" + first.InstanceID} {
					resp, err := client.Get(api.URL + "?include_live=false&" + query)
					if err != nil {
						t.Fatalf("saved read waited for Chaptarr: %v", err)
					}
					var out CreateResponse
					err = json.NewDecoder(resp.Body).Decode(&out)
					resp.Body.Close()
					if err != nil || resp.StatusCode != 200 || out.StatusKnown == nil || *out.StatusKnown {
						t.Fatalf("saved read claimed live truth: %+v %v", out, err)
					}
				}
				var id, title, term string
				if err := s.db.QueryRow(`SELECT foreign_id,title,search_term FROM request_log WHERE id=?`, first.RequestID).Scan(&id, &title, &term); err != nil || id != "gr:48297245" || title != "The Subtle Art of Not Giving a Fuck" || term != "Mark Manson" {
					t.Fatalf("selected identity changed: %q %q %q %v", id, title, term, err)
				}
			})
		}
	}
}

func TestRevokedDuringNativeLookupPreventsEveryWrite(t *testing.T) {
	var s *Service
	var uid int64
	var writes atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == "/api/v1/book" {
			_, err := s.db.Exec(`DELETE FROM user_default_instances WHERE user_id=?`, uid)
			if err != nil {
				t.Error(err)
			}
			fmt.Fprint(w, `[{"id":7,"foreignBookId":"gr:48297245","title":"Selected","mediaType":"ebook","monitored":false}]`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer upstream.Close()
	s, uid = newChaptarrBookTestService(t, upstream.URL)
	out, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", Title: "Selected", ForeignID: "gr:48297245", BookFormat: "ebook"})
	if err != nil {
		t.Fatal(err)
	}
	s.SweepDispatch(context.Background())
	states, _ := s.deliveryStates(out.RequestID)
	if writes.Load() != 0 || states[0].Code != "access_unavailable" || states[0].State != "attention" {
		t.Fatalf("revocation not enforced: writes=%d states=%+v", writes.Load(), states)
	}
}

func TestRetiredSourceSubmissionNeverReadsChaptarr(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("retired source reached a provider") }))
	defer upstream.Close()
	s, uid := newChaptarrBookTestService(t, upstream.URL)
	r := httptest.NewRequest("POST", "/api/requests", strings.NewReader(`{"media_type":"book","catalog_ref":{"provider":"openlibrary","id":"OL17590212W"}}`))
	r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: uid, Role: "user"}))
	w := httptest.NewRecorder()
	NewHandler(s).Create(w, r)
	var body map[string]string
	if w.Code != 410 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["code"] != "catalog_retired" {
		t.Fatalf("retirement: %d %s", w.Code, w.Body.String())
	}
}

func TestNativeRetryOnlyRequeuesTheSelectedFormat(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("saving a format retry contacted Chaptarr")
	}))
	defer upstream.Close()
	s, uid := newChaptarrBookTestService(t, upstream.URL)
	out, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "gr:48297245", Title: "Selected", BookFormat: "both"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE request_dispatch SET state='attention',attempts=3,code='import_failed' WHERE request_id=?`, out.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeliveryAction(context.Background(), uid, out.RequestID, "retry", "", "ebook"); err != nil {
		t.Fatal(err)
	}
	states, err := s.deliveryStates(out.RequestID)
	if err != nil || len(states) != 2 {
		t.Fatalf("format state: %+v %v", states, err)
	}
	for _, state := range states {
		if state.Format == "ebook" && (state.State != "queued" || state.Attempts != 0) {
			t.Fatalf("selected retry was not queued: %+v", state)
		}
		if state.Format == "audiobook" && (state.State != "attention" || state.Attempts != 3) {
			t.Fatalf("retry changed the other format: %+v", state)
		}
	}
}
