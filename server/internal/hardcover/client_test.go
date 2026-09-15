package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fake serves the two operations Trending issues, checking the bearer token
// and answering the ids and hydration shapes seen live.
func fake(t *testing.T, token string, ids []int64, books []map[string]any) (*httptest.Server, *[]string) {
	t.Helper()
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"No Authorization header"}`))
			return
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		queries = append(queries, body.Query)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(body.Query, "books_trending"):
			if body.Variables["limit"] != float64(50) {
				t.Errorf("limit = %v, want 50", body.Variables["limit"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"books_trending": map[string]any{"ids": ids}}})
		case strings.Contains(body.Query, "books(where"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"books": books}})
		default:
			t.Errorf("unexpected query %q", body.Query)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &queries
}

func book(id int64, title string, extra map[string]any) map[string]any {
	m := map[string]any{"id": id, "title": title, "release_year": 2020, "rating": 4.3, "ratings_count": 10, "users_count": 20,
		"description": " Desc ", "image": map[string]any{"url": "https://assets.hardcover.app/x.jpg"},
		"contributions":            []map[string]any{{"author": map[string]any{"name": "Matt Dinniman"}}, {"author": map[string]any{"name": " "}}},
		"book_series":              []map[string]any{{"position": 1, "series": map[string]any{"name": "Dungeon Crawler Carl"}}},
		"default_physical_edition": map[string]any{"isbn_13": "9798228815889"},
		"default_ebook_edition":    map[string]any{"isbn_13": "9798228815889"},
		"default_audio_edition":    map[string]any{"isbn_13": "978-3748087335"},
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestTrendingKeepsHardcoverOrderAndDropsUnhydratedIDs(t *testing.T) {
	// Hydration comes back in a different order and misses id 3.
	srv, queries := fake(t, "tok", []int64{1, 2, 3},
		[]map[string]any{book(2, "Second", nil), book(1, "First", nil)})
	books, err := NewClientForURL(srv.URL).Trending(context.Background(), "tok", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(*queries) != 2 {
		t.Fatalf("queries = %d, want the ids call then one hydration call", len(*queries))
	}
	if len(books) != 2 || books[0].ID != 1 || books[1].ID != 2 {
		t.Fatalf("books = %+v, want ids 1,2 in trending order", books)
	}
	b := books[0]
	if b.ForeignID != "hc:1" || b.Title != "First" || b.Year != 2020 || b.ReadersCount != 20 || b.Description != "Desc" {
		t.Fatalf("book = %+v", b)
	}
	if len(b.Authors) != 1 || b.Authors[0] != "Matt Dinniman" {
		t.Fatalf("authors = %v, want blank contributor dropped", b.Authors)
	}
	if b.SeriesName != "Dungeon Crawler Carl" || b.SeriesPosition != 1 {
		t.Fatalf("series = %q #%v", b.SeriesName, b.SeriesPosition)
	}
	if b.ImageURL != "https://assets.hardcover.app/x.jpg" {
		t.Fatalf("image = %q", b.ImageURL)
	}
	// Duplicate ISBN across editions collapses; hyphens are stripped.
	if len(b.ISBN13s) != 2 || b.ISBN13s[0] != "9798228815889" || b.ISBN13s[1] != "9783748087335" {
		t.Fatalf("isbns = %v", b.ISBN13s)
	}
	keys := b.IdentityKeys()
	if keys[0] != "hc-book:1" || keys[1] != "isbn:9798228815889" {
		t.Fatalf("identity keys = %v", keys)
	}
}

func TestTrendingRejectsBooksWithoutTitleOrImageOffHTTPS(t *testing.T) {
	srv, _ := fake(t, "tok", []int64{1, 2},
		[]map[string]any{book(1, "  ", nil), book(2, "Ok", map[string]any{"image": map[string]any{"url": "http://insecure/x.jpg"}})})
	books, err := NewClientForURL(srv.URL).Trending(context.Background(), "tok", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 || books[0].ID != 2 || books[0].ImageURL != "" {
		t.Fatalf("books = %+v, want only the titled book with no insecure image", books)
	}
}

func TestTrendingEmptyListIsNotAnError(t *testing.T) {
	srv, queries := fake(t, "tok", nil, nil)
	books, err := NewClientForURL(srv.URL).Trending(context.Background(), "tok", 50)
	if err != nil || books == nil || len(books) != 0 {
		t.Fatalf("books, err = %v, %v; want an empty non-nil list", books, err)
	}
	if len(*queries) != 1 {
		t.Fatalf("queries = %d, want no hydration call for no ids", len(*queries))
	}
}

func TestTrendingReportsAuthAndProviderErrors(t *testing.T) {
	srv, _ := fake(t, "right", []int64{1}, nil)
	if _, err := NewClientForURL(srv.URL).Trending(context.Background(), "wrong", 50); err != ErrUnauthorized {
		t.Fatalf("401 → %v, want ErrUnauthorized", err)
	}

	expired := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"Could not verify JWT: JWTExpired"}]}`))
	}))
	defer expired.Close()
	if _, err := NewClientForURL(expired.URL).Trending(context.Background(), "tok", 50); err != ErrUnauthorized {
		t.Fatalf("expired JWT → %v, want ErrUnauthorized", err)
	}

	trendingError := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books_trending":{"ids":null,"error":"trending unavailable"}}}`))
	}))
	defer trendingError.Close()
	_, err := NewClientForURL(trendingError.URL).Trending(context.Background(), "tok", 50)
	if err == nil || !strings.Contains(err.Error(), "could not compute") {
		t.Fatalf("trending error → %v, want a safe provider failure, not an empty list", err)
	}

	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer limited.Close()
	if _, err := NewClientForURL(limited.URL).Trending(context.Background(), "tok", 50); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("429 → %v", err)
	}
}
