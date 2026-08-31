package ai

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/grokoauth"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func TestGrokAccountHandlersDisableCaching(t *testing.T) {
	h := &Handler{}
	tests := map[string]http.HandlerFunc{
		"status": h.GrokStatus,
		"begin":  h.BeginGrokDeviceLogin,
		"poll":   h.CheckGrokDeviceLogin,
		"cancel": h.CancelGrokDeviceLogin,
		"unlink": h.UnlinkGrok,
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

func TestSharedGrokAdminHandlerRejectsRegularUser(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ai/grok/status", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: 1, Role: auth.RoleUser}))
	(&Handler{}).SharedGrokStatus(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
}

// grokUpstream fakes the xAI device-flow endpoints with instant approval.
func grokUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	segment := base64.RawURLEncoding.EncodeToString
	payload, err := json.Marshal(map[string]any{"email": "grok@example.com", "plan": "supergrok"})
	if err != nil {
		t.Fatal(err)
	}
	idToken := segment([]byte(`{"alg":"none"}`)) + "." + segment(payload) + "." + segment([]byte("sig"))
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/device/code", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "device-code-1",
			"user_code":        "GROK-1234",
			"verification_uri": "https://accounts.x.ai/oauth2/device",
			"expires_in":       900,
			"interval":         1,
		})
	})
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "grok-bearer-token",
			"refresh_token": "grok-refresh-token",
			"id_token":      idToken,
			"expires_in":    3600,
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// newGrokTestManager attaches a Grok manager (same DB and cipher as
// newResolverTestHandler) backed by an instantly-approving fake upstream.
// The returned clock pointer steps past device-flow poll intervals.
func newGrokTestManager(t *testing.T, h *Handler, database *sql.DB) (*grokoauth.Manager, *time.Time) {
	t.Helper()
	server := grokUpstream(t)
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x27}, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	manager := grokoauth.NewManager(database, cipher, grokoauth.Options{
		AuthBaseURL: server.URL,
		Clock:       func() time.Time { return now },
	})
	h.SetGrokManager(manager)
	return manager, &now
}

func linkPersonalGrok(t *testing.T, manager *grokoauth.Manager, now *time.Time, userID int64) {
	t.Helper()
	login, err := manager.BeginDeviceLogin(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Second)
	check, err := manager.CheckDeviceLogin(context.Background(), userID, login.FlowID)
	if err != nil || check.Status != grokoauth.LoginConnected {
		t.Fatalf("link personal grok: %#v err=%v", check, err)
	}
}

func linkSharedGrok(t *testing.T, manager *grokoauth.Manager, now *time.Time, actorID int64) {
	t.Helper()
	login, err := manager.BeginDeviceLoginForAccount(context.Background(), grokoauth.SharedAccount(), actorID)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Second)
	check, err := manager.CheckDeviceLoginForAccount(context.Background(), grokoauth.SharedAccount(), actorID, login.FlowID)
	if err != nil || check.Status != grokoauth.LoginConnected {
		t.Fatalf("link shared grok: %#v err=%v", check, err)
	}
}

