package downloads

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

func (h *Handler) ActivitySettings(changed func()) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		claims := auth.GetClaims(r.Context())
		if claims == nil || !auth.HasPermission(claims.Role, auth.PermissionAdmin) {
			writeError(w, 403, "admin access required")
			return
		}
		if h.activity == nil {
			writeError(w, 503, "download settings unavailable")
			return
		}
		s := h.activity.settings
		if r.Method == http.MethodPut {
			var body struct {
				UserScope string `json:"user_scope"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || (body.UserScope != "all" && body.UserScope != "mine") {
				writeError(w, 400, "user_scope must be all or mine")
				return
			}
			if h.activity.authorize != nil {
				if err := h.activity.authorize(r.Context(), claims.UserID, claims.DeviceID, auth.PermissionAdmin); err != nil {
					writeError(w, 403, "admin access changed")
					return
				}
			}
			if _, err := s.SetDownloadsUserScope(body.UserScope); err != nil {
				writeError(w, 503, "download settings unavailable")
				return
			}
			if changed != nil {
				changed()
			}
		}
		settings, err := s.Read()
		if err != nil {
			writeError(w, 503, "download settings unavailable")
			return
		}
		writeJSON(w, map[string]string{"user_scope": settings.DownloadsUserScope})
	}
}

// InvalidateActivity is wired to the existing queue/config invalidations.
func (h *Handler) InvalidateActivity() {
	if h.activity != nil {
		h.activity.Invalidate()
	}
}
