package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
)

func TestSharedAIKeySaveReauthorizesAfterValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		revoke func(*testing.T, *rbacRouterHarness)
	}{
		{
			name: "admin demoted",
			revoke: func(t *testing.T, harness *rbacRouterHarness) {
				t.Helper()
				if _, err := harness.database.Exec(`UPDATE users SET role = ? WHERE id = ?`, auth.RoleUser, harness.adminID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "admin device revoked",
			revoke: func(t *testing.T, harness *rbacRouterHarness) {
				t.Helper()
				if err := harness.authService.RevokeDevice(harness.adminDeviceID); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream, started, release := newBlockingOpenAIValidationUpstream(t)
			t.Setenv("OPENAI_BASE_URL", upstream.URL+"/v1")
			harness := newRBACRouterHarness(t, false)
			const candidate = "sk-test-shared-reauthorization"
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				result <- serveRBACRequestWithBody(
					harness.router,
					http.MethodPut,
					"/api/admin/credentials",
					harness.adminToken,
					`{"openai_key":"`+candidate+`","ai_provider":"openai","ai_model":"gpt-4.1-mini"}`,
				)
			}()

			awaitBlockedValidation(t, started)
			test.revoke(t, harness)
			release()
			recorder := awaitBlockedResponse(t, result)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("save after authority revocation status=%d, want 403; body=%s", recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), candidate) {
				t.Fatalf("authorization failure exposed candidate credential: %s", recorder.Body.String())
			}
			if harness.registry.IsConfigured(credentials.KeyOpenAIKey) {
				t.Fatal("shared OpenAI credential persisted after authority revocation")
			}
			if got := harness.registry.GetAIConfig(); got.Provider == credentials.AIProviderOpenAI {
				t.Fatalf("shared OpenAI selection persisted after authority revocation: %#v", got)
			}
		})
	}
}

func TestPersonalAIKeySaveAndRotationReauthorizeAfterValidation(t *testing.T) {
	t.Run("combined save after device revocation", func(t *testing.T) {
		upstream, started, release := newBlockingOpenAIValidationUpstream(t)
		t.Setenv("OPENAI_BASE_URL", upstream.URL+"/v1")
		harness := newRBACRouterHarness(t, false)
		const candidate = "sk-test-personal-reauthorization"
		result := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			result <- serveRBACRequestWithBody(
				harness.router,
				http.MethodPut,
				"/api/ai/settings",
				harness.requesterToken,
				`{"provider":"openai","model":"gpt-4.1-mini","api_key":"`+candidate+`"}`,
			)
		}()

		awaitBlockedValidation(t, started)
		if err := harness.authService.RevokeDevice(harness.requesterDeviceID); err != nil {
			t.Fatal(err)
		}
		release()
		recorder := awaitBlockedResponse(t, result)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("personal save after device revocation status=%d, want 403; body=%s", recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), candidate) {
			t.Fatalf("authorization failure exposed candidate credential: %s", recorder.Body.String())
		}
		if _, found, err := harness.registry.LoadUserAIProfile(t.Context(), harness.requesterID); err != nil || found {
			t.Fatalf("personal profile persisted after device revocation: found=%t err=%v", found, err)
		}
	})

	t.Run("key rotation after role disabled", func(t *testing.T) {
		upstream, started, release := newBlockingOpenAIValidationUpstream(t)
		t.Setenv("OPENAI_BASE_URL", upstream.URL+"/v1")
		harness := newRBACRouterHarness(t, false)
		const (
			oldKey    = "sk-test-personal-old"
			candidate = "sk-test-personal-new"
		)
		if err := harness.registry.SetUserAIConfig(harness.requesterID, credentials.AIProviderOpenAI, "gpt-4.1-mini"); err != nil {
			t.Fatal(err)
		}
		if err := harness.registry.SetUserAICredential(harness.requesterID, credentials.AIProviderOpenAI, oldKey); err != nil {
			t.Fatal(err)
		}
		result := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			result <- serveRBACRequestWithBody(
				harness.router,
				http.MethodPut,
				"/api/ai/credentials/openai",
				harness.requesterToken,
				`{"api_key":"`+candidate+`","model":"gpt-4.1-mini"}`,
			)
		}()

		awaitBlockedValidation(t, started)
		if _, err := harness.database.Exec(`UPDATE users SET role = 'disabled' WHERE id = ?`, harness.requesterID); err != nil {
			t.Fatal(err)
		}
		release()
		recorder := awaitBlockedResponse(t, result)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("personal rotation after role disable status=%d, want 403; body=%s", recorder.Code, recorder.Body.String())
		}
		key, found, err := harness.registry.UserAICredential(harness.requesterID, credentials.AIProviderOpenAI)
		if err != nil || !found || key != oldKey {
			t.Fatalf("personal rotation changed old key after authority revocation: found=%t err=%v", found, err)
		}
	})
}

func newBlockingOpenAIValidationUpstream(t *testing.T) (*httptest.Server, <-chan struct{}, func()) {
	t.Helper()
	started := make(chan struct{}, 1)
	releaseChannel := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseChannel) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		select {
		case <-releaseChannel:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-reauth","object":"chat.completion.chunk","created":1,"model":"gpt-4.1-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(func() {
		release()
		server.Close()
	})
	return server, started, release
}

func awaitBlockedValidation(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider validation did not reach the blocking upstream")
	}
}

func awaitBlockedResponse(t *testing.T, result <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case recorder := <-result:
		return recorder
	case <-time.After(2 * time.Second):
		t.Fatal("credential save did not finish after provider validation was released")
		return nil
	}
}
