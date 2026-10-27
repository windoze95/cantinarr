package mediaaccess

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

func TestAccountChangesInvalidateAvailability(t *testing.T) {
	e := newEnv(t)
	uid := e.user("household")
	id := e.jellyfin("TV", instance.MediaServerConfig{})
	remote := e.provider.addUser("recipient", false, false)
	h := NewHandler(e.svc, e.svc.logger)
	changes := 0
	h.SetConfigChangedObserver(func() { changes++ })
	router := chi.NewRouter()
	path := "/api/admin/users/" + itoa(uid) + "/media-servers/" + id + "/account"
	router.Put("/api/admin/users/{userID}/media-servers/{instanceID}/account", h.LinkAccount)
	router.Delete("/api/admin/users/{userID}/media-servers/{instanceID}/account", h.UnlinkAccount)
	if rec := serve(router, "PUT", path, uid, `{"remote_user_id":"`+remote+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	if changes != 1 {
		t.Fatalf("link invalidations = %d", changes)
	}
	if rec := serve(router, "DELETE", path, uid, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d %s", rec.Code, rec.Body.String())
	}
	if changes != 2 {
		t.Fatalf("unlink invalidations = %d", changes)
	}
	serve(router, "DELETE", path, uid, "")
	if changes != 2 {
		t.Fatalf("failed unlink invalidated state: %d", changes)
	}
}
