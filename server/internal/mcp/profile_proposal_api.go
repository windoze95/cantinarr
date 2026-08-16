package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

var (
	errProfileProposalNotPending   = errors.New("this proposal was already decided or replaced")
	errProfileProposalStale        = errors.New("the live settings no longer match this proposal")
	errProfileProposalUnavailable  = errors.New("the live settings are unavailable")
	errProfileProposalToolDisabled = errors.New("the profile apply tool is disabled")
)

// ProfileProposalHandler exposes the admin approval surface for profile
// changes parked by external MCP agents. Plans, hashes, and instance
// fingerprints stay inside ToolServer methods; the app only ever sees the
// rendered diff and lifecycle metadata.
type ProfileProposalHandler struct {
	server *ToolServer
}

func NewProfileProposalHandler(server *ToolServer) *ProfileProposalHandler {
	return &ProfileProposalHandler{server: server}
}

func (h *ProfileProposalHandler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	stored, err := h.server.profileProposals.list(status)
	if err != nil {
		writeSettingsChangeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load profile change proposals"})
		return
	}
	proposals := make([]ProfileChangeProposal, 0, len(stored))
	for _, p := range stored {
		proposals = append(proposals, p.ProfileChangeProposal)
	}
	writeSettingsChangeJSON(w, http.StatusOK, map[string]any{"proposals": proposals})
}

func (h *ProfileProposalHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeSettingsChangeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid proposal id"})
		return
	}
	proposal, err := h.server.profileProposalDetail(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeSettingsChangeJSON(w, http.StatusNotFound, map[string]string{"error": "proposal not found"})
		return
	}
	if err != nil {
		writeSettingsChangeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load proposal"})
		return
	}
	writeSettingsChangeJSON(w, http.StatusOK, proposal)
}

func (h *ProfileProposalHandler) Approve(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeSettingsChangeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeSettingsChangeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid proposal id"})
		return
	}
	proposal, err := h.server.approveProfileProposal(r.Context(), id, CallContext{
		UserID: claims.UserID, Role: claims.Role, DeviceID: claims.DeviceID,
		Reauthorize: true, Origin: OriginInteractiveChat,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeSettingsChangeJSON(w, http.StatusNotFound, map[string]string{"error": "proposal not found"})
	case errors.Is(err, errProfileProposalNotPending):
		writeSettingsChangeJSON(w, http.StatusConflict, map[string]string{"error": "This proposal was already decided, replaced, or expired. Refresh the list."})
	case errors.Is(err, errProfileProposalStale):
		writeSettingsChangeJSON(w, http.StatusConflict, map[string]string{"error": "The profile or its dependencies changed after this was proposed. Nothing was written; ask the assistant to propose it again from current settings."})
	case errors.Is(err, errProfileProposalToolDisabled):
		writeSettingsChangeJSON(w, http.StatusConflict, map[string]string{"error": "Profile changes are disabled in Settings > AI Tools. The proposal is still pending."})
	case errors.Is(err, errProfileProposalUnavailable):
		writeSettingsChangeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "The arr instance could not be read. Nothing was written; the proposal is still pending."})
	case errors.Is(err, ErrToolAuthorization):
		writeSettingsChangeJSON(w, http.StatusForbidden, map[string]string{"error": "permission denied"})
	case err != nil:
		writeSettingsChangeJSON(w, http.StatusInternalServerError, map[string]string{"error": secrets.RedactText(err.Error())})
	default:
		writeSettingsChangeJSON(w, http.StatusOK, proposal)
	}
}

func (h *ProfileProposalHandler) Reject(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeSettingsChangeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeSettingsChangeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid proposal id"})
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	rejected, err := h.server.profileProposals.reject(id, claims.UserID, strings.TrimSpace(body.Note))
	if err != nil {
		writeSettingsChangeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not reject proposal"})
		return
	}
	if !rejected {
		if _, getErr := h.server.profileProposals.get(id); errors.Is(getErr, sql.ErrNoRows) {
			writeSettingsChangeJSON(w, http.StatusNotFound, map[string]string{"error": "proposal not found"})
			return
		}
		writeSettingsChangeJSON(w, http.StatusConflict, map[string]string{"error": "This proposal was already decided, replaced, or expired. Refresh the list."})
		return
	}
	h.server.notifyProfileProposalDecided(id)
	updated, err := h.server.profileProposals.get(id)
	if err != nil {
		writeSettingsChangeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load proposal"})
		return
	}
	writeSettingsChangeJSON(w, http.StatusOK, updated.ProfileChangeProposal)
}

