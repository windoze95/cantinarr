package request

import (
	"github.com/windoze95/cantinarr-server/internal/auth"
	"net/http"
)

// SavedMusicRequest is intent only. Live files and queue state are read
// independently, so a stalled Lidarr cannot hide a saved request receipt.
type SavedMusicRequest struct {
	RequestID          int64           `json:"request_id"`
	ForeignID          string          `json:"foreign_id"`
	CanonicalForeignID string          `json:"canonical_foreign_id,omitempty"`
	CatalogRef         *CatalogRef     `json:"catalog_ref,omitempty"`
	Status             string          `json:"status"`
	Delivery           []DeliveryState `json:"delivery"`
}

func (s *Service) SavedMusicRequests(userID int64, instanceID string) ([]SavedMusicRequest, error) {
	id, err := s.deliveryInstance(userID, "music", instanceID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT r.id,COALESCE(r.foreign_id,''),COALESCE(r.catalog_provider,''),COALESCE(r.catalog_id,''),r.status,COALESCE(r.park_reason,''),COALESCE((SELECT d.canonical_foreign_id FROM request_dispatch d WHERE d.request_id=r.id AND d.canonical_foreign_id!='' LIMIT 1),'') FROM request_log r WHERE r.media_type='music' AND r.instance_id=? AND r.user_id=? ORDER BY r.id DESC`, id, userID)
	if err != nil {
		return nil, err
	}
	out := []SavedMusicRequest{}
	for rows.Next() {
		var row SavedMusicRequest
		var provider, source, park string
		if err = rows.Scan(&row.RequestID, &row.ForeignID, &provider, &source, &row.Status, &park, &row.CanonicalForeignID); err != nil {
			break
		}
		if provider != "" {
			row.CatalogRef = &CatalogRef{Provider: provider, ID: source}
		}
		if park == "delivery" || row.Status == StatusAvailable || row.Status == StatusDownloading || row.Status == StatusPartial {
			row.Status = StatusRequested
		}
		out = append(out, row)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}
	for i := range out {
		out[i].Delivery, err = s.deliveryStates(out[i].RequestID)
		if err != nil {
			return nil, err
		}
	}
	if _, err = s.deliveryInstance(userID, "music", id); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *Handler) GetSavedMusic(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	rows, err := h.service.SavedMusicRequests(claims.UserID, r.URL.Query().Get("instance_id"))
	if err != nil {
		writeJSON(w, requestErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": rows})
}
