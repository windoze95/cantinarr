package request

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

func TestCreateBookRejectsBlankTrimmedFields(t *testing.T) {
	handler := NewHandler(nil)
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "foreign id",
			body: `{"media_type":"book","foreign_id":"  \t ","title":"Flock","book_format":"audiobook"}`,
			want: "foreign_id required for book requests",
		},
		{
			name: "title",
			body: `{"media_type":"book","foreign_id":"flock","title":"  \n ","book_format":"audiobook"}`,
			want: "title required for book requests",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/requests", strings.NewReader(tt.body))
			req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: 1, Role: auth.RoleUser}))
			resp := httptest.NewRecorder()
			handler.Create(resp, req)
			if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), tt.want) {
				t.Fatalf("response = %d %s, want 400 containing %q", resp.Code, resp.Body.String(), tt.want)
			}
		})
	}
}

func TestGetBookStatusRejectsWhitespaceForeignID(t *testing.T) {
	handler := NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/requests/book-status?foreign_id=%20%20%09", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: 1, Role: auth.RoleUser}))
	resp := httptest.NewRecorder()
	handler.GetBookStatus(resp, req)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "foreign_id required") {
		t.Fatalf("response = %d %s, want requester-safe missing foreign_id error", resp.Code, resp.Body.String())
	}
}

func TestBookRequestErrorResponseUsesStableRequesterSafeCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		format     string
		wantStatus int
		wantCode   string
		wantError  string
	}{
		{
			name:       "audiobook edition missing",
			err:        ErrBookEditionUnavailable,
			format:     BookFormatAudiobook,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "book_edition_unavailable",
			wantError:  "No audiobook edition is available for this title.",
		},
		{
			name:       "catalog settling",
			err:        ErrBookCatalogPending,
			format:     BookFormatEbook,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "book_catalog_pending",
			wantError:  "The book library is still preparing this title. Try again in a moment.",
		},
		{
			name:       "interrupted outcome is still reconciling",
			err:        fmt.Errorf("lost response: %w", ErrBookOutcomePending),
			format:     BookFormatAudiobook,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "book_outcome_pending",
			wantError:  "The book library is still confirming this request. Cantinarr will keep checking it.",
		},
		{
			name:       "configuration needs admin",
			err:        fmt.Errorf("profiles: %w", ErrBookConfigurationInvalid),
			format:     BookFormatEbook,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "book_configuration_invalid",
			wantError:  "An admin needs to check this book library’s profiles and folders.",
		},
		{
			name:       "upstream credentials need admin",
			err:        fmt.Errorf("read profiles: %w", &chaptarr.HTTPStatusError{Method: http.MethodGet, Path: "/api/v1/qualityprofile", StatusCode: http.StatusUnauthorized}),
			format:     BookFormatAudiobook,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "book_connection_invalid",
			wantError:  "An admin needs to check this book library’s connection.",
		},
		{
			name:       "book match not verified",
			err:        ErrBookMatchNotFound,
			format:     BookFormatEbook,
			wantStatus: http.StatusConflict,
			wantCode:   "book_match_not_found",
			wantError:  "Cantinarr couldn’t verify this book match. Try again.",
		},
		{
			name:       "multi-book result",
			err:        ErrBookMultiWorkUnsupported,
			format:     BookFormatEbook,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "book_multi_work_unsupported",
			wantError:  "This result contains multiple books. Choose an individual title instead.",
		},
		{
			name:       "edition mutation rejected",
			err:        ErrBookMutationRejected,
			format:     BookFormatAudiobook,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "book_request_rejected",
			wantError:  "The book library rejected this title or edition. Refresh the catalog and try again, or ask an admin to check the book library.",
		},
		{
			name:       "search rejected",
			err:        ErrBookSearchRejected,
			format:     BookFormatAudiobook,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "book_search_rejected",
			wantError:  "The book was prepared, but the book library rejected its download search. Ask an admin to check the book library.",
		},
		{
			name:       "search was not acknowledged",
			err:        ErrBookSearchUnconfirmed,
			format:     BookFormatEbook,
			wantStatus: http.StatusBadGateway,
			wantCode:   "book_search_unconfirmed",
			wantError:  "The book was prepared, but its download search could not be confirmed. Try again or ask an admin to check the book library.",
		},
		{
			name:       "unknown upstream detail is hidden",
			err:        errors.New("upstream http://chaptarr:8787 returned secret detail"),
			format:     BookFormatEbook,
			wantStatus: http.StatusInternalServerError,
			wantCode:   "book_request_failed",
			wantError:  "This book could not be requested. Try again.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := bookRequestErrorResponse(tt.err, tt.format)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", status, tt.wantStatus)
			}
			if body["code"] != tt.wantCode {
				t.Fatalf("code = %q, want %q", body["code"], tt.wantCode)
			}
			if body["error"] != tt.wantError {
				t.Fatalf("error = %q, want %q", body["error"], tt.wantError)
			}
		})
	}
}

func TestBookReadErrorResponseHidesUpstreamDetails(t *testing.T) {
	upstream := errors.New("GET http://chaptarr:8787/api/v1/book leaked internal detail")
	for _, tt := range []struct {
		operation string
		wantCode  string
		wantError string
	}{
		{
			operation: "status",
			wantCode:  "book_status_unavailable",
			wantError: "The book status could not be checked. Try again.",
		},
		{
			operation: "library",
			wantCode:  "book_library_unavailable",
			wantError: "The book library could not be loaded. Try again.",
		},
	} {
		status, body := bookReadErrorResponse(upstream, tt.operation)
		if status != http.StatusBadGateway || body["code"] != tt.wantCode || body["error"] != tt.wantError {
			t.Fatalf("%s response = %d %#v", tt.operation, status, body)
		}
		if strings.Contains(body["error"], "chaptarr:8787") || strings.Contains(body["error"], "/api/v1/") {
			t.Fatalf("%s response leaked upstream detail: %#v", tt.operation, body)
		}
	}
}
