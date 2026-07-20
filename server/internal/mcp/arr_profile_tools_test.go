package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

const profileToolFormats = `[
	{"id":3,"name":"Not English","specifications":[{"name":"Not English","implementation":"LanguageSpecification","negate":true,"required":false,"fields":[{"name":"value","value":1}]}]},
	{"id":4,"name":"x265","specifications":[{"name":"x265","implementation":"ReleaseTitleSpecification","negate":false,"required":false,"fields":[]}]}
]`

type profileToolFakeArr struct {
	mu         sync.Mutex
	profile    json.RawMessage
	formats    json.RawMessage
	languages  json.RawMessage
	putBodies  []json.RawMessage
	rejectNext bool
}

func newProfileToolFakeArr() *profileToolFakeArr {
	return &profileToolFakeArr{
		profile:   json.RawMessage(settingsProfileHD),
		formats:   json.RawMessage(profileToolFormats),
		languages: json.RawMessage(`[{"id":-1,"name":"Any"},{"id":1,"name":"English"}]`),
	}
}

func (fake *profileToolFakeArr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/qualityprofile":
		_, _ = w.Write(append(append([]byte{'['}, fake.profile...), ']'))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/customformat":
		_, _ = w.Write(fake.formats)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/language":
		_, _ = w.Write(fake.languages)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v3/qualityprofile/1":
		body, _ := io.ReadAll(r.Body)
		fake.putBodies = append(fake.putBodies, append(json.RawMessage(nil), body...))
		if fake.rejectNext {
			fake.rejectNext = false
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`[{"propertyName":"cutoff","errorMessage":"must be an allowed quality"}]`))
			return
		}
		fake.profile = append(json.RawMessage(nil), body...)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(body)
	default:
		http.NotFound(w, r)
	}
}

func (fake *profileToolFakeArr) putCount() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return len(fake.putBodies)
}

func (fake *profileToolFakeArr) setProfile(raw string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.profile = json.RawMessage(raw)
}

func (fake *profileToolFakeArr) setFormats(raw string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.formats = json.RawMessage(raw)
}

func (fake *profileToolFakeArr) setLanguages(raw string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.languages = json.RawMessage(raw)
}

func (fake *profileToolFakeArr) rejectPut() {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.rejectNext = true
}

func newProfileToolIntegrationServer(t *testing.T, fake *profileToolFakeArr) (*ToolServer, *httptest.Server) {
	t.Helper()
	server, upstream, _, _ := newProfileToolIntegrationServerWithStoreForService(t, fake, "radarr")
	return server, upstream
}

func newProfileToolIntegrationServerWithStore(t *testing.T, fake *profileToolFakeArr) (*ToolServer, *httptest.Server, *instance.Store, *instance.Instance) {
	return newProfileToolIntegrationServerWithStoreForService(t, fake, "radarr")
}

func newProfileToolIntegrationServerWithStoreForService(t *testing.T, fake *profileToolFakeArr, service string) (*ToolServer, *httptest.Server, *instance.Store, *instance.Instance) {
	t.Helper()
	upstream := httptest.NewServer(fake)
	t.Cleanup(upstream.Close)
	inst := &instance.Instance{ServiceType: service, Name: "Main", URL: upstream.URL, APIKey: "profile-secret-key", IsDefault: true}
	server, store := newSettingsToolServerWithStore(t, []*instance.Instance{inst})
	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) { return auth.RoleAdmin, nil })
	return server, upstream, store, inst
}

func profileToolCallContext(turnID, trustedText string) CallContext {
	return CallContext{
		UserID:            77,
		Role:              auth.RoleAdmin,
		DeviceID:          "device-77",
		Reauthorize:       true,
		Origin:            OriginInteractiveChat,
		TrustedUserText:   trustedText,
		InteractiveTurnID: turnID,
	}
}

func previewX265ProfileChange(t *testing.T, server *ToolServer, turnID string) string {
	t.Helper()
	return previewX265ProfileChangeForService(t, server, "radarr", turnID)
}

