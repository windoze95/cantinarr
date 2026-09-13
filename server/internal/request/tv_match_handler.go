package request

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

func (h *Handler) tvMatchAdmin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return 0, false
	}
	if !h.service.userIsAdmin(claims.UserID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": ErrTVMatchAdmin.Error()})
		return 0, false
	}
	return claims.UserID, true
}

func tvMatchID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "tmdb_id"))
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid TMDB ID"})
		return 0, false
	}
	return id, true
}

func tvMatchReply(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeJSON(w, requestErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) ListTVMatches(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.tvMatchAdmin(w, r)
	if !ok {
		return
	}
	out, err := h.service.ListTVMatches(admin)
	tvMatchReply(w, out, err)
}

func (h *Handler) GetTVMatch(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.tvMatchAdmin(w, r)
	if !ok {
		return
	}
	id, ok := tvMatchID(w, r)
	if !ok {
		return
	}
	out, err := h.service.TVMatchDetail(admin, id, r.URL.Query().Get("instance_id"))
	tvMatchReply(w, out, err)
}

func (h *Handler) SaveTVMatch(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.tvMatchAdmin(w, r)
	if !ok {
		return
	}
	id, ok := tvMatchID(w, r)
	if !ok {
		return
	}
	var edit TVMatchEdit
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&edit); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid correction"})
		return
	}
	if r.Method == http.MethodDelete {
		edit.Mode = "default"
	}
	out, err := h.service.SaveTVMatch(admin, id, edit)
	tvMatchReply(w, out, err)
}

func (h *Handler) TVMatchCandidates(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.tvMatchAdmin(w, r)
	if !ok {
		return
	}
	out, err := h.service.TVMatchCandidates(admin, r.URL.Query().Get("instance_id"), r.URL.Query().Get("q"))
	tvMatchReply(w, out, err)
}

func (h *Handler) TVRepairPreviews(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.tvMatchAdmin(w, r)
	if !ok {
		return
	}
	id, ok := tvMatchID(w, r)
	if !ok {
		return
	}
	out, err := h.service.TVRepairPreviews(admin, id)
	tvMatchReply(w, out, err)
}

func (h *Handler) RepairTVMatch(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.tvMatchAdmin(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request ID"})
		return
	}
	var edit struct {
		Revision string `json:"revision"`
	}
	if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&edit); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "revision required"})
		return
	}
	out, err := h.service.RepairTVMatch(admin, id, edit.Revision)
	tvMatchReply(w, out, err)
}
