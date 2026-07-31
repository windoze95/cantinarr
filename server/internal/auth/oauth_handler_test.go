package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAuthorizationServerMetadataAdvertisesIssuerResponseParameter(t *testing.T) {
	handler := NewOAuthHandler(setupTestService(t), "https://canonical.example.com")
	req := httptest.NewRequest(http.MethodGet, "http://cantinarr.internal/.well-known/oauth-authorization-server", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "media.example.com")
	recorder := httptest.NewRecorder()

	handler.AuthorizationServerMetadata(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var metadata struct {
		Issuer                                     string `json:"issuer"`
		AuthorizationResponseISSParameterSupported bool   `json:"authorization_response_iss_parameter_supported"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata.Issuer != "https://canonical.example.com" {
		t.Fatalf("issuer = %q, want %q", metadata.Issuer, "https://canonical.example.com")
	}
	if !metadata.AuthorizationResponseISSParameterSupported {
		t.Fatal("authorization_response_iss_parameter_supported = false, want true")
	}
}

func TestAuthorizationServerMetadataDoesNotAdvertiseUnstableIssuer(t *testing.T) {
	handler := NewOAuthHandler(setupTestService(t), "")
	req := httptest.NewRequest(http.MethodGet, "http://cantinarr.internal/.well-known/oauth-authorization-server", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "proxy-alias.example.com")
	recorder := httptest.NewRecorder()

	handler.AuthorizationServerMetadata(recorder, req)

	var metadata map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if _, advertised := metadata["authorization_response_iss_parameter_supported"]; advertised {
		t.Fatalf("request-derived issuer unexpectedly advertised RFC 9207 support: %#v", metadata)
	}
	redirect, err := oauthCodeRedirect("https://client.example.com/callback", "code", "state", "")
	if err != nil {
		t.Fatalf("oauthCodeRedirect: %v", err)
	}
	parsed, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if got := parsed.Query().Get("iss"); got != "" {
		t.Fatalf("request-derived authorization redirect iss = %q, want omitted", got)
	}
}

func TestMCPUnauthorizedChallengeUsesCanonicalIssuer(t *testing.T) {
	handler := NewOAuthHandler(setupTestService(t), "https://canonical.example.com")
	req := httptest.NewRequest(http.MethodPost, "http://internal:8585/mcp", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	recorder := httptest.NewRecorder()

	handler.MCPAuthMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthenticated request reached MCP handler")
	})).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	want := `Bearer resource_metadata="https://canonical.example.com/.well-known/oauth-protected-resource/mcp"`
	if got := recorder.Header().Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
}

func TestAuthorizeSuccessIncludesIssuer(t *testing.T) {
	svc := setupTestService(t)
	handler := NewOAuthHandler(svc, "https://canonical.example.com")
	redirectURI := "https://client.example.com/oauth/callback?existing=value"
	client, err := svc.RegisterOAuthClient("Test MCP Client", []string{redirectURI})
	if err != nil {
		t.Fatalf("register OAuth client: %v", err)
	}

	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ClientID},
		"redirect_uri":          {redirectURI},
		"state":                 {"client-state"},
		"code_challenge":        {"test-challenge"},
		"code_challenge_method": {"S256"},
		"resource":              {"https://canonical.example.com/mcp"},
		"username":              {"admin"},
		"password":              {"testpass123"},
	}
	req := httptest.NewRequest(http.MethodPost, "http://cantinarr.internal/oauth/authorize", bytes.NewBufferString(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "media.example.com")
	recorder := httptest.NewRecorder()

	handler.Authorize(recorder, req)

	if recorder.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusFound, recorder.Body.String())
	}
	location, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorization redirect: %v", err)
	}
	if location.Query().Get("code") == "" {
		t.Fatal("authorization redirect omitted code")
	}
	if got := location.Query().Get("state"); got != "client-state" {
		t.Fatalf("state = %q, want %q", got, "client-state")
	}
	if got := location.Query().Get("iss"); got != "https://canonical.example.com" {
		t.Fatalf("iss = %q, want %q", got, "https://canonical.example.com")
	}
	if got := location.Query().Get("existing"); got != "value" {
		t.Fatalf("existing query parameter = %q, want %q", got, "value")
	}
}

func TestRegisterClientApplicationType(t *testing.T) {
	tests := []struct {
		name            string
		applicationType *string
		redirectURI     string
		wantStatus      int
		wantType        string
		wantError       string
	}{
		{
			name:            "native",
			applicationType: stringPointer("native"),
			redirectURI:     "http://127.0.0.1:49152/callback",
			wantStatus:      http.StatusCreated,
			wantType:        "native",
		},
		{
			name:            "web",
			applicationType: stringPointer("web"),
			redirectURI:     "https://client.example.com/callback",
			wantStatus:      http.StatusCreated,
			wantType:        "web",
		},
		{
			name:            "web rejects native loopback redirect",
			applicationType: stringPointer("web"),
			redirectURI:     "http://127.0.0.1:49152/callback",
			wantStatus:      http.StatusBadRequest,
			wantError:       "invalid_redirect_uri",
		},
		{
			name:        "omitted remains compatible",
			redirectURI: "http://localhost:49152/callback",
			wantStatus:  http.StatusCreated,
			wantType:    "web",
		},
		{
			name:            "unsupported",
			applicationType: stringPointer("service"),
			redirectURI:     "https://client.example.com/callback",
			wantStatus:      http.StatusBadRequest,
			wantError:       "invalid_client_metadata",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewOAuthHandler(setupTestService(t), "")
			payload := map[string]any{
				"client_name":   "Test MCP Client",
				"redirect_uris": []string{tt.redirectURI},
			}
			if tt.applicationType != nil {
				payload["application_type"] = *tt.applicationType
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal registration request: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "https://media.example.com/oauth/register", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			handler.RegisterClient(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			var response map[string]any
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatalf("decode registration response: %v", err)
			}
			if tt.wantError != "" {
				if got, _ := response["error"].(string); got != tt.wantError {
					t.Fatalf("error = %q, want %q", got, tt.wantError)
				}
				return
			}
			if got, _ := response["application_type"].(string); got != tt.wantType {
				t.Fatalf("application_type = %q, want %q", got, tt.wantType)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}