func previewX265ProfileChangeForService(t *testing.T, server *ToolServer, service, turnID string) string {
	t.Helper()
	result, err := server.ExecuteTool(context.Background(), "preview_profile_change", json.RawMessage(`{
		"service":"`+service+`","profile_id":1,
		"changes":{"custom_format_scores":[{"format_name":"x265","score":25}]}
	}`), profileToolCallContext(turnID, "Set x265 to 25"))
	if err != nil {
		t.Fatalf("preview_profile_change: %v", err)
	}
	marker := "Change reference: "
	start := strings.Index(result.Text, marker)
	if start < 0 {
		t.Fatalf("preview omitted reference:\n%s", result.Text)
	}
	reference := strings.SplitN(result.Text[start+len(marker):], "\n", 2)[0]
	if !isProfileChangeReference(reference) || strings.Contains(result.Text, "APPLY ") {
		t.Fatalf("invalid preview reference/instruction:\n%s", result.Text)
	}
	if !strings.Contains(result.Text, `custom format "x265" [4]: +0 -> +25`) ||
		!strings.Contains(result.Text, "call apply_profile_change now") ||
		!strings.Contains(result.Text, "did not write anything") || !strings.Contains(result.Text, "Do not ask the admin to copy") {
		t.Fatalf("preview omitted complete diff/safety language:\n%s", result.Text)
	}
	return reference
}

func previewLanguageScoreProfileChange(t *testing.T, server *ToolServer, service, turnID string) string {
	t.Helper()
	result, err := server.ExecuteTool(context.Background(), "preview_profile_change", json.RawMessage(`{
		"service":"`+service+`","profile_id":1,
		"changes":{"custom_format_scores":[{"format_name":"Not English","score":-9000}]}
	}`), profileToolCallContext(turnID, "Set Not English to -9000"))
	if err != nil {
		t.Fatalf("preview language score: %v", err)
	}
	marker := "Change reference: "
	start := strings.Index(result.Text, marker)
	if start < 0 {
		t.Fatalf("preview omitted reference:\n%s", result.Text)
	}
	reference := strings.SplitN(result.Text[start+len(marker):], "\n", 2)[0]
	if !isProfileChangeReference(reference) || !strings.Contains(result.Text, `custom format "Not English" [3]: -10000 -> -9000`) {
		t.Fatalf("invalid language-score preview:\n%s", result.Text)
	}
	return reference
}

func TestProfileChangeAppliesInSameExplicitRequestTurnAndIsOneShot(t *testing.T) {
	fake := newProfileToolFakeArr()
	server, _ := newProfileToolIntegrationServer(t, fake)
	reference := previewX265ProfileChange(t, server, "turn-preview")
	if fake.putCount() != 0 {
		t.Fatal("preview wrote to the arr")
	}

	for name, callCtx := range map[string]CallContext{
		"later turn":         profileToolCallContext("turn-later", "Please apply it"),
		"empty user message": profileToolCallContext("turn-preview", ""),
		"other device": {
			UserID: 77, Role: auth.RoleAdmin, DeviceID: "device-other", Reauthorize: true,
			Origin: OriginInteractiveChat, TrustedUserText: "Set x265 to 25", InteractiveTurnID: "turn-preview",
		},
		"external origin": {
			UserID: 77, Role: auth.RoleAdmin, DeviceID: "device-77", Reauthorize: true,
			Origin: OriginExternalMCP, TrustedUserText: "Set x265 to 25", InteractiveTurnID: "turn-preview",
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), callCtx)
			if err != nil {
				t.Fatalf("refusal: %v", err)
			}
			if strings.Contains(result.Text, "Applied the requested change") || fake.putCount() != 0 {
				t.Fatalf("invalid same-turn claim wrote: %q", result.Text)
			}
		})
	}

	applyCtx := profileToolCallContext("turn-preview", "Set x265 to 25")
	result, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), applyCtx)
	if err != nil {
		t.Fatalf("apply_profile_change: %v", err)
	}
	if !strings.Contains(result.Text, "Applied the requested change") || !strings.Contains(result.Text, "recorded change #") || !strings.Contains(result.Text, "future release selection") || fake.putCount() != 1 || result.StructuredData == nil {
		t.Fatalf("apply result=%q puts=%d", result.Text, fake.putCount())
	}
	fake.mu.Lock()
	stored := string(fake.profile)
	fake.mu.Unlock()
	if !strings.Contains(stored, `"score":25`) || !strings.Contains(stored, `"futureField":"round-trip-me"`) {
		t.Fatalf("stored profile lost requested or unknown fields: %s", stored)
	}

	replay, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-replay", "Set x265 to 25"))
	if err != nil || !strings.Contains(replay.Text, "No valid same-turn profile change") || fake.putCount() != 1 {
		t.Fatalf("replay result=%v err=%v puts=%d", replay, err, fake.putCount())
	}
}

