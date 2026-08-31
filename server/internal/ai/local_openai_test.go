package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// The local provider is shared-only: personal payloads never list it and
// personal writes never accept it, because its endpoint can name
// cluster-internal hosts that must not ride a non-admin path.
func TestPersonalAISettingsExcludeSharedOnlyProvider(t *testing.T) {
	h, _, database, userID := newResolverTestHandler(t)
	if _, err := database.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/ai/settings", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleUser}))
	rec := httptest.NewRecorder()
	h.AISettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Providers []credentials.AIProviderOption `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Providers) == 0 {
		t.Fatal("personal settings response listed no providers")
	}
	for _, provider := range response.Providers {
		if provider.ID == credentials.AIProviderLocalOpenAI || provider.SharedOnly {
			t.Fatalf("personal settings leaked shared-only provider: %#v", provider)
		}
	}
}

func TestPersonalAISelectionRejectsSharedOnlyProvider(t *testing.T) {
	h, _, _, userID := newResolverTestHandler(t)

	req := httptest.NewRequest(http.MethodPut, "/api/ai/settings", strings.NewReader(`{"provider":"local_openai","model":"qwen3.6:35b-a3b"}`))
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleUser}))
	rec := httptest.NewRecorder()
	h.UpdateAISettings(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/ai/credentials/local_openai", strings.NewReader(`{"api_key":"proxy-token"}`))
	route := chi.NewRouteContext()
	route.URLParams.Add("provider", "local_openai")
	ctx := context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleUser})
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
	rec = httptest.NewRecorder()
	h.UpdatePersonalAICredential(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("credential status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// A keyless local profile still authenticates: the SDK always sends an
// Authorization header, so the runner substitutes a fixed placeholder that
// local servers ignore and proxies can allowlist.
func TestLocalOpenAITurnSendsPlaceholderBearerAndPinnedConfig(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)

	h, _, _, userID := newResolverTestHandler(t)
	h.validationProbe = nil
	err := h.ValidateSharedAISettings(context.Background(), credentials.AIProfile{
		Config:            credentials.AIConfig{Provider: credentials.AIProviderLocalOpenAI, Model: "qwen3.6:35b-a3b"},
		CredentialPresent: true,
		BaseURL:           server.URL + "/v1",
		ReasoningEffort:   "none",
	})
	if err != nil {
		t.Fatalf("validate keyless local profile: %v", err)
	}
	_ = userID
	req := <-requests
	if got := req.header.Get("Authorization"); got != "Bearer cantinarr-local" {
		t.Fatalf("Authorization=%q, want the placeholder bearer", got)
	}
	if got := req.body["model"]; got != "qwen3.6:35b-a3b" {
		t.Fatalf("model=%v", got)
	}
	if got := req.body["reasoning_effort"]; got != "none" {
		t.Fatalf("reasoning_effort=%v, want none", got)
	}
}

// A stored key wins over the placeholder.
func TestLocalOpenAITurnUsesStoredKeyWhenPresent(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)

	h, _, _, _ := newResolverTestHandler(t)
	h.validationProbe = nil
	err := h.ValidateSharedAISettings(context.Background(), credentials.AIProfile{
		Config:            credentials.AIConfig{Provider: credentials.AIProviderLocalOpenAI, Model: "qwen3.6:35b-a3b"},
		APIKey:            "proxy-token",
		CredentialPresent: true,
		BaseURL:           server.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("validate keyed local profile: %v", err)
	}
	req := <-requests
	if got := req.header.Get("Authorization"); got != "Bearer proxy-token" {
		t.Fatalf("Authorization=%q, want the stored key", got)
	}
}

// The interactive chat loop reaches the local provider through the same
// service, with tool support intact.
func TestLocalOpenAIInteractiveSendsToConfiguredEndpoint(t *testing.T) {
	requests := make(chan providerRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureProviderRequest(r)
		writeOpenAITextSSE(w)
	}))
	t.Cleanup(server.Close)

	history := transcript{textTranscriptMessage(agentRoleUser, "hello")}
	_, err := NewOpenAIService(localOpenAICredential(""), "qwen3.6:35b-a3b", server.URL+"/v1", "none",
		mcp.NewToolServer(nil, nil, nil, nil)).SendMessage(
		context.Background(), history, ChatContext{UserID: 7, Role: auth.RoleUser}, StreamCallbacks{},
	)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	req := <-requests
	if req.path != "/v1/chat/completions" {
		t.Fatalf("path=%q", req.path)
	}
	if got := req.header.Get("Authorization"); got != "Bearer cantinarr-local" {
		t.Fatalf("Authorization=%q", got)
	}
	if _, found := req.body["tools"]; !found {
		t.Fatal("interactive request lost its tools")
	}
}
