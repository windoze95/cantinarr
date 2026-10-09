package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	openai "github.com/openai/openai-go/v3"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"google.golang.org/genai"
)

type fallbackRecorder struct{ events []ModelFallback }

func (r *fallbackRecorder) RecordModelFallback(e ModelFallback) error {
	r.events = append(r.events, e)
	return nil
}

func TestConfirmedModelUnavailableRejectsUnrelatedFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"missing", &openai.Error{StatusCode: 404, Code: "model_not_found", Message: "secret"}, true},
		{"retired", &openai.Error{StatusCode: 410, Message: "Model old-model has been retired"}, true},
		{"gemini", genai.APIError{Code: 404, Message: "models/old-model is not found for API version v1beta"}, true},
		{"codex", codexapp.ErrModelUnavailable, true},
		{"route404", &openai.Error{StatusCode: 404, Message: "Not found"}, false},
		{"tool404", &openai.Error{StatusCode: 404, Message: "model old-model tool schema not found"}, false},
		{"auth", &openai.Error{StatusCode: 401, Code: "model_not_found"}, false},
		{"forbidden", &openai.Error{StatusCode: 403, Code: "model_not_found"}, false},
		{"rate", &openai.Error{StatusCode: 429, Code: "model_not_found"}, false},
		{"quota", &responsesStreamError{Code: "insufficient_quota", Message: "old-model unavailable"}, false},
		{"outage", &openai.Error{StatusCode: 503, Code: "model_not_found"}, false},
		{"temporary", &openai.Error{StatusCode: 400, Code: "model_unavailable", Message: "model old-model temporarily unavailable"}, false},
		{"unsupported_tool", &openai.Error{StatusCode: 400, Code: "unsupported_model", Message: "old-model does not support this tool"}, false},
		{"schema", &openai.Error{StatusCode: 400, Message: "model old-model does not support this tool schema"}, false},
		{"plain", errors.New("model_not_found"), false},
		{"cancel", context.Canceled, false},
		{"timeout", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := confirmedModelUnavailable(tc.err, "old-model"); got != tc.want {
				t.Fatalf("got %t want %t", got, tc.want)
			}
		})
	}
}

func TestChatModelFallbackPreservesSelectionAccountAndBilling(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		enabled, replacementFails bool
		status                    int
		code                      string
		wantCalls                 int
	}{
		{"enabled", true, false, 404, "model_not_found", 2},
		{"disabled", false, false, 404, "model_not_found", 1},
		{"replacement_failure", true, true, 404, "model_not_found", 2},
		{"invalid_credential", true, false, 401, "invalid_api_key", 1},
		{"unrelated_not_found", true, false, 404, "route_not_found", 1},
		{"invalid_request", true, false, 400, "invalid_request", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var models, keys []string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				models = append(models, body["model"].(string))
				keys = append(keys, r.Header.Get("Authorization"))
				if len(models) == 1 || tc.replacementFails {
					writeOpenAIAPIError(w, tc.status, tc.code, "", "opaque upstream detail sk-secret-dont-expose")
					return
				}
				if body["reasoning_effort"] != nil {
					t.Error("fallback inherited unsupported reasoning effort")
				}
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
				writeOpenAITextSSE(w)
			}))
			defer upstream.Close()
			t.Setenv("OPENAI_BASE_URL", upstream.URL+"/v1")
			h, registry, _, userID := newResolverTestHandler(t)
			h.toolServer = mcp.NewToolServer(nil, nil, nil, nil)
			h.conversations = newConversationStore()
			sink := &fallbackRecorder{}
			h.SetModelFallbackSink(sink)
			if err := registry.SetCredential(credentials.KeyOpenAIKey, "shared-secret"); err != nil {
				t.Fatal(err)
			}
			if err := registry.SetAIConfig(credentials.AIProviderOpenAI, "shared-model"); err != nil {
				t.Fatal(err)
			}
			if err := registry.SetUserAIProfile(userID, credentials.AIProviderOpenAI, "retired-model", "personal-secret", tc.enabled); err != nil {
				t.Fatal(err)
			}
			response := postChat(t, h, userID)
			if len(models) != tc.wantCalls {
				t.Fatalf("models=%v body=%s", models, response.Body.String())
			}
			for _, key := range keys {
				if key != "Bearer personal-secret" {
					t.Fatal("billing source changed")
				}
			}
			saved, _, err := registry.GetUserAIConfig(userID)
			if err != nil || saved.Model != "retired-model" || saved.ModelFallbackEnabled != tc.enabled {
				t.Fatalf("saved=%+v err=%v", saved, err)
			}
			body := response.Body.String()
			if strings.Contains(body, "secret") {
				t.Fatal("upstream details leaked")
			}
			if tc.wantCalls == 2 {
				if models[1] != "gpt-4.1-mini" || len(sink.events) != 1 {
					t.Fatalf("models=%v events=%+v", models, sink.events)
				}
				event := sink.events[0]
				if event.Source != "personal" || event.UserID != userID || event.SettingsPath != "/settings/ai" || event.SelectedModel != "retired-model" || event.Succeeded == tc.replacementFails {
					t.Fatalf("event=%+v", event)
				}
				if !strings.Contains(body, `"model_fallback"`) || !strings.Contains(body, `"replacement_model":"gpt-4.1-mini"`) {
					t.Fatalf("missing visible notice: %s", body)
				}
				if tc.replacementFails && !strings.Contains(body, "no further model was tried") {
					t.Fatal("replacement failure was not clear")
				}
			} else if len(sink.events) != 0 || strings.Contains(body, `"model_fallback"`) {
				t.Fatal("unexpected fallback")
			}
		})
	}
}