func TestProfileChangeRefusesStaleProfileAndConsumesReference(t *testing.T) {
	fake := newProfileToolFakeArr()
	server, _ := newProfileToolIntegrationServer(t, fake)
	reference := previewX265ProfileChange(t, server, "turn-preview")
	fake.setProfile(strings.Replace(settingsProfileHD, `"cutoffFormatScore":10000`, `"cutoffFormatScore":9999`, 1))

	result, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-preview", "Set x265 to 25"))
	if err != nil {
		t.Fatalf("stale apply: %v", err)
	}
	if !strings.Contains(result.Text, "changed since preview") || fake.putCount() != 0 {
		t.Fatalf("stale result=%q puts=%d", result.Text, fake.putCount())
	}
	replay, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-replay", "Set x265 to 25"))
	if err != nil || !strings.Contains(replay.Text, "No valid same-turn profile change") || fake.putCount() != 0 {
		t.Fatalf("stale token replay result=%v err=%v puts=%d", replay, err, fake.putCount())
	}
}

func TestProfileChangeRefusesStaleCustomFormatsAndLanguageCatalogBeforeWrite(t *testing.T) {
	t.Run("custom formats", func(t *testing.T) {
		fake := newProfileToolFakeArr()
		server, _ := newProfileToolIntegrationServer(t, fake)
		reference := previewX265ProfileChange(t, server, "turn-preview")
		fake.setFormats(strings.Replace(profileToolFormats, `"fields":[]`, `"fields":[{"name":"value","value":"x265"}]`, 1))

		result, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-preview", "Set x265 to 25"))
		if err != nil || !strings.Contains(result.Text, "changed since preview") || fake.putCount() != 0 {
			t.Fatalf("stale custom-format result=%v err=%v puts=%d", result, err, fake.putCount())
		}
	})

	t.Run("language catalog", func(t *testing.T) {
		fake := newProfileToolFakeArr()
		server, _ := newProfileToolIntegrationServer(t, fake)
		preview, err := server.ExecuteTool(context.Background(), "preview_profile_change", json.RawMessage(`{
			"service":"radarr","profile_id":1,
			"changes":{"language_name":"English","custom_format_scores":[{"format_name":"Not English","score":0}]}
		}`), profileToolCallContext("turn-preview", "Use English without a language score"))
		if err != nil {
			t.Fatalf("preview language change: %v", err)
		}
		marker := "Change reference: "
		start := strings.Index(preview.Text, marker)
		if start < 0 {
			t.Fatalf("preview omitted reference:\n%s", preview.Text)
		}
		reference := strings.SplitN(preview.Text[start+len(marker):], "\n", 2)[0]
		fake.setLanguages(`[{"id":-1,"name":"Any"},{"id":1,"name":"English (US)"}]`)

		result, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-preview", "Use English without a language score"))
		if err != nil || !strings.Contains(result.Text, "changed since preview") || fake.putCount() != 0 {
			t.Fatalf("stale language result=%v err=%v puts=%d", result, err, fake.putCount())
		}
	})
}

func TestLanguageSpecificationScoreBindsLiveCatalogForRadarrAndSonarr(t *testing.T) {
	for _, service := range []string{"radarr", "sonarr"} {
		t.Run(service, func(t *testing.T) {
			fake := newProfileToolFakeArr()
			server, _, _, _ := newProfileToolIntegrationServerWithStoreForService(t, fake, service)
			reference := previewLanguageScoreProfileChange(t, server, service, "turn-preview")

			server.profileChanges.mu.Lock()
			proposal, ok := server.profileChanges.proposals[reference]
			server.profileChanges.mu.Unlock()
			if !ok || !proposal.HasLanguageHash || proposal.LanguageHash == ([32]byte{}) {
				t.Fatalf("language-score proposal did not bind the live catalog: %#v", proposal)
			}

			fake.setLanguages(`[{"id":-1,"name":"Any"},{"id":1,"name":"English (changed)"}]`)
			result, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-preview", "Set Not English to -9000"))
			if err != nil {
				t.Fatalf("apply after language catalog change: %v", err)
			}
			if !strings.Contains(result.Text, "changed since preview") || fake.putCount() != 0 {
				t.Fatalf("language-catalog stale result=%q puts=%d", result.Text, fake.putCount())
			}
		})
	}
}

