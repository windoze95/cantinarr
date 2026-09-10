package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

func TestListeningBookUsesAuthorizedAvailableAudioRecords(t *testing.T) {
	books := []chaptarr.Book{
		{ID: 1, ForeignBookID: "hc:1", MediaType: "audiobook", Statistics: chaptarr.BookStatistics{BookFileCount: 1}, Editions: []chaptarr.Edition{
			{ASIN: "B012345678", ISBN10: "0306406152", Format: "Audiobook"},
			{ASIN: "B111111111", Format: "Ebook"},
		}},
		{ID: 2, ForeignBookID: "hc:1", MediaType: "ebook", Statistics: chaptarr.BookStatistics{BookFileCount: 1}, Editions: []chaptarr.Edition{{ASIN: "B222222222"}}},
		{ID: 3, ForeignBookID: "hc:other", MediaType: "audiobook", Statistics: chaptarr.BookStatistics{BookFileCount: 1}, Editions: []chaptarr.Edition{{ASIN: "B333333333"}}},
		{ID: 4, ForeignBookID: "hc:pending", MediaType: "audiobook", Editions: []chaptarr.Edition{{ASIN: "B444444444"}}},
	}
	calls := 0
	listCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/api/v1/book" {
			listCalls++
			_ = json.NewEncoder(w).Encode(books)
		} else if r.URL.Path == "/api/v1/book/1" {
			_ = json.NewEncoder(w).Encode(books[0])
		} else {
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	s, uid := newChaptarrBookTestService(t, upstream.URL)
	_, id, err := s.resolveChaptarr(uid, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, badID := range []string{"", "not-granted"} {
		if _, err := s.ResolveListeningBook(context.Background(), uid, badID, "hc:1"); !errors.Is(err, mediaserver.ErrBookAccess) {
			t.Fatalf("ungranted source: %v", err)
		}
	}
	if calls != 0 {
		t.Fatal("ungranted source contacted")
	}
	q, err := s.ResolveListeningBook(context.Background(), uid, id, "hc:1")
	if err != nil || !q.Available || !reflect.DeepEqual(q.ASINs, []string{"B012345678"}) || !reflect.DeepEqual(q.ISBNs, []string{"9780306406157"}) {
		t.Fatalf("query=%+v err=%v", q, err)
	}
	for _, foreign := range []string{"hc:missing", "hc:pending"} {
		q, err := s.ResolveListeningBook(context.Background(), uid, id, foreign)
		if err != nil || q.Available {
			t.Fatalf("not downloaded: %+v %v", q, err)
		}
	}
	// A cached available state is merely a locator. Removal or re-keying of
	// the current record must suppress listening even before cache expiry.
	s.cacheBookProjection("book-live:"+id, &bookLiveProjection{Formats: map[string]map[string]string{}, Records: map[int]bookLiveRecord{1: {ForeignID: "hc:1", Format: "audiobook", Status: StatusAvailable}}})
	before := listCalls
	books[0].Statistics.BookFileCount = 0
	q, err = s.ResolveListeningBook(context.Background(), uid, id, "hc:1")
	if err != nil || q.Available {
		t.Fatalf("stale availability trusted: %+v %v", q, err)
	}
	books[0].Statistics.BookFileCount = 1
	books[0].ForeignBookID = "hc:replacement"
	q, err = s.ResolveListeningBook(context.Background(), uid, id, "hc:1")
	if err != nil || q.Available {
		t.Fatalf("stale identity trusted: %+v %v", q, err)
	}
	if listCalls != before {
		t.Fatal("cached record locator reloaded the entire library")
	}
}

func TestListeningBookLoadsSeparateAudioEditions(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%t", cached), func(t *testing.T) {
			book := chaptarr.Book{ID: 7, ForeignBookID: "gr:603000101", MediaType: "audiobook", Statistics: chaptarr.BookStatistics{BookFileCount: 1}}
			status := http.StatusOK
			editionCalls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/book":
					_ = json.NewEncoder(w).Encode([]chaptarr.Book{book})
				case "/api/v1/book/7":
					_ = json.NewEncoder(w).Encode(book)
				case "/api/v1/edition":
					editionCalls++
					if r.URL.Query().Get("bookId") != "7" {
						t.Error("edition lookup did not target the available book")
					}
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode([]chaptarr.Edition{
						{BookID: 7, ASIN: "B012345678", ISBN10: "0306406152", Format: "Audiobook"},
						{BookID: 7, ASIN: "B111111111", Format: "Ebook"},
						{BookID: 8, ASIN: "B222222222", Format: "Audiobook"},
					})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer upstream.Close()
			s, uid := newChaptarrBookTestService(t, upstream.URL)
			_, id, err := s.resolveChaptarr(uid, "")
			if err != nil {
				t.Fatal(err)
			}
			if cached {
				s.cacheBookProjection("book-live:"+id, &bookLiveProjection{Records: map[int]bookLiveRecord{7: {ForeignID: book.ForeignBookID, Format: BookFormatAudiobook, Status: StatusAvailable}}})
			}
			q, err := s.ResolveListeningBook(context.Background(), uid, id, book.ForeignBookID)
			if err != nil || !q.Available || !reflect.DeepEqual(q.ASINs, []string{"B012345678"}) || !reflect.DeepEqual(q.ISBNs, []string{"9780306406157"}) || editionCalls != 1 {
				t.Fatalf("separate audio editions: query=%+v err=%v edition calls=%d", q, err, editionCalls)
			}
			status = http.StatusServiceUnavailable
			if _, err := s.ResolveListeningBook(context.Background(), uid, id, book.ForeignBookID); err == nil {
				t.Fatal("edition outage was treated as an empty identifier list")
			}
		})
	}
}
