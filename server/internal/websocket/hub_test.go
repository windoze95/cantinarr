package websocket

import (
	"net/http"
	"testing"
)

func TestMakeOriginChecker_NilWhenEmpty(t *testing.T) {
	if makeOriginChecker(nil) != nil {
		t.Fatal("nil input should return nil checker")
	}
	if makeOriginChecker([]string{}) != nil {
		t.Fatal("empty slice should return nil checker")
	}
}

func TestMakeOriginChecker_AllowsConfigured(t *testing.T) {
	checker := makeOriginChecker([]string{"http://localhost:3000", "https://app.example.com"})
	if checker == nil {
		t.Fatal("checker should not be nil")
	}

	r := &http.Request{Header: http.Header{"Origin": {"http://localhost:3000"}}}
	if !checker(r) {
		t.Fatal("configured origin should be allowed")
	}

	r.Header.Set("Origin", "https://app.example.com")
	if !checker(r) {
		t.Fatal("second configured origin should be allowed")
	}
}

func TestMakeOriginChecker_RejectsUnknown(t *testing.T) {
	checker := makeOriginChecker([]string{"http://localhost:3000"})

	r := &http.Request{Header: http.Header{"Origin": {"https://evil.com"}}}
	if checker(r) {
		t.Fatal("unknown origin should be rejected")
	}
}
