package remoteaccess

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	projectdb "github.com/windoze95/cantinarr-server/internal/db"
)

func testService(t *testing.T, overrides EnvironmentOverrides) *Service {
	t.Helper()
	database, err := projectdb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	service, err := NewService(database, overrides)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func request(method, target, remote string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remote
	return req
}

func boolPointer(value bool) *bool { return &value }

func TestCompatibilityModeIsCompleteNoOp(t *testing.T) {
	service := testService(t, EnvironmentOverrides{})
	called := false
	handler := service.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := request(http.MethodPost, "http://cantinarr.test/api/auth/login", "198.51.100.10:45000")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if !called || recorder.Code != http.StatusNoContent {
		t.Fatalf("compatibility request called=%v status=%d", called, recorder.Code)
	}
	status := service.StatusFor(req)
	if status.ConnectionSecurity != ConnectionRemoteHTTP || status.HTTPSRequired || !status.WouldCurrentConnectionBeAllowed {
		t.Fatalf("status = %+v", status)
	}
}

func TestStrictModeBlocksBeforeHandlerAndKeepsGuidanceReachable(t *testing.T) {
	service := testService(t, EnvironmentOverrides{
		RequireHTTPSOutsideLocalNetwork: boolPointer(true),
	})
	called := false
	handler := service.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, "/api/auth/login", http.StatusUpgradeRequired},
		{http.MethodGet, "/api/config", http.StatusUpgradeRequired},
		{http.MethodGet, "/oauth/authorize", http.StatusUpgradeRequired},
		{http.MethodGet, "/api/auth/status", http.StatusNoContent},
		{http.MethodGet, "/api/health", http.StatusNoContent},
		{http.MethodGet, "/", http.StatusNoContent},
		{http.MethodGet, "/settings", http.StatusNoContent},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			called = false
			req := request(tc.method, "http://cantinarr.test"+tc.path, "198.51.100.10:45000")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.want, recorder.Body.String())
			}
			if called != (tc.want == http.StatusNoContent) {
				t.Fatalf("handler called = %v", called)
			}
		})
	}
}

func TestStrictModeDoesNotReadRejectedCredentialBody(t *testing.T) {
	service := testService(t, EnvironmentOverrides{
		RequireHTTPSOutsideLocalNetwork: boolPointer(true),
	})
	body := &countingReader{Reader: strings.NewReader(`{"password":"secret"}`)}
	req := request(http.MethodPost, "http://cantinarr.test/api/auth/login", "203.0.113.10:46000")
	req.Body = io.NopCloser(body)
	recorder := httptest.NewRecorder()
	service.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("rejected request reached handler")
	})).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUpgradeRequired || body.reads != 0 {
		t.Fatalf("status=%d body reads=%d", recorder.Code, body.reads)
	}
}

type countingReader struct {
	io.Reader
	reads int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads++
	return r.Reader.Read(p)
}

