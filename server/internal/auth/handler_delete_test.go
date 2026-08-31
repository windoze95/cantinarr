package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The delete hook prepares before the delete and commits only after it
// succeeded: a refused delete (self, last admin) must never fire the commit,
// or a still-existing user would lose their media-server access.
func TestHandleDeleteUser_RunsCommittedHookOnlyOnSuccess(t *testing.T) {
	svc := setupTestService(t)
	handler := NewHandler(svc)
	var prepared, committed []int64
	handler.SetUserDeleteHook(func(userID int64) func() {
		prepared = append(prepared, userID)
		return func() { committed = append(committed, userID) }
	})

	connect, err := svc.CreateConnectToken(1, "guest", "http://example.com")
	if err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	connectURL, _ := url.Parse(connect.Link)
	guest, err := svc.RedeemConnectToken(connectURL.Query().Get("token"), "Guest Phone", "guest-hw")
	if err != nil {
		t.Fatalf("redeem connect token: %v", err)
	}

	router := chi.NewRouter()
	router.Delete("/api/admin/users/{userID}", handler.HandleDeleteUser)
	del := func(actor int64, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("DELETE", "/api/admin/users/"+target, nil)
		req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{UserID: actor, Username: "admin", Role: RoleAdmin}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// Self-delete is refused: prepared, never committed.
	if rec := del(1, "1"); rec.Code != http.StatusBadRequest {
		t.Fatalf("self delete = %d, want 400", rec.Code)
	}
	if len(prepared) != 1 || len(committed) != 0 {
		t.Fatalf("after refused delete: prepared=%v committed=%v", prepared, committed)
	}

	// A real delete commits.
	target := guest.User.ID
	if rec := del(1, itoa(target)); rec.Code != http.StatusOK {
		t.Fatalf("delete guest = %d %s", rec.Code, rec.Body.String())
	}
	if len(prepared) != 2 || prepared[1] != target || len(committed) != 1 || committed[0] != target {
		t.Fatalf("after delete: prepared=%v committed=%v", prepared, committed)
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
