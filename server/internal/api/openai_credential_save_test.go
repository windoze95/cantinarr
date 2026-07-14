package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type strictOpenAISaveRequest struct {
	path          string
	authorization string
	body          map[string]any
}

// TestOpenAIAPIKeySavesThroughAuthenticatedRouter covers the two public save
// paths that must reach the same production OpenAI validation adapter. The fake
// upstream reproduces OpenAI's hosted contract: tool_choice is rejected when a
// request does not also contain tools.
func TestOpenAIAPIKeySavesThroughAuthenticatedRouter(t *testing.T) {
	upstream, requests := newStrictOpenAISaveUpstream(t)
	t.Setenv("OPENAI_BASE_URL", upstream.URL+"/v1")

	harness := newRBACRouterHarness(t, false)
	const (
		model        = "gpt-4.1-mini"
		sharedKey    = "sk-test-shared-openai"
		personalKey  = "sk-test-personal-openai"
		sharedBody   = `{"openai_key":"sk-test-shared-openai","ai_provider":"openai","ai_model":"gpt-4.1-mini"}`
		personalBody = `{"provider":"openai","model":"gpt-4.1-mini","api_key":"sk-test-personal-openai"}`
	)

	denied := serveRBACRequestWithBody(
		harness.router,
		http.MethodPut,
		"/api/admin/credentials",
		harness.requesterToken,
		sharedBody,
	)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("requester shared save status=%d, want 403; body=%s", denied.Code, denied.Body.String())
	}
	assertNoStrictOpenAISaveRequest(t, requests)
	if harness.registry.IsConfigured(credentials.KeyOpenAIKey) {
		t.Fatal("denied requester shared save persisted an OpenAI key")
	}

	shared := serveRBACRequestWithBody(
		harness.router,
		http.MethodPut,
		"/api/admin/credentials",
		harness.adminToken,
		sharedBody,
	)
	if shared.Code != http.StatusOK {
		t.Fatalf("admin shared save status=%d, want 200; body=%s", shared.Code, shared.Body.String())
	}
	assertResponseOmitsSyntheticSecrets(t, shared.Body.String(), sharedKey, personalKey)
	assertStrictOpenAISaveRequest(t, requests, sharedKey, model)
	if got := harness.registry.GetCredential(credentials.KeyOpenAIKey); got != sharedKey {
		t.Fatalf("stored shared credential=%q, want synthetic shared key", got)
	}
	if got := harness.registry.GetAIConfig(); got.Provider != credentials.AIProviderOpenAI || got.Model != model {
		t.Fatalf("stored shared config=%#v", got)
	}
	assertEncryptedSetting(t, harness, credentials.KeyOpenAIKey, sharedKey)

	personal := serveRBACRequestWithBody(
		harness.router,
		http.MethodPut,
		"/api/ai/settings",
		harness.requesterToken,
		personalBody,
	)
	if personal.Code != http.StatusOK {
		t.Fatalf("requester personal save status=%d, want 200; body=%s", personal.Code, personal.Body.String())
	}
	assertResponseOmitsSyntheticSecrets(t, personal.Body.String(), sharedKey, personalKey)
	assertStrictOpenAISaveRequest(t, requests, personalKey, model)

	profile, found, err := harness.registry.LoadUserAIProfile(t.Context(), harness.requesterID)
	if err != nil || !found {
		t.Fatalf("load requester personal profile: found=%t err=%v", found, err)
	}
	if profile.Config.Provider != credentials.AIProviderOpenAI || profile.Config.Model != model || profile.APIKey != personalKey {
		t.Fatalf("requester personal profile=%#v", profile)
	}
	if _, found, err := harness.registry.LoadUserAIProfile(t.Context(), harness.adminID); err != nil || found {
		t.Fatalf("requester save crossed into admin profile: found=%t err=%v", found, err)
	}
	assertEncryptedUserCredential(t, harness, harness.requesterID, credentials.AIProviderOpenAI, personalKey)
	if got := harness.registry.GetCredential(credentials.KeyOpenAIKey); got != sharedKey {
		t.Fatal("personal save changed the shared OpenAI credential")
	}
}

