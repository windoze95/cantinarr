package instance

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/httpx/httpxtest"
)

// TestValidateArrURLNeverRidesOutboundProxy: the connection test dials the
// arr directly no matter what proxy the admin installed, so a VPN-tunnelled
// proxy cannot make a healthy LAN instance look unreachable.
func TestValidateArrURLNeverRidesOutboundProxy(t *testing.T) {
	t.Cleanup(func() { httpx.SetOutboundProxy(nil) })
	proxy := httpxtest.New(t)
	httpx.SetOutboundProxy(proxy.URL())

	var hits int
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/api/v3/system/status" || r.Header.Get("X-Api-Key") != "k" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer arr.Close()

	if err := validateArrURL(arr.URL, "k", "v3"); err != nil {
		t.Fatalf("validateArrURL: %v", err)
	}
	if hits != 1 {
		t.Fatalf("arr hits = %d, want 1 direct hit", hits)
	}
	if proxyHits := proxy.Hits(); len(proxyHits) != 0 {
		t.Fatalf("the connection test reached the proxy: %+v", proxyHits)
	}
}
