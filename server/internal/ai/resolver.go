package ai

import (
	"context"
	"time"

	"github.com/windoze95/cantinarr-server/internal/codexapp"
	"github.com/windoze95/cantinarr-server/internal/credentials"
)

const (
	aiSourceNone     = "none"
	aiSourcePersonal = "personal"
	aiSourceShared   = "shared"
)

type resolvedAI struct {
	Available bool
	Source    string
	Provider  string
	Model     string
	Reason    string
	APIKey    string
	Account   codexapp.AccountRef
}

func (h *Handler) resolveAI(ctx context.Context, userID int64) resolvedAI {
	personal, selected, err := h.creds.LoadUserAIProfile(ctx, userID)
	if selected {
		resolved := resolvedAI{
			Source:   aiSourcePersonal,
			Provider: personal.Config.Provider,
			Model:    personal.Config.Model,
			Account:  codexapp.PersonalAccount(userID),
		}
		if err != nil {
			resolved.Reason = "storage_error"
			return resolved
		}
		if personal.Config.Provider == credentials.AIProviderCodex {
			if h.codex == nil || !h.codex.Available() {
				resolved.Reason = "codex_unavailable"
				return resolved
			}
			connected, err := h.codex.AccountExists(resolved.Account)
			if err != nil {
				resolved.Reason = "storage_error"
				return resolved
			}
			if !connected {
				resolved.Reason = "personal_codex_disconnected"
				return resolved
			}
			resolved.Available = true
			return resolved
		}
		if !personal.CredentialPresent {
			resolved.Reason = "personal_credential_missing"
			return resolved
		}
		resolved.Available = true
		resolved.APIKey = personal.APIKey
		return resolved
	}
	if err != nil {
		return resolvedAI{Source: aiSourceNone, Reason: "storage_error"}
	}

	shared, granted, err := h.creds.LoadSharedAIProfileForUser(ctx, userID)
	if !granted {
		if err != nil {
			return resolvedAI{Source: aiSourceNone, Reason: "storage_error"}
		}
		return resolvedAI{Source: aiSourceNone, Reason: "shared_access_disabled"}
	}
	resolved := resolvedAI{
		Source:   aiSourceShared,
		Provider: shared.Config.Provider,
		Model:    shared.Config.Model,
		Account:  codexapp.SharedAccount(),
	}
	if err != nil {
		resolved.Reason = "storage_error"
		return resolved
	}
	if shared.Config.Provider == credentials.AIProviderCodex {
		if h.codex == nil || !h.codex.Available() {
			resolved.Reason = "codex_unavailable"
			return resolved
		}
		connected, err := h.codex.AccountExists(resolved.Account)
		if err != nil {
			resolved.Reason = "storage_error"
			return resolved
		}
		if !connected {
			resolved.Reason = "shared_codex_disconnected"
			return resolved
		}
		resolved.Available = true
		return resolved
	}
	if !shared.CredentialPresent {
		resolved.Reason = "shared_credential_missing"
		return resolved
	}
	resolved.Available = true
	resolved.APIKey = shared.APIKey
	return resolved
}

func (h *Handler) resolveAIForUser(userID int64) resolvedAI {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.resolveAI(ctx, userID)
}

// resolveSharedAI resolves only the admin-owned included profile. It never
// reads a user row, personal selection, or per-user grant. Server-owned work
// such as autonomous remediation uses this path so its billing source is the
// explicit global provider selected by the administrator.
func (h *Handler) resolveSharedAI(ctx context.Context) resolvedAI {
	shared, err := h.creds.LoadSharedAIProfile(ctx)
	resolved := resolvedAI{
		Source:   aiSourceShared,
		Provider: shared.Config.Provider,
		Model:    shared.Config.Model,
		Account:  codexapp.SharedAccount(),
	}
	if err != nil {
		resolved.Reason = "storage_error"
		return resolved
	}
	if shared.Config.Provider == credentials.AIProviderCodex {
		if h.codex == nil || !h.codex.Available() {
			resolved.Reason = "codex_unavailable"
			return resolved
		}
		connected, err := h.codex.AccountExists(resolved.Account)
		if err != nil {
			resolved.Reason = "storage_error"
			return resolved
		}
		if !connected {
			resolved.Reason = "shared_codex_disconnected"
			return resolved
		}
		resolved.Available = true
		return resolved
	}
	if !shared.CredentialPresent {
		resolved.Reason = "shared_credential_missing"
		return resolved
	}
	resolved.Available = true
	resolved.APIKey = shared.APIKey
	return resolved
}

func (h *Handler) sharedProviderConfigured(ctx context.Context) bool {
	return h.resolveSharedAI(ctx).Available
}