func TestLocalAddressClassificationAndCGNATOptIn(t *testing.T) {
	for _, tc := range []struct {
		remote     string
		trustCGNAT bool
		want       ConnectionSecurity
	}{
		{"127.0.0.1:1", false, ConnectionLocalHTTP},
		{"192.168.1.20:1", false, ConnectionLocalHTTP},
		{"10.20.30.40:1", false, ConnectionLocalHTTP},
		{"172.20.0.4:1", false, ConnectionLocalHTTP},
		{"169.254.1.4:1", false, ConnectionLocalHTTP},
		{"[fd12::4]:1", false, ConnectionLocalHTTP},
		{"[fe80::4]:1", false, ConnectionLocalHTTP},
		{"100.64.1.4:1", false, ConnectionRemoteHTTP},
		{"100.64.1.4:1", true, ConnectionLocalHTTP},
		{"203.0.113.4:1", true, ConnectionRemoteHTTP},
	} {
		t.Run(tc.remote, func(t *testing.T) {
			settings, err := applyOverrides(Settings{TrustCGNATAsLocal: tc.trustCGNAT}, EnvironmentOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			req := request(http.MethodGet, "http://cantinarr.test/api/auth/status", tc.remote)
			if got := classify(req, settings); got != tc.want {
				t.Fatalf("classify() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestTrustedProxyUsesRightmostUntrustedHop(t *testing.T) {
	settings, err := applyOverrides(Settings{
		TrustedProxyCIDRs: []string{"172.20.0.0/16"},
	}, EnvironmentOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	req := request(http.MethodGet, "http://cantinarr.test/api/auth/status", "172.20.0.2:1234")
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-For", "10.0.0.9, 203.0.113.8")
	if got := classify(req, settings); got != ConnectionRemoteHTTP {
		t.Fatalf("spoofed local hop classified as %s", got)
	}

	req.Header.Set("X-Forwarded-Proto", "https")
	if got := classify(req, settings); got != ConnectionHTTPS {
		t.Fatalf("trusted HTTPS proxy classified as %s", got)
	}

	req.RemoteAddr = "172.21.0.2:1234"
	if got := classify(req, settings); got != ConnectionUntrustedProxy {
		t.Fatalf("untrusted proxy classified as %s", got)
	}
}

func TestTrustedProxyClientProtocolWinsOverBackendTLS(t *testing.T) {
	settings, err := applyOverrides(Settings{
		TrustedProxyCIDRs: []string{"172.20.0.2"},
	}, EnvironmentOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	req := request(http.MethodGet, "https://cantinarr.internal/api/auth/status", "172.20.0.2:1234")
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-For", "203.0.113.8")

	if got := classify(req, settings); got != ConnectionRemoteHTTP {
		t.Fatalf("HTTP client behind TLS backend classified as %s", got)
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	if got := classify(req, settings); got != ConnectionHTTPS {
		t.Fatalf("HTTPS client behind TLS backend classified as %s", got)
	}
}

func TestUpdatePreventsLockoutAndAcceptsProposedTrustedProxy(t *testing.T) {
	service := testService(t, EnvironmentOverrides{})
	remote := request(http.MethodPut, "http://cantinarr.test/api/admin/remote-access-security", "203.0.113.10:4000")
	_, err := service.Update(remote, Settings{RequireHTTPSOutsideLocalNetwork: true})
	if !errors.Is(err, ErrWouldBlockCurrentConnection) {
		t.Fatalf("Update() error = %v", err)
	}

	proxied := request(http.MethodPut, "http://cantinarr.test/api/admin/remote-access-security", "172.20.0.2:4000")
	proxied.Header.Set("X-Forwarded-Proto", "https")
	proxied.Header.Set("X-Forwarded-For", "203.0.113.10")
	status, err := service.Update(proxied, Settings{
		RequireHTTPSOutsideLocalNetwork: true,
		TrustedProxyCIDRs:               []string{"172.20.0.2"},
	})
	if err != nil {
		t.Fatalf("Update() through proposed proxy: %v", err)
	}
	if status.ConnectionSecurity != ConnectionHTTPS || !status.WouldCurrentConnectionBeAllowed {
		t.Fatalf("status = %+v", status)
	}
}

func TestEnvironmentFalseRecoversStoredStrictPolicy(t *testing.T) {
	service := testService(t, EnvironmentOverrides{})
	local := request(http.MethodPut, "http://cantinarr.test/api/admin/remote-access-security", "192.168.1.2:4000")
	if _, err := service.Update(local, Settings{RequireHTTPSOutsideLocalNetwork: true}); err != nil {
		t.Fatal(err)
	}

	recovery := false
	restarted, err := NewService(service.db, EnvironmentOverrides{
		RequireHTTPSOutsideLocalNetwork: &recovery,
	})
	if err != nil {
		t.Fatal(err)
	}
	status := restarted.StatusFor(request(http.MethodGet, "http://cantinarr.test/api/auth/status", "203.0.113.5:4000"))
	if status.RequireHTTPSOutsideLocalNetwork || status.HTTPSRequired || !status.ManagedByEnvironment.RequireHTTPSOutsideLocalNetwork {
		t.Fatalf("recovery status = %+v", status)
	}
}

func TestTrustedProxyValidation(t *testing.T) {
	for _, value := range []string{"not-an-address", "0.0.0.0/0", "::/0"} {
		t.Run(value, func(t *testing.T) {
			_, err := normalizeSettings(Settings{TrustedProxyCIDRs: []string{value}})
			if err == nil {
				t.Fatal("normalizeSettings() error = nil")
			}
		})
	}
	normalized, err := normalizeSettings(Settings{TrustedProxyCIDRs: []string{"172.20.0.2", "172.20.0.2/32"}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(strings.Join(normalized.TrustedProxyCIDRs, ",")), []byte("172.20.0.2/32")) {
		t.Fatalf("canonical proxies = %#v", normalized.TrustedProxyCIDRs)
	}
	mapped, err := normalizeSettings(Settings{TrustedProxyCIDRs: []string{"::ffff:192.168.1.0/120"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(mapped.TrustedProxyCIDRs) != 1 || mapped.TrustedProxyCIDRs[0] != "192.168.1.0/24" {
		t.Fatalf("mapped IPv4 proxy = %#v", mapped.TrustedProxyCIDRs)
	}
}
