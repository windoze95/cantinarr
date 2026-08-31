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

// TestLocalProviderSavesThroughAuthenticatedRouter proves the local provider
// end to end through the real router: the admin save's validation turn lands
// on the configured upstream while the env-default upstream stays silent, the
// endpoint persists plaintext and echoes back to admins, and a requester's
// personal openai save still validates against the default endpoint —
// personal keys are never redirected by the shared local endpoint.
func TestLocalProviderSavesThroughAuthenticatedRouter(t *testing.T) {
	configuredUpstream, configuredRequests := newStrictOpenAISaveUpstream(t)
	envUpstream, envRequests := newStrictOpenAISaveUpstream(t)
	t.Setenv("OPENAI_BASE_URL", envUpstream.URL+"/v1")

	harness := newRBACRouterHarness(t, false)
	const (
		model       = "gpt-4.1-mini"
		personalKey = "sk-test-personal-openai"
	)
	sharedPayload, err := json.Marshal(map[string]string{
		credentials.KeyAIProvider:         credentials.AIProviderLocalOpenAI,
		credentials.KeyAIModel:            model,
		credentials.KeyLocalOpenAIBaseURL: configuredUpstream.URL + "/v1",
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
	if shared.Code != http.StatusOK {
		t.Fatalf("admin local-provider save status=%d, want 200; body=%s", shared.Code, shared.Body.String())
	}
	// Keyless local profiles authenticate with the fixed placeholder bearer.
	assertStrictOpenAISaveRequest(t, configuredRequests, "cantinarr-local", model)
	assertNoStrictOpenAISaveRequest(t, envRequests)
	if got := harness.registry.GetSetting(credentials.KeyLocalOpenAIBaseURL); got != configuredUpstream.URL+"/v1" {
		t.Fatalf("stored base URL=%q", got)
	}

	status := serveRBACRequest(harness.router, http.MethodGet, "/api/admin/credentials", harness.adminToken)
	if status.Code != http.StatusOK {
		t.Fatalf("admin status read=%d; body=%s", status.Code, status.Body.String())
	}
	var decoded struct {
		AI struct {
			LocalBaseURL string `json:"local_openai_base_url"`
		} `json:"ai"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AI.LocalBaseURL != configuredUpstream.URL+"/v1" {
		t.Fatalf("echoed base URL=%q", decoded.AI.LocalBaseURL)
	}

	personal := serveRBACRequestWithBody(
		harness.router,
		http.MethodPut,
		"/api/ai/settings",
		harness.requesterToken,
		`{"provider":"openai","model":"gpt-4.1-mini","api_key":"sk-test-personal-openai"}`,
	)
	if personal.Code != http.StatusOK {
		t.Fatalf("requester personal save status=%d, want 200; body=%s", personal.Code, personal.Body.String())
	}
	assertStrictOpenAISaveRequest(t, envRequests, personalKey, model)
	assertNoStrictOpenAISaveRequest(t, configuredRequests)
	if strings.Contains(personal.Body.String(), configuredUpstream.URL) {
		t.Fatalf("personal settings response leaked the shared base URL: %s", personal.Body.String())
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

// TestCredentialSaveErrorsAreJSON pins the error transport through the real
// router: the app renders body["error"] only when the response is labeled
// application/json, so a text/plain error means users see a generic "Failed
// to save settings." with the actual reason discarded.
func TestCredentialSaveErrorsAreJSON(t *testing.T) {
	harness := newRBACRouterHarness(t, false)

	resp := serveRBACRequestWithBody(
		harness.router,
		http.MethodPut,
		"/api/admin/credentials",
		harness.adminToken,
		`{"ai_provider":"not-a-provider"}`,
	)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unknown-provider save status=%d, want 400; body=%s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("error Content-Type = %q, want application/json so the app can show the reason", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not valid JSON: %v (%s)", err, resp.Body.String())
	}
	if body["error"] == "" {
		t.Fatalf("error body missing the error field: %s", resp.Body.String())
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
		// The probe must carry the same tool payload a real chat serializes —
		// a provider that rejects a tool schema has to fail at save time, not
		// on the first real chat (#497) — while tool_choice none keeps the
		// reply a plain text turn.
		if choice, found := request.body["tool_choice"]; !found || choice != "none" {
			t.Fatalf("save validation tool_choice=%v, want \"none\" alongside the tool payload", request.body["tool_choice"])
		}
		tools, ok := request.body["tools"].([]any)
		if !ok || len(tools) == 0 {
			t.Fatal("save validation sent no tools; the probe must carry the chat tool payload")
		}
		seenGrabRelease := false
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			fn, ok := tool["function"].(map[string]any)
			if !ok {
				continue
			}
			if fn["name"] == "grab_release" {
				seenGrabRelease = true
			}
		}
		if !seenGrabRelease {
			t.Fatal("save validation omitted grab_release; the probe must exercise the full admin catalog")
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
