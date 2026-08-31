package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// An admin-pinned effort must reach every interactive chat leg verbatim.
func TestOpenAIInteractiveSendsPinnedReasoningEffort(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	history := transcript{textTranscriptMessage(agentRoleUser, "hello")}
	_, err := NewOpenAIService("secret", "local-model", "", "low", mcp.NewToolServer(nil, nil, nil, nil)).SendMessage(
		context.Background(), history, ChatContext{UserID: 7, Role: auth.RoleUser}, StreamCallbacks{},
	)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	req := <-requests
	if got := req.body["reasoning_effort"]; got != "low" {
		t.Fatalf("reasoning_effort=%v, want low", got)
	}
}

// Auto (no pin) keeps the interactive wire shape effort-free so hosted models
// and OpenAI-compatible servers keep their own defaults.
func TestOpenAIInteractiveAutoOmitsReasoningEffort(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	history := transcript{textTranscriptMessage(agentRoleUser, "hello")}
	_, err := NewOpenAIService("secret", "local-model", "", "", mcp.NewToolServer(nil, nil, nil, nil)).SendMessage(
		context.Background(), history, ChatContext{UserID: 7, Role: auth.RoleUser}, StreamCallbacks{},
	)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	req := <-requests
	if got, found := req.body["reasoning_effort"]; found {
		t.Fatalf("reasoning_effort=%v, want omitted", got)
	}
}

// A backend that rejects the pinned field (non-reasoning model, bare proxy)
// must cost one silent retry, not the chat turn.
func TestOpenAIInteractiveDropsRejectedReasoningEffort(t *testing.T) {
	var calls atomic.Int32
	requests := make(chan providerRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		if calls.Add(1) == 1 {
			writeOpenAIAPIError(w, http.StatusBadRequest, "unknown_parameter", "reasoning_effort", "Unrecognized request argument supplied: reasoning_effort")
			return
		}
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	history := transcript{textTranscriptMessage(agentRoleUser, "hello")}
	_, err := NewOpenAIService("secret", "local-model", "", "medium", mcp.NewToolServer(nil, nil, nil, nil)).SendMessage(
		context.Background(), history, ChatContext{UserID: 7, Role: auth.RoleUser}, StreamCallbacks{},
	)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	first := <-requests
	if got := first.body["reasoning_effort"]; got != "medium" {
		t.Fatalf("first reasoning_effort=%v, want medium", got)
	}
	second := <-requests
	if got, found := second.body["reasoning_effort"]; found {
		t.Fatalf("retry reasoning_effort=%v, want omitted", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("model requests = %d, want 2", got)
	}
}

// Validation must prove the exact pinned configuration production turns will
// use: the admin's effort leads the attempt ladder.
func TestOpenAIValidationPinnedEffortLeadsLadder(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")

	h, _, _, userID := newResolverTestHandler(t)
	h.validationProbe = nil
	err := h.ValidatePersonalAISettings(context.Background(), userID, credentials.AIProfile{
		Config:          credentials.AIConfig{Provider: credentials.AIProviderOpenAI, Model: "local-model"},
		APIKey:          "contract-secret",
		ReasoningEffort: "medium",
	})
	if err != nil {
		t.Fatalf("validate pinned-effort profile: %v", err)
	}
	req := <-requests
	if got := req.body["reasoning_effort"]; got != "medium" {
		t.Fatalf("reasoning_effort=%v, want medium (pinned effort must lead the ladder)", got)
	}
}