func TestFallbackNeverReplaysAnExecutedTool(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeNudgeToolCallSSE(w, "one", "get_trending", `{"media_type":"movie"}`)
			return
		}
		writeOpenAIAPIError(w, 404, "model_not_found", "model", "model unavailable")
	}))
	defer upstream.Close()
	t.Setenv("OPENAI_BASE_URL", upstream.URL+"/v1")
	h, _, database, userID := newResolverTestHandler(t)
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x27}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry := credentials.NewRegistry(database, cipher, credentials.WithTMDBBaseURL(fakeTMDBForNudge(t).URL), credentials.WithDefaultTMDBToken("tmdb-test"))
	h.creds = registry
	h.toolServer = mcp.NewToolServer(registry, nil, nil, nil)
	h.conversations = newConversationStore()
	h.toolServer.SetCallAuthorizer(func(_ context.Context, c mcp.CallContext) (string, error) { return c.Role, nil })
	if err := registry.SetUserAIProfile(userID, credentials.AIProviderOpenAI, "old-model", "secret", true); err != nil {
		t.Fatal(err)
	}
	response := postChat(t, h, userID)
	if calls.Load() != 2 || strings.Contains(response.Body.String(), `"model_fallback"`) || !strings.Contains(response.Body.String(), `"tool_start"`) || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("calls=%d body=%s", calls.Load(), response.Body.String())
	}
}

func TestFallbackProviderAdaptersKeepTheirEndpointAndCredential(t *testing.T) {
	for _, tc := range []struct{ provider, env, model, path, authHeader, authValue string }{
		{credentials.AIProviderAnthropic, "ANTHROPIC_BASE_URL", "claude-sonnet-5-5", "/v1/messages", "X-Api-Key", "personal-secret"},
		{credentials.AIProviderGemini, "GOOGLE_GEMINI_BASE_URL", "gemini-3.8-flash", "/v1beta/models/gemini-3.8-flash:streamGenerateContent", "X-Goog-Api-Key", "personal-secret"},
		{credentials.AIProviderGrok, "XAI_BASE_URL", "grok-4.7", "/v1/chat/completions", "Authorization", "Bearer personal-secret"},
		{credentials.AIProviderGrokOAuth, "GROK_OAUTH_BASE_URL", "grok-4.6", "/v1/responses", "Authorization", "Bearer grok-bearer-token"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			var requests []providerRequest
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, captureProviderRequest(r))
				if len(requests) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					switch tc.provider {
					case credentials.AIProviderAnthropic:
						_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"model: retired-model"}}`))
					case credentials.AIProviderGemini:
						_, _ = w.Write([]byte(`{"error":{"code":404,"message":"models/retired-model is not found for API version v1beta","status":"NOT_FOUND"}}`))
					default:
						_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"retired-model unavailable","type":"invalid_request_error"}}`))
					}
					return
				}
				switch tc.provider {
				case credentials.AIProviderAnthropic:
					writeAnthropicTextSSE(w)
				case credentials.AIProviderGemini:
					writeGeminiTextSSE(w)
				case credentials.AIProviderGrokOAuth:
					writeGrokOAuthTextSSE(w)
				default:
					writeOpenAITextSSE(w)
				}
			}))
			defer upstream.Close()
			base := upstream.URL
			if tc.provider == credentials.AIProviderGrok || tc.provider == credentials.AIProviderGrokOAuth {
				base += "/v1"
			}
			t.Setenv(tc.env, base)
			h, registry, database, userID := newResolverTestHandler(t)
			h.toolServer = mcp.NewToolServer(registry, nil, nil, nil)
			h.conversations = newConversationStore()
			key := "personal-secret"
			if tc.provider == credentials.AIProviderGrokOAuth {
				manager, now := newGrokTestManager(t, h, database)
				linkPersonalGrok(t, manager, now, userID)
				key = ""
			}
			if err := registry.SetUserAIProfile(userID, tc.provider, "retired-model", key, true); err != nil {
				t.Fatal(err)
			}
			response := postChat(t, h, userID)
			if len(requests) != 2 || !strings.Contains(response.Body.String(), `"status":"used"`) {
				t.Fatalf("requests=%d body=%s", len(requests), response.Body.String())
			}
			for _, req := range requests {
				if req.header.Get(tc.authHeader) != tc.authValue {
					t.Fatal("provider credential changed")
				}
				if len(req.body["tools"].([]any)) == 0 {
					t.Fatal("required tools were not sent")
				}
			}
			if requests[1].path != tc.path || (tc.provider != credentials.AIProviderGemini && requests[1].body["model"] != tc.model) {
				t.Fatalf("replacement endpoint/model = %s %v", requests[1].path, requests[1].body["model"])
			}
		})
	}
}

