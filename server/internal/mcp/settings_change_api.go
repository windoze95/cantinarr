package mcp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	errSettingChangeConflict        = errors.New("the live settings no longer match this change")
	errSettingChangeUnavailable     = errors.New("the live settings are unavailable")
	errSettingChangeNotRestorable   = errors.New("this settings change cannot be restored")
	errSettingChangeAlreadyRestored = errors.New("this settings change already has a restore")
)

// SettingsChangeHandler exposes the admin-only external settings ledger.
// Raw snapshots and instance fingerprints stay inside ToolServer methods.
type SettingsChangeHandler struct {
	server *ToolServer
}

func NewSettingsChangeHandler(server *ToolServer) *SettingsChangeHandler {
	return &SettingsChangeHandler{server: server}
}

func (h *SettingsChangeHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	beforeID, _ := strconv.ParseInt(r.URL.Query().Get("before_id"), 10, 64)
	changes, err := h.server.settingsChanges.list(limit, beforeID)
	if err != nil {
		writeSettingsChangeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load change history"})
		return
	}
	writeSettingsChangeJSON(w, http.StatusOK, map[string]any{"changes": changes})
}

func (h *SettingsChangeHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeSettingsChangeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid change id"})
		return
	}
	change, err := h.server.settingChangeDetail(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeSettingsChangeJSON(w, http.StatusNotFound, map[string]string{"error": "change not found"})
		return
	}
	if err != nil {
		writeSettingsChangeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load change"})
		return
	}
	writeSettingsChangeJSON(w, http.StatusOK, change)
}

func (h *SettingsChangeHandler) Revert(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeSettingsChangeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeSettingsChangeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid change id"})
		return
	}
	change, err := h.server.revertSettingChange(r.Context(), id, CallContext{
		UserID: claims.UserID, Role: claims.Role, DeviceID: claims.DeviceID,
		Reauthorize: true, Origin: OriginInteractiveChat,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeSettingsChangeJSON(w, http.StatusNotFound, map[string]string{"error": "change not found"})
	case errors.Is(err, errSettingChangeConflict):
		writeSettingsChangeJSON(w, http.StatusConflict, map[string]string{"error": "Current settings changed after this history entry. Refresh and compare before restoring."})
	case errors.Is(err, errSettingChangeAlreadyRestored):
		writeSettingsChangeJSON(w, http.StatusConflict, map[string]string{"error": "Previous settings were already restored or a restore requires review."})
	case errors.Is(err, errSettingChangeNotRestorable):
		writeSettingsChangeJSON(w, http.StatusConflict, map[string]string{"error": "This history entry cannot be restored."})
	case errors.Is(err, errSettingChangeUnavailable):
		writeSettingsChangeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Current settings could not be verified. Nothing was restored."})
	case errors.Is(err, ErrToolAuthorization):
		writeSettingsChangeJSON(w, http.StatusForbidden, map[string]string{"error": "permission denied"})
	case err != nil:
		writeSettingsChangeJSON(w, http.StatusInternalServerError, map[string]string{"error": secrets.RedactText(err.Error())})
	default:
		writeSettingsChangeJSON(w, http.StatusOK, change)
	}
}

func writeSettingsChangeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *ToolServer) settingChangeDetail(ctx context.Context, id int64) (ExternalSettingChange, error) {
	stored, err := s.settingsChanges.get(id)
	if err != nil {
		return ExternalSettingChange{}, err
	}
	detail := stored.ExternalSettingChange
	detail.CanRevert = false
	if stored.ResourceType == "custom_format" {
		return s.customFormatSettingChangeDetail(ctx, stored, detail)
	}
	if stored.ResourceType != "quality_profile" {
		detail.CurrentStatus = "unavailable"
		detail.CurrentError = "Live comparison is not available for this resource type yet."
		return detail, nil
	}
	canRevertRecord := restorableSettingChangeRecord(stored)
	alreadyRestored := false
	if canRevertRecord {
		alreadyRestored, err = s.settingsChanges.hasBlockingRevert(stored.ID)
		if err != nil {
			return ExternalSettingChange{}, err
		}
	}
	profileID, err := strconv.Atoi(stored.ResourceID)
	if err != nil || profileID <= 0 {
		return ExternalSettingChange{}, fmt.Errorf("stored settings change has an invalid profile id")
	}
	reader, freshID, _, binding, refusal := s.freshSettingsTargetFor(stored.ServiceType, stored.InstanceID)
	if refusal != "" || freshID != stored.InstanceID || binding != stored.InstanceBinding {
		detail.CurrentStatus = "unavailable"
		detail.CurrentError = "The instance connection changed or is unavailable, so Cantinarr did not contact it."
		return detail, nil
	}
	mutator, ok := reader.(qualityProfileMutator)
	if !ok {
		detail.CurrentStatus = "unavailable"
		detail.CurrentError = "This server build cannot read the selected quality profile."
		return detail, nil
	}
	snapshot, err := loadProfileSettingsSnapshot(ctx, mutator, profileID, false)
	if err != nil {
		detail.CurrentStatus = "unavailable"
		detail.CurrentError = "Cantinarr could not read the current profile."
		return detail, nil
	}
	values, err := profileCurrentFieldValues(snapshot.ProfileRaw, snapshot.CustomFormats, stored.Changes)
	if err != nil {
		detail.CurrentStatus = "unavailable"
		detail.CurrentError = "Cantinarr could not compare the current profile safely."
		return detail, nil
	}
	detail.Changes = withCurrentSettingValues(stored.Changes, values, nil)
	profileMatches := hashString(snapshot.ProfileHash) == stored.AfterHash
	dependenciesMatch := false
	if profileMatches {
		dependenciesMatch, err = profileDependencyMatchesStored(ctx, mutator, stored.ServiceType, &snapshot, stored.DependencyHash)
		if err != nil {
			detail.CurrentStatus = "unavailable"
			detail.CurrentError = "Cantinarr could not verify the current profile dependencies."
			return detail, nil
		}
	}
	if profileMatches && dependenciesMatch {
		detail.CurrentStatus = "matches_applied"
		detail.CanRevert = canRevertRecord && !alreadyRestored
	} else {
		detail.CurrentStatus = "different"
		if allCurrentFieldsMatchApplied(detail.Changes) {
			detail.CurrentError = "Other settings or dependencies on this profile changed after this entry."
		}
	}
	if alreadyRestored {
		detail.CurrentError = "This change already has a restore record, so it cannot be restored again."
	}
	return detail, nil
}

func restorableSettingChangeRecord(stored storedSettingChange) bool {
	return stored.Status == settingChangeStatusApplied &&
		stored.ResourceType == "quality_profile" &&
		stored.Operation == "update" && stored.ParentID == nil
}

func (s *ToolServer) customFormatSettingChangeDetail(ctx context.Context, stored storedSettingChange, detail ExternalSettingChange) (ExternalSettingChange, error) {
	formatID, err := strconv.Atoi(stored.ResourceID)
	lookupByName := err != nil && strings.HasPrefix(stored.ResourceID, "name:")
	if (err != nil || formatID <= 0) && !lookupByName {
		detail.CurrentStatus = "unavailable"
		detail.CurrentError = "This history entry has no verified custom-format identity."
		return detail, nil
	}
	afterHash, err := decodeStoredSettingHash(stored.AfterHash)
	if err != nil {
		return ExternalSettingChange{}, err
	}
	calculatedAfterHash, err := canonicalJSONHash(stored.AfterRaw)
	if err != nil || calculatedAfterHash != afterHash {
		return ExternalSettingChange{}, fmt.Errorf("stored custom-format snapshot failed integrity validation")
	}
	reader, freshID, _, binding, refusal := s.freshSettingsTargetFor(stored.ServiceType, stored.InstanceID)
	if refusal != "" || freshID != stored.InstanceID || binding != stored.InstanceBinding {
		detail.CurrentStatus = "unavailable"
		detail.CurrentError = "The instance connection changed or is unavailable, so Cantinarr did not contact it."
		return detail, nil
	}
	mutator, ok := reader.(CustomFormatMutator)
	if !ok {
		detail.CurrentStatus = "unavailable"
		detail.CurrentError = "This server build cannot read the selected custom format."
		return detail, nil
	}
	formats, err := mutator.GetCustomFormatsRawContext(ctx)
	if err != nil {
		detail.CurrentStatus = "unavailable"
		detail.CurrentError = "Cantinarr could not read the current custom formats."
		return detail, nil
	}
	var (
		currentRaw json.RawMessage
		exists     bool
	)
	if lookupByName {
		matched, matchErr := findCustomFormatByName(formats, stored.ResourceName)
		err = matchErr
		if matched != nil {
			currentRaw = append(json.RawMessage(nil), matched.raw...)
			exists = true
		}
	} else {
		currentRaw, exists, err = uniqueRawCustomFormatByID(formats, formatID)
	}
	if err != nil {
		detail.CurrentStatus = "unavailable"
		detail.CurrentError = "Cantinarr could not identify the current custom format safely."
		return detail, nil
	}
	plannedAfter := stored.Status != settingChangeStatusApplied
	values, states, err := customFormatCurrentFieldValues(
		currentRaw, exists, stored.Changes, stored.BeforeRaw, stored.AfterRaw, plannedAfter,
	)
	if err != nil {
		detail.CurrentStatus = "unavailable"
		detail.CurrentError = "Cantinarr could not compare the current custom format safely."
		return detail, nil
	}
	detail.Changes = withCurrentSettingValues(stored.Changes, values, states)
	detail.CurrentStatus = "different"
	if exists && ((!plannedAfter && customFormatObjectsEquivalent(currentRaw, stored.AfterRaw)) ||
		(plannedAfter && customFormatObjectContainsPlan(currentRaw, stored.AfterRaw))) {
		detail.CurrentStatus = "matches_applied"
	} else if allCurrentFieldsMatchApplied(detail.Changes) {
		detail.CurrentError = "Other fields on this custom format changed after this entry."
	}
	return detail, nil
}

