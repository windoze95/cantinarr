package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// TestAIAPIKeyProviderScopeRoleGrantMatrix exercises the real HTTP adapters
// through the authenticated API. It deliberately crosses every API-key
// provider with both roles, personal/shared scope, credential readiness, and
// the included-AI grant. This is the boundary most likely to regress when a
// provider SDK, validation request, or resolver rule changes.
func TestAIAPIKeyProviderScopeRoleGrantMatrix(t *testing.T) {
	upstream, captures := newAIProviderMatrixUpstream(t)
	t.Setenv("ANTHROPIC_BASE_URL", upstream.URL)
	t.Setenv("OPENAI_BASE_URL", upstream.URL)
	t.Setenv("GOOGLE_GEMINI_BASE_URL", upstream.URL)

	harness := newRBACRouterHarness(t, false)
	providers := []string{
		credentials.AIProviderAnthropic,
		credentials.AIProviderOpenAI,
		credentials.AIProviderGemini,
	}
	actors := []struct {
		name  string
		role  string
		id    int64
		token string
	}{
		{name: "admin", role: auth.RoleAdmin, id: harness.adminID, token: harness.adminToken},
		{name: "requester", role: auth.RoleUser, id: harness.requesterID, token: harness.requesterToken},
	}

	for _, provider := range providers {
		for _, actor := range actors {
			for _, source := range []string{"personal", "shared"} {
				for _, credentialReady := range []bool{false, true} {
					for _, sharedGranted := range []bool{false, true} {
						name := strings.Join([]string{
							provider,
							actor.name,
							source,
							"credential=" + strconv.FormatBool(credentialReady),
							"grant=" + strconv.FormatBool(sharedGranted),
						}, "/")
						t.Run(name, func(t *testing.T) {
							configureAIAPIKeyMatrixCase(t, harness, actor.id, provider, source, credentialReady, sharedGranted)

							wantAvailable := credentialReady && (source == "personal" || sharedGranted)
							wantSource := source
							if source == "shared" && !sharedGranted {
								wantSource = "none"
							}
							assertAIAvailabilityMatrix(t, harness.router, actor.token, wantAvailable, wantSource)

							recorder := serveRBACRequestWithBody(
								harness.router,
								http.MethodPost,
								"/api/ai/chat",
								actor.token,
								`{"messages":[{"role":"user","content":"matrix probe"}]}`,
							)
							if !wantAvailable {
								if recorder.Code != http.StatusServiceUnavailable {
									t.Fatalf("chat status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
								}
								assertNoAIProviderMatrixCapture(t, captures)
								return
							}

							if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"text":"MATRIX_OK"`) || !strings.Contains(recorder.Body.String(), "data: [DONE]") {
								t.Fatalf("chat did not complete: status=%d body=%s", recorder.Code, recorder.Body.String())
							}
							capture := awaitAIProviderMatrixCapture(t, captures)
							if capture.provider != provider {
								t.Fatalf("upstream provider = %q, want %q", capture.provider, provider)
							}
							wantKey := matrixCredential(provider, source, actor.id)
							if capture.apiKey != wantKey {
								t.Fatalf("upstream used wrong credential boundary: got %q, want %q", capture.apiKey, wantKey)
							}
							wantTools := matrixToolNames(actor.role)
							if strings.Join(capture.tools, "\x00") != strings.Join(wantTools, "\x00") {
								t.Fatalf("offered tools for role %q differ:\n got %v\nwant %v", actor.role, capture.tools, wantTools)
							}
						})
					}
				}
			}
		}
	}
}

type aiProviderMatrixCapture struct {
	provider string
	apiKey   string
	tools    []string
}

func newAIProviderMatrixUpstream(t *testing.T) (*httptest.Server, <-chan aiProviderMatrixCapture) {
	t.Helper()
	captures := make(chan aiProviderMatrixCapture, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid test request"}`, http.StatusBadRequest)
			return
		}

		provider := ""
		apiKey := ""
		switch {
		case strings.Contains(r.URL.Path, "messages"):
			provider = credentials.AIProviderAnthropic
			apiKey = r.Header.Get("X-Api-Key")
		case strings.Contains(r.URL.Path, "chat/completions"):
			provider = credentials.AIProviderOpenAI
			apiKey = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		case strings.Contains(r.URL.Path, "streamGenerateContent"):
			provider = credentials.AIProviderGemini
			apiKey = r.Header.Get("X-Goog-Api-Key")
			if apiKey == "" {
				apiKey = r.URL.Query().Get("key")
			}
		default:
			http.Error(w, `{"error":"unknown test endpoint"}`, http.StatusNotFound)
			return
		}

		captures <- aiProviderMatrixCapture{
			provider: provider,
			apiKey:   apiKey,
			tools:    matrixRequestToolNames(provider, body),
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch provider {
		case credentials.AIProviderAnthropic:
			fmt.Fprint(w, "event: message_start\n")
			fmt.Fprint(w, `data: {"type":"message_start","message":{"id":"msg_matrix","type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`+"\n\n")
			fmt.Fprint(w, "event: content_block_start\n")
			fmt.Fprint(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`+"\n\n")
			fmt.Fprint(w, "event: content_block_delta\n")
			fmt.Fprint(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"MATRIX_OK"}}`+"\n\n")
			fmt.Fprint(w, "event: content_block_stop\n")
			fmt.Fprint(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
			fmt.Fprint(w, "event: message_delta\n")
			fmt.Fprint(w, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`+"\n\n")
			fmt.Fprint(w, "event: message_stop\n")
			fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
		case credentials.AIProviderOpenAI:
			fmt.Fprint(w, `data: {"id":"chatcmpl-matrix","object":"chat.completion.chunk","created":1,"model":"gpt-4.1-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"MATRIX_OK"},"finish_reason":null}]}`+"\n\n")
			fmt.Fprint(w, `data: {"id":"chatcmpl-matrix","object":"chat.completion.chunk","created":1,"model":"gpt-4.1-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case credentials.AIProviderGemini:
			fmt.Fprint(w, `data: {"candidates":[{"content":{"parts":[{"text":"MATRIX_OK"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`+"\n\n")
		}
	}))
	t.Cleanup(server.Close)
	return server, captures
}

func configureAIAPIKeyMatrixCase(t *testing.T, harness *rbacRouterHarness, userID int64, provider, source string, credentialReady, sharedGranted bool) {
	t.Helper()
	model := matrixModel(provider)
	if err := harness.registry.SetAIConfig(provider, model); err != nil {
		t.Fatal(err)
	}
	sharedKey := credentials.AIKeyCredentialKey(provider)
	if err := harness.registry.DeleteCredential(sharedKey); err != nil {
		t.Fatal(err)
	}
	if source == "shared" && credentialReady {
		if err := harness.registry.SetCredential(sharedKey, matrixCredential(provider, source, userID)); err != nil {
			t.Fatal(err)
		}
	}

	if err := harness.registry.DeleteUserAIConfig(userID); err != nil {
		t.Fatal(err)
	}
	if err := harness.registry.DeleteUserAICredential(userID, provider); err != nil {
		t.Fatal(err)
	}
	if source == "personal" {
		if err := harness.registry.SetUserAIConfig(userID, provider, model); err != nil {
			t.Fatal(err)
		}
		if credentialReady {
			if err := harness.registry.SetUserAICredential(userID, provider, matrixCredential(provider, source, userID)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := harness.database.Exec(`UPDATE users SET ai_shared_enabled = ? WHERE id = ?`, sharedGranted, userID); err != nil {
		t.Fatal(err)
	}
}

func assertAIAvailabilityMatrix(t *testing.T, router http.Handler, token string, wantAvailable bool, wantSource string) {
	t.Helper()
	recorder := serveRBACRequest(router, http.MethodGet, "/api/ai/available", token)
	if recorder.Code != http.StatusOK {
		t.Fatalf("availability status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Available bool   `json:"available"`
		Source    string `json:"source"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Available != wantAvailable || response.Source != wantSource {
		t.Fatalf("availability = %#v, want available=%t source=%q", response, wantAvailable, wantSource)
	}
}

func awaitAIProviderMatrixCapture(t *testing.T, captures <-chan aiProviderMatrixCapture) aiProviderMatrixCapture {
	t.Helper()
	select {
	case capture := <-captures:
		return capture
	case <-time.After(2 * time.Second):
		t.Fatal("provider adapter did not reach the test upstream")
		return aiProviderMatrixCapture{}
	}
}

func assertNoAIProviderMatrixCapture(t *testing.T, captures <-chan aiProviderMatrixCapture) {
	t.Helper()
	select {
	case capture := <-captures:
		t.Fatalf("unavailable AI case reached %s upstream", capture.provider)
	default:
	}
}

func matrixCredential(provider, source string, userID int64) string {
	if source == "shared" {
		return "matrix-shared-" + provider
	}
	return fmt.Sprintf("matrix-personal-%d-%s", userID, provider)
}

func matrixModel(provider string) string {
	switch provider {
	case credentials.AIProviderAnthropic:
		return "claude-test"
	case credentials.AIProviderOpenAI:
		return "gpt-4.1-mini"
	default:
		return "gemini-test"
	}
}

func matrixToolNames(role string) []string {
	server := mcp.NewToolServer(nil, nil, nil, nil)
	tools := server.GetToolsForRole(role)
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func matrixRequestToolNames(provider string, body map[string]any) []string {
	var names []string
	tools, _ := body["tools"].([]any)
	for _, rawTool := range tools {
		tool, _ := rawTool.(map[string]any)
		switch provider {
		case credentials.AIProviderAnthropic:
			if name, _ := tool["name"].(string); name != "" {
				names = append(names, name)
			}
		case credentials.AIProviderOpenAI:
			function, _ := tool["function"].(map[string]any)
			if name, _ := function["name"].(string); name != "" {
				names = append(names, name)
			}
		case credentials.AIProviderGemini:
			declarations, _ := tool["functionDeclarations"].([]any)
			for _, rawDeclaration := range declarations {
				declaration, _ := rawDeclaration.(map[string]any)
				if name, _ := declaration["name"].(string); name != "" {
					names = append(names, name)
				}
			}
		}
	}
	sort.Strings(names)
	return names
}
