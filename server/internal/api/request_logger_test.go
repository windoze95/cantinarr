package api

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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
		w.WriteHeader(http.StatusBadGateway)
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
	if !strings.Contains(got, "POST /api/webhooks/arr/{instanceID} 502") {
		t.Fatalf("safe request log lost useful request metadata: %q", got)
	}
}

func TestSafeRequestLoggerOnlyLogsFailures(t *testing.T) {
	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	for _, status := range []int{200, 201, 204, 302, 304, 400, 401, 403, 404, 429, 500, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			logs.Reset()
			handler := safeRequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/config", nil))
			if logged := logs.Len() > 0; logged != (status >= 400) {
				t.Fatalf("status %d logged=%v: %s", status, logged, &logs)
			}
		})
	}
}

func TestSafeRequestLoggerDistinguishesCanceledRequests(t *testing.T) {
	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	for _, lifecycle := range []string{"active", "canceled", "deadline_exceeded"} {
		t.Run(lifecycle, func(t *testing.T) {
			logs.Reset()
			ctx := context.Background()
			if lifecycle == "canceled" {
				var cancel context.CancelCauseFunc
				ctx, cancel = context.WithCancelCause(ctx)
				cancel(errors.New("private cancellation details"))
			} else if lifecycle == "deadline_exceeded" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			handler := safeRequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/?token=secret", nil).WithContext(ctx))
			got := logs.String()
			if !strings.Contains(got, "502") || strings.Contains(got, "secret") || strings.Contains(got, "private") {
				t.Fatalf("lost status or leaked request details: %q", got)
			}
			if lifecycle == "active" {
				if strings.Contains(got, "request_context=") {
					t.Fatalf("active request mislabeled: %q", got)
				}
			} else if !strings.Contains(got, "request_context="+lifecycle) {
				t.Fatalf("missing lifecycle evidence: %q", got)
			}
		})
	}
}
