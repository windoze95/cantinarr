package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

// Guards the router assumption: the static `cover` route must win over the
// `/*` proxy wildcard, otherwise cover requests silently fall through to the
// generic proxy (which can't fetch the login-gated image) and show no art.
func TestCoverRouteBeatsProxyWildcard(t *testing.T) {
	r := chi.NewRouter()
	var hit string
	r.Get("/instances/{instanceID}/cover", func(http.ResponseWriter, *http.Request) { hit = "cover" })
	r.HandleFunc("/instances/{instanceID}/*", func(http.ResponseWriter, *http.Request) { hit = "wildcard" })

	r.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/instances/abc/cover", nil))
	if hit != "cover" {
		t.Fatalf("/instances/abc/cover hit %q, want cover", hit)
	}
	hit = ""
	r.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/instances/abc/api/v1/book", nil))
	if hit != "wildcard" {
		t.Fatalf("/instances/abc/api/v1/book hit %q, want wildcard", hit)
	}
}

func TestSanitizeCoverPath(t *testing.T) {
	cases := []struct {
		in     string
		wantOK bool
		want   string
	}{
		{"/MediaCover/Books/541/cover.jpg?lastWrite=1", true, "/MediaCover/Books/541/cover.jpg?lastWrite=1"},
		{"/MediaCoverProxy/abc/x.jpg", true, "/MediaCoverProxy/abc/x.jpg"},
		{"MediaCover/Books/1/cover.jpg", true, "/MediaCover/Books/1/cover.jpg"}, // leading slash added
		{"", false, ""},
		{"/api/v1/config/host", false, ""},          // not a cover path
		{"/MediaCover/../api/v1/config", false, ""}, // traversal
		{"http://evil.example/x.jpg", false, ""},    // absolute url
		{"//evil.example/x.jpg", false, ""},         // host-relative
	}
	for _, c := range cases {
		got, ok := sanitizeCoverPath(c.in)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("sanitizeCoverPath(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestSessionLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("username") == "admin" && r.FormValue("password") == "secret" {
			http.SetCookie(w, &http.Cookie{Name: "ReadarrAuth", Value: "tok123"})
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusFound)
			return
		}
		w.Header().Set("Location", "/login?returnUrl=&loginFailed=true")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	sc := newSessionCache()
	cookie, err := sc.login(srv.URL, "admin", "secret")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(cookie, "ReadarrAuth=tok123") {
		t.Errorf("cookie = %q, want it to contain ReadarrAuth=tok123", cookie)
	}
	if _, err := sc.login(srv.URL, "admin", "wrong"); err == nil {
		t.Error("expected error on bad credentials (loginFailed)")
	}
}

func TestFetchCoverLoginsCachesAndReauths(t *testing.T) {
	var logins int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login":
			logins++
			http.SetCookie(w, &http.Cookie{Name: "auth", Value: "fresh"})
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/MediaCover"):
			c := r.Header.Get("Cookie")
			if c == "" || strings.Contains(c, "stale") {
				w.Header().Set("Location", "/login")
				w.WriteHeader(http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("JPEGDATA"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	inst := &instance.Instance{ID: "i1", URL: srv.URL, Username: "u", Password: "p"}
	sc := newSessionCache()

	// First fetch logs in once and returns the image.
	resp, err := sc.fetchCover(inst, srv.URL+"/MediaCover/x.jpg")
	if err != nil {
		t.Fatalf("fetchCover: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "JPEGDATA" {
		t.Errorf("body = %q, want JPEGDATA", body)
	}
	if logins != 1 {
		t.Errorf("logins = %d, want 1", logins)
	}

	// Second fetch reuses the cached cookie — no new login.
	resp2, err := sc.fetchCover(inst, srv.URL+"/MediaCover/x.jpg")
	if err != nil {
		t.Fatalf("fetchCover 2: %v", err)
	}
	resp2.Body.Close()
	if logins != 1 {
		t.Errorf("logins after cached fetch = %d, want 1", logins)
	}

	// A stale cookie triggers exactly one re-login, then succeeds.
	sc.set("i1", "auth=stale")
	resp3, err := sc.fetchCover(inst, srv.URL+"/MediaCover/x.jpg")
	if err != nil {
		t.Fatalf("fetchCover stale: %v", err)
	}
	resp3.Body.Close()
	if logins != 2 {
		t.Errorf("logins after stale = %d, want 2", logins)
	}
}
