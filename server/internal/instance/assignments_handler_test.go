package instance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

func TestAssignmentEndpointSelectionAndAuthorization(t *testing.T) {
	s := newTestStore(t)
	alice, bob, admin := createUser(t, s, "alice"), createUser(t, s, "bob"), createUser(t, s, "admin")
	if _, err := s.db.Exec("UPDATE users SET role='admin' WHERE id=?", admin); err != nil {
		t.Fatal(err)
	}
	id := mkDefaultInstance(t, s, "lidarr", "Music")
	h := NewHandler(s, nil)
	changed := 0
	h.SetConfigChangedObserver(func() { changed++ })
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.RequirePermission(auth.PermissionInstancesManage))
		r.Get("/{instanceID}/assignments", h.GetAssignments)
		r.Patch("/{instanceID}/assignments", h.ChangeAssignments)
	})
	call := func(role, method, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/"+id+"/assignments", strings.NewReader(body))
		if role != "" {
			req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: admin, Role: role}))
		}
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		return out
	}
	for _, role := range []string{"", auth.RoleUser} {
		for _, method := range []string{"GET", "PATCH"} {
			out := call(role, method, `{"action":"add","user_ids":[`+jsonInt(alice)+`]}`)
			if out.Code != http.StatusUnauthorized && out.Code != http.StatusForbidden {
				t.Fatalf("unauthorized %s %s: %d", role, method, out.Code)
			}
		}
	}
	for _, body := range []string{
		`{"action":"replace","user_ids":[` + jsonInt(alice) + `]}`,
		`{"action":"add","user_ids":[]}`,
		`{"action":"add","user_ids":[` + jsonInt(alice) + `,99999]}`,
		`{"action":"add","user_ids":[` + jsonInt(alice) + `,` + jsonInt(admin) + `]}`,
	} {
		if out := call(auth.RoleAdmin, "PATCH", body); out.Code != 400 {
			t.Fatalf("invalid selection %d %s", out.Code, out.Body)
		}
		if ok, _ := s.UserHasInstanceAccess(alice, id); ok {
			t.Fatal("partial mutation from invalid selection")
		}
	}
	add := `{"action":"add","user_ids":[` + jsonInt(alice) + `,` + jsonInt(bob) + `,` + jsonInt(alice) + `]}`
	for i := 0; i < 2; i++ {
		if out := call(auth.RoleAdmin, "PATCH", add); out.Code != 200 {
			t.Fatalf("add %d %s", out.Code, out.Body)
		}
	}
	if err := s.SetUserDefault(alice, "lidarr", id); err != nil {
		t.Fatal(err)
	}
	remove := `{"action":"remove","user_ids":[` + jsonInt(alice) + `]}`
	out := call(auth.RoleAdmin, "PATCH", remove)
	if out.Code != 200 {
		t.Fatalf("remove %d %s", out.Code, out.Body)
	}
	var rows []Assignment
	if err := json.Unmarshal(out.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	byID := map[int64]Assignment{}
	for _, row := range rows {
		byID[row.UserID] = row
	}
	if byID[alice].Assigned || byID[alice].PreferredInstanceID != "" || byID[alice].EffectiveDefaultID != "" {
		t.Fatal("removal left access or default", byID[alice])
	}
	if !byID[bob].Assigned || byID[bob].EffectiveDefaultID != id {
		t.Fatal("removal affected unselected user", byID[bob])
	}
	if byID[admin].EffectiveDefaultID != id {
		t.Fatal("administrator routing lost")
	}
	if changed != 3 {
		t.Fatalf("config changes %d", changed)
	}
}
