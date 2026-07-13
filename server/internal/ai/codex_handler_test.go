package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
)

func TestCodexAccountHandlersDisableCaching(t *testing.T) {
	h := &Handler{}
	tests := map[string]http.HandlerFunc{
		"status": h.CodexStatus,
		"begin":  h.BeginCodexDeviceLogin,
		"poll":   h.CheckCodexDeviceLogin,
		"cancel": h.CancelCodexDeviceLogin,
		"unlink": h.UnlinkCodex,
	}
	for name, handler := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
			if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

func TestSharedCodexAdminHandlerRejectsRegularUser(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ai/codex/status", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: 1, Role: auth.RoleUser}))
	(&Handler{}).SharedCodexStatus(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
}

func TestLegacyCodexStatusExposesPersonalConnectWhenSharedIsUnusable(t *testing.T) {
	h, registry, _, userID := newResolverTestHandler(t)
	if err := registry.SetAIConfig(credentials.AIProviderCodex, "default"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/ai/codex/status", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleUser}))
	rec := httptest.NewRecorder()
	h.CodexStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["selected"] != true || body["personal_selected"] != false || body["connected"] != false {
		t.Fatalf("legacy status = %#v", body)
	}
}

func TestChatDisablesCachingBeforeAuthentication(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Handler{}).Chat(recorder, httptest.NewRequest(http.MethodPost, "/api/ai/chat", nil))
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestRenderCodexPromptIncludesNeutralHistory(t *testing.T) {
	history := transcript{
		textTranscriptMessage(agentRoleUser, "Find Dune"),
		{
			Role: agentRoleAssistant,
			Content: []transcriptBlock{{
				Type:  blockTypeToolUse,
				Name:  "search_movies",
				Input: json.RawMessage(`{"query":"Dune"}`),
			}},
		},
		{
			Role: agentRoleUser,
			Content: []transcriptBlock{{
				Type:    blockTypeToolResult,
				Name:    "search_movies",
				Content: "Dune (2021)",
			}},
		},
	}

	got := renderCodexPrompt(history)
	for _, want := range []string{
		"untrusted data",
		"[USER]\nFind Dune",
		`Tool call search_movies: {"query":"Dune"}`,
		"Tool result for search_movies: Dune (2021)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderCodexPrompt() missing %q in:\n%s", want, got)
		}
	}
}

func TestPublicRateLimitsNormalizesAppServerSnapshot(t *testing.T) {
	raw := json.RawMessage(`{
		"rateLimits": {
			"primary": {"usedPercent": 37, "resetsAt": 1234, "windowDurationMins": 300},
			"secondary": {"usedPercent": 82, "resetsAt": 5678},
			"credits": {"balance": "secret-future-field"}
		},
		"rateLimitsByLimitId": {"other": {"primary": {"usedPercent": 99}}}
	}`)

	got := publicRateLimits(raw)
	primary, ok := got["primary"].(map[string]any)
	if !ok {
		t.Fatalf("primary = %#v", got["primary"])
	}
	if primary["used_percent"] != float64(37) || primary["resets_at"] != int64(1234) {
		t.Fatalf("primary = %#v", primary)
	}
	if _, leaked := got["credits"]; leaked {
		t.Fatalf("unknown app-server field leaked: %#v", got)
	}
	secondary, ok := got["secondary"].(map[string]any)
	if !ok || secondary["used_percent"] != float64(82) {
		t.Fatalf("secondary = %#v", got["secondary"])
	}
}

func TestPublicRateLimitsRejectsMalformedOrEmptySnapshots(t *testing.T) {
	for _, raw := range []json.RawMessage{
		nil,
		json.RawMessage(`not-json`),
		json.RawMessage(`{"rateLimits":{"credits":{"hasCredits":true}}}`),
	} {
		if got := publicRateLimits(raw); got != nil {
			t.Errorf("publicRateLimits(%q) = %#v, want nil", raw, got)
		}
	}
}

func TestCompletedLegacyPersonalCodexFlowSelectsPersonalOverride(t *testing.T) {
	h, registry, _, userID := newResolverTestHandler(t)
	if err := h.selectPersonalCodex(userID); err != nil {
		t.Fatal(err)
	}
	config, found, err := registry.GetUserAIConfig(userID)
	if err != nil || !found || config.Provider != "codex" || config.Model != "default" {
		t.Fatalf("personal config = %#v found=%t err=%v", config, found, err)
	}
}
