package request

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcceptedBookAddRecoversAfterWorkerRestart(t *testing.T) {
	for _, tc := range []struct {
		format  string
		records map[string]int
	}{
		{BookFormatEbook, map[string]int{BookFormatEbook: 42}},
		{BookFormatAudiobook, map[string]int{BookFormatAudiobook: 43}},
		{BookFormatBoth, map[string]int{BookFormatEbook: 42, BookFormatAudiobook: 43}},
	} {
		t.Run(tc.format, func(t *testing.T) {
			stub := newPendingImportAPIStub(t)
			var libraryReady atomic.Bool
			var adds, monitors, searches atomic.Int32
			monitored := map[int]*atomic.Bool{42: {}, 43: {}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
					adds.Add(1)
					// Chaptarr's accepted import response has a pending ID, not a book ID.
					w.WriteHeader(http.StatusAccepted)
					fmt.Fprint(w, `{"pendingId":3,"message":"Queued for import"}`)
				case libraryReady.Load() && r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
					fmt.Fprintf(w, `[
						{"id":42,"foreignBookId":"gr:253739298","title":"Waiting Book","mediaType":"ebook","monitored":%t},
						{"id":43,"foreignBookId":"gr:253739298","title":"Waiting Book","mediaType":"audiobook","monitored":%t}
					]`, monitored[42].Load(), monitored[43].Load())
				case r.Method == http.MethodPut && r.URL.Path == "/api/v1/book/monitor":
					var body struct {
						BookIDs   []int `json:"bookIds"`
						Monitored bool  `json:"monitored"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if !libraryReady.Load() || !body.Monitored || len(body.BookIDs) != 1 {
						t.Errorf("unexpected monitor before recovery: %+v", body)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					flag := monitored[body.BookIDs[0]]
					if flag == nil {
						t.Errorf("monitor used unknown book ID: %d", body.BookIDs[0])
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					flag.Store(true)
					monitors.Add(1)
					fmt.Fprint(w, `[]`)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/command":
					var body struct {
						Name    string `json:"name"`
						BookIDs []int  `json:"bookIds"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if body.Name != "BookSearch" || len(body.BookIDs) != 1 || monitored[body.BookIDs[0]] == nil || !monitored[body.BookIDs[0]].Load() {
						t.Errorf("search did not target a monitored book: %+v", body)
					}
					searches.Add(1)
					fmt.Fprint(w, `{}`)
				default:
					stub.server.Config.Handler.ServeHTTP(w, r)
				}
			}))
			defer server.Close()

			svc, uid := newChaptarrBookTestService(t, server.URL)
			out, err := svc.CreateMediaRequest(uid, &CreateRequest{
				MediaType: "book", ForeignID: "gr:253739298", Title: "Waiting Book", BookFormat: tc.format,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertDelivery := func(service *Service, state, code string, attempts int) []DeliveryState {
				t.Helper()
				states, err := service.deliveryStates(out.RequestID)
				if err != nil || len(states) != len(tc.records) {
					t.Fatalf("delivery states = %+v, err = %v", states, err)
				}
				for _, d := range states {
					if tc.records[d.Format] == 0 || d.State != state || d.Code != code || d.Attempts != attempts {
						t.Fatalf("delivery = %+v, want %s/%s at attempt %d", d, state, code, attempts)
					}
				}
				return states
			}
			assertRequest := func(status, park string) {
				t.Helper()
				var gotStatus, gotPark string
				if err := svc.db.QueryRow(`SELECT status,COALESCE(park_reason,'') FROM request_log WHERE id=?`, out.RequestID).Scan(&gotStatus, &gotPark); err != nil {
					t.Fatal(err)
				}
				if gotStatus != status || gotPark != park {
					t.Fatalf("request = %s/%s, want %s/%s", gotStatus, gotPark, status, park)
				}
			}

			svc.SweepDispatch(context.Background())
			for _, d := range assertDelivery(svc, "retry", "book_add_unconfirmed", 1) {
				if d.NextAttemptAt == nil || !d.NextAttemptAt.After(time.Now()) {
					t.Fatalf("unconfirmed add has no scheduled retry: %+v", d)
				}
			}
			assertRequest(StatusPending, "delivery")
			if got := adds.Load(); int(got) != len(tc.records) {
				t.Fatalf("initial adds = %d, want %d", got, len(tc.records))
			}
			if monitors.Load() != 0 || searches.Load() != 0 {
				t.Fatal("accepted import was monitored or searched before the book existed")
			}

			// A new worker must recover the saved schedule and observe native work
			// before replaying an add whose response only acknowledged an import.
			restarted := NewService(svc.db, svc.registry, nil, nil)
			restarted.SweepDispatch(context.Background())
			assertDelivery(restarted, "retry", "book_add_unconfirmed", 1)
			if _, err := svc.db.Exec(`UPDATE request_dispatch SET next_attempt_at=0 WHERE request_id=?`, out.RequestID); err != nil {
				t.Fatal(err)
			}
			restarted.SweepDispatch(context.Background())
			assertDelivery(restarted, "waiting_library", "author_import", 2)
			assertRequest(StatusPending, "author_import")
			restarted.sweepDeliveryImports()
			assertDelivery(restarted, "waiting_library", "author_import", 2)

			// The import eventually creates unmonitored records. Resume the same
			// requests by monitoring their exact formats, without another add.
			libraryReady.Store(true)
			stub.existsJSON.Store(`{"exists":true,"pending":false}`)
			restarted.sweepDeliveryImports()
			assertDelivery(restarted, "complete", StatusRequested, 3)
			assertRequest(StatusRequested, "")
			for format, id := range map[string]int{BookFormatEbook: 42, BookFormatAudiobook: 43} {
				if got, want := monitored[id].Load(), tc.records[format] != 0; got != want {
					t.Errorf("%s monitored = %t, want %t", format, got, want)
				}
				if tc.records[format] == 0 {
					continue
				}
				var savedID int
				if err := svc.db.QueryRow(`SELECT book_record_id FROM request_dispatch WHERE request_id=? AND format=?`, out.RequestID, format).Scan(&savedID); err != nil || savedID != id {
					t.Errorf("saved %s record = %d, err = %v; want %d", format, savedID, err, id)
				}
			}
			if int(adds.Load()) != len(tc.records) || int(monitors.Load()) != len(tc.records) || int(searches.Load()) != len(tc.records) {
				t.Fatalf("add/monitor/search calls = %d/%d/%d, want %d each", adds.Load(), monitors.Load(), searches.Load(), len(tc.records))
			}
			if stub.retryHits.Load() != 0 {
				t.Fatal("delivery restarted an active native import")
			}
		})
	}
}
