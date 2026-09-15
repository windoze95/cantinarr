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

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// Synthetic IDs reproduce the reported catalog/library split without claiming
// that a title match or the differing page counts prove identity.
func TestBookIdentityBindsStatusAndDurableDelivery(t *testing.T) {
	for _, approval := range []bool{false, true} {
		t.Run(fmt.Sprint("approval=", approval), func(t *testing.T) {
			writes := 0
			var rekeyed atomic.Bool
			books := []chaptarr.Book{
				{ID: 1, AuthorID: 7, Title: "Ahsoka", ForeignBookID: "hc:501", MediaType: "ebook", Statistics: chaptarr.BookStatistics{BookFileCount: 1}, Editions: []chaptarr.Edition{{ISBN13: "9780306406157"}}},
				{ID: 2, AuthorID: 7, Title: "Ahsoka", ForeignBookID: "hc:501", MediaType: "audiobook", Statistics: chaptarr.BookStatistics{BookFileCount: 1}},
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					http.Error(w, "unexpected write", 500)
					return
				}
				switch r.URL.Path {
				case "/api/v1/book":
					current := append([]chaptarr.Book(nil), books...)
					if rekeyed.Load() {
						for i := range current {
							current[i].ForeignBookID = "hc:502"
						}
					}
					json.NewEncoder(w).Encode(current)
				case "/api/v1/queue":
					fmt.Fprint(w, `{"records":[]}`)
				case "/api/v1/book/lookup":
					if rekeyed.Load() {
						http.Error(w, "catalog unavailable", http.StatusServiceUnavailable)
						return
					}
					fmt.Fprint(w, `[{"foreignBookId":"gr:101","title":"Ahsoka (Star Wars)","editions":[{"isbn10":"0306406152"}]}]`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			s, uid := newChaptarrBookTestService(t, upstream.URL)
			if approval {
				requireApproval(t, s)
			}
			status, err := s.GetUserBookStatusForInstance(uid, "gr:101", "", "Ahsoka (Star Wars)", "ahso")
			if err != nil || status.Status != StatusAvailable || status.CanonicalForeignID != "hc:501" {
				t.Fatalf("status=%+v err=%v", status, err)
			}
			receipt, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "gr:101", Title: "Ahsoka (Star Wars)", SearchTerm: "ahso", BookFormat: BookFormatBoth})
			if err != nil {
				t.Fatal(err)
			}
			if approval {
				s.reconcileBookApprovals(context.Background())
			} else {
				s.SweepDispatch(context.Background())
			}
			var original, title string
			if err := s.db.QueryRow(`SELECT foreign_id,title FROM request_log WHERE id=?`, receipt.RequestID).Scan(&original, &title); err != nil {
				t.Fatal(err)
			}
			if original != "gr:101" || title != "Ahsoka (Star Wars)" {
				t.Fatalf("selected metadata changed to %q %q", original, title)
			}
			var complete int
			if err := s.db.QueryRow(`SELECT count(*) FROM request_dispatch WHERE request_id=? AND state='complete' AND canonical_foreign_id='hc:501' AND book_record_id IN (1,2)`, receipt.RequestID).Scan(&complete); err != nil {
				t.Fatal(err)
			}
			if complete != 2 || writes != 0 {
				t.Fatalf("completed=%d writes=%d", complete, writes)
			}
			rekeyed.Store(true)
			s.InvalidateBookDigests(receipt.InstanceID)
			status, err = s.GetUserBookStatusForInstance(uid, "gr:101", receipt.InstanceID, "Ahsoka (Star Wars)", "ahso")
			if err != nil || status.Status != StatusAvailable || status.StatusKnown != nil && !*status.StatusKnown || status.CanonicalForeignID != "hc:502" {
				t.Fatalf("accepted record lost during catalog outage: status=%+v err=%v", status, err)
			}
			delivery, err := s.DeliveryStatus(uid, "book", "gr:101", receipt.InstanceID, nil, true)
			if err != nil || delivery.Status != StatusAvailable || delivery.StatusKnown == nil || !*delivery.StatusKnown || delivery.CanonicalForeignID != "hc:502" {
				t.Fatalf("saved delivery lost during catalog outage: delivery=%+v err=%v", delivery, err)
			}
		})
	}
}

