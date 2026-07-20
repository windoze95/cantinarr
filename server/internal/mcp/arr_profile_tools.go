package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const (
	maxProfilePreviewDiffBytes      = 20 << 10
	maxProfileSettingsSnapshotBytes = 4 << 20
)

var (
	errProfilePreviewStale  = errors.New("the quality profile preview is stale")
	errProfileTargetChanged = errors.New("the selected arr instance changed since preview")
)

var arrProfileToolDefinitions = []Tool{
	{
		Name:          "preview_profile_change",
		Permission:    auth.PermissionInstancesManage,
		InAppChatOnly: true,
		Description:   "Prepare and show a narrow full-object quality-profile update for one Radarr/Sonarr/Chaptarr instance. Returns a one-use reference that apply_profile_change may consume only in this same authenticated chat turn; it never writes. Use only after an explicit admin request. In-app chat only, admin only",
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"service": map[string]interface{}{
					"type": "string", "enum": []string{"radarr", "sonarr", "chaptarr"},
				},
				"instance_id": map[string]interface{}{
					"type": "string", "description": "Instance ID from list_arr_instances (default: the service's current default instance)",
				},
				"profile_id": map[string]interface{}{
					"type": "integer", "minimum": 1,
				},
				"changes": profileChangesInputSchema(),
			},
			"required": []string{"service", "profile_id", "changes"},
		},
	},
	{
		Name:          "apply_profile_change",
		Permission:    auth.PermissionInstancesManage,
		InAppChatOnly: true,
		Description:   "Apply one same-turn previewed quality-profile update after an explicit admin request. Reauthorizes, refuses stale settings, verifies the complete result, and records a durable before/after history entry for safe review and revert. In-app chat only, admin only",
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"change_reference": map[string]interface{}{
					"type":      "string",
					"minLength": len(profileChangeReferencePrefix) + 43,
					"maxLength": len(profileChangeReferencePrefix) + 43,
				},
			},
			"required": []string{"change_reference"},
		},
	},
}

func profileChangesInputSchema() map[string]interface{} {
	int32Schema := func() map[string]interface{} {
		return map[string]interface{}{"type": "integer", "minimum": -2147483648, "maximum": 2147483647}
	}
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"minProperties":        1,
		"properties": map[string]interface{}{
			"upgrade_allowed":          map[string]interface{}{"type": "boolean"},
			"quality_cutoff_id":        map[string]interface{}{"type": "integer", "minimum": 0},
			"min_format_score":         int32Schema(),
			"cutoff_format_score":      int32Schema(),
			"min_upgrade_format_score": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 2147483647},
			"custom_format_scores": map[string]interface{}{
				"type": "array", "maxItems": maxProfileFormatScoreChanges,
				"items": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]interface{}{
						"format_name": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": maxCustomFormatNameBytes},
						"score":       int32Schema(),
					},
					"required": []string{"format_name", "score"},
				},
			},
			"language_name": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": maxCustomFormatNameBytes},
		},
	}
}

type previewProfileChangeParams struct {
	Service    string              `json:"service"`
	InstanceID string              `json:"instance_id"`
	ProfileID  int                 `json:"profile_id"`
	Changes    profileChangesInput `json:"changes"`
}

type applyProfileChangeParams struct {
	ChangeReference string `json:"change_reference"`
}

type profileSettingsSnapshot struct {
	ProfileRaw       json.RawMessage
	ProfileName      string
	CustomFormats    []json.RawMessage
	Languages        []json.RawMessage
	HasLanguages     bool
	ProfileHash      [32]byte
	CustomFormatHash [32]byte
	LanguageHash     [32]byte
}