func allCurrentFieldsMatchApplied(fields []SettingFieldChange) bool {
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		if field.CurrentState != "matches_applied" {
			return false
		}
	}
	return true
}

func uniqueRawCustomFormatByID(raws []json.RawMessage, formatID int) (json.RawMessage, bool, error) {
	var matched json.RawMessage
	count := 0
	for _, raw := range raws {
		head, err := decodeCustomFormatHead(raw)
		if err != nil {
			return nil, false, fmt.Errorf("a custom format has an unreadable id or name")
		}
		if head.ID == formatID {
			count++
			matched = append(json.RawMessage(nil), raw...)
		}
	}
	if count > 1 {
		return nil, false, fmt.Errorf("custom format id %d is ambiguous", formatID)
	}
	return matched, count == 1, nil
}

func (s *ToolServer) revertSettingChange(ctx context.Context, id int64, callCtx CallContext) (ExternalSettingChange, error) {
	stored, err := s.settingsChanges.get(id)
	if err != nil {
		return ExternalSettingChange{}, err
	}
	if !restorableSettingChangeRecord(stored) {
		return ExternalSettingChange{}, errSettingChangeNotRestorable
	}
	profileID, err := strconv.Atoi(stored.ResourceID)
	if err != nil || profileID <= 0 {
		return ExternalSettingChange{}, errSettingChangeConflict
	}
	beforeHash, err := decodeStoredSettingHash(stored.BeforeHash)
	if err != nil {
		return ExternalSettingChange{}, err
	}
	afterHash, err := decodeStoredSettingHash(stored.AfterHash)
	if err != nil {
		return ExternalSettingChange{}, err
	}

	unlock, err := s.lockArrSettingsMutation(ctx, stored.ServiceType, stored.InstanceID)
	if err != nil {
		return ExternalSettingChange{}, err
	}
	defer unlock()
	callCtx, err = s.authorizeCall(ctx, callCtx)
	if err != nil || !auth.HasPermission(callCtx.Role, auth.PermissionInstancesManage) {
		return ExternalSettingChange{}, ErrToolAuthorization
	}
	alreadyRestored, err := s.settingsChanges.hasBlockingRevert(stored.ID)
	if err != nil {
		return ExternalSettingChange{}, err
	}
	if alreadyRestored {
		return ExternalSettingChange{}, errSettingChangeAlreadyRestored
	}
	reader, freshID, _, binding, refusal := s.freshSettingsTargetFor(stored.ServiceType, stored.InstanceID)
	if refusal != "" || freshID != stored.InstanceID || binding != stored.InstanceBinding {
		return ExternalSettingChange{}, errSettingChangeUnavailable
	}
	mutator, ok := reader.(qualityProfileMutator)
	if !ok {
		return ExternalSettingChange{}, errSettingChangeUnavailable
	}
	current, err := loadProfileSettingsSnapshot(ctx, mutator, profileID, false)
	if err != nil {
		return ExternalSettingChange{}, errSettingChangeUnavailable
	}
	if current.ProfileHash != afterHash {
		return ExternalSettingChange{}, errSettingChangeConflict
	}
	dependenciesMatch, err := profileDependencyMatchesStored(ctx, mutator, stored.ServiceType, &current, stored.DependencyHash)
	if err != nil {
		return ExternalSettingChange{}, errSettingChangeUnavailable
	}
	if !dependenciesMatch {
		return ExternalSettingChange{}, errSettingChangeConflict
	}
	if calculated, hashErr := canonicalProfileJSONHash(stored.BeforeRaw); hashErr != nil || calculated != beforeHash {
		return ExternalSettingChange{}, fmt.Errorf("stored rollback snapshot failed integrity validation")
	}
	beforeProfile, _, err := decodeMutableProfile(stored.BeforeRaw)
	if err != nil || beforeProfile.ID != profileID {
		return ExternalSettingChange{}, fmt.Errorf("stored rollback snapshot targets a different profile")
	}
	reverseFields := make([]SettingFieldChange, len(stored.Changes))
	for i, field := range stored.Changes {
		field.Before, field.After = field.After, field.Before
		field.Current = nil
		field.CurrentState = ""
		reverseFields[i] = field
	}

	var historyChange storedSettingChange
	beforeWrite := func(ctx context.Context) error {
		var guardErr error
		callCtx, guardErr = s.authorizeCall(ctx, callCtx)
		if guardErr != nil || !auth.HasPermission(callCtx.Role, auth.PermissionInstancesManage) {
			return ErrToolAuthorization
		}
		alreadyRestored, guardErr := s.settingsChanges.hasBlockingRevert(stored.ID)
		if guardErr != nil {
			return guardErr
		}
		if alreadyRestored {
			return errSettingChangeAlreadyRestored
		}
		freshReader, currentID, _, currentBinding, currentRefusal := s.freshSettingsTargetFor(stored.ServiceType, stored.InstanceID)
		if currentRefusal != "" || currentID != stored.InstanceID || currentBinding != stored.InstanceBinding {
			return errSettingChangeUnavailable
		}
		freshMutator, ok := freshReader.(qualityProfileMutator)
		if !ok {
			return errSettingChangeUnavailable
		}
		latest, loadErr := loadProfileSettingsSnapshot(ctx, freshMutator, profileID, false)
		if loadErr != nil {
			return errSettingChangeUnavailable
		}
		if latest.ProfileHash != afterHash {
			return errSettingChangeConflict
		}
		dependenciesMatch, loadErr := profileDependencyMatchesStored(ctx, freshMutator, stored.ServiceType, &latest, stored.DependencyHash)
		if loadErr != nil {
			return errSettingChangeUnavailable
		}
		if !dependenciesMatch {
			return errSettingChangeConflict
		}
		historyChange, guardErr = s.settingsChanges.create(newSettingChange{
			ParentID: &stored.ID, ActorUserID: callCtx.UserID, ActorDeviceID: callCtx.DeviceID,
			Source: "admin_revert", ServiceType: stored.ServiceType,
			InstanceID: stored.InstanceID, InstanceName: stored.InstanceName,
			ResourceType: stored.ResourceType, ResourceID: stored.ResourceID,
			ResourceName: stored.ResourceName, Operation: "revert",
			Summary: settingChangeSummary("quality_profile", "revert", stored.ResourceName),
			Changes: reverseFields, BeforeRaw: latest.ProfileRaw, AfterRaw: stored.BeforeRaw,
			BeforeHash: latest.ProfileHash, AfterHash: beforeHash,
			DependencyHash: profileDependencyHash(latest), InstanceBinding: stored.InstanceBinding,
		})
		return guardErr
	}

	if err := UpdateQualityProfileHelper(ctx, mutator, profileID, stored.BeforeRaw, beforeWrite); err != nil {
		if historyChange.ID != 0 {
			status := settingChangeStatusFailed
			var partial *PartialMutationError
			if errors.As(err, &partial) {
				status = settingChangeStatusOutcomeUnknown
			}
			_, _ = s.settingsChanges.finish(historyChange.ID, status, secrets.RedactText(err.Error()))
		}
		if errors.Is(err, errSettingChangeConflict) || errors.Is(err, errSettingChangeUnavailable) ||
			errors.Is(err, errSettingChangeAlreadyRestored) || errors.Is(err, ErrToolAuthorization) {
			return ExternalSettingChange{}, err
		}
		return ExternalSettingChange{}, secrets.RedactError(err)
	}
	historyChange, err = s.settingsChanges.finish(historyChange.ID, settingChangeStatusApplied, "")
	if err != nil {
		return ExternalSettingChange{}, &PartialMutationError{Completed: "the previous quality profile settings were restored and verified", Pending: "finalizing the durable change-history record", Err: err}
	}
	result := historyChange.ExternalSettingChange
	result.CurrentStatus = "matches_applied"
	result.CanRevert = false
	values := make(map[string]string, len(result.Changes))
	for _, field := range result.Changes {
		values[field.Key] = field.After
	}
	result.Changes = withCurrentSettingValues(result.Changes, values, nil)
	return result, nil
}

func decodeStoredSettingHash(value string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(result) {
		return result, fmt.Errorf("stored settings change hash is invalid")
	}
	copy(result[:], decoded)
	return result, nil
}
