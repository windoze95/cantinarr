package push

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestEnrollDoesNotFollowRedirects(t *testing.T) {
	var redirectedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if got := r.Header.Get("X-Enroll-Token"); got != "" {
			t.Errorf("redirect destination received X-Enroll-Token %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/credential-sink", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	if _, err := Enroll(source.URL, "Cantinarr", "enroll-secret"); err == nil {
		t.Fatal("Enroll accepted an upstream redirect")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect destination received %d requests, want 0", got)
	}
}

func TestEnroll_Success(t *testing.T) {
	var gotToken, gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotToken = r.Header.Get("X-Enroll-Token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tenant_id":"t-abc","api_key":"pgk_xyz"}`))
	}))
	defer srv.Close()

	res, err := Enroll(srv.URL, "My Instance", "secret-tok")
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if res.APIKey != "pgk_xyz" || res.TenantID != "t-abc" {
		t.Fatalf("unexpected response: %#v", res)
	}
	if gotPath != "/v1/enroll" || gotMethod != http.MethodPost {
		t.Fatalf("hit %s %s, want POST /v1/enroll", gotMethod, gotPath)
	}
	if gotToken != "secret-tok" {
		t.Fatalf("X-Enroll-Token = %q, want secret-tok", gotToken)
	}
}

func TestEnroll_NoTokenHeaderWhenEmpty(t *testing.T) {
	hadHeader := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadHeader = r.Header["X-Enroll-Token"]
		_, _ = w.Write([]byte(`{"tenant_id":"t","api_key":"pgk_1"}`))
	}))
	defer srv.Close()
	if _, err := Enroll(srv.URL, "x", ""); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if hadHeader {
		t.Fatal("X-Enroll-Token should be absent when enrollToken is empty")
	}
}

func TestEnroll_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"enrollment_closed"}}`, http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := Enroll(srv.URL, "x", ""); err == nil {
		t.Fatal("expected error on 403, got nil")
	}
}

func TestEnroll_EmptyKeyRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tenant_id":"t-1","api_key":""}`))
	}))
	defer srv.Close()
	if _, err := Enroll(srv.URL, "x", ""); err == nil {
		t.Fatal("expected error on empty api_key")
	}
}
