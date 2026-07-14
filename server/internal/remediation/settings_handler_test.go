package remediation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/ai"
)

func putRemediationSettings(t *testing.T, handler *Handler, settings Settings) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/admin/remediation-settings", bytes.NewReader(body))
	handler.UpdateSettings(recorder, request)
	return recorder
}

func TestUpdateSettingsValidatesAndBindsChangedModelOverride(t *testing.T) {
	service, _, _ := setupTestService(t)
	handler := NewHandler(service)
	calls := 0
	handler.SetSharedModelOverrideValidator(func(_ context.Context, model string) (string, error) {
		calls++
		if model != "remediation-model" {
			t.Fatalf("validated model=%q", model)
		}
		return "openai", nil
	})

	settings := Defaults()
	settings.ModelOverride = " remediation-model "
	settings.ModelOverrideProvider = "client-supplied-provider"
	response := putRemediationSettings(t, handler, settings)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	stored := service.Settings()
	if calls != 1 || stored.ModelOverride != "remediation-model" || stored.ModelOverrideProvider != "openai" {
		t.Fatalf("calls=%d stored=%+v", calls, stored)
	}

	// An unchanged override does not spend another provider turn.
	response = putRemediationSettings(t, handler, stored)
	if response.Code != http.StatusOK || calls != 1 {
		t.Fatalf("unchanged status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}

func TestUpdateSettingsRejectsInvalidModelOverrideWithoutSaving(t *testing.T) {
	service, _, _ := setupTestService(t)
	handler := NewHandler(service)
	handler.SetSharedModelOverrideValidator(func(context.Context, string) (string, error) {
		return "openai", ai.ErrAIValidation
	})

	settings := Defaults()
	settings.Enabled = true
	settings.ModelOverride = "unavailable-model"
	response := putRemediationSettings(t, handler, settings)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	stored := service.Settings()
	if stored.Enabled || stored.ModelOverride != "" || stored.ModelOverrideProvider != "" {
		t.Fatalf("failed validation changed stored settings: %+v", stored)
	}
}
