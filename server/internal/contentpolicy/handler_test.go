package contentpolicy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func newHandlerEnv(t *testing.T) (*testEnv, http.Handler) {
	t.Helper()
	env := newTestEnv(t)
	h := NewHandler(env.svc)
	r := chi.NewRouter()
	r.Get("/api/admin/users/{userID}/content-policy", h.GetUserPolicy)
	r.Put("/api/admin/users/{userID}/content-policy", h.PutUserPolicy)
	r.Delete("/api/admin/users/{userID}/content-policy", h.DeleteUserPolicy)
	r.Get("/api/admin/certifications", h.Certifications)
	return env, r
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerPolicyLifecycle(t *testing.T) {
	env, h := newHandlerEnv(t)
	kid := env.user(t, "kid", "user")
	admin := env.user(t, "admin", "admin")
	path := func(id int64) string { return "/api/admin/users/" + itoa64(id) + "/content-policy" }

	if rec := do(t, h, http.MethodGet, path(kid), ""); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not a kids account") {
		t.Fatalf("GET before: %d %s", rec.Code, rec.Body.String())
	}

	body := `{"max_movie_rating":"pg-13","max_tv_rating":"TV-14","rating_region":"us","block_unrated":false,"blocked_movie_genres":[27],"blocked_tv_genres":[]}`
	rec := do(t, h, http.MethodPut, path(kid), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	var stored Policy
	if err := json.Unmarshal(rec.Body.Bytes(), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.MaxMovieRating != "PG-13" || stored.RatingRegion != "US" || stored.BlockUnrated || len(stored.BlockedMovieGenres) != 1 {
		t.Fatalf("stored = %+v", stored)
	}
	if !strings.Contains(rec.Body.String(), `"blocked_tv_genres":[]`) {
		t.Fatalf("empty genre list must encode as []: %s", rec.Body.String())
	}

	if rec := do(t, h, http.MethodGet, path(kid), ""); rec.Code != http.StatusOK {
		t.Fatalf("GET after: %d", rec.Code)
	}

	if rec := do(t, h, http.MethodPut, path(admin), body); rec.Code != http.StatusConflict {
		t.Fatalf("PUT admin: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, http.MethodPut, path(9999), body); rec.Code != http.StatusNotFound {
		t.Fatalf("PUT unknown user: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, path(kid), `{"max_movie_rating":"12A","max_tv_rating":"TV-14","rating_region":"US"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT foreign cert: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, http.MethodPut, path(kid), `{"max_movie_rating":"PG","max_tv_rating":"TV-14","rating_region":"ZZ"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown region: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, path(kid), `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT bad body: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/admin/users/abc/content-policy", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT bad id: %d", rec.Code)
	}

	if rec := do(t, h, http.MethodDelete, path(kid), ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "cleared") {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, http.MethodDelete, path(kid), ""); rec.Code != http.StatusOK {
		t.Fatalf("second DELETE: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, path(kid), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET after delete: %d", rec.Code)
	}
}

func TestHandlerCertifications(t *testing.T) {
	_, h := newHandlerEnv(t)
	rec := do(t, h, http.MethodGet, "/api/admin/certifications", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("certifications: %d", rec.Code)
	}
	var resp CertificationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Source != SourceTMDB || len(resp.Movie["US"]) == 0 || len(resp.TV["GB"]) == 0 {
		t.Fatalf("resp = %+v", resp)
	}
	if !strings.Contains(rec.Body.String(), `"default":true`) {
		t.Fatal("US defaults should be marked")
	}
}

func itoa64(i int64) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + itoaInt(int(i)))
}

func itoaInt(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
