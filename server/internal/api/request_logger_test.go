package api

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestSafeRequestLoggerNeverLogsQueryOrHeaders(t *testing.T) {
	var logs bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})

	router := chi.NewRouter()
	router.Use(safeRequestLogger)
	router.Post("/api/webhooks/arr/{instanceID}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/arr/dynamic-uuid-sentinel?token=query-secret", nil)
	req.Header.Set("Authorization", "Bearer header-secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	got := logs.String()
	if strings.Contains(got, "query-secret") || strings.Contains(got, "header-secret") || strings.Contains(got, "token=") {
		t.Fatalf("safe request log leaked credentials: %q", got)
	}
	if strings.Contains(got, "dynamic-uuid-sentinel") {
		t.Fatalf("safe request log exposed a dynamic path value: %q", got)
	}
	if !strings.Contains(got, "POST /api/webhooks/arr/{instanceID} 204") {
		t.Fatalf("safe request log lost useful request metadata: %q", got)
	}
}
