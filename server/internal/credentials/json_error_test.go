package credentials

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type safeMessageError struct{ msg string }

func (e safeMessageError) Error() string           { return e.msg }
func (e safeMessageError) SafeUserMessage() string { return e.msg }

// The app's client only decodes application/json bodies; an error written via
// http.Error arrives as text/plain and every failure renders as a generic
// "Failed to save settings." — the #497 report's expired key hid behind
// exactly that. These pin the JSON content type and a decodable error field.
func TestWriteJSONErrorIsDecodableJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONError(rec, `unknown credential key: "odd"`, http.StatusBadRequest)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, rec.Body.String())
	}
	if body["error"] != `unknown credential key: "odd"` {
		t.Fatalf("error = %q, want the message verbatim (quotes escaped by the encoder)", body["error"])
	}
}

func TestValidationErrorCarriesSafeMessageAsJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeCredentialValidationError(rec, safeMessageError{msg: "The provider credential or account connection was rejected. Check or reconnect the provider credential. Nothing was saved."})

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if !strings.Contains(body["error"], "credential or account connection was rejected") {
		t.Fatalf("error = %q, want the provider-specific rejection message", body["error"])
	}

	// Errors without a safe message keep the generic validation copy.
	rec = httptest.NewRecorder()
	writeCredentialValidationError(rec, errors.New("raw provider detail"))
	body = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("fallback body is not valid JSON: %v", err)
	}
	if strings.Contains(body["error"], "raw provider detail") {
		t.Fatal("raw provider error leaked to the client")
	}
	if !strings.Contains(body["error"], "could not complete a test message") {
		t.Fatalf("fallback error = %q", body["error"])
	}
}
