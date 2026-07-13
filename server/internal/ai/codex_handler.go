package ai

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
)

// CodexStatus reports only safe metadata for the current user's ChatGPT link.
// The encrypted auth blob and app-server details never cross this boundary.
func (h *Handler) CodexStatus(w http.ResponseWriter, r *http.Request) {
	setCodexNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeCodexError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	personal, hasPersonal, personalErr := h.creds.GetUserAIConfig(claims.UserID)
	personalSelected := personalErr == nil && hasPersonal && personal.Provider == credentials.AIProviderCodex
	available := auth.HasPermission(claims.Role, auth.PermissionAIChat) && h.codex != nil && h.codex.Available()
	connected := false
	if h.codex != nil {
		connected, _ = h.codex.AccountExists(codexapp.PersonalAccount(claims.UserID))
	}
	resolved := h.resolveAI(r.Context(), claims.UserID)
	selected := personalSelected
	// Compatibility for the first Codex-capable app: it only exposes its
	// personal Connect screen when selected=true. If shared Codex is selected
	// but not usable for this caller, let that old client link a personal
	// override. Once included Codex is usable it should open chat instead.
	if personalErr == nil && !hasPersonal {
		if shared, _, err := h.creds.LoadSharedAIProfileForUser(r.Context(), claims.UserID); err == nil &&
			shared.Config.Provider == credentials.AIProviderCodex &&
			!(resolved.Available && resolved.Source == aiSourceShared && resolved.Provider == credentials.AIProviderCodex) {
			selected = true
		}
	}
	response := map[string]any{
		"available":         available,
		"selected":          selected,
		"personal_selected": personalSelected,
		"connected":         connected,
		"effective":         resolved.Available && resolved.Source == aiSourcePersonal && resolved.Provider == credentials.AIProviderCodex,
	}

	if connected {
		status, err := h.codex.Status(r.Context(), claims.UserID, available)
		if err != nil {
			// A rate-limit refresh is best-effort. Return the last encrypted-row
			// metadata when the upstream is temporarily unavailable.
			log.Printf("codex status refresh failed for user_id=%d: %v", claims.UserID, err)
			status, err = h.codex.Status(r.Context(), claims.UserID, false)
		}
		if err == nil {
			response["connected"] = status.Connected
			response["account_email"] = status.Email
			response["plan_type"] = status.PlanType
			response["stale"] = status.Stale
			if !status.UpdatedAt.IsZero() {
				response["updated_at"] = status.UpdatedAt.Format(time.RFC3339)
			}
			if limits := publicRateLimits(status.RateLimits); limits != nil {
				response["rate_limits"] = limits
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// BeginCodexDeviceLogin starts one user-owned ChatGPT device authorization.
func (h *Handler) BeginCodexDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setCodexNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeCodexError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.codex == nil || !h.codex.Available() {
		writeCodexError(w, http.StatusServiceUnavailable, "ChatGPT (Codex) is unavailable on this server")
		return
	}
	if h.codex.HasAccount(claims.UserID) {
		writeCodexError(w, http.StatusConflict, "Disconnect the current ChatGPT account before linking another one")
		return
	}

	login, err := h.codex.BeginDeviceLogin(r.Context(), claims.UserID)
	if err != nil {
		writeCodexManagerError(w, err)
		return
	}
	writeDeviceLogin(w, login)
}

// SharedCodexStatus reports safe metadata for the singleton admin-funded
// ChatGPT account. This handler is mounted only behind CredentialsManage.
func (h *Handler) SharedCodexStatus(w http.ResponseWriter, r *http.Request) {
	setCodexNoStore(w)
	if !hasSharedCodexAdmin(r) {
		writeCodexError(w, http.StatusForbidden, "forbidden")
		return
	}
	selected := false
	if profile, err := h.creds.LoadSharedAIProfile(r.Context()); err == nil {
		selected = profile.Config.Provider == credentials.AIProviderCodex
	}
	available := h.codex != nil && h.codex.Available()
	connected := false
	if h.codex != nil {
		connected, _ = h.codex.AccountExists(codexapp.SharedAccount())
	}
	response := map[string]any{
		"available": available,
		"selected":  selected,
		"connected": connected,
	}
	if connected {
		status, err := h.codex.StatusForAccount(r.Context(), codexapp.SharedAccount(), available)
		if err != nil {
			log.Printf("shared codex status refresh failed: %v", err)
			status, err = h.codex.StatusForAccount(r.Context(), codexapp.SharedAccount(), false)
		}
		if err == nil {
			response["connected"] = status.Connected
			response["account_email"] = status.Email
			response["plan_type"] = status.PlanType
			response["stale"] = status.Stale
			if !status.UpdatedAt.IsZero() {
				response["updated_at"] = status.UpdatedAt.Format(time.RFC3339)
			}
			if limits := publicRateLimits(status.RateLimits); limits != nil {
				response["rate_limits"] = limits
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (h *Handler) BeginSharedCodexDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setCodexNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil || !auth.HasPermission(claims.Role, auth.PermissionCredentialsManage) {
		writeCodexError(w, http.StatusForbidden, "forbidden")
		return
	}
	if h.codex == nil || !h.codex.Available() {
		writeCodexError(w, http.StatusServiceUnavailable, "ChatGPT (Codex) is unavailable on this server")
		return
	}
	connected, err := h.codex.AccountExists(codexapp.SharedAccount())
	if err != nil {
		writeCodexError(w, http.StatusInternalServerError, "Could not check the shared ChatGPT account")
		return
	}
	if connected {
		writeCodexError(w, http.StatusConflict, "Disconnect the shared ChatGPT account before linking another one")
		return
	}
	login, err := h.codex.BeginDeviceLoginForAccount(r.Context(), codexapp.SharedAccount(), claims.UserID)
	if err != nil {
		writeCodexManagerError(w, err)
		return
	}
	writeDeviceLogin(w, login)
}

func (h *Handler) CheckSharedCodexDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setCodexNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil || !auth.HasPermission(claims.Role, auth.PermissionCredentialsManage) {
		writeCodexError(w, http.StatusForbidden, "forbidden")
		return
	}
	if h.codex == nil {
		writeCodexError(w, http.StatusServiceUnavailable, "ChatGPT (Codex) is unavailable on this server")
		return
	}
	check, err := h.codex.CheckDeviceLoginForAccount(r.Context(), codexapp.SharedAccount(), claims.UserID, chi.URLParam(r, "flowID"))
	if err != nil {
		writeCodexManagerError(w, err)
		return
	}
	writeDeviceLoginCheck(w, check)
}

func (h *Handler) CancelSharedCodexDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setCodexNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil || !auth.HasPermission(claims.Role, auth.PermissionCredentialsManage) {
		writeCodexError(w, http.StatusForbidden, "forbidden")
		return
	}
	if h.codex == nil {
		writeCodexError(w, http.StatusServiceUnavailable, "ChatGPT (Codex) is unavailable on this server")
		return
	}
	if err := h.codex.CancelDeviceLoginForAccount(codexapp.SharedAccount(), claims.UserID, chi.URLParam(r, "flowID")); err != nil {
		writeCodexManagerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) UnlinkSharedCodex(w http.ResponseWriter, r *http.Request) {
	setCodexNoStore(w)
	if !hasSharedCodexAdmin(r) {
		writeCodexError(w, http.StatusForbidden, "forbidden")
		return
	}
	if h.codex == nil {
		writeCodexError(w, http.StatusServiceUnavailable, "ChatGPT (Codex) is unavailable on this server")
		return
	}
	if err := h.codex.UnlinkAccount(codexapp.SharedAccount()); err != nil {
		log.Printf("shared codex unlink failed: %v", err)
		writeCodexError(w, http.StatusInternalServerError, "Could not disconnect shared ChatGPT")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func hasSharedCodexAdmin(r *http.Request) bool {
	claims := auth.GetClaims(r.Context())
	return claims != nil && auth.HasPermission(claims.Role, auth.PermissionCredentialsManage)
}

// CheckCodexDeviceLogin polls only a flow owned by the authenticated user.
func (h *Handler) CheckCodexDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setCodexNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeCodexError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.codex == nil {
		writeCodexError(w, http.StatusServiceUnavailable, "ChatGPT (Codex) is unavailable on this server")
		return
	}
	check, err := h.codex.CheckDeviceLogin(r.Context(), claims.UserID, chi.URLParam(r, "flowID"))
	if err != nil {
		writeCodexManagerError(w, err)
		return
	}
	if check.Status == codexapp.LoginConnected {
		if err := h.selectPersonalCodex(claims.UserID); err != nil {
			writeCodexError(w, http.StatusInternalServerError, "ChatGPT was linked but the personal AI selection could not be saved")
			return
		}
	}

	writeDeviceLoginCheck(w, check)
}

func (h *Handler) selectPersonalCodex(userID int64) error {
	return h.creds.SetUserAIConfig(userID, credentials.AIProviderCodex, credentials.DefaultAIModel(credentials.AIProviderCodex))
}

func writeDeviceLogin(w http.ResponseWriter, login codexapp.DeviceLogin) {
	expiresIn := max(int(time.Until(login.ExpiresAt).Seconds()), 0)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"flow_id":          login.FlowID,
		"verification_uri": login.VerificationURI,
		"user_code":        login.UserCode,
		"expires_in":       expiresIn,
		"interval":         login.IntervalSeconds,
	})
}

func writeDeviceLoginCheck(w http.ResponseWriter, check codexapp.DeviceLoginCheck) {
	response := map[string]any{"status": string(check.Status)}
	if check.Error != "" {
		response["error"] = check.Error
	}
	if check.Account.Connected {
		response["account"] = map[string]any{
			"email":     check.Account.Email,
			"plan_type": check.Account.PlanType,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// CancelCodexDeviceLogin cancels one pending flow owned by the caller.
func (h *Handler) CancelCodexDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setCodexNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeCodexError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.codex == nil {
		writeCodexError(w, http.StatusServiceUnavailable, "ChatGPT (Codex) is unavailable on this server")
		return
	}
	if err := h.codex.CancelDeviceLogin(claims.UserID, chi.URLParam(r, "flowID")); err != nil {
		writeCodexManagerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnlinkCodex deletes the caller's encrypted ChatGPT authorization and any
// pending device flow. It does not affect another Cantinarr user.
func (h *Handler) UnlinkCodex(w http.ResponseWriter, r *http.Request) {
	setCodexNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeCodexError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.codex == nil {
		writeCodexError(w, http.StatusServiceUnavailable, "ChatGPT (Codex) is unavailable on this server")
		return
	}
	if err := h.codex.Unlink(claims.UserID); err != nil {
		log.Printf("codex unlink failed for user_id=%d: %v", claims.UserID, err)
		writeCodexError(w, http.StatusInternalServerError, "Could not disconnect ChatGPT")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeCodexManagerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, codexapp.ErrUnavailable):
		writeCodexError(w, http.StatusServiceUnavailable, "ChatGPT (Codex) is unavailable on this server")
	case errors.Is(err, codexapp.ErrNotConnected):
		writeCodexError(w, http.StatusConflict, "No ChatGPT account is linked")
	case errors.Is(err, codexapp.ErrFlowNotFound):
		writeCodexError(w, http.StatusNotFound, "ChatGPT sign-in flow not found")
	case errors.Is(err, codexapp.ErrFlowExpired):
		writeCodexError(w, http.StatusGone, "ChatGPT sign-in expired; start again")
	case errors.Is(err, codexapp.ErrLoginInProgress):
		writeCodexError(w, http.StatusConflict, "A ChatGPT sign-in is already in progress")
	case errors.Is(err, codexapp.ErrAlreadyConnected):
		writeCodexError(w, http.StatusConflict, "Disconnect the current ChatGPT account before linking another one")
	default:
		log.Printf("codex account operation failed: %v", err)
		writeCodexError(w, http.StatusBadGateway, "ChatGPT sign-in could not be completed")
	}
}

func writeCodexError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func setCodexNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

// publicRateLimits normalizes the app-server's camelCase snapshot into the
// stable snake_case API consumed by Flutter. Unknown future fields stay hidden.
func publicRateLimits(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	if nested, ok := value["rateLimits"].(map[string]any); ok {
		value = nested
	}
	out := make(map[string]any, 2)
	for _, name := range []string{"primary", "secondary"} {
		window, ok := value[name].(map[string]any)
		if !ok {
			continue
		}
		public := make(map[string]any, 3)
		if used, ok := window["usedPercent"].(float64); ok {
			public["used_percent"] = used
		}
		if reset, ok := window["resetsAt"].(float64); ok {
			public["resets_at"] = int64(reset)
		}
		if duration, ok := window["windowDurationMins"].(float64); ok {
			public["window_duration_mins"] = int64(duration)
		}
		if len(public) > 0 {
			out[name] = public
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