func TestCompletedPersonalGrokFlowSelectsPersonalOverride(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	manager, now := newGrokTestManager(t, h, database)
	linkPersonalGrok(t, manager, now, userID)
	if err := h.selectPersonalGrok(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	config, found, err := registry.GetUserAIConfig(userID)
	if err != nil || !found || config.Provider != credentials.AIProviderGrokOAuth || config.Model != "grok-4.6" {
		t.Fatalf("personal config = %#v found=%t err=%v", config, found, err)
	}
}

func TestPersonalGrokFlowDoesNotSelectProviderWhenResponseTestFails(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	manager, now := newGrokTestManager(t, h, database)
	linkPersonalGrok(t, manager, now, userID)
	h.validationProbe = func(context.Context, credentials.AIProfile, codexapp.AccountRef) error {
		return errors.New("model did not respond")
	}
	if err := h.selectPersonalGrok(context.Background(), userID); err == nil {
		t.Fatal("selectPersonalGrok succeeded after a failed response test")
	}
	if config, found, err := registry.GetUserAIConfig(userID); err != nil || found {
		t.Fatalf("personal config=%#v found=%t err=%v", config, found, err)
	}
}

func TestPersonalGrokReconnectPreservesAndTestsSelectedModel(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	manager, now := newGrokTestManager(t, h, database)
	linkPersonalGrok(t, manager, now, userID)
	if err := registry.SetUserAIConfig(userID, credentials.AIProviderGrokOAuth, "grok-4.3"); err != nil {
		t.Fatal(err)
	}
	h.validationProbe = func(_ context.Context, profile credentials.AIProfile, account codexapp.AccountRef) error {
		if account != codexapp.PersonalAccount(userID) || profile.Config.Model != "grok-4.3" {
			t.Fatalf("account=%#v profile=%#v", account, profile)
		}
		return nil
	}
	if err := h.selectPersonalGrok(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	config, found, err := registry.GetUserAIConfig(userID)
	if err != nil || !found || config.Model != "grok-4.3" {
		t.Fatalf("config=%#v found=%t err=%v", config, found, err)
	}
}

func TestGrokDeviceFlowThroughHandlersSelectsProvider(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	_, now := newGrokTestManager(t, h, database)
	claims := &auth.Claims{UserID: userID, Role: auth.RoleUser}

	beginReq := httptest.NewRequest(http.MethodPost, "/api/ai/grok/device/begin", nil)
	beginReq = beginReq.WithContext(context.WithValue(beginReq.Context(), auth.ClaimsKey, claims))
	beginRec := httptest.NewRecorder()
	h.BeginGrokDeviceLogin(beginRec, beginReq)
	if beginRec.Code != http.StatusOK {
		t.Fatalf("begin status=%d body=%s", beginRec.Code, beginRec.Body.String())
	}
	var begin struct {
		FlowID          string `json:"flow_id"`
		VerificationURI string `json:"verification_uri"`
		UserCode        string `json:"user_code"`
	}
	if err := json.NewDecoder(beginRec.Body).Decode(&begin); err != nil {
		t.Fatal(err)
	}
	if begin.FlowID == "" || begin.UserCode != "GROK-1234" || begin.VerificationURI != "https://accounts.x.ai/oauth2/device" {
		t.Fatalf("begin = %#v", begin)
	}

	*now = now.Add(2 * time.Second)
	checkReq := httptest.NewRequest(http.MethodGet, "/api/ai/grok/device/"+begin.FlowID, nil)
	checkReq = checkReq.WithContext(context.WithValue(checkReq.Context(), auth.ClaimsKey, claims))
	route := chi.NewRouteContext()
	route.URLParams.Add("flowID", begin.FlowID)
	checkReq = checkReq.WithContext(context.WithValue(checkReq.Context(), chi.RouteCtxKey, route))
	checkRec := httptest.NewRecorder()
	h.CheckGrokDeviceLogin(checkRec, checkReq)
	if checkRec.Code != http.StatusOK {
		t.Fatalf("check status=%d body=%s", checkRec.Code, checkRec.Body.String())
	}
	var check struct {
		Status  string         `json:"status"`
		Account map[string]any `json:"account"`
	}
	if err := json.NewDecoder(checkRec.Body).Decode(&check); err != nil {
		t.Fatal(err)
	}
	if check.Status != "connected" || check.Account["email"] != "grok@example.com" {
		t.Fatalf("check = %#v", check)
	}

	config, found, err := registry.GetUserAIConfig(userID)
	if err != nil || !found || config.Provider != credentials.AIProviderGrokOAuth {
		t.Fatalf("config=%#v found=%t err=%v", config, found, err)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/ai/grok/status", nil)
	statusReq = statusReq.WithContext(context.WithValue(statusReq.Context(), auth.ClaimsKey, claims))
	statusRec := httptest.NewRecorder()
	h.GrokStatus(statusRec, statusReq)
	var status map[string]any
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status["connected"] != true || status["selected"] != true || status["effective"] != true ||
		status["account_email"] != "grok@example.com" || status["plan_type"] != "supergrok" {
		t.Fatalf("status = %#v", status)
	}
}

func TestResolveAIPersonalGrokOAuthStates(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	if err := registry.SetUserAIConfig(userID, credentials.AIProviderGrokOAuth, "grok-4.6"); err != nil {
		t.Fatal(err)
	}

	// No manager wired: the provider is selected but unusable.
	resolved := h.resolveAI(context.Background(), userID)
	if resolved.Available || resolved.Source != aiSourcePersonal || resolved.Reason != "grok_unavailable" {
		t.Fatalf("no-manager resolved = %#v", resolved)
	}

	manager, now := newGrokTestManager(t, h, database)
	resolved = h.resolveAI(context.Background(), userID)
	if resolved.Available || resolved.Reason != "personal_grok_disconnected" {
		t.Fatalf("disconnected resolved = %#v", resolved)
	}

	linkPersonalGrok(t, manager, now, userID)
	resolved = h.resolveAI(context.Background(), userID)
	if !resolved.Available || resolved.Source != aiSourcePersonal ||
		resolved.Provider != credentials.AIProviderGrokOAuth || resolved.APIKey != "" {
		t.Fatalf("linked resolved = %#v", resolved)
	}
}

func TestResolveAISharedGrokOAuthStates(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	if _, err := database.Exec(`UPDATE users SET ai_shared_enabled = 1 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAIConfig(credentials.AIProviderGrokOAuth, "grok-4.6"); err != nil {
		t.Fatal(err)
	}
	manager, now := newGrokTestManager(t, h, database)

	resolved := h.resolveAI(context.Background(), userID)
	if resolved.Available || resolved.Source != aiSourceShared || resolved.Reason != "shared_grok_disconnected" {
		t.Fatalf("disconnected shared resolved = %#v", resolved)
	}

	linkSharedGrok(t, manager, now, userID)
	resolved = h.resolveAI(context.Background(), userID)
	if !resolved.Available || resolved.Source != aiSourceShared || resolved.Provider != credentials.AIProviderGrokOAuth {
		t.Fatalf("linked shared resolved = %#v", resolved)
	}
}

func TestAISettingsSurfaceIncludesGrokProviders(t *testing.T) {
	h, registry, database, userID := newResolverTestHandler(t)
	manager, now := newGrokTestManager(t, h, database)
	linkPersonalGrok(t, manager, now, userID)
	if err := registry.SetUserAICredential(userID, credentials.AIProviderGrok, "xai-api-key"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/ai/settings", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: auth.RoleUser}))
	rec := httptest.NewRecorder()
	h.AISettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Providers []struct {
			ID string `json:"id"`
		} `json:"providers"`
		Personal struct {
			Credentials map[string]bool `json:"credentials"`
		} `json:"personal"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, provider := range body.Providers {
		ids[provider.ID] = true
	}
	if !ids[credentials.AIProviderGrok] || !ids[credentials.AIProviderGrokOAuth] {
		t.Fatalf("advertised providers = %#v", ids)
	}
	if !body.Personal.Credentials[credentials.AIProviderGrok] {
		t.Fatalf("grok API key not reported: %#v", body.Personal.Credentials)
	}
	if !body.Personal.Credentials[credentials.AIProviderGrokOAuth] {
		t.Fatalf("grok OAuth link not reported: %#v", body.Personal.Credentials)
	}
}