func TestProfileChangeRefusesRevocationDisableAndInstanceRepointBeforeWrite(t *testing.T) {
	t.Run("authorization revoked after claim", func(t *testing.T) {
		fake := newProfileToolFakeArr()
		server, _ := newProfileToolIntegrationServer(t, fake)
		reference := previewX265ProfileChange(t, server, "turn-preview")
		authorizations := 0
		server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) {
			authorizations++
			if authorizations > 1 {
				return auth.RoleUser, nil
			}
			return auth.RoleAdmin, nil
		})

		_, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-preview", "Set x265 to 25"))
		if !errors.Is(err, ErrToolAuthorization) || fake.putCount() != 0 {
			t.Fatalf("revoked apply err=%v puts=%d", err, fake.putCount())
		}
		server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) { return auth.RoleAdmin, nil })
		replay, replayErr := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-replay", "Set x265 to 25"))
		if replayErr != nil || !strings.Contains(replay.Text, "No valid same-turn profile change") || fake.putCount() != 0 {
			t.Fatalf("revoked replay=%v err=%v puts=%d", replay, replayErr, fake.putCount())
		}
	})

	t.Run("tool disabled after claim", func(t *testing.T) {
		fake := newProfileToolFakeArr()
		server, _ := newProfileToolIntegrationServer(t, fake)
		reference := previewX265ProfileChange(t, server, "turn-preview")
		authorizations := 0
		server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) {
			authorizations++
			if authorizations == 2 {
				if err := server.SetToolEnabled("apply_profile_change", false); err != nil {
					t.Fatalf("disable apply tool: %v", err)
				}
			}
			return auth.RoleAdmin, nil
		})

		result, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-preview", "Set x265 to 25"))
		if err != nil || !strings.Contains(result.Text, "disabled after") || fake.putCount() != 0 {
			t.Fatalf("disabled apply result=%v err=%v puts=%d", result, err, fake.putCount())
		}
	})

	t.Run("instance credentials changed", func(t *testing.T) {
		fake := newProfileToolFakeArr()
		server, _, store, inst := newProfileToolIntegrationServerWithStore(t, fake)
		reference := previewX265ProfileChange(t, server, "turn-preview")
		inst.APIKey = "rotated-profile-secret-key"
		if err := store.Update(inst); err != nil {
			t.Fatalf("rotate instance credential: %v", err)
		}

		result, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-preview", "Set x265 to 25"))
		if err != nil || !strings.Contains(result.Text, "instance changed since preview") || fake.putCount() != 0 {
			t.Fatalf("repointed apply result=%v err=%v puts=%d", result, err, fake.putCount())
		}
	})
}

func TestProfileChangeSurfacesBoundedValidationAndConsumesReference(t *testing.T) {
	fake := newProfileToolFakeArr()
	server, upstream := newProfileToolIntegrationServer(t, fake)
	reference := previewX265ProfileChange(t, server, "turn-preview")
	fake.rejectPut()

	_, err := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-preview", "Set x265 to 25"))
	if err == nil || !strings.Contains(err.Error(), "cutoff: must be an allowed quality") {
		t.Fatalf("validation error = %v", err)
	}
	for _, leaked := range []string{"profile-secret-key", upstream.URL} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("validation error leaked %q: %v", leaked, err)
		}
	}
	if fake.putCount() != 1 {
		t.Fatalf("PUT count = %d, want one rejected write", fake.putCount())
	}
	replay, replayErr := server.ExecuteTool(context.Background(), "apply_profile_change", json.RawMessage(`{"change_reference":"`+reference+`"}`), profileToolCallContext("turn-replay", "Set x265 to 25"))
	if replayErr != nil || !strings.Contains(replay.Text, "No valid same-turn profile change") || fake.putCount() != 1 {
		t.Fatalf("validation token replay result=%v err=%v puts=%d", replay, replayErr, fake.putCount())
	}
}

func TestProfileChangeToolsAreStrictAdminOnlyAndInAppOnly(t *testing.T) {
	server := NewToolServer(nil, nil, nil, nil)
	for _, name := range []string{"preview_profile_change", "apply_profile_change"} {
		definition := findToolDefinition(name)
		if definition == nil || definition.Permission != auth.PermissionInstancesManage || !definition.InAppChatOnly {
			t.Fatalf("%s definition = %#v", name, definition)
		}
	}

	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) { return auth.RoleUser, nil })
	result, err := server.ExecuteTool(context.Background(), "preview_profile_change", json.RawMessage(`{"service":"radarr","profile_id":1,"changes":{"upgrade_allowed":false}}`), profileToolCallContext("turn", "change it"))
	if err != nil || result.Text != "This action is not permitted for your role." {
		t.Fatalf("user role result=%v err=%v", result, err)
	}

	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) { return auth.RoleAdmin, nil })
	external := profileToolCallContext("turn", "change it")
	external.Origin = OriginExternalMCP
	result, err = server.ExecuteTool(context.Background(), "preview_profile_change", json.RawMessage(`{"service":"radarr","profile_id":1,"changes":{"upgrade_allowed":false}}`), external)
	if err != nil || !strings.Contains(result.Text, "only in Cantinarr") {
		t.Fatalf("external preview result=%v err=%v", result, err)
	}
}