func (s *ToolServer) previewProfileChange(ctx context.Context, input json.RawMessage, callCtx CallContext) (*ToolResult, error) {
	var params previewProfileChangeParams
	if err := decodeStrictToolInput(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	if callCtx.Origin != OriginInteractiveChat || callCtx.InteractiveTurnID == "" || strings.TrimSpace(callCtx.TrustedUserText) == "" {
		return &ToolResult{Text: "Quality-profile changes are available only in Cantinarr's authenticated in-app AI chat."}, nil
	}
	if params.ProfileID <= 0 {
		return nil, fmt.Errorf("profile_id must be positive")
	}

	_, resolvedID, _, refusal := s.settingsTargetFor(params.Service, params.InstanceID)
	if refusal != "" {
		return &ToolResult{Text: refusal}, nil
	}

	var (
		mutator qualityProfileMutator
		label   string
		binding instance.ArrSettingsFingerprint
		unlock  func()
		err     error
	)
	for attempts := 0; attempts < 3; attempts++ {
		unlock, err = s.lockArrSettingsMutation(ctx, params.Service, resolvedID)
		if err != nil {
			return nil, err
		}
		callCtx, err = s.authorizeCall(ctx, callCtx)
		if err != nil {
			unlock()
			return nil, err
		}
		if !s.IsToolEnabled("preview_profile_change") {
			unlock()
			return &ToolResult{Text: "This tool is disabled by the administrator."}, nil
		}
		if !auth.HasPermission(callCtx.Role, auth.PermissionInstancesManage) {
			unlock()
			return nil, ErrToolAuthorization
		}

		reader, freshID, freshLabel, freshBinding, freshRefusal := s.freshSettingsTargetFor(params.Service, params.InstanceID)
		if freshRefusal != "" {
			unlock()
			return &ToolResult{Text: freshRefusal}, nil
		}
		if freshID != resolvedID {
			unlock()
			resolvedID = freshID
			continue
		}
		var ok bool
		mutator, ok = reader.(qualityProfileMutator)
		if !ok {
			unlock()
			return &ToolResult{Text: arrServiceLabel(params.Service) + " quality-profile writes are not available on this server build."}, nil
		}
		label, binding = freshLabel, freshBinding
		break
	}
	if unlock == nil || mutator == nil {
		return &ToolResult{Text: "The default instance changed repeatedly while this preview was queued. Retry with an explicit instance_id."}, nil
	}
	defer unlock()

	snapshot, err := loadProfileSettingsSnapshot(ctx, mutator, params.ProfileID, false)
	if err != nil {
		if isCustomFormatsNotFound(err) {
			return &ToolResult{Text: customFormatsUnavailableText(params.Service, label)}, nil
		}
		return nil, err
	}
	needLanguages, err := profileChangeNeedsLanguageCatalog(params.Service, snapshot.CustomFormats, params.Changes)
	if err != nil {
		return nil, err
	}
	if needLanguages {
		if err := loadProfileSnapshotLanguages(ctx, mutator, &snapshot); err != nil {
			return nil, err
		}
	}
	plan, desiredBody, diff, profileName, err := resolveProfileChangePlan(params.Service, snapshot.ProfileRaw, snapshot.CustomFormats, snapshot.Languages, params.Changes)
	if err != nil {
		return nil, err
	}
	desiredHash, err := canonicalProfileJSONHash(desiredBody)
	if err != nil {
		return nil, fmt.Errorf("hash proposed quality profile: %w", err)
	}
	if profilePreviewDiffSize(diff) > maxProfilePreviewDiffBytes {
		return nil, fmt.Errorf("the complete preview is too large; split this change into smaller previews")
	}
	if s.profileChanges == nil {
		return nil, fmt.Errorf("profile change gate is unavailable")
	}
	proposal, err := s.profileChanges.save(profileChangeProposal{
		UserID:             callCtx.UserID,
		DeviceID:           callCtx.DeviceID,
		IssuedTurnID:       callCtx.InteractiveTurnID,
		Service:            params.Service,
		InstanceID:         resolvedID,
		InstanceBinding:    binding,
		ProfileID:          params.ProfileID,
		ProfileName:        profileName,
		ProfileHash:        snapshot.ProfileHash,
		DesiredProfileHash: desiredHash,
		CustomFormatHash:   snapshot.CustomFormatHash,
		LanguageHash:       snapshot.LanguageHash,
		HasLanguageHash:    needLanguages,
		Plan:               plan,
		Diff:               diff,
	})
	if err != nil {
		return nil, err
	}
	return &ToolResult{Text: renderProfileChangePreview(proposal, label)}, nil
}

func (s *ToolServer) applyProfileChange(ctx context.Context, input json.RawMessage, callCtx CallContext) (*ToolResult, error) {
	var params applyProfileChangeParams
	if err := decodeStrictToolInput(input, &params); err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	reference := params.ChangeReference
	if callCtx.Origin != OriginInteractiveChat || callCtx.InteractiveTurnID == "" || strings.TrimSpace(callCtx.TrustedUserText) == "" {
		return &ToolResult{Text: "Quality-profile changes are available only in Cantinarr's authenticated in-app AI chat."}, nil
	}
	if s.profileChanges == nil {
		return &ToolResult{Text: "No valid profile change is pending. Run preview_profile_change again."}, nil
	}
	proposal, ok := s.profileChanges.claimAutonomous(reference, callCtx.UserID, callCtx.DeviceID, callCtx.InteractiveTurnID, callCtx.Origin)
	if !ok {
		return &ToolResult{Text: "No valid same-turn profile change is pending for this user and device. It may be expired, superseded, already used, or from another chat turn. Run preview_profile_change again in response to the admin's current request."}, nil
	}

	unlock, err := s.lockArrSettingsMutation(ctx, proposal.Service, proposal.InstanceID)
	if err != nil {
		return nil, consumedProfileReferenceError(err)
	}
	defer unlock()

	callCtx, err = s.authorizeCall(ctx, callCtx)
	if err != nil {
		return nil, consumedProfileReferenceError(err)
	}
	if !s.IsToolEnabled("apply_profile_change") {
		return &ToolResult{Text: "The apply tool was disabled after confirmation. No write was attempted; the one-use reference was consumed. Preview again if the tool is re-enabled."}, nil
	}
	if !auth.HasPermission(callCtx.Role, auth.PermissionInstancesManage) {
		return nil, consumedProfileReferenceError(ErrToolAuthorization)
	}
	reader, freshID, label, binding, refusal := s.freshSettingsTargetFor(proposal.Service, proposal.InstanceID)
	if refusal != "" || freshID != proposal.InstanceID || binding != proposal.InstanceBinding {
		return &ToolResult{Text: "The selected arr instance changed since preview. No write was attempted; the one-use reference was consumed. Preview the current settings again."}, nil
	}
	mutator, ok := reader.(qualityProfileMutator)
	if !ok {
		return &ToolResult{Text: "Quality-profile writes are no longer available for the selected instance. No write was attempted; preview again after fixing the instance."}, nil
	}

	snapshot, err := loadProfileSettingsSnapshot(ctx, mutator, proposal.ProfileID, proposal.HasLanguageHash || proposal.Service == "radarr")
	if err != nil {
		return nil, consumedProfileReferenceError(err)
	}
	if !snapshot.matches(proposal) {
		return staleProfileChangeResult(), nil
	}
	body, diff, err := mutateProfileWithPlan(proposal.Service, snapshot.ProfileRaw, snapshot.CustomFormats, proposal.Plan)
	if err != nil {
		return nil, consumedProfileReferenceError(err)
	}
	if err := verifyPreviewedProfileBody(proposal, body, diff); err != nil {
		return staleProfileChangeResult(), nil
	}
	fields, err := profileSettingFieldChanges(snapshot.ProfileRaw, body, snapshot.CustomFormats, proposal.Plan)
	if err != nil {
		return nil, consumedProfileReferenceError(err)
	}
	dependencyHash := profileDependencyHash(snapshot)
	instanceName := s.arrInstanceName(proposal.Service, proposal.InstanceID)
	if instanceName == "" {
		return &ToolResult{Text: "The selected arr instance no longer has a readable identity. No write was attempted; preview again after fixing the instance."}, nil
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
		historyChange, err = s.settingsChanges.create(newSettingChange{
			ActorUserID: callCtx.UserID, ActorDeviceID: callCtx.DeviceID,
			Source: "ai_chat", ServiceType: proposal.Service,
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
		switch {
		case errors.Is(err, errSettingsToolDisabled):
			return &ToolResult{Text: "The apply tool was disabled immediately before the write. No write was attempted; the one-use reference was consumed. Preview again if it is re-enabled."}, nil
		case errors.Is(err, errProfileTargetChanged):
			return &ToolResult{Text: "The selected arr instance changed immediately before the write. No write was attempted; the one-use reference was consumed. Preview again."}, nil
		case errors.Is(err, errProfilePreviewStale):
			return staleProfileChangeResult(), nil
		default:
			return nil, consumedProfileReferenceError(err)
		}
	}

	historyChange, err = s.settingsChanges.finish(historyChange.ID, settingChangeStatusApplied, "")
	if err != nil {
		return nil, &PartialMutationError{
			Completed: "the quality profile update was applied and verified",
			Pending:   "finalizing its durable change-history record",
			Err:       err,
		}
	}

	return &ToolResult{
		Text:           fmt.Sprintf("Applied the requested change to quality profile %d (%q) on %s, verified the complete stored profile, and recorded change #%d. These settings affect future release selection for media using this profile; they do not remux files or set default playback audio/subtitle tracks.", proposal.ProfileID, proposal.ProfileName, label, historyChange.ID),
		StructuredData: historyChange.ExternalSettingChange,
	}, nil
}

func loadProfileSettingsSnapshot(ctx context.Context, mutator qualityProfileMutator, profileID int, includeLanguages bool) (profileSettingsSnapshot, error) {
	profiles, err := mutator.GetQualityProfilesRawContext(ctx)
	if err != nil {
		return profileSettingsSnapshot{}, err
	}
	profileRaw, profileName, err := uniqueRawProfileByID(profiles, profileID)
	if err != nil {
		return profileSettingsSnapshot{}, err
	}
	formats, err := mutator.GetCustomFormatsRawContext(ctx)
	if err != nil {
		return profileSettingsSnapshot{}, err
	}
	if len(profileRaw) > maxProfileSettingsSnapshotBytes || rawMessagesSize(formats) > maxProfileSettingsSnapshotBytes {
		return profileSettingsSnapshot{}, fmt.Errorf("the quality profile or custom-format collection exceeds the safe mutation size limit")
	}
	if _, _, err := decodeCustomFormatMutationInfos(formats); err != nil {
		return profileSettingsSnapshot{}, err
	}
	if _, _, err := decodeMutableProfile(profileRaw); err != nil {
		return profileSettingsSnapshot{}, err
	}
	snapshot := profileSettingsSnapshot{
		ProfileRaw:    append(json.RawMessage(nil), profileRaw...),
		ProfileName:   profileName,
		CustomFormats: cloneRawMessages(formats),
	}
	snapshot.ProfileHash, err = canonicalProfileJSONHash(profileRaw)
	if err != nil {
		return profileSettingsSnapshot{}, fmt.Errorf("hash quality profile: %w", err)
	}
	snapshot.CustomFormatHash, err = canonicalJSONCollectionHash(formats)
	if err != nil {
		return profileSettingsSnapshot{}, fmt.Errorf("hash custom formats: %w", err)
	}
	if includeLanguages {
		if err := loadProfileSnapshotLanguages(ctx, mutator, &snapshot); err != nil {
			return profileSettingsSnapshot{}, err
		}
	}
	return snapshot, nil
}

func profileChangeNeedsLanguageCatalog(service string, formats []json.RawMessage, changes profileChangesInput) (bool, error) {
	if service == "radarr" && changes.LanguageName != nil {
		return true, nil
	}
	if (service != "radarr" && service != "sonarr") || len(changes.CustomFormatScores) == 0 {
		return false, nil
	}

	requestedNames := make(map[string]struct{}, len(changes.CustomFormatScores))
	for _, change := range changes.CustomFormatScores {
		requestedNames[change.FormatName] = struct{}{}
	}
	decoded, _, err := decodeCustomFormatMutationInfos(formats)
	if err != nil {
		return false, err
	}
	for _, format := range decoded {
		if format.LanguageSpecification {
			if _, requested := requestedNames[format.Name]; requested {
				return true, nil
			}
		}
	}
	return false, nil
}

func loadProfileSnapshotLanguages(ctx context.Context, mutator qualityProfileMutator, snapshot *profileSettingsSnapshot) error {
	languageReader, ok := mutator.(arrLanguageReader)
	if !ok {
		return fmt.Errorf("the selected service cannot read a live language catalog")
	}
	languages, err := languageReader.GetLanguagesRawContext(ctx)
	if err != nil {
		return err
	}
	if rawMessagesSize(languages) > maxProfileSettingsSnapshotBytes {
		return fmt.Errorf("the language catalog exceeds the safe mutation size limit")
	}
	if _, err := resolveProfileLanguageCatalog(languages); err != nil {
		return err
	}
	languageHash, err := canonicalLanguageCollectionHash(languages)
	if err != nil {
		return fmt.Errorf("hash languages: %w", err)
	}
	snapshot.Languages = cloneRawMessages(languages)
	snapshot.HasLanguages = true
	snapshot.LanguageHash = languageHash
	return nil
}

func (snapshot profileSettingsSnapshot) matches(proposal profileChangeProposal) bool {
	return snapshot.ProfileHash == proposal.ProfileHash &&
		snapshot.CustomFormatHash == proposal.CustomFormatHash &&
		(!proposal.HasLanguageHash || snapshot.LanguageHash == proposal.LanguageHash)
}

func verifyPreviewedProfileBody(proposal profileChangeProposal, body json.RawMessage, diff []string) error {
	hash, err := canonicalProfileJSONHash(body)
	if err != nil || hash != proposal.DesiredProfileHash || !slices.Equal(diff, proposal.Diff) {
		return errProfilePreviewStale
	}
	return nil
}

func resolveProfileLanguageCatalog(raws []json.RawMessage) ([]resolvedProfileLanguage, error) {
	languages := make([]resolvedProfileLanguage, 0, len(raws))
	seenIDs := make(map[int]struct{}, len(raws))
	seenNames := make(map[string]struct{}, len(raws))
	for _, raw := range raws {
		var language arrIDName
		if err := json.Unmarshal(raw, &language); err != nil || strings.TrimSpace(language.Name) == "" || len(language.Name) > maxCustomFormatNameBytes {
			return nil, fmt.Errorf("the arr service returned a language with an unreadable id or name")
		}
		if _, duplicate := seenIDs[language.ID]; duplicate {
			return nil, fmt.Errorf("the arr service returned duplicate language id %d", language.ID)
		}
		if _, duplicate := seenNames[language.Name]; duplicate {
			return nil, fmt.Errorf("the arr service returned duplicate language name %q", language.Name)
		}
		seenIDs[language.ID] = struct{}{}
		seenNames[language.Name] = struct{}{}
		languages = append(languages, resolvedProfileLanguage{ID: language.ID, Name: language.Name})
	}
	return languages, nil
}

func decodeStrictToolInput(input json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}

func cloneRawMessages(values []json.RawMessage) []json.RawMessage {
	cloned := make([]json.RawMessage, len(values))
	for i := range values {
		cloned[i] = append(json.RawMessage(nil), values[i]...)
	}
	return cloned
}

func rawMessagesSize(values []json.RawMessage) int {
	total := 0
	for _, value := range values {
		if len(value) > maxProfileSettingsSnapshotBytes-total {
			return maxProfileSettingsSnapshotBytes + 1
		}
		total += len(value)
	}
	return total
}

func profilePreviewDiffSize(diff []string) int {
	total := 0
	for _, line := range diff {
		total += len(line) + 4
	}
	return total
}

func renderProfileChangePreview(proposal profileChangeProposal, label string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Change reference: %s\nExpires: %s\nTarget: %s quality profile %d (%q)\n\nProposed changes:\n", proposal.Reference, proposal.ExpiresAt.UTC().Format(time.RFC3339), label, proposal.ProfileID, proposal.ProfileName)
	for _, line := range proposal.Diff {
		fmt.Fprintf(&out, "- %s\n", line)
	}
	fmt.Fprintf(&out, "\nThis preview did not write anything. If and only if the admin explicitly requested this configuration change in the current message, call apply_profile_change now with the change reference. Do not ask the admin to copy or type the reference. Cantinarr will reauthorize, refuse observed profile/dependency/instance drift, verify the full stored object, and record durable before/after history. The final check is optimistic because the arr API cannot atomically compare-and-swap the following full-object PUT.")
	return out.String()
}

func staleProfileChangeResult() *ToolResult {
	return &ToolResult{Text: "The profile, custom formats, relevant language catalog, or instance connection/credentials changed since preview. No write was attempted; the one-use reference was consumed. Review the live settings and run preview_profile_change again."}
}

func consumedProfileReferenceError(err error) error {
	return fmt.Errorf("the one-use profile change reference was consumed; run preview_profile_change again before retrying: %w", err)
}