func TestFallbackNoRecommendationAndSameRecommendationFailClearly(t *testing.T) {
	for _, r := range []resolvedAI{
		{Provider: credentials.AIProviderLocalOpenAI, Model: "old", BaseURL: "http://local.internal"},
		{Provider: credentials.AIProviderOpenAI, Model: "old", BaseURL: "https://custom.example/v1"},
		{Provider: credentials.AIProviderOpenAI, Model: "gpt-4.1-mini"},
	} {
		r.ModelFallbackEnabled = true
		r.Source = aiSourceShared
		h := &Handler{}
		called := false
		_, err := h.retryUnavailableModel(context.Background(), r, "chat", &openai.Error{StatusCode: 404, Code: "model_not_found"}, nil, func(string) error { called = true; return nil })
		var safe *modelFallbackError
		if called || !errors.As(err, &safe) || strings.Contains(err.Error(), "internal") {
			t.Fatalf("called=%t err=%v", called, err)
		}
	}
}

func TestFallbackEventIdentityAndSafeDisplay(t *testing.T) {
	h := &Handler{}
	profile := resolvedAI{ModelFallbackEnabled: true, Provider: credentials.AIProviderOpenAI, Source: aiSourcePersonal,
		Account: codexapp.PersonalAccount(7), Model: "retired-private-secret\nmodel", APIKey: "private-secret"}
	unavailable := &openai.Error{StatusCode: 404, Code: "model_not_found", Message: "opaque upstream secret"}
	run := func(string) error { return nil }
	first, err := h.retryUnavailableModel(context.Background(), profile, "chat", unavailable, nil, run)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.retryUnavailableModel(context.Background(), profile, "chat", unavailable, nil, run)
	if err != nil || first.EventKey != second.EventKey {
		t.Fatalf("repeated fallback did not retain event identity: %v", err)
	}
	display, _ := json.Marshal(first)
	if strings.Contains(string(display), "private-secret") || strings.Contains(string(display), "opaque upstream") || strings.Contains(first.SelectedModel, "\n") || strings.Contains(string(display), first.EventKey) {
		t.Fatalf("unsafe notification: %s", display)
	}
	profile.APIKey = "new-account-secret"
	other, err := h.retryUnavailableModel(context.Background(), profile, "chat", unavailable, nil, run)
	if err != nil || first.EventKey == other.EventKey {
		t.Fatalf("changed account reused old event: %v", err)
	}
}

func TestOpenAIResponsesModelRetirementFallback(t *testing.T) {
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if len(paths) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: error\ndata: {\"type\":\"error\",\"code\":\"model_not_found\",\"message\":\"retired\"}\n\n"))
			return
		}
		writeOpenAITextSSE(w)
	}))
	defer upstream.Close()
	t.Setenv("OPENAI_BASE_URL", upstream.URL+"/v1")
	h, registry, _, userID := newResolverTestHandler(t)
	h.toolServer = mcp.NewToolServer(nil, nil, nil, nil)
	h.conversations = newConversationStore()
	if err := registry.SetUserAIProfile(userID, credentials.AIProviderOpenAI, "gpt-6-astra", "same-key", true); err != nil {
		t.Fatal(err)
	}
	response := postChat(t, h, userID)
	if !reflect.DeepEqual(paths, []string{"/v1/responses", "/v1/chat/completions"}) || !strings.Contains(response.Body.String(), `"status":"used"`) {
		t.Fatalf("paths=%v body=%s", paths, response.Body.String())
	}
}