// profileProposalDetail returns one proposal, annotating a pending one with a
// best-effort live-applicability check so the approving admin is told about
// drift before they tap, not after.
func (s *ToolServer) profileProposalDetail(ctx context.Context, id int64) (ProfileChangeProposal, error) {
	stored, err := s.profileProposals.get(id)
	if err != nil {
		return ProfileChangeProposal{}, err
	}
	out := stored.ProfileChangeProposal
	if stored.Status != profileProposalStatusPending {
		return out, nil
	}
	out.CurrentStatus = "unavailable"
	reader, freshID, _, binding, refusal := s.freshSettingsTargetFor(stored.Service, stored.InstanceID)
	if refusal != "" || freshID != stored.InstanceID || binding != stored.InstanceBinding {
		out.CurrentStatus = "stale"
		return out, nil
	}
	mutator, ok := reader.(qualityProfileMutator)
	if !ok {
		return out, nil
	}
	snapshot, err := loadProfileSettingsSnapshot(ctx, mutator, stored.ProfileID, stored.HasLanguageHash || stored.Service == "radarr")
	if err != nil {
		return out, nil
	}
	if snapshot.matches(stored.executionProposal()) {
		out.CurrentStatus = "applicable"
	} else {
		out.CurrentStatus = "stale"
	}
	return out, nil
}

