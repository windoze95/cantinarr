package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/grokoauth"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func serveFallbackMetadata(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/language-models":
		_, _ = w.Write([]byte(`{"models":[{"id":"provider-chosen-text","aliases":["grok-build-latest"],"input_modalities":["text"],"output_modalities":["text"]}]}`))
	case "/settings":
		_, _ = w.Write([]byte(`{"default_model":"provider-default"}`))
	case "/v1/models":
		_, _ = w.Write([]byte(`{"data":[{"id":"first-is-not-default","api_backend":"responses"},{"id":"provider-default","api_backend":"responses"}]}`))
	default:
		if !strings.HasPrefix(r.URL.Path, "/v1beta/models/gemini-") {
			http.NotFound(w, r)
			return true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": strings.TrimPrefix(r.URL.Path, "/v1beta/"), "supportedGenerationMethods": []string{"generateContent"}})
	}
	return true
}

func TestGeminiFallbackUsesProviderAliasForSameFamily(t *testing.T) {
	for _, tc := range []struct{ selected, want string }{
		{"gemini-2.0-flash", "gemini-flash-latest"},
		{"models/gemini-2.0-flash-001", "gemini-flash-latest"},
		{"gemini-2.5-flash-lite-preview-06-17", "gemini-flash-lite-latest"},
		{"gemini-3.1-pro-preview", "gemini-pro-latest"},
		{"gemini-pro", "gemini-pro-latest"},
		{"gemini-99.5-pro", "gemini-pro-latest"},
		{"gemini-pro-latest", ""},
		{"gemini-3.8-flash-tts", ""},
		{"gemini-2.5-flash-image", ""},
		{"gemini-3.8-live", ""},
		{"gemini-embedding-001", ""},
		{"unknown-model", ""},
	} {
		t.Run(tc.selected, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("X-Goog-Api-Key") != "same-key" || r.URL.Path != "/v1beta/models/"+tc.want {
					t.Error("wrong endpoint or account")
				}
				serveFallbackMetadata(w, r)
			}))
			defer upstream.Close()
			t.Setenv("GOOGLE_GEMINI_BASE_URL", upstream.URL)
			got, err := geminiFallbackModel(context.Background(), resolvedAI{Model: tc.selected, APIKey: "same-key"})
			if got != tc.want || (err != nil) != (tc.want == "") || (tc.want == "" && calls != 0) {
				t.Fatalf("got %q, %v, calls=%d", got, err, calls)
			}
		})
	}
}

