package nzbget

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

// TestCallSendsJSONRPCEnvelopeWithBasicAuth pins NZBGet's dialect: a POST to
// /jsonrpc carrying a JSON-RPC 2.0 envelope and HTTP Basic auth.
func TestCallSendsJSONRPCEnvelopeWithBasicAuth(t *testing.T) {
	var gotPath, gotContentType string
	var gotReq rpcRequest
	var gotUser, gotPass string
	var gotAuth bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotUser, gotPass, gotAuth = r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"result":"21.1"}`)
	}))
	t.Cleanup(srv.Close)

	version, err := NewClient(srv.URL, "nzbget", "tegbzn6789").Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != "21.1" {
		t.Errorf("version = %q, want 21.1", version)
	}
	if gotPath != "/jsonrpc" {
		t.Errorf("path = %s, want /jsonrpc", gotPath)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %s, want application/json", gotContentType)
	}
	if !gotAuth || gotUser != "nzbget" || gotPass != "tegbzn6789" {
		t.Errorf("basic auth = (%q, %q, %v), want (nzbget, tegbzn6789, true)", gotUser, gotPass, gotAuth)
	}
	if gotReq.JSONRPC != "2.0" || gotReq.Method != "version" || gotReq.Params == nil || len(gotReq.Params) != 0 {
		t.Errorf("request = %+v, want jsonrpc 2.0 method=version params=[]", gotReq)
	}
}

// TestNoAuthHeaderWhenCredentialsBlank pins that a credential-less NZBGet
// (control ip whitelisting) gets no Authorization header at all.
func TestNoAuthHeaderWhenCredentialsBlank(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"result":"21.1"}`)
	}))
	t.Cleanup(srv.Close)

	if _, err := NewClient(srv.URL, "", "").Version(); err != nil {
		t.Fatalf("Version: %v", err)
	}
	if gotAuthHeader != "" {
		t.Errorf("Authorization header = %q, want empty", gotAuthHeader)
	}
}

// TestRPCErrorEnvelopeSurfaced pins the JSON-RPC error envelope handling: an
// HTTP 200 with an error object must fail with the server's message.
func TestRPCErrorEnvelopeSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"result":null,"error":{"name":"AccessDenied","code":403,"message":"Access denied"}}`)
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, "u", "p").ListGroups()
	if err == nil {
		t.Fatal("ListGroups accepted a JSON-RPC error envelope")
	}
	if !strings.Contains(err.Error(), "Access denied") {
		t.Fatalf("error = %v, want the RPC error message surfaced", err)
	}
}

// TestUnauthorizedDoesNotEchoCredentials pins the credential-echo property on
// the 401 path.
func TestUnauthorizedDoesNotEchoCredentials(t *testing.T) {
	const password = "NZBGET_PASSWORD_SENTINEL"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "401 unauthorized for "+password, http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, "nzbget", password).GetStatus()
	if err == nil {
		t.Fatal("GetStatus accepted a 401 response")
	}
	if !strings.Contains(err.Error(), "invalid credentials") {
		t.Errorf("error = %v, want invalid-credentials message", err)
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("error echoed the password: %v", err)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	client := NewClient(source.URL, "nzbget", "nzbget-secret")
	if _, err := client.ListGroups(); err == nil {
		t.Fatal("ListGroups accepted an upstream redirect")
	}
	if _, err := client.GetStatus(); err == nil {
		t.Fatal("GetStatus accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

// TestEditQueueFallsBackToLegacySignature pins the v16+/pre-v16 shim: the
// modern 3-parameter editqueue is tried first, and on an RPC error the legacy
// 4-parameter signature (with the Offset param) is used.
// TestRedirectErrorNamesLocation pins that a refused redirect reports where
// the service tried to send us, so scheme misconfigurations are self-diagnosing.
func TestRedirectErrorNamesLocation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://nzbget.internal/jsonrpc", http.StatusMovedPermanently)
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, "user", "password").Version()
	if err == nil || !strings.Contains(err.Error(), "https://nzbget.internal/jsonrpc") {
		t.Fatalf("redirect error = %v, want the Location named", err)
	}
	if strings.Contains(err.Error(), "password") {
		t.Errorf("error %q echoes credentials", err.Error())
	}
}

func TestEditQueueFallsBackToLegacySignature(t *testing.T) {
	var mu sync.Mutex
	var calls []rpcRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		calls = append(calls, req)
		mu.Unlock()
		if len(req.Params) == 3 {
			// Pre-v16 server: reject the modern signature.
			_, _ = io.WriteString(w, `{"error":{"name":"InvalidParams","code":-32602,"message":"invalid params"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"result": true}`)
	}))
	t.Cleanup(srv.Close)

	if err := NewClient(srv.URL, "u", "p").PauseGroups([]int{7, 8}); err != nil {
		t.Fatalf("PauseGroups: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2 (modern then legacy)", len(calls))
	}
	modern, legacy := calls[0], calls[1]
	if modern.Method != "editqueue" || len(modern.Params) != 3 ||
		modern.Params[0] != "GroupPause" || modern.Params[1] != "" {
		t.Errorf("modern call = %+v, want editqueue [GroupPause \"\" ids]", modern)
	}
	if legacy.Method != "editqueue" || len(legacy.Params) != 4 ||
		legacy.Params[0] != "GroupPause" || legacy.Params[1] != float64(0) || legacy.Params[2] != "" {
		t.Errorf("legacy call = %+v, want editqueue [GroupPause 0 \"\" ids]", legacy)
	}
	ids, ok := legacy.Params[3].([]interface{})
	if !ok || len(ids) != 2 || ids[0] != float64(7) || ids[1] != float64(8) {
		t.Errorf("legacy ids = %v, want [7 8]", legacy.Params[3])
	}
}

// TestLoHiSizeReassembly pins the 32-bit Lo/Hi split NZBGet uses for byte
// counts, including the fallback to the rounded MB field when the pair is zero.
func TestLoHiSizeReassembly(t *testing.T) {
	over4GiB := Group{FileSizeLo: 705032704, FileSizeHi: 1}
	if got := over4GiB.SizeBytes(); got != 5000000000 {
		t.Errorf("SizeBytes = %d, want 5000000000", got)
	}
	mbOnly := Group{FileSizeMB: 50}
	if got := mbOnly.SizeBytes(); got != 50*1024*1024 {
		t.Errorf("SizeBytes (MB fallback) = %d, want %d", got, int64(50*1024*1024))
	}
	remaining := Group{RemainingSizeLo: 268435456}
	if got := remaining.RemainingBytes(); got != 268435456 {
		t.Errorf("RemainingBytes = %d, want 268435456", got)
	}
	entry := HistoryEntry{FileSizeLo: 4294967295, FileSizeHi: 2}
	if got := entry.SizeBytes(); got != 2*4294967296+4294967295 {
		t.Errorf("history SizeBytes = %d, want %d", got, int64(2*4294967296+4294967295))
	}
}
