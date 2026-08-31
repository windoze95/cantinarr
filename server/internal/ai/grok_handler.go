package ai

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/grokoauth"
)

// GrokStatus reports only safe metadata for the current user's xAI OAuth
// link. Tokens never cross this boundary.
func (h *Handler) GrokStatus(w http.ResponseWriter, r *http.Request) {
	setGrokNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeGrokError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	personal, hasPersonal, personalErr := h.creds.GetUserAIConfig(claims.UserID)
	personalSelected := personalErr == nil && hasPersonal && personal.Provider == credentials.AIProviderGrokOAuth
	available := auth.HasPermission(claims.Role, auth.PermissionAIChat) && h.grok != nil && h.grok.Available()
	connected := false
	if h.grok != nil {
		connected, _ = h.grok.AccountExists(grokoauth.PersonalAccount(claims.UserID))
	}
	resolved := h.resolveAI(r.Context(), claims.UserID)
	response := map[string]any{
		"available":         available,
		"selected":          personalSelected,
		"personal_selected": personalSelected,
		"connected":         connected,
		"effective":         resolved.Available && resolved.Source == aiSourcePersonal && resolved.Provider == credentials.AIProviderGrokOAuth,
	}
	if connected {
		if status, err := h.grok.StatusForAccount(grokoauth.PersonalAccount(claims.UserID)); err == nil {
			applyGrokStatus(response, status)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// BeginGrokDeviceLogin starts one user-owned xAI device authorization.
func (h *Handler) BeginGrokDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setGrokNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeGrokError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.grok == nil || !h.grok.Available() {
		writeGrokError(w, http.StatusServiceUnavailable, "Grok OAuth is unavailable on this server")
		return
	}
	if h.grok.HasAccount(claims.UserID) {
		writeGrokError(w, http.StatusConflict, "Disconnect the current xAI account before linking another one")
		return
	}

	login, err := h.grok.BeginDeviceLogin(r.Context(), claims.UserID)
	if err != nil {
		writeGrokManagerError(w, err)
		return
	}
	writeGrokDeviceLogin(w, login)
}

// CheckGrokDeviceLogin polls only a flow owned by the authenticated user.
func (h *Handler) CheckGrokDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setGrokNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeGrokError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.grok == nil {
		writeGrokError(w, http.StatusServiceUnavailable, "Grok OAuth is unavailable on this server")
		return
	}
	check, err := h.grok.CheckDeviceLogin(r.Context(), claims.UserID, chi.URLParam(r, "flowID"))
	if err != nil {
		writeGrokManagerError(w, err)
		return
	}
	if check.Status == grokoauth.LoginConnected {
		// A new authorization may belong to a different xAI account than an
		// earlier link. Never replay prior transcripts across that boundary.
		if h.conversations != nil {
			h.conversations.DeleteForUser(claims.UserID)
		}
		if err := h.selectPersonalGrok(r.Context(), claims.UserID); err != nil {
			log.Printf("personal Grok OAuth validation failed user_id=%d: %s", claims.UserID, AIValidationDiagnostic(err))
			// The account link itself is already stored at this point; only
			// the provider selection was left untouched — say exactly that.
			message := "xAI connected, but Cantinarr could not activate the personal provider. Your provider selection was not changed."
			if errors.Is(err, ErrAIValidation) {
				message = AIValidationUserMessage(err)
			}
			writeGrokError(w, http.StatusUnprocessableEntity, message)
			return
		}
	}
	writeGrokDeviceLoginCheck(w, check)
}

func (h *Handler) selectPersonalGrok(ctx context.Context, userID int64) error {
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	config := credentials.AIConfig{
		Provider: credentials.AIProviderGrokOAuth,
		Model:    credentials.DefaultAIModel(credentials.AIProviderGrokOAuth),
	}
	if selected, found, err := h.creds.GetUserAIConfig(userID); err != nil {
		return err
	} else if found && selected.Provider == credentials.AIProviderGrokOAuth {
		config = selected
	}
	if err := h.ValidatePersonalAISettings(ctx, userID, credentials.AIProfile{Config: config, CredentialPresent: true}); err != nil {
		return err
	}
	return h.creds.SetUserAIConfig(userID, config.Provider, config.Model)
}

// CancelGrokDeviceLogin cancels one pending flow owned by the caller.
func (h *Handler) CancelGrokDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setGrokNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeGrokError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.grok == nil {
		writeGrokError(w, http.StatusServiceUnavailable, "Grok OAuth is unavailable on this server")
		return
	}
	if err := h.grok.CancelDeviceLogin(claims.UserID, chi.URLParam(r, "flowID")); err != nil {
		writeGrokManagerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnlinkGrok deletes the caller's encrypted xAI authorization and any pending
// device flow. It does not affect another Cantinarr user.
func (h *Handler) UnlinkGrok(w http.ResponseWriter, r *http.Request) {
	setGrokNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeGrokError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.grok == nil {
		writeGrokError(w, http.StatusServiceUnavailable, "Grok OAuth is unavailable on this server")
		return
	}
	if err := h.grok.Unlink(claims.UserID); err != nil {
		log.Printf("grok unlink failed for user_id=%d: %v", claims.UserID, err)
		writeGrokError(w, http.StatusInternalServerError, "Could not disconnect Grok OAuth")
		return
	}
	if h.conversations != nil {
		h.conversations.DeleteForUser(claims.UserID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// SharedGrokStatus reports safe metadata for the singleton admin-funded xAI
// account. This handler is mounted only behind CredentialsManage.
func (h *Handler) SharedGrokStatus(w http.ResponseWriter, r *http.Request) {
	setGrokNoStore(w)
	if !hasSharedCodexAdmin(r) {
		writeGrokError(w, http.StatusForbidden, "forbidden")
		return
	}
	selected := false
	if profile, err := h.creds.LoadSharedAIProfile(r.Context()); err == nil {
		selected = profile.Config.Provider == credentials.AIProviderGrokOAuth
	}
	available := h.grok != nil && h.grok.Available()
	connected := false
	if h.grok != nil {
		connected, _ = h.grok.AccountExists(grokoauth.SharedAccount())
	}
	response := map[string]any{
		"available": available,
		"selected":  selected,
		"connected": connected,
	}
	if connected {
		if status, err := h.grok.StatusForAccount(grokoauth.SharedAccount()); err == nil {
			applyGrokStatus(response, status)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (h *Handler) BeginSharedGrokDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setGrokNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil || !auth.HasPermission(claims.Role, auth.PermissionCredentialsManage) {
		writeGrokError(w, http.StatusForbidden, "forbidden")
		return
	}
	if h.grok == nil || !h.grok.Available() {
		writeGrokError(w, http.StatusServiceUnavailable, "Grok OAuth is unavailable on this server")
		return
	}
	connected, err := h.grok.AccountExists(grokoauth.SharedAccount())
	if err != nil {
		writeGrokError(w, http.StatusInternalServerError, "Could not check the shared xAI account")
		return
	}
	if connected {
		writeGrokError(w, http.StatusConflict, "Disconnect the shared xAI account before linking another one")
		return
	}
	login, err := h.grok.BeginDeviceLoginForAccount(r.Context(), grokoauth.SharedAccount(), claims.UserID)
	if err != nil {
		writeGrokManagerError(w, err)
		return
	}
	writeGrokDeviceLogin(w, login)
}

func (h *Handler) CheckSharedGrokDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setGrokNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil || !auth.HasPermission(claims.Role, auth.PermissionCredentialsManage) {
		writeGrokError(w, http.StatusForbidden, "forbidden")
		return
	}
	if h.grok == nil {
		writeGrokError(w, http.StatusServiceUnavailable, "Grok OAuth is unavailable on this server")
		return
	}
	check, err := h.grok.CheckDeviceLoginForAccount(r.Context(), grokoauth.SharedAccount(), claims.UserID, chi.URLParam(r, "flowID"))
	if err != nil {
		writeGrokManagerError(w, err)
		return
	}
	if check.Status == grokoauth.LoginConnected {
		// A newly published shared authorization may belong to a different xAI
		// account than an earlier link. Never replay prior users' transcripts
		// across that external account boundary.
		if h.conversations != nil {
			h.conversations.DeleteAll()
		}
		config := credentials.AIConfig{
			Provider: credentials.AIProviderGrokOAuth,
			Model:    credentials.DefaultAIModel(credentials.AIProviderGrokOAuth),
		}
		selected := false
		if profile, profileErr := h.creds.LoadSharedAIProfile(r.Context()); profileErr == nil && profile.Config.Provider == credentials.AIProviderGrokOAuth {
			config = profile.Config
			selected = true
		}
		if validateErr := h.ValidateSharedAISettings(r.Context(), credentials.AIProfile{Config: config, CredentialPresent: true}); validateErr != nil {
			log.Printf("shared Grok OAuth validation failed: %s", AIValidationDiagnostic(validateErr))
			writeGrokError(w, http.StatusUnprocessableEntity, AIValidationUserMessage(validateErr))
			return
		}
		if selected {
			h.SharedAISettingsValidated(config)
		}
	}
	writeGrokDeviceLoginCheck(w, check)
}

func (h *Handler) CancelSharedGrokDeviceLogin(w http.ResponseWriter, r *http.Request) {
	setGrokNoStore(w)
	claims := auth.GetClaims(r.Context())
	if claims == nil || !auth.HasPermission(claims.Role, auth.PermissionCredentialsManage) {
		writeGrokError(w, http.StatusForbidden, "forbidden")
		return
	}
	if h.grok == nil {
		writeGrokError(w, http.StatusServiceUnavailable, "Grok OAuth is unavailable on this server")
		return
	}
	if err := h.grok.CancelDeviceLoginForAccount(grokoauth.SharedAccount(), claims.UserID, chi.URLParam(r, "flowID")); err != nil {
		writeGrokManagerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) UnlinkSharedGrok(w http.ResponseWriter, r *http.Request) {
	setGrokNoStore(w)
	if !hasSharedCodexAdmin(r) {
		writeGrokError(w, http.StatusForbidden, "forbidden")
		return
	}
	if h.grok == nil {
		writeGrokError(w, http.StatusServiceUnavailable, "Grok OAuth is unavailable on this server")
		return
	}
	if err := h.grok.UnlinkAccount(grokoauth.SharedAccount()); err != nil {
		log.Printf("shared grok unlink failed: %v", err)
		writeGrokError(w, http.StatusInternalServerError, "Could not disconnect shared Grok OAuth")
		return
	}
	if h.conversations != nil {
		h.conversations.DeleteAll()
	}
	w.WriteHeader(http.StatusNoContent)
}

func applyGrokStatus(response map[string]any, status grokoauth.AccountStatus) {
	response["connected"] = status.Connected
	if status.Email != "" {
		response["account_email"] = status.Email
	}
	if status.PlanType != "" {
		response["plan_type"] = status.PlanType
	}
	if !status.UpdatedAt.IsZero() {
		response["updated_at"] = status.UpdatedAt.Format(time.RFC3339)
	}
}

func writeGrokDeviceLogin(w http.ResponseWriter, login grokoauth.DeviceLogin) {
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

func writeGrokDeviceLoginCheck(w http.ResponseWriter, check grokoauth.DeviceLoginCheck) {
	response := map[string]any{"status": string(check.Status)}
	if check.Error != "" {
		response["error"] = check.Error
	}
	if check.Account.Connected {
		account := map[string]any{"email": check.Account.Email}
		if check.Account.PlanType != "" {
			account["plan_type"] = check.Account.PlanType
		}
		response["account"] = account
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func writeGrokManagerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, grokoauth.ErrUnavailable):
		writeGrokError(w, http.StatusServiceUnavailable, "Grok OAuth is unavailable on this server")
	case errors.Is(err, grokoauth.ErrNotConnected):
		writeGrokError(w, http.StatusConflict, "No xAI account is linked")
	case errors.Is(err, grokoauth.ErrFlowNotFound):
		writeGrokError(w, http.StatusNotFound, "xAI sign-in flow not found")
	case errors.Is(err, grokoauth.ErrFlowExpired):
		writeGrokError(w, http.StatusGone, "xAI sign-in expired; start again")
	case errors.Is(err, grokoauth.ErrLoginInProgress):
		writeGrokError(w, http.StatusConflict, "An xAI sign-in is already in progress")
	case errors.Is(err, grokoauth.ErrAlreadyConnected):
		writeGrokError(w, http.StatusConflict, "Disconnect the current xAI account before linking another one")
	default:
		log.Printf("grok account operation failed: %v", err)
		writeGrokError(w, http.StatusBadGateway, "xAI sign-in could not be completed")
	}
}

func writeGrokError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func setGrokNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}
