package request

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

func TestCreateRejectsOversizedBody(t *testing.T) {
	h := NewHandler(nil)
	body := `{"media_type":"movie","tmdb_id":1,"padding":"` + strings.Repeat("x", 64<<10) + `"}`
	r := httptest.NewRequest(http.MethodPost, "/api/requests", strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: 1, Role: auth.RoleUser}))
	w := httptest.NewRecorder()
	h.Create(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", w.Code, w.Body.String())
	}
}