// approveProfileProposal executes one parked proposal after an admin's
// in-app consent. It is the approval-queue sibling of applyProfileChange:
// the same per-instance lock, the same live reauthorization, the same
// hash-pinned drift refusals, the same verified full-object write, and the
// same durable configuration-history record — only the consent provenance
// differs (an authenticated admin REST action instead of a same-turn chat
// claim) and failures map to HTTP statuses instead of model-facing prose.
//
// Transient failures (arr unreachable, authorization refused, tool disabled)
// release the proposal back to pending; only positive drift proof or an
// actually attempted write finishes it terminally.
func (s *ToolServer) approveProfileProposal(ctx context.Context, id int64, callCtx CallContext) (ProfileChangeProposal, error) {
	if s.profileProposals == nil {
		return ProfileChangeProposal{}, fmt.Errorf("profile change proposals are unavailable")
	}
	stored, err := s.profileProposals.get(id)
	if err != nil {
		return ProfileChangeProposal{}, err
	}
	if stored.Status != profileProposalStatusPending {
		return ProfileChangeProposal{}, errProfileProposalNotPending
	}

	unlock, err := s.lockArrSettingsMutation(ctx, stored.Service, stored.InstanceID)
	if err != nil {
		return ProfileChangeProposal{}, err
	}
	defer unlock()

	// Claim under the instance lock: park and approval serialize on it, so a
	// released claim can never race a superseding park into two pending rows.
	claimed, err := s.profileProposals.claimExecuting(id)
	if err != nil {
		return ProfileChangeProposal{}, err
	}
	if !claimed {
		return ProfileChangeProposal{}, errProfileProposalNotPending
	}
	release := func() { s.profileProposals.release(id) }

	callCtx, err = s.authorizeCall(ctx, callCtx)
	if err != nil {
		release()
		return ProfileChangeProposal{}, err
	}
	if !auth.HasPermission(callCtx.Role, auth.PermissionInstancesManage) {
		release()
		return ProfileChangeProposal{}, ErrToolAuthorization
	}
	// The write gate is apply_profile_change: disabling it means "no
	// AI-originated profile writes", and an admin approval executes exactly
	// such a write.
	if !s.IsToolEnabled("apply_profile_change") {
		release()
		return ProfileChangeProposal{}, errProfileProposalToolDisabled
	}

	proposal := stored.executionProposal()
	// Best-effort like every other terminal bookkeeping write: if the
	// transition itself fails the row stays executing and the sweep ages it
	// out with a pointer at configuration history. Every terminal transition
	// broadcasts the drained pending count so other admins' badges follow.
	terminate := func(text string) {
		_ = s.profileProposals.markFailed(id, callCtx.UserID, text)
		s.notifyProfileProposalDecided(id)
	}

	reader, freshID, _, binding, refusal := s.freshSettingsTargetFor(proposal.Service, proposal.InstanceID)
	if refusal != "" || freshID != proposal.InstanceID || binding != proposal.InstanceBinding {
		terminate("The arr instance's identity or connection changed after this was proposed. Nothing was written.")
		return ProfileChangeProposal{}, errProfileProposalStale
	}
	mutator, ok := reader.(qualityProfileMutator)
	if !ok {
		release()
		return ProfileChangeProposal{}, errProfileProposalUnavailable
	}
	snapshot, err := loadProfileSettingsSnapshot(ctx, mutator, proposal.ProfileID, proposal.HasLanguageHash || proposal.Service == "radarr")
	if err != nil {
		release()
		return ProfileChangeProposal{}, fmt.Errorf("%w: %s", errProfileProposalUnavailable, secrets.RedactText(err.Error()))
	}
	if !snapshot.matches(proposal) {
		terminate("The profile, custom formats, or language catalog changed after this was proposed. Nothing was written.")
		return ProfileChangeProposal{}, errProfileProposalStale
	}
	body, diff, err := mutateProfileWithPlan(proposal.Service, snapshot.ProfileRaw, snapshot.CustomFormats, proposal.Plan)
	if err != nil {
		terminate("The proposed change no longer applies to the live profile. Nothing was written.")
		return ProfileChangeProposal{}, errProfileProposalStale
	}
	if err := verifyPreviewedProfileBody(proposal, body, diff); err != nil {
		terminate("The live profile no longer produces the proposed result. Nothing was written.")
		return ProfileChangeProposal{}, errProfileProposalStale
	}
	fields, err := profileSettingFieldChanges(snapshot.ProfileRaw, body, snapshot.CustomFormats, proposal.Plan)
	if err != nil {
		terminate("The proposed change could not be projected for history. Nothing was written.")
		return ProfileChangeProposal{}, errProfileProposalStale
	}
	dependencyHash := profileDependencyHash(snapshot)
	instanceName := s.arrInstanceName(proposal.Service, proposal.InstanceID)
	if instanceName == "" {
		release()
		return ProfileChangeProposal{}, errProfileProposalUnavailable
	}
	var historyChange storedSettingChange

	beforeWrite := func(ctx context.Context) error {
		var guardErr error
		callCtx, guardErr = s.authorizeCall(ctx, callCtx)
		if guardErr != nil {
			return guardErr
		}
		if !auth.HasPermission(callCtx.Role, auth.PermissionInstancesManage) {
			return ErrToolAuthorization
		}
		if !s.IsToolEnabled("apply_profile_change") {
			return errSettingsToolDisabled
		}
		freshReader, currentID, _, currentBinding, currentRefusal := s.freshSettingsTargetFor(proposal.Service, proposal.InstanceID)
		if currentRefusal != "" || currentID != proposal.InstanceID || currentBinding != proposal.InstanceBinding {
			return errProfileTargetChanged
		}
		freshMutator, ok := freshReader.(qualityProfileMutator)
		if !ok {
			return errProfileTargetChanged
		}
		latest, err := loadProfileSettingsSnapshot(ctx, freshMutator, proposal.ProfileID, proposal.HasLanguageHash || proposal.Service == "radarr")
		if err != nil {
			return err
		}
		if !latest.matches(proposal) {
			return errProfilePreviewStale
		}
		if profileDependencyHash(latest) != dependencyHash {
			return errProfilePreviewStale
		}
		latestBody, latestDiff, err := mutateProfileWithPlan(proposal.Service, latest.ProfileRaw, latest.CustomFormats, proposal.Plan)
		if err != nil {
			return err
		}
		if err := verifyPreviewedProfileBody(proposal, latestBody, latestDiff); err != nil {
			return err
		}
		// The actor is the proposer: configuration history answers "what
		// changed my arr and through what channel" — the external agent's
		// client did, with consent recorded on the proposal row (decided_by).
		historyChange, err = s.settingsChanges.create(newSettingChange{
			ActorUserID: stored.ProposedBy, ActorDeviceID: stored.ProposerDeviceID,
			Source: "external_mcp", ServiceType: proposal.Service,
			InstanceID: proposal.InstanceID, InstanceName: instanceName,
			ResourceType: "quality_profile", ResourceID: strconv.Itoa(proposal.ProfileID),
			ResourceName: proposal.ProfileName, Operation: "update",
			Summary: settingChangeSummary("quality_profile", "update", proposal.ProfileName),
			Changes: fields, BeforeRaw: latest.ProfileRaw, AfterRaw: latestBody,
			BeforeHash: latest.ProfileHash, AfterHash: proposal.DesiredProfileHash,
			DependencyHash: dependencyHash, InstanceBinding: proposal.InstanceBinding,
		})
		return err
	}

	if err := UpdateQualityProfileHelper(ctx, mutator, proposal.ProfileID, body, beforeWrite); err != nil {
		if historyChange.ID != 0 {
			status := settingChangeStatusFailed
			var partial *PartialMutationError
			if errors.As(err, &partial) {
				status = settingChangeStatusOutcomeUnknown
			}
			_, _ = s.settingsChanges.finish(historyChange.ID, status, secrets.RedactText(err.Error()))
		}
		var notStarted *MutationNotStartedError
		startedNothing := errors.As(err, &notStarted)
		switch {
		case errors.Is(err, errSettingsToolDisabled):
			release()
			return ProfileChangeProposal{}, errProfileProposalToolDisabled
		case errors.Is(err, ErrToolAuthorization):
			release()
			return ProfileChangeProposal{}, ErrToolAuthorization
		case errors.Is(err, errProfileTargetChanged), errors.Is(err, errProfilePreviewStale):
			terminate("The live settings drifted immediately before the write. Nothing was written.")
			return ProfileChangeProposal{}, errProfileProposalStale
		case startedNothing:
			// The guard failed before any write was attempted for a reason
			// other than drift (typically the arr became unreachable): the
			// proposal itself is not disproven.
			release()
			return ProfileChangeProposal{}, fmt.Errorf("%w: %s", errProfileProposalUnavailable, secrets.RedactText(err.Error()))
		default:
			// The write was attempted: its outcome (failed or unknown) is
			// recorded in configuration history, and re-approving blind
			// would risk repeating a write with an unknowable result.
			terminate("The write did not complete cleanly: " + secrets.RedactText(err.Error()) + " Configuration history holds the outcome record.")
			return ProfileChangeProposal{}, fmt.Errorf("the profile write failed: %s", secrets.RedactText(err.Error()))
		}
	}

	if _, err := s.settingsChanges.finish(historyChange.ID, settingChangeStatusApplied, ""); err != nil {
		terminate(fmt.Sprintf("Applied and verified, but finalizing history record #%d failed.", historyChange.ID))
		return ProfileChangeProposal{}, fmt.Errorf("the profile update applied and verified, but finalizing its history record failed: %s", secrets.RedactText(err.Error()))
	}
	if err := s.profileProposals.markApplied(id, callCtx.UserID, historyChange.ID); err != nil {
		return ProfileChangeProposal{}, fmt.Errorf("the profile update applied and was recorded as change #%d, but the proposal record could not be finished: %s", historyChange.ID, secrets.RedactText(err.Error()))
	}
	s.notifyProfileProposalDecided(id)
	updated, err := s.profileProposals.get(id)
	if err != nil {
		return ProfileChangeProposal{}, fmt.Errorf("the profile update applied; reloading the proposal failed: %s", secrets.RedactText(err.Error()))
	}
	return updated.ProfileChangeProposal, nil
}
