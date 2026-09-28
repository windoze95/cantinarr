package request

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

func TestExistingBookRequestConfiguresOnlyMissingFormat(t *testing.T) {
	for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
		for _, approval := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/approval=%v", format, approval), func(t *testing.T) {
				opposite := BookFormatEbook
				if format == BookFormatEbook {
					opposite = BookFormatAudiobook
				}
				var added chaptarr.AddBookRequest
				var monitored []int
				var writes atomic.Int32
				author := map[string]any{
					"id": 7, "authorName": "Author", "foreignAuthorId": "hc:200",
					opposite + "QualityProfileId": 90, opposite + "MetadataProfileId": 91,
					opposite + "RootFolderPath": "/custom/owned", opposite + "MonitorFuture": true,
					format + "MetadataProfileId": 20,
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method != http.MethodGet {
						writes.Add(1)
					}
					switch r.Method + " " + r.URL.Path {
					case "GET /api/v1/book":
						fmt.Fprintf(w, `[{"id":10,"authorId":7,"foreignBookId":"hc:100","title":"Already owned","mediaType":%q,"monitored":true,"statistics":{"bookFileCount":1}}]`, opposite)
					case "GET /api/v1/queue":
						fmt.Fprint(w, `{"records":[],"totalRecords":0}`)
					case "GET /api/v1/author/7":
						// The owned format keeps custom settings. The missing format
						// has Chaptarr's positive-ID "None" metadata profile.
						_ = json.NewEncoder(w).Encode(author)
					case "PUT /api/v1/author/7":
						if err := json.NewDecoder(r.Body).Decode(&author); err != nil {
							t.Error(err)
						}
						w.WriteHeader(http.StatusAccepted)
					case "GET /api/v1/qualityprofile":
						fmt.Fprint(w, chaptarrQualityProfiles0720)
					case "GET /api/v1/metadataprofile":
						fmt.Fprint(w, chaptarrMetadataProfiles0720)
					case "GET /api/v1/rootfolder":
						fmt.Fprint(w, chaptarrRootFolders0720)
					case "POST /api/v1/book":
						if err := json.NewDecoder(r.Body).Decode(&added); err != nil {
							t.Error(err)
						}
						fmt.Fprintf(w, `{"id":11,"authorId":7,"foreignBookId":"hc:100","mediaType":%q,"monitored":false}`, format)
					case "PUT /api/v1/book/monitor":
						var body struct {
							BookIDs []int `json:"bookIds"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						monitored = body.BookIDs
						fmt.Fprint(w, `[]`)
					case "POST /api/v1/command":
						fmt.Fprint(w, `{}`)
					default:
						t.Errorf("unexpected provider call: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer upstream.Close()
				s, uid := newChaptarrBookTestService(t, upstream.URL)
				if approval {
					requireApproval(t, s)
				}
				out, err := s.createAndDispatchForTest(uid, &CreateRequest{MediaType: "book", ForeignID: "hc:100", Title: "Already owned", BookFormat: format})
				if err != nil {
					t.Fatal(err)
				}
				if approval {
					if writes.Load() != 0 {
						t.Fatal("mutated Chaptarr before approval")
					}
					out, err = s.approveAndDispatchForTest(createTestAdmin(t, s), out.RequestID, nil)
					if err != nil {
						t.Fatal(err)
					}
				}
				if len(out.Delivery) != 1 || out.Delivery[0].State != "complete" {
					t.Fatalf("request did not complete: %+v", out)
				}
				if added.AuthorID != 7 || added.ForeignBookID != "hc:100" || added.MediaType != format || added.Author.AddOptions.Monitor != "specificBook" {
					t.Fatalf("wrong add scope: %+v", added)
				}
				config, _ := bookConfigFromAuthor(&chaptarr.Author{
					EbookQualityProfileID: added.Author.EbookQualityProfileID, AudiobookQualityProfileID: added.Author.AudiobookQualityProfileID,
					EbookMetadataProfileID: added.Author.EbookMetadataProfileID, AudiobookMetadataProfileID: added.Author.AudiobookMetadataProfileID,
					EbookRootFolderPath: added.Author.EbookRootFolderPath, AudiobookRootFolderPath: added.Author.AudiobookRootFolderPath,
				})
				q, m, root := config.forFormat(format)
				wantQ, wantM := 11, 21
				if format == BookFormatAudiobook {
					wantQ, wantM = 12, 22
				}
				if q != wantQ || m != wantM || root != "/library/"+format+"s" {
					t.Fatalf("missing format not configured: %d %d %q", q, m, root)
				}
				if q, m, root = config.forFormat(opposite); q != 90 || m != 91 || root != "/custom/owned" {
					t.Fatalf("owned format changed: %d %d %q", q, m, root)
				}
				if added.Author.EbookMonitorFuture != (opposite == BookFormatEbook) || added.Author.AudiobookMonitorFuture != (opposite == BookFormatAudiobook) {
					t.Fatal("changed the author's future-book preferences")
				}
				if len(monitored) != 1 || monitored[0] != 11 {
					t.Fatalf("monitored unrelated books: %v", monitored)
				}
			})
		}
	}
}

func TestBookConfigurationAttentionLogsReasonAndRetryRecovers(t *testing.T) {
	var ambiguous atomic.Bool
	ambiguous.Store(true)
	var adds atomic.Int32
	author := map[string]any{"id": 7, "foreignAuthorId": "hc:200", "ebookQualityProfileId": 90, "audiobookQualityProfileId": 92, "audiobookRootFolderPath": "/custom/audio", "audiobookMetadataProfileId": 20}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/book":
			fmt.Fprint(w, `[{"id":10,"authorId":7,"foreignBookId":"hc:100","title":"Owned","mediaType":"ebook","monitored":true}]`)
		case "GET /api/v1/queue":
			fmt.Fprint(w, `{"records":[],"totalRecords":0}`)
		case "GET /api/v1/author/7":
			// A partial audiobook setup must preserve its selected quality and
			// folder even when unrelated profile choices would be ambiguous.
			_ = json.NewEncoder(w).Encode(author)
		case "PUT /api/v1/author/7":
			if err := json.NewDecoder(r.Body).Decode(&author); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusAccepted)
		case "GET /api/v1/metadataprofile":
			if ambiguous.Load() {
				fmt.Fprint(w, `[{"id":20,"profileType":0},{"id":22,"profileType":1},{"id":23,"profileType":1}]`)
			} else {
				fmt.Fprint(w, chaptarrMetadataProfiles0720)
			}
		case "POST /api/v1/book":
			adds.Add(1)
			var body chaptarr.AddBookRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Author.AudiobookQualityProfileID != 92 || body.Author.AudiobookRootFolderPath != "/custom/audio" || body.Author.AudiobookMetadataProfileID != 22 || body.Author.EbookMetadataProfileID != 0 {
				t.Errorf("overwrote existing choices or configured an unrequested format: %+v", body.Author)
			}
			fmt.Fprint(w, `{"id":11,"foreignBookId":"hc:100","mediaType":"audiobook","monitored":true}`)
		default:
			t.Errorf("unexpected provider call: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	s, uid := newChaptarrBookTestService(t, upstream.URL)
	out, err := s.createAndDispatchForTest(uid, &CreateRequest{MediaType: "book", ForeignID: "hc:100", Title: "Owned", BookFormat: BookFormatAudiobook})
	if err != nil || len(out.Delivery) != 1 {
		t.Fatalf("save: %+v %v", out, err)
	}
	state := out.Delivery[0]
	if state.State != "attention" || state.Code != "book_configuration" || !strings.Contains(state.Message, "a metadata profile for this audiobook") || adds.Load() != 0 {
		t.Fatalf("unsafe or unexplained config failure: %+v adds=%d", state, adds.Load())
	}
	for _, want := range []string{"request_id=1", "user_id=1", "instance_id=", "format=audiobook", "state=attention", "code=book_configuration", "a metadata profile for this audiobook"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("log missing %q: %s", want, &logs)
		}
	}
	ambiguous.Store(false)
	if _, err = s.DeliveryAction(context.Background(), uid, out.RequestID, "retry", ""); err != nil {
		t.Fatal(err)
	}
	s.SweepDispatch(context.Background())
	states, err := s.deliveryStates(out.RequestID)
	if err != nil || len(states) != 1 || states[0].State != "complete" || adds.Load() != 1 {
		t.Fatalf("retry: %+v %v adds=%d", states, err, adds.Load())
	}
	s.SweepDispatch(context.Background())
	if adds.Load() != 1 {
		t.Fatal("completed request added twice")
	}
}