func TestLiveOpenAIAPIKeySavesThroughAuthenticatedRouter(t *testing.T) {
	if os.Getenv("CANTINARR_LIVE_AI_TESTS") != "1" {
		t.Skip("set CANTINARR_LIVE_AI_TESTS=1 to run hosted-provider save tests")
	}
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		t.Skip("OPENAI_API_KEY is not set")
	}
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	harness := newRBACRouterHarness(t, false)

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	sharedPayload, err := json.Marshal(map[string]string{
		credentials.KeyOpenAIKey:  key,
		credentials.KeyAIProvider: credentials.AIProviderOpenAI,
		credentials.KeyAIModel:    "gpt-4.1-mini",
	})
	if err != nil {
		t.Fatal(err)
	}
	shared := serveRBACRequestWithBody(
		harness.router,
		http.MethodPut,
		"/api/admin/credentials",
		harness.adminToken,
		string(sharedPayload),
	)
	if strings.Contains(shared.Body.String(), key) {
		t.Fatal("hosted shared save response exposed the OpenAI key")
	}
	if shared.Code != http.StatusOK {
		t.Fatalf("hosted shared OpenAI save status=%d", shared.Code)
	}
	if got := harness.registry.GetCredential(credentials.KeyOpenAIKey); got != key {
		t.Fatal("hosted shared save did not persist the exact OpenAI key")
	}
	assertEncryptedSetting(t, harness, credentials.KeyOpenAIKey, key)

	personalPayload, err := json.Marshal(map[string]string{
		"provider": credentials.AIProviderOpenAI,
		"model":    "gpt-4.1-mini",
		"api_key":  key,
	})
	if err != nil {
		t.Fatal(err)
	}
	personal := serveRBACRequestWithBody(
		harness.router,
		http.MethodPut,
		"/api/ai/settings",
		harness.requesterToken,
		string(personalPayload),
	)
	if strings.Contains(personal.Body.String(), key) {
		t.Fatal("hosted personal save response exposed the OpenAI key")
	}
	if personal.Code != http.StatusOK {
		t.Fatalf("hosted personal OpenAI save status=%d", personal.Code)
	}
	profile, found, err := harness.registry.LoadUserAIProfile(t.Context(), harness.requesterID)
	if err != nil || !found || profile.Config.Provider != credentials.AIProviderOpenAI ||
		profile.Config.Model != "gpt-4.1-mini" || profile.APIKey != key {
		t.Fatal("hosted personal save did not persist the exact isolated OpenAI profile")
	}
	if _, found, err := harness.registry.LoadUserAIProfile(t.Context(), harness.adminID); err != nil || found {
		t.Fatal("hosted requester save crossed into the admin personal profile")
	}
	assertEncryptedUserCredential(t, harness, harness.requesterID, credentials.AIProviderOpenAI, key)
	if got := harness.registry.GetCredential(credentials.KeyOpenAIKey); got != key {
		t.Fatal("hosted personal save changed the shared OpenAI credential")
	}
	if strings.Contains(logs.String(), key) {
		t.Fatal("hosted OpenAI save wrote the API key to logs")
	}
}

func newStrictOpenAISaveUpstream(t *testing.T) (*httptest.Server, <-chan strictOpenAISaveRequest) {
	t.Helper()
	requests := make(chan strictOpenAISaveRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"error":{"message":"read request"}}`, http.StatusBadRequest)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
			return
		}
		requests <- strictOpenAISaveRequest{
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			body:          body,
		}
		if _, hasChoice := body["tool_choice"]; hasChoice {
			if tools, ok := body["tools"].([]any); !ok || len(tools) == 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
					"message": "Tool choice is only allowed when tools are specified.",
					"type":    "invalid_request_error",
					"param":   "tool_choice",
					"code":    nil,
				}})
				return
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-save","object":"chat.completion.chunk","created":1,"model":"gpt-4.1-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-save","object":"chat.completion.chunk","created":1,"model":"gpt-4.1-mini","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":1,"total_tokens":8}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func assertNoStrictOpenAISaveRequest(t *testing.T, requests <-chan strictOpenAISaveRequest) {
	t.Helper()
	select {
	case request := <-requests:
		t.Fatalf("authorization failure reached OpenAI upstream: %#v", request.body)
	default:
	}
}

func assertStrictOpenAISaveRequest(t *testing.T, requests <-chan strictOpenAISaveRequest, key, model string) {
	t.Helper()
	select {
	case request := <-requests:
		if request.path != "/v1/chat/completions" {
			t.Fatalf("OpenAI validation path=%q", request.path)
		}
		if request.authorization != "Bearer "+key {
			t.Fatalf("OpenAI validation authorization did not use the expected synthetic key")
		}
		if request.body["model"] != model {
			t.Fatalf("OpenAI validation model=%v, want %q", request.body["model"], model)
		}
		if _, found := request.body["tool_choice"]; found {
			t.Fatalf("tool-free OpenAI save validation sent tool_choice: %#v", request.body)
		}
		if _, found := request.body["tools"]; found {
			t.Fatalf("tool-free OpenAI save validation sent tools: %#v", request.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OpenAI save validation did not reach the strict upstream")
	}
}

func assertResponseOmitsSyntheticSecrets(t *testing.T, response string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(response, secret) {
			t.Fatalf("credential save response exposed a synthetic secret: %s", response)
		}
	}
}

func assertEncryptedSetting(t *testing.T, harness *rbacRouterHarness, key, plaintext string) {
	t.Helper()
	var stored string
	if err := harness.database.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == plaintext || strings.Contains(stored, plaintext) || !secrets.IsEncrypted(stored) {
		t.Fatal("shared credential was not encrypted at rest")
	}
}

func assertEncryptedUserCredential(t *testing.T, harness *rbacRouterHarness, userID int64, provider, plaintext string) {
	t.Helper()
	var stored string
	if err := harness.database.QueryRow(`
		SELECT credential_blob FROM user_ai_credentials
		WHERE user_id = ? AND provider = ?`, userID, provider).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == plaintext || strings.Contains(stored, plaintext) || !secrets.IsEncrypted(stored) {
		t.Fatal("personal credential was not encrypted at rest")
	}
}