func TestFallbackMetadataMustConfirmACompatibleAlias(t *testing.T) {
	for _, tc := range []struct{ name, provider, body string }{
		{"gemini_missing", "gemini", `{}`},
		{"gemini_wrong_method", "gemini", `{"name":"models/gemini-pro-latest","supportedGenerationMethods":["embedContent"]}`},
		{"gemini_wrong_family", "gemini", `{"name":"models/gemini-pro-latest","supportedGenerationMethods":["generateContent"]}`},
		{"gemini_specialized", "gemini", `{"name":"models/gemini-3.8-flash-tts","supportedGenerationMethods":["generateContent"]}`},
		{"gemini_same_target", "gemini", `{"name":"models/gemini-2.0-flash","supportedGenerationMethods":["generateContent"]}`},
		{"grok_missing_alias", "grok", `{"models":[{"id":"newest-looking-model","input_modalities":["text"],"output_modalities":["text"]}]}`},
		{"grok_wrong_modality", "grok", `{"models":[{"id":"grok-build-latest","input_modalities":["text"],"output_modalities":["image"]}]}`},
		{"grok_same_target", "grok", `{"models":[{"id":"old-model","aliases":["grok-build-latest"],"input_modalities":["text"],"output_modalities":["text"]}]}`},
		{"grok_ambiguous_alias", "grok", `{"models":[{"id":"a","aliases":["grok-build-latest"],"input_modalities":["text"],"output_modalities":["text"]},{"id":"b","aliases":["grok-build-latest"],"input_modalities":["text"],"output_modalities":["text"]}]}`},
		{"malformed", "grok", `{"id":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer upstream.Close()
			t.Setenv("GOOGLE_GEMINI_BASE_URL", upstream.URL)
			t.Setenv("XAI_BASE_URL", upstream.URL)
			var err error
			if tc.provider == "gemini" {
				_, err = geminiFallbackModel(context.Background(), resolvedAI{Model: "gemini-2.0-flash"})
			} else {
				_, err = grokFallbackModel(context.Background(), resolvedAI{Model: "old-model"})
			}
			if err == nil {
				t.Fatal("invented an alternative without compatible provider metadata")
			}
		})
	}
}

func TestGrokOAuthDefaultIsLiveAndScopedToLinkedAccount(t *testing.T) {
	h, _, database, userID := newResolverTestHandler(t)
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x38}, 32))
	if err != nil {
		t.Fatal(err)
	}
	h.grok = grokoauth.NewManager(database, cipher, grokoauth.Options{})
	for _, shared := range []bool{false, true} {
		token := "personal-token"
		if shared {
			token = "shared-token"
		}
		blob, err := cipher.Encrypt(`{"access_token":"` + token + `"}`)
		if err != nil {
			t.Fatal(err)
		}
		if shared {
			_, err = database.Exec(`INSERT INTO shared_grok_account(singleton,auth_blob) VALUES(1,?)`, blob)
		} else {
			_, err = database.Exec(`INSERT INTO user_grok_accounts(user_id,auth_blob) VALUES(?,?)`, userID, blob)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	defaultModel := "provider-default-a"
	settingsReads, modelReads := 0, 0
	wantToken := "personal-token"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+wantToken || r.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" || r.Header.Get("X-Grok-Client-Identifier") != "cantinarr" || r.Header.Get("X-Grok-Client-Version") != grokOAuthProtocolVersion || r.Header.Get("X-Grok-Client-Mode") != "headless" {
			t.Error("wrong account or protocol")
		}
		switch r.URL.Path {
		case "/settings":
			settingsReads++
			_ = json.NewEncoder(w).Encode(map[string]string{"default_model": defaultModel})
		case "/v1/models":
			modelReads++
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": "first-model", "apiBackend": "responses"}, map[string]string{"id": defaultModel, "apiBackend": "responses"}}})
		default:
			t.Error("unexpected endpoint")
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	t.Setenv("GROK_OAUTH_BASE_URL", upstream.URL+"/v1")
	for i, account := range []codexapp.AccountRef{codexapp.PersonalAccount(userID), codexapp.PersonalAccount(userID), codexapp.SharedAccount()} {
		if i > 0 {
			defaultModel = "provider-default-b"
		}
		if i == 2 {
			wantToken = "shared-token"
		}
		got, err := h.grokOAuthFallbackModel(context.Background(), resolvedAI{Account: account, Model: "retired"})
		if err != nil || got != defaultModel {
			t.Fatalf("got=%q err=%v", got, err)
		}
	}
	if settingsReads != 3 || modelReads != 3 {
		t.Fatal("recommendation was cached across requests/accounts")
	}
}

func TestGrokOAuthRejectsMissingAmbiguousOrIncompatibleDefaults(t *testing.T) {
	for _, body := range []string{
		`[]`,
		`[{"id":"first-model","api_backend":"responses"}]`,
		`[{"id":"chosen"}]`,
		`[{"id":"chosen","api_backend":"chat_completions"}]`,
		`[{"id":"chosen","api_backend":"responses","hidden":true}]`,
		`[{"id":"chosen","api_backend":"responses","_meta":{"hidden":true}}]`,
		`[{"id":"chosen","api_backend":"responses","base_url":"https://other-provider.example/v1"}]`,
		`[{"id":"chosen","model":"retired","api_backend":"responses"}]`,
		`[{"id":"chosen","api_backend":"responses"},{"id":"chosen","api_backend":"responses"}]`,
	} {
		var models []grokRecommendedModel
		if err := json.Unmarshal([]byte(body), &models); err != nil {
			t.Fatal(err)
		}
		if got, err := selectGrokDefault(models, "chosen", "retired", "https://cli-chat-proxy.grok.com/v1"); err == nil || got != "" {
			t.Fatalf("body=%s model=%q err=%v", body, got, err)
		}
	}
}

func TestGrokOAuthDefaultCanBeSubscriptionOnly(t *testing.T) {
	for _, restriction := range []string{`"supportedInApi":false`, `"supported_in_api":false`, `"_meta":{"supportedInApi":false}`} {
		var models []grokRecommendedModel
		body := `[{"id":"chosen","api_backend":"responses",` + restriction + `}]`
		if err := json.Unmarshal([]byte(body), &models); err != nil {
			t.Fatal(err)
		}
		got, err := selectGrokDefault(models, "chosen", "retired", "https://cli-chat-proxy.grok.com/v1")
		if err != nil || got != "chosen" {
			t.Fatalf("OAuth-only default rejected: %q %v", got, err)
		}
	}
}

func TestRecommendationReadsRejectRedirectsFailuresOversizeAndCancellation(t *testing.T) {
	for _, status := range []int{302, 401, 404, 429, 503, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/stolen-key")
				w.WriteHeader(status)
				if status == 200 {
					_, _ = w.Write([]byte(`{"oversized":"` + strings.Repeat("x", 1<<20) + `"}`))
				}
			}))
			defer upstream.Close()
			var result any
			if err := readRecommendationJSON(context.Background(), upstream.URL, http.Header{"X-Goog-Api-Key": {"secret"}}, &result); err == nil || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := readRecommendationJSON(ctx, upstream.URL, nil, &result); err == nil || calls != 1 {
				t.Fatal("canceled metadata read made a request")
			}
		})
	}
}

func TestUnsupportedPersonalFallbackPreferenceRejected(t *testing.T) {
	for _, provider := range []string{credentials.AIProviderOpenAI, credentials.AIProviderAnthropic} {
		h, registry, _, userID := newResolverTestHandler(t)
		if err := registry.SetUserAIProfile(userID, provider, "retired", "key"); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/ai/settings", strings.NewReader(`{"provider":"`+provider+`","model":"retired","model_fallback_enabled":true}`))
		req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleUser}))
		response := httptest.NewRecorder()
		h.UpdateAISettings(response, req)
		if response.Code != 400 {
			t.Fatalf("unsupported preference accepted: %d %s", response.Code, response.Body.String())
		}
	}
}