func TestAmbiguousBookIdentityIsUnknownAndNeverMutates(t *testing.T) {
	writes := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
			http.Error(w, "unexpected write", 500)
			return
		}
		switch r.URL.Path {
		case "/api/v1/book":
			fmt.Fprint(w, `[{"id":1,"foreignBookId":"hc:501","goodreadsWorkId":"gr:101","mediaType":"ebook"},{"id":2,"foreignBookId":"hc:502","goodreadsWorkId":"gr:101","mediaType":"ebook"}]`)
		case "/api/v1/queue":
			fmt.Fprint(w, `{"records":[]}`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer upstream.Close()
	s, uid := newChaptarrBookTestService(t, upstream.URL)
	status, err := s.GetUserBookStatusForInstance(uid, "gr:101", "", "Ahsoka", "ahso")
	if err != nil || status.StatusKnown == nil || *status.StatusKnown || status.StatusUnknownReason != "identity_ambiguous" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	r, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "gr:101", Title: "Ahsoka", BookFormat: BookFormatEbook})
	if err != nil {
		t.Fatal(err)
	}
	s.SweepDispatch(context.Background())
	var state string
	if err := s.db.QueryRow(`SELECT state FROM request_dispatch WHERE request_id=?`, r.RequestID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "attention" || writes != 0 {
		t.Fatalf("state=%q writes=%d", state, writes)
	}
}

func TestVerifiedBookIdentityAddsOnlyTheMissingLibraryFormat(t *testing.T) {
	var added []chaptarr.AddBookRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book":
			fmt.Fprint(w, `[{"id":1,"authorId":7,"title":"Ahsoka","foreignBookId":"hc:501","goodreadsWorkId":"gr:101","mediaType":"ebook","monitored":true,"statistics":{"bookFileCount":1}}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/book/lookup":
			fmt.Fprint(w, `[{"title":"Ahsoka (Star Wars)","foreignBookId":"gr:101"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/author/7":
			fmt.Fprint(w, `{"id":7,"authorName":"E.K. Johnston","foreignAuthorId":"author-ekj","qualityProfileId":3,"metadataProfileId":4,"path":"/library/books"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/book":
			var body chaptarr.AddBookRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			added = append(added, body)
			fmt.Fprint(w, `{"id":2,"title":"Ahsoka","foreignBookId":"hc:501","mediaType":"audiobook","monitored":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	s, uid := newChaptarrBookTestService(t, upstream.URL)
	receipt, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: "book", ForeignID: "gr:101", Title: "Ahsoka (Star Wars)", BookFormat: BookFormatBoth, SearchTerm: "ahso"})
	if err != nil {
		t.Fatal(err)
	}
	s.SweepDispatch(context.Background())
	if len(added) != 1 || added[0].ForeignBookID != "hc:501" || added[0].MediaType != BookFormatAudiobook {
		t.Fatalf("unexpected adds: %+v", added)
	}
	var bindings int
	if err := s.db.QueryRow(`SELECT count(*) FROM request_dispatch WHERE request_id=? AND state='complete' AND canonical_foreign_id='hc:501'`, receipt.RequestID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if bindings != 2 {
		t.Fatalf("only %d formats bound", bindings)
	}
}

func TestBookDigestCarriesCompleteAuthorCountsAndTypedIDs(t *testing.T) {
	authors := []chaptarr.Author{}
	books := []chaptarr.Book{}
	for i := 1; i <= bookAuthorsMaxItems+1; i++ {
		authors = append(authors, authorRecord(i, fmt.Sprint("author-", i), fmt.Sprint("Author ", i)))
		books = append(books, authorBook(i*2, i, fmt.Sprint("book-", i), "ebook", 1), authorBook(i*2+1, i, fmt.Sprint("book-", i), "audiobook", 1))
	}
	books[0].GoodreadsWorkID = "gr:101"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/book":
			json.NewEncoder(w).Encode(books)
		case "/api/v1/author":
			json.NewEncoder(w).Encode(authors)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	s, uid := newChaptarrBookTestService(t, upstream.URL)
	digest, err := s.GetBookLibraryDigestForInstance(uid, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(digest.Authors) != bookAuthorsMaxItems+1 || len(digest.Titles) != bookAuthorsMaxItems+1 {
		t.Fatalf("digest lost rows: authors=%d titles=%d", len(digest.Authors), len(digest.Titles))
	}
	for _, a := range digest.Authors {
		if a.TitleCount != 1 || a.AvailableCount != 1 {
			t.Fatalf("counted formats: %+v", a)
		}
	}
	found := false
	for _, id := range digest.Titles[0].IdentityKeys {
		found = found || id == "gr-work:101"
	}
	if !found {
		t.Fatal("digest dropped provider identity")
	}
}

func TestBibliographyKeepsDistinctRecordsAndSortsUnconfirmedDatesLast(t *testing.T) {
	now := time.Now().Year()
	titles := []LibraryTitle{{Title: "Placeholder", Year: now + 53}, {Title: "Queen's Peril", ForeignBookID: "a", Year: 2020}, {Title: "Star Wars Queen's Peril", ForeignBookID: "b", Year: 2020}, {Title: "Announced", Year: now + 1}, {Title: "Undated"}}
	sortAuthorTitles(titles)
	if len(titles) != 5 || titles[0].Title != "Announced" || titles[1].ForeignBookID != "a" || titles[2].ForeignBookID != "b" || titles[3].Year != now+53 || titles[4].Title != "Undated" {
		t.Fatalf("wrong order or metadata lost: %+v", titles)
	}
}
