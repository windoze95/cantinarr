package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/httpx/httpxtest"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// TestInstanceProxyTransportClassByServiceType pins which instances ride the
// admin's outbound proxy through the reverse proxy: a Plex instance (plex.tv,
// an internet host) does; an arr instance (a LAN host) never does, even with
// the proxy installed.
func TestInstanceProxyTransportClassByServiceType(t *testing.T) {
	t.Cleanup(func() { httpx.SetOutboundProxy(nil) })
	proxy := httpxtest.New(t)
	proxy.SetResponse(http.StatusOK, `{"via":"proxy"}`)
	httpx.SetOutboundProxy(proxy.URL())

	var arrHits int
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"via":"direct"}`))
	}))
	defer arr.Close()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	radarr := &instance.Instance{ServiceType: "radarr", Name: "Movies", URL: arr.URL, APIKey: "k"}
	if err := store.Create(radarr); err != nil {
		t.Fatalf("create radarr: %v", err)
	}
	// A Plex instance's URL is plex.tv in production; here an http:// stand-in
	// so the fake proxy sees an absolute-form request rather than a CONNECT.
	plex := &instance.Instance{ServiceType: "plex", Name: "Plex", URL: "http://plex.example.invalid", APIKey: "token"}
	if err := store.Create(plex); err != nil {
		t.Fatalf("create plex: %v", err)
	}

	r := chi.NewRouter()
	r.HandleFunc("/api/instances/{instanceID}/*", NewHandler(store).InstanceProxy())
	srv := httptest.NewServer(r)
	defer srv.Close()

	fetch := func(id string) string {
		t.Helper()
		resp, err := http.Get(srv.URL + "/api/instances/" + id + "/api/v3/system/status")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
		}
		return string(body)
	}

	if body := fetch(radarr.ID); body != `{"via":"direct"}` || arrHits != 1 {
		t.Fatalf("radarr answered %q with %d direct hits; want a direct answer", body, arrHits)
	}
	if hits := proxy.Hits(); len(hits) != 0 {
		t.Fatalf("the arr request reached the proxy: %+v", hits)
	}

	if body := fetch(plex.ID); body != `{"via":"proxy"}` {
		t.Fatalf("plex answered %q; want the proxy's answer", body)
	}
	hits := proxy.Hits()
	if len(hits) != 1 || hits[0].Target != "http://plex.example.invalid/api/v3/system/status" {
		t.Fatalf("proxy hits = %+v; want the plex request in absolute form", hits)
	}
}
