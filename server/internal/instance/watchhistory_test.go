package instance

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/tautulli"
	"github.com/windoze95/cantinarr-server/internal/tracearr"
	"github.com/windoze95/cantinarr-server/internal/watchhistory"
)

func newWatchHistoryRegistry(t *testing.T) (*Store, *Registry) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := NewStore(database, cipher)
	return store, NewRegistry(store)
}

func TestNewWatchHistoryProviderByType(t *testing.T) {
	build := func(serviceType string) watchhistory.Provider {
		t.Helper()
		p, err := NewWatchHistoryProvider(&Instance{ServiceType: serviceType, URL: "http://t", APIKey: "k"})
		if err != nil {
			t.Fatalf("NewWatchHistoryProvider(%s): %v", serviceType, err)
		}
		return p
	}
	if _, ok := build("tautulli").(*tautulli.Provider); !ok {
		t.Error("tautulli did not build a *tautulli.Provider")
	}
	if _, ok := build("tracearr").(*tracearr.Provider); !ok {
		t.Error("tracearr did not build a *tracearr.Provider")
	}
	if _, err := NewWatchHistoryProvider(&Instance{ServiceType: "radarr"}); err == nil || !strings.Contains(err.Error(), "not a watch-history instance") {
		t.Errorf("radarr error = %v, want not-a-watch-history-instance", err)
	}
}

func TestWatchHistoryTypesAreAllowedAndListed(t *testing.T) {
	for _, serviceType := range WatchHistoryTypes() {
		if !allowedServiceTypes[serviceType] {
			t.Errorf("%s is a watch-history type but not an allowed service type", serviceType)
		}
		if !strings.Contains(serviceTypeListError, "'"+serviceType+"'") {
			t.Errorf("%s missing from serviceTypeListError", serviceType)
		}
		if grantableServiceTypes[serviceType] {
			t.Errorf("%s must not be grantable: it is an admin surface", serviceType)
		}
		if !IsWatchHistoryType(serviceType) {
			t.Errorf("IsWatchHistoryType(%s) = false", serviceType)
		}
	}
	if IsWatchHistoryType("jellyfin") || IsWatchHistoryType("radarr") {
		t.Error("media servers and arrs are not watch-history types")
	}
}

func TestRegistryCachesWatchHistoryProvidersUntilInvalidated(t *testing.T) {
	store, registry := newWatchHistoryRegistry(t)
	inst := &Instance{ServiceType: "tracearr", Name: "Tracearr", URL: "http://tracearr:3000", APIKey: "trr_pub_x"}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create: %v", err)
	}
	sab := &Instance{ServiceType: "sabnzbd", Name: "SAB", URL: "http://sab", APIKey: "k"}
	if err := store.Create(sab); err != nil {
		t.Fatalf("create sabnzbd: %v", err)
	}

	first, err := registry.GetWatchHistoryProvider(inst.ID)
	if err != nil {
		t.Fatalf("GetWatchHistoryProvider: %v", err)
	}
	second, err := registry.GetWatchHistoryProvider(inst.ID)
	if err != nil {
		t.Fatalf("second GetWatchHistoryProvider: %v", err)
	}
	if first != second {
		t.Error("the same instance must hand back the cached provider")
	}
	registry.InvalidateClient(inst.ID)
	third, err := registry.GetWatchHistoryProvider(inst.ID)
	if err != nil {
		t.Fatalf("GetWatchHistoryProvider after invalidation: %v", err)
	}
	if third == first {
		t.Error("invalidation must rebuild the provider (and drop its stats cache)")
	}

	if _, err := registry.GetWatchHistoryProvider(sab.ID); err == nil || !strings.Contains(err.Error(), "not a watch-history instance") {
		t.Errorf("sabnzbd error = %v, want a wrong-type error", err)
	}
	if _, err := registry.GetWatchHistoryProvider("tracearr-missing"); err == nil || !strings.Contains(err.Error(), "instance not found") {
		t.Errorf("missing error = %v, want not found", err)
	}
}

func TestValidateConnectionForTracearr(t *testing.T) {
	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/public/health" {
			t.Errorf("path = %s, want the v1 health endpoint", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer trr_pub_ok" {
			t.Errorf("Authorization = %q, want the bearer key", r.Header.Get("Authorization"))
		}
		if status != 0 {
			http.Error(w, "nope", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"2.2.3","servers":[]}`))
	}))
	t.Cleanup(srv.Close)

	inst := &Instance{ServiceType: "tracearr", Name: "Tracearr", URL: srv.URL, APIKey: "trr_pub_ok"}
	if err := validateConnection(inst); err != nil {
		t.Fatalf("validateConnection = %v, want nil", err)
	}
	status = http.StatusUnauthorized
	if err := validateConnection(inst); err == nil || !strings.Contains(err.Error(), "rejected the API key") {
		t.Fatalf("validateConnection with a bad key = %v, want a key-rejected error", err)
	}

	if err := validateRequiredFields(&Instance{ServiceType: "tracearr", Name: "Tracearr", URL: "http://tracearr:3000"}); err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Errorf("validateRequiredFields without a key = %v, want api_key required", err)
	}
}
