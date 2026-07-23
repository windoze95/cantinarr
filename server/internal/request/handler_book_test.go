package request

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
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
