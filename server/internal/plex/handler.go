package plex

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// Handler exposes the admin Plex-integration API: PIN link flow, invite
// configuration, and one-tap invites. All routes are mounted behind the
// users:manage permission.
type Handler struct {
	svc    *Service
	logger *slog.Logger
}

func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{svc: svc, logger: logger}
}

// Status returns whether an account is linked and how invites are configured.
// Never includes the token.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.Status())
}

// BeginLink starts the PIN flow: the app opens `url` for the admin and polls
// CheckLink with `pin_id`.
func (h *Handler) BeginLink(w http.ResponseWriter, r *http.Request) {
	pinID, code, authURL, err := h.svc.BeginLink(r.Context())
	if err != nil {
		h.logger.Error("plex: begin link", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach plex.tv"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pin_id": pinID,
		"code":   code,
		"url":    authURL,
	})
}

type checkLinkRequest struct {
	PinID int64 `json:"pin_id"`
}

// CheckLink polls the PIN; {linked:false} means keep waiting.
func (h *Handler) CheckLink(w http.ResponseWriter, r *http.Request) {
	var req checkLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PinID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pin_id required"})
		return
	}
	linked, account, err := h.svc.CheckLink(r.Context(), req.PinID)
	if err != nil {
		h.logger.Error("plex: check link", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach plex.tv"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"linked":  linked,
		"account": account,
	})
}

// Unlink forgets the linked account and invite configuration.
func (h *Handler) Unlink(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Unlink(); err != nil {
		h.logger.Error("plex: unlink", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to unlink"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Servers lists the linked account's owned servers for the picker.
func (h *Handler) Servers(w http.ResponseWriter, r *http.Request) {
	servers, err := h.svc.Servers(r.Context())
	if err != nil {
		h.writePlexError(w, err, "list servers")
		return
	}
	out := make([]map[string]any, 0, len(servers))
	for _, s := range servers {
		out = append(out, map[string]any{
			"name":               s.Name,
			"machine_identifier": s.ClientIdentifier,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": out})
}

// Libraries lists a server's sections for the picker.
func (h *Handler) Libraries(w http.ResponseWriter, r *http.Request) {
	machineID := chi.URLParam(r, "machineID")
	libs, err := h.svc.Libraries(r.Context(), machineID)
	if err != nil {
		h.writePlexError(w, err, "list libraries")
		return
	}
	out := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		out = append(out, map[string]any{
			"id":    l.ID,
			"title": l.Title,
			"type":  l.Type,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": out})
}

type updateSettingsRequest struct {
	MachineIdentifier string  `json:"machine_identifier"`
	ServerName        string  `json:"server_name"`
	LibrarySectionIDs []int64 `json:"library_section_ids"`
	AutoInvite        bool    `json:"auto_invite"`
}

// UpdateSettings selects the server/libraries invites share and toggles
// auto-invite.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req updateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	err := h.svc.UpdateSettings(req.MachineIdentifier, req.ServerName, req.LibrarySectionIDs, req.AutoInvite)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotLinked):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "link a Plex account first"})
		case errors.Is(err, ErrNotConfigured):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "machine_identifier required"})
		default:
			h.logger.Error("plex: update settings", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save settings"})
		}
		return
	}
	writeJSON(w, http.StatusOK, h.svc.Status())
}

// InviteUser sends the Plex invite for one user's shared email.
func (h *Handler) InviteUser(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user ID"})
		return
	}
	outcome, err := h.svc.InviteUser(r.Context(), userID)
	if err != nil {
		switch {
		case errors.Is(err, ErrNoEmail):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "user has not shared a Plex email"})
		case errors.Is(err, ErrNotLinked), errors.Is(err, ErrNotConfigured):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Plex invites are not configured"})
		default:
			h.logger.Error("plex: invite user", "err", err, "user_id", userID)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Plex invite failed"})
		}
		return
	}
	status := "invited"
	if outcome.AlreadyShared {
		status = "already_shared"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status, "email": outcome.Email})
}

func (h *Handler) writePlexError(w http.ResponseWriter, err error, op string) {
	if errors.Is(err, ErrNotLinked) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "link a Plex account first"})
		return
	}
	h.logger.Error("plex: "+op, "err", err)
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach plex.tv"})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
