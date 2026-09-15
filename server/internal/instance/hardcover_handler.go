package instance

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/hardcover"
)

func writeHardcoverJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeHardcoverError(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, errHardcoverStorage.Error()
	switch {
	case errors.Is(err, hardcover.ErrUnauthorized):
		status, message = http.StatusBadRequest, "Hardcover rejected the API token. Copy it again from Hardcover's settings and try once more."
	case errors.Is(err, hardcover.ErrInsufficientScope):
		status, message = http.StatusBadRequest, hardcover.ErrInsufficientScope.Error()
	case errors.Is(err, errHardcoverChanged):
		status, message = http.StatusConflict, errHardcoverChanged.Error()
	case errors.Is(err, errHardcoverMissing), errors.Is(err, errHardcoverFlowMissing):
		status, message = http.StatusNotFound, err.Error()
	case errors.Is(err, errHardcoverReconnect):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, hardcover.ErrProvider), errors.Is(err, hardcover.ErrSlowDown):
		status, message = http.StatusBadGateway, "could not reach Hardcover to verify the connection; try again"
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (h *Handler) hardcoverOAuthInstance(w http.ResponseWriter, r *http.Request) (*Instance, bool) {
	inst, ok := h.hardcoverInstance(w, r)
	if !ok {
		return nil, false
	}
	if !SupportsHardcover(inst.ServiceType) {
		http.Error(w, `{"error":"Hardcover connects to Chaptarr instances only"}`, http.StatusBadRequest)
		return nil, false
	}
	return inst, true
}

func (h *Handler) BeginHardcoverDevice(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.hardcoverOAuthInstance(w, r)
	if !ok {
		return
	}
	result, err := h.hardcover.Begin(r.Context(), auth.GetClaims(r.Context()).UserID, inst.ID)
	if err != nil {
		writeHardcoverError(w, err)
		return
	}
	writeHardcoverJSON(w, result)
}
func (h *Handler) CheckHardcoverDevice(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.hardcoverOAuthInstance(w, r)
	if !ok {
		return
	}
	result, err := h.hardcover.Check(r.Context(), auth.GetClaims(r.Context()).UserID, inst.ID, chi.URLParam(r, "flowID"))
	if err != nil {
		writeHardcoverError(w, err)
		return
	}
	if result.Status == "connected" {
		h.hardcover.ForgetUnlinked()
	}
	writeHardcoverJSON(w, result)
}
func (h *Handler) CancelHardcoverDevice(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.hardcoverOAuthInstance(w, r)
	if !ok {
		return
	}
	result, err := h.hardcover.Cancel(auth.GetClaims(r.Context()).UserID, inst.ID, chi.URLParam(r, "flowID"))
	if err != nil {
		writeHardcoverError(w, err)
		return
	}
	writeHardcoverJSON(w, result)
}

// ApplyHardcoverConnection accepts the source connection id plus exactly the
// target instances/revisions displayed to the admin. Each target is independent.
func (h *Handler) ApplyHardcoverConnection(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.hardcoverOAuthInstance(w, r)
	if !ok {
		return
	}
	var body struct {
		ConnectionID string                 `json:"connection_id"`
		Instances    []HardcoverApplyTarget `json:"instances"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || body.ConnectionID == "" || len(body.Instances) == 0 || len(body.Instances) > 100 {
		http.Error(w, `{"error":"provide a connection ID and 1 to 100 explicit instances with their revisions"}`, http.StatusBadRequest)
		return
	}
	state, err := h.store.HardcoverState(inst.ID)
	if err != nil {
		writeHardcoverError(w, errHardcoverStorage)
		return
	}
	if state.ConnectionID != body.ConnectionID || state.Reconnect {
		writeHardcoverError(w, errHardcoverChanged)
		return
	}
	results := make([]HardcoverApplyResult, 0, len(body.Instances))
	seen := map[string]bool{inst.ID: true}
	for _, target := range body.Instances {
		result := HardcoverApplyResult{InstanceID: target.InstanceID}
		if target.InstanceID == "" || seen[target.InstanceID] || target.Revision < 0 {
			result.Error = "Choose each other Chaptarr instance once."
		} else {
			seen[target.InstanceID] = true
			err = h.store.applyHardcoverConnection(r.Context(), inst.ID, body.ConnectionID, target)
			if errors.Is(err, errHardcoverChanged) {
				result.Error = "This connection or instance changed. Reload it and try again."
			} else if err != nil {
				result.Error = errHardcoverStorage.Error()
			} else {
				result.Applied = true
				h.notifyHardcoverChanged(target.InstanceID)
			}
		}
		results = append(results, result)
	}
	h.hardcover.ForgetUnlinked()
	writeHardcoverJSON(w, map[string]any{"results": results})
}
