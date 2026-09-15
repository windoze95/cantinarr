package request

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/requestquota"
)

func writeQuotaError(w http.ResponseWriter, err error) bool {
	var exceeded *requestquota.Exceeded
	if !errors.As(err, &exceeded) {
		return false
	}
	writeJSON(w, http.StatusTooManyRequests, exceeded)
	return true
}

func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var req CreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request body"})
		return
	}
	out, err := h.service.PreviewRequest(claims.UserID, &req)
	if err != nil {
		writeJSON(w, requestErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}

func (h *Handler) MyQuotas(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	h.quotaView(w, claims.UserID)
}

func (h *Handler) quotaView(w http.ResponseWriter, userID int64) {
	tx, err := h.service.db.Begin()
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "Request allowances are temporarily unavailable."})
		return
	}
	defer tx.Rollback()
	out, err := h.service.Quotas.Read(tx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "user not found"})
		return
	}
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "Request allowances are temporarily unavailable."})
		return
	}
	writeJSON(w, 200, out)
}

func (h *Handler) adminQuotaTarget(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return 0, 0, false
	}
	role, err := requestquota.Role(h.service.db, claims.UserID)
	if err != nil || role != "admin" {
		writeJSON(w, 403, map[string]string{"error": "only admins can manage request allowances"})
		return 0, 0, false
	}
	var userID int64
	if raw := chi.URLParam(r, "userID"); raw != "" {
		userID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || userID <= 0 {
			writeJSON(w, 400, map[string]string{"error": "invalid user id"})
			return 0, 0, false
		}
	}
	return claims.UserID, userID, true
}

// Quota settings are separate from request-settings: older clients cannot
// overwrite allowances when saving the options they understand.
func (h *Handler) AdminQuotas(w http.ResponseWriter, r *http.Request) {
	adminID, userID, ok := h.adminQuotaTarget(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPut {
		var body struct {
			Allowances []requestquota.Rule `json:"allowances"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid allowance rules"})
			return
		}
		if err := h.service.Quotas.Save(adminID, userID, body.Allowances); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if userID != 0 {
			h.service.quotaChanged(userID)
		} else {
			h.service.allQuotasChanged()
		}
	}
	if userID != 0 {
		h.quotaView(w, userID)
		return
	}
	rules, err := h.service.Quotas.Defaults(h.service.db)
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "Request allowances are temporarily unavailable."})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"allowances": rules})
}

func (h *Handler) ResetQuotas(w http.ResponseWriter, r *http.Request) {
	adminID, userID, ok := h.adminQuotaTarget(w, r)
	if !ok {
		return
	}
	if userID == 0 {
		writeJSON(w, 400, map[string]string{"error": "user id required"})
		return
	}
	var body struct {
		Allowances []requestquota.Key `json:"allowances"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "select allowances to reset"})
		return
	}
	out, err := h.service.Quotas.Reset(adminID, userID, body.Allowances)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	h.service.quotaChanged(userID)
	writeJSON(w, 200, out)
}

func (s *Service) allQuotasChanged() {
	rows, err := s.db.Query(`SELECT id FROM users`)
	if err != nil {
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			rows.Close()
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	for _, id := range ids {
		s.quotaChanged(id)
	}
}
