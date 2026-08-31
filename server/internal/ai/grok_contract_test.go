package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/credentials"
)

// TestGrokValidationProviderContract pins the xAI request shape: the OpenAI
// wire format against the XAI base URL, bearer auth, and — because Grok
// models reason internally without an effort control — no reasoning_effort
// field plus the generous reasoning output budget.
func TestGrokValidationProviderContract(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("XAI_BASE_URL", server.URL+"/v1")

	if err := validateAPIKeyProfile(t, credentials.AIProviderGrok, "grok-4.6"); err != nil {
		t.Fatalf("validate Grok profile: %v", err)
	}
	req := <-requests
	if req.path != "/v1/chat/completions" {
		t.Fatalf("path=%q, want /v1/chat/completions", req.path)
	}
	if got := req.header.Get("Authorization"); got != "Bearer contract-secret" {
		t.Fatalf("Authorization=%q", got)
	}
	if _, found := req.body["reasoning_effort"]; found {
		t.Fatalf("grok validation request sent reasoning_effort: %#v", req.body["reasoning_effort"])
	}
	want := openAIValidationReasoningMaxTokens
	if aiValidationMaxTokens > want {
		want = aiValidationMaxTokens
	}
	if got := int(req.body["max_completion_tokens"].(float64)); got != want {
		t.Fatalf("max_completion_tokens=%d, want %d", got, want)
	}
	if got := req.body["model"]; got != "grok-4.6" {
		t.Fatalf("model=%v", got)
	}
}

// TestGrokOAuthValidationUsesLiveBearerToken proves a grok_oauth save-time
// probe authenticates with the linked account's access token, not a stored
// API key.
func TestGrokOAuthValidationUsesLiveBearerToken(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("XAI_BASE_URL", server.URL+"/v1")

	h, _, database, userID := newResolverTestHandler(t)
	manager, now := newGrokTestManager(t, h, database)
	linkPersonalGrok(t, manager, now, userID)
	h.validationProbe = nil

	err := h.ValidatePersonalAISettings(context.Background(), userID, credentials.AIProfile{
		Config:            credentials.AIConfig{Provider: credentials.AIProviderGrokOAuth, Model: "grok-4.6"},
		CredentialPresent: true,
	})
	if err != nil {
		t.Fatalf("validate grok_oauth profile: %v", err)
	}
	req := <-requests
	if got := req.header.Get("Authorization"); got != "Bearer grok-bearer-token" {
		t.Fatalf("Authorization=%q, want the linked account's bearer token", got)
	}
}