func TestCodexChatFallbackUsesAccountUpgradeAndPreservesSelection(t *testing.T) {
	h, userID := newCodexChatBudgetHandler(t, "--fake-model-fallback")
	if err := h.creds.SetAIConfig(credentials.AIProviderCodex, "retired-model"); err != nil {
		t.Fatal(err)
	}
	if err := h.creds.SetSetting(credentials.KeyAIModelFallbackEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	sink := &fallbackRecorder{}
	h.SetModelFallbackSink(sink)
	response := postChat(t, h, userID)
	body := response.Body.String()
	if !strings.Contains(body, "answer from replacement") || !strings.Contains(body, `"status":"used"`) || strings.Contains(body, `"error"`) {
		t.Fatalf("body=%s", body)
	}
	if len(sink.events) != 1 || sink.events[0].ReplacementModel != "recommended-model" || sink.events[0].RecommendationSource != "OpenAI recommended upgrade" || sink.events[0].Source != aiSourceShared {
		t.Fatalf("events=%+v", sink.events)
	}
	if saved := h.creds.GetAIConfig(); saved.Model != "retired-model" {
		t.Fatalf("saved=%+v", saved)
	}
}

func TestAutonomousFallbackUsesSharedOverrideAndRetainsExecutedHistory(t *testing.T) {
	var models []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := captureProviderRequest(r)
		models = append(models, req.body["model"].(string))
		if r.Header.Get("Authorization") != "Bearer shared-secret" {
			t.Error("wrong shared credential")
		}
		encoded, _ := json.Marshal(req.body)
		if !strings.Contains(string(encoded), "already-executed") {
			t.Error("lost executed tool result")
		}
		if req.body["model"] == "retired-override" {
			writeOpenAIAPIError(w, 404, "model_not_found", "model", "unavailable")
			return
		}
		writeOpenAITextSSE(w)
	}))
	defer upstream.Close()
	t.Setenv("OPENAI_BASE_URL", upstream.URL+"/v1")
	h, registry, _, userID := newResolverTestHandler(t)
	h.toolServer = mcp.NewToolServer(nil, nil, nil, nil)
	if err := registry.SetAIConfig(credentials.AIProviderOpenAI, "shared-model"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetCredential(credentials.KeyOpenAIKey, "shared-secret"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetSetting(credentials.KeyAIModelFallbackEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserAIProfile(userID, credentials.AIProviderGemini, "personal-model", "personal-secret", false); err != nil {
		t.Fatal(err)
	}
	turn, err := h.ResolveSharedAutonomousTurn(context.Background(), AutonomousModelOverride{Provider: credentials.AIProviderOpenAI, Model: "retired-override"})
	if err != nil {
		t.Fatal(err)
	}
	p := TurnParams{System: "test", History: Transcript{{Role: RoleAssistant, Content: []TranscriptBlock{{Type: BlockToolUse, ID: "call", Name: "test", Input: json.RawMessage(`{}`)}}}, {Role: RoleUser, Content: []TranscriptBlock{{Type: BlockToolResult, ToolUseID: "call", Content: "already-executed"}}}}}
	for i := 0; i < 2; i++ {
		res, err := turn.Runner.NextTurn(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		if res.ModelFallback == nil || res.ModelFallback.Scope != "remediation_override" {
			t.Fatalf("result=%+v", res)
		}
	}
	if !reflect.DeepEqual(models, []string{"retired-override", "gpt-4.1-mini", "gpt-4.1-mini"}) {
		t.Fatalf("models=%v", models)
	}
}

func TestPersonalFallbackPreferenceOnlySaveAfterRetirement(t *testing.T) {
	h, registry, _, userID := newResolverTestHandler(t)
	if err := registry.SetUserAIProfile(userID, credentials.AIProviderOpenAI, "retired", "secret"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.validationProbe = func(context.Context, credentials.AIProfile, codexapp.AccountRef) error {
		calls++
		return errors.New("retired")
	}
	update := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/ai/settings", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleUser}))
		w := httptest.NewRecorder()
		h.UpdateAISettings(w, r)
		return w
	}
	w := update(`{"provider":"openai","model":"retired","model_fallback_enabled":true}`)
	if w.Code != 200 || calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
	}
	resolved := h.resolveAI(context.Background(), userID)
	if !resolved.ModelFallbackEnabled || resolved.Model != "retired" {
		t.Fatalf("resolved=%+v", resolved)
	}
	w = update(`{"provider":"openai","model":"new-model","model_fallback_enabled":true}`)
	if w.Code != 422 || calls != 1 {
		t.Fatalf("new model bypassed validation: %d calls=%d", w.Code, calls)
	}
	saved, _, _ := registry.GetUserAIConfig(userID)
	if saved.Model != "retired" || !saved.ModelFallbackEnabled {
		t.Fatalf("failed save mutated profile: %+v", saved)
	}
}
