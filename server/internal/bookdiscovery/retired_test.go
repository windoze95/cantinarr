package bookdiscovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

func TestRetiredRoutesReturnTheSameStableAction(t *testing.T) {
	h := NewHandler()
	for name, endpoint := range map[string]http.HandlerFunc{"feed": h.Feed, "search": h.Search, "book": h.Book, "genres": h.Genres, "request-target": h.RequestTarget} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/?instance_id=missing&q=book", nil)
			r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: 1, Role: "user"}))
			w := httptest.NewRecorder()
			endpoint(w, r)
			var body map[string]string
			if w.Code != http.StatusGone || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["code"] != Code || body["search_path"] != "/dashboard/books" {
				t.Fatalf("retired response: %d %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("retirement must replace cached catalog responses")
			}
		})
	}
	unauthorized := httptest.NewRecorder()
	h.Search(unauthorized, httptest.NewRequest("GET", "/", nil))
	if unauthorized.Code != 401 {
		t.Fatal("retired endpoints still require authentication")
	}
}
