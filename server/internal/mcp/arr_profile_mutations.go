package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

type profileFormatScoreChange struct {
	FormatName string `json:"format_name"`
	Score      int64  `json:"score"`
}

type profileChangesInput struct {
	UpgradeAllowed        *bool                      `json:"upgrade_allowed"`
	QualityCutoffID       *int                       `json:"quality_cutoff_id"`
	MinFormatScore        *int64                     `json:"min_format_score"`
	CutoffFormatScore     *int64                     `json:"cutoff_format_score"`
	MinUpgradeFormatScore *int64                     `json:"min_upgrade_format_score"`
	CustomFormatScores    []profileFormatScoreChange `json:"custom_format_scores"`
	LanguageName          *string                    `json:"language_name"`
}

type resolvedProfileFormatScore struct {
	FormatID   int
	FormatName string
	Score      int64
}

type resolvedProfileLanguage struct {
	ID   int
	Name string
}

type profileChangePlan struct {
	UpgradeAllowed        *bool
	QualityCutoffID       *int
	MinFormatScore        *int64
	CutoffFormatScore     *int64
	MinUpgradeFormatScore *int64
	CustomFormatScores    []resolvedProfileFormatScore
	Language              *resolvedProfileLanguage
}

func (p profileChangePlan) clone() profileChangePlan {
	clone := p
	clone.UpgradeAllowed = clonePtr(p.UpgradeAllowed)
	clone.QualityCutoffID = clonePtr(p.QualityCutoffID)
	clone.MinFormatScore = clonePtr(p.MinFormatScore)
	clone.CutoffFormatScore = clonePtr(p.CutoffFormatScore)
	clone.MinUpgradeFormatScore = clonePtr(p.MinUpgradeFormatScore)
	clone.CustomFormatScores = append([]resolvedProfileFormatScore(nil), p.CustomFormatScores...)
	if p.Language != nil {
		language := *p.Language
		clone.Language = &language
	}
	return clone
}

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

type qualityProfileMutator interface {
	GetQualityProfilesRawContext(context.Context) ([]json.RawMessage, error)
	GetCustomFormatsRawContext(context.Context) ([]json.RawMessage, error)
	UpdateQualityProfileRawContext(context.Context, int, json.RawMessage) (json.RawMessage, error)
}

type arrLanguageReader interface {
	GetLanguagesRawContext(context.Context) ([]json.RawMessage, error)
}

type profileMutationView struct {
	ID                    int                  `json:"id"`
	Name                  string               `json:"name"`
	UpgradeAllowed        *bool                `json:"upgradeAllowed"`
	Cutoff                *int                 `json:"cutoff"`
	MinFormatScore        *int64               `json:"minFormatScore"`
	CutoffFormatScore     *int64               `json:"cutoffFormatScore"`
	MinUpgradeFormatScore *int64               `json:"minUpgradeFormatScore"`
	Language              *arrIDName           `json:"language"`
	Items                 []arrProfileItemView `json:"items"`
	FormatItems           []arrFormatItemView  `json:"formatItems"`
}

type customFormatMutationInfo struct {
	ID                    int
	Name                  string
	LanguageSpecification bool
}

const maxProfileFormatScoreChanges = 256

// resolveProfileChangePlan converts caller-facing names into instance-local
// IDs and validates the narrow mutation surface before a proposal is stored.
func resolveProfileChangePlan(service string, profileRaw json.RawMessage, customFormats, languages []json.RawMessage, changes profileChangesInput) (profileChangePlan, json.RawMessage, []string, string, error) {
	if err := validateProfileChangesInput(service, changes); err != nil {
		return profileChangePlan{}, nil, nil, "", err
	}
	profile, _, err := decodeMutableProfile(profileRaw)
	if err != nil {
		return profileChangePlan{}, nil, nil, "", err
	}
	formats, _, err := decodeCustomFormatMutationInfos(customFormats)
	if err != nil {
		return profileChangePlan{}, nil, nil, "", err
	}

	plan := profileChangePlan{
		UpgradeAllowed:        clonePtr(changes.UpgradeAllowed),
		QualityCutoffID:       clonePtr(changes.QualityCutoffID),
		MinFormatScore:        clonePtr(changes.MinFormatScore),
		CutoffFormatScore:     clonePtr(changes.CutoffFormatScore),
		MinUpgradeFormatScore: clonePtr(changes.MinUpgradeFormatScore),
	}
	seenNames := make(map[string]struct{}, len(changes.CustomFormatScores))
	formatByName := make(map[string]customFormatMutationInfo, len(formats))
	for _, format := range formats {
		formatByName[format.Name] = format
	}
	for _, score := range changes.CustomFormatScores {
		if _, duplicate := seenNames[score.FormatName]; duplicate {
			return profileChangePlan{}, nil, nil, "", fmt.Errorf("custom_format_scores contains %q more than once", score.FormatName)
		}
		seenNames[score.FormatName] = struct{}{}
		format, ok := formatByName[score.FormatName]
		if !ok {
			return profileChangePlan{}, nil, nil, "", fmt.Errorf("no custom format is named exactly %q; available: %s", score.FormatName, customFormatNameList(formats))
		}
		plan.CustomFormatScores = append(plan.CustomFormatScores, resolvedProfileFormatScore{
			FormatID: format.ID, FormatName: format.Name, Score: score.Score,
		})
	}
	sort.Slice(plan.CustomFormatScores, func(i, j int) bool { return plan.CustomFormatScores[i].FormatID < plan.CustomFormatScores[j].FormatID })

	if changes.LanguageName != nil {
		language, err := resolveProfileLanguage(languages, *changes.LanguageName)
		if err != nil {
			return profileChangePlan{}, nil, nil, "", err
		}
		plan.Language = language
	}

	body, diff, err := mutateProfileWithPlan(service, profileRaw, customFormats, plan)
	if err != nil {
		return profileChangePlan{}, nil, nil, "", err
	}
	if len(diff) == 0 {
		return profileChangePlan{}, nil, nil, "", fmt.Errorf("the requested values already match quality profile %d (%q)", profile.ID, profile.Name)
	}
	return plan, body, diff, profile.Name, nil
}

func validateProfileChangesInput(service string, changes profileChangesInput) error {
	if service != "radarr" && service != "sonarr" && service != "chaptarr" {
		return fmt.Errorf("service must be radarr, sonarr, or chaptarr")
	}
	if changes.UpgradeAllowed == nil && changes.QualityCutoffID == nil && changes.MinFormatScore == nil &&
		changes.CutoffFormatScore == nil && changes.MinUpgradeFormatScore == nil &&
		len(changes.CustomFormatScores) == 0 && changes.LanguageName == nil {
		return fmt.Errorf("changes must contain at least one setting")
	}
	if changes.QualityCutoffID != nil && *changes.QualityCutoffID < 0 {
		return fmt.Errorf("quality_cutoff_id must be zero or positive")
	}
	for name, value := range map[string]*int64{
		"min_format_score":         changes.MinFormatScore,
		"cutoff_format_score":      changes.CutoffFormatScore,
		"min_upgrade_format_score": changes.MinUpgradeFormatScore,
	} {
		if value != nil && (*value < math.MinInt32 || *value > math.MaxInt32) {
			return fmt.Errorf("%s must fit a signed 32-bit integer", name)
		}
	}
	if changes.MinUpgradeFormatScore != nil && *changes.MinUpgradeFormatScore < 1 {
		return fmt.Errorf("min_upgrade_format_score must be at least 1")
	}
	if len(changes.CustomFormatScores) > maxProfileFormatScoreChanges {
		return fmt.Errorf("custom_format_scores exceeds the %d-item limit", maxProfileFormatScoreChanges)
	}
	for i, score := range changes.CustomFormatScores {
		if strings.TrimSpace(score.FormatName) == "" {
			return fmt.Errorf("custom_format_scores[%d].format_name must be nonblank", i)
		}
		if len(score.FormatName) > maxCustomFormatNameBytes {
			return fmt.Errorf("custom_format_scores[%d].format_name exceeds the 256-byte limit", i)
		}
		if score.Score < math.MinInt32 || score.Score > math.MaxInt32 {
			return fmt.Errorf("custom_format_scores[%d].score must fit a signed 32-bit integer", i)
		}
	}
	if changes.LanguageName != nil {
		if service != "radarr" {
			return fmt.Errorf("language_name is supported only for Radarr profiles; Sonarr language behavior uses LanguageSpecification custom formats and Chaptarr has no release-language specification")
		}
		if strings.TrimSpace(*changes.LanguageName) == "" || *changes.LanguageName != strings.TrimSpace(*changes.LanguageName) {
			return fmt.Errorf("language_name must be a nonblank exact name without surrounding whitespace")
		}
		if len(*changes.LanguageName) > maxCustomFormatNameBytes {
			return fmt.Errorf("language_name exceeds the 256-byte limit")
		}
	}
	return nil
}

// mutateProfileWithPlan performs the canonical full-object mutation in memory.
// It preserves every unmodeled field, quality item, and format-item object.
func mutateProfileWithPlan(service string, profileRaw json.RawMessage, customFormats []json.RawMessage, plan profileChangePlan) (json.RawMessage, []string, error) {
	profile, object, err := decodeMutableProfile(profileRaw)
	if err != nil {
		return nil, nil, err
	}
	formats, formatByID, err := decodeCustomFormatMutationInfos(customFormats)
	if err != nil {
		return nil, nil, err
	}
	formatObjects, scores, err := profileFormatItemObjects(object, formats)
	if err != nil {
		return nil, nil, err
	}
	cutoffs, cutoffOrder, err := profileCutoffIndex(profile.Items)
	if err != nil {
		return nil, nil, fmt.Errorf("quality profile %d has invalid items: %w", profile.ID, err)
	}
	currentCutoff, ok := cutoffs[*profile.Cutoff]
	if !ok || !currentCutoff.Allowed {
		return nil, nil, fmt.Errorf("quality profile %d has cutoff %d, which is not an allowed quality or group", profile.ID, *profile.Cutoff)
	}
	if service == "radarr" && profile.Language == nil {
		return nil, nil, fmt.Errorf("quality profile %d is missing its required Radarr language field", profile.ID)
	}

	diff := make([]string, 0, 6+len(plan.CustomFormatScores))
	if plan.UpgradeAllowed != nil {
		if profile.UpgradeAllowed == nil {
			return nil, nil, fmt.Errorf("quality profile %d is missing upgradeAllowed", profile.ID)
		}
		if *profile.UpgradeAllowed != *plan.UpgradeAllowed {
			diff = append(diff, fmt.Sprintf("upgrade policy: %s -> %s", onOff(*profile.UpgradeAllowed), onOff(*plan.UpgradeAllowed)))
			object["upgradeAllowed"] = *plan.UpgradeAllowed
		}
	}
	if plan.QualityCutoffID != nil {
		if profile.Cutoff == nil {
			return nil, nil, fmt.Errorf("quality profile %d is missing cutoff", profile.ID)
		}
		newCutoff, ok := cutoffs[*plan.QualityCutoffID]
		if !ok || !newCutoff.Allowed {
			return nil, nil, fmt.Errorf("quality_cutoff_id %d is not an allowed quality or group; allowed: %s", *plan.QualityCutoffID, allowedProfileCutoffList(cutoffs, cutoffOrder))
		}
		if *profile.Cutoff != *plan.QualityCutoffID {
			oldName := "unknown"
			if oldCutoff, exists := cutoffs[*profile.Cutoff]; exists {
				oldName = oldCutoff.Name
			}
			diff = append(diff, fmt.Sprintf("quality cutoff: %q [%d] -> %q [%d]", oldName, *profile.Cutoff, newCutoff.Name, *plan.QualityCutoffID))
			object["cutoff"] = *plan.QualityCutoffID
		}
	}
	if err := mutateProfileIntField(object, profile.ID, "minFormatScore", "minimum custom-format score", profile.MinFormatScore, plan.MinFormatScore, &diff); err != nil {
		return nil, nil, err
	}
	if err := mutateProfileIntField(object, profile.ID, "cutoffFormatScore", "custom-format cutoff score", profile.CutoffFormatScore, plan.CutoffFormatScore, &diff); err != nil {
		return nil, nil, err
	}
	if plan.MinUpgradeFormatScore != nil && profile.MinUpgradeFormatScore == nil {
		return nil, nil, fmt.Errorf("quality profile %d does not expose minUpgradeFormatScore on this service version", profile.ID)
	}
	if err := mutateProfileIntField(object, profile.ID, "minUpgradeFormatScore", "minimum upgrade score gain", profile.MinUpgradeFormatScore, plan.MinUpgradeFormatScore, &diff); err != nil {
		return nil, nil, err
	}

	for _, change := range plan.CustomFormatScores {
		format, ok := formatByID[change.FormatID]
		if !ok || format.Name != change.FormatName {
			return nil, nil, fmt.Errorf("custom format %d (%q) no longer matches the previewed collection", change.FormatID, change.FormatName)
		}
		old := scores[change.FormatID]
		if old == change.Score {
			continue
		}
		diff = append(diff, fmt.Sprintf("custom format %q [%d]: %+d -> %+d", change.FormatName, change.FormatID, old, change.Score))
		formatObjects[change.FormatID]["score"] = change.Score
		scores[change.FormatID] = change.Score
	}

	resultingLanguageID := 0
	if profile.Language != nil {
		resultingLanguageID = profile.Language.ID
	}
	if plan.Language != nil {
		if service != "radarr" || profile.Language == nil {
			return nil, nil, fmt.Errorf("quality profile %d does not expose a mutable Radarr language field", profile.ID)
		}
		resultingLanguageID = plan.Language.ID
		if profile.Language.ID != plan.Language.ID || profile.Language.Name != plan.Language.Name {
			diff = append(diff, fmt.Sprintf("profile language: %q [%d] -> %q [%d]", profile.Language.Name, profile.Language.ID, plan.Language.Name, plan.Language.ID))
			languageObject, ok := object["language"].(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("quality profile %d has an unreadable Radarr language object", profile.ID)
			}
			// Preserve fields introduced by newer arr builds while replacing the
			// two identity fields the live language catalog resolved.
			languageObject["id"] = plan.Language.ID
			languageObject["name"] = plan.Language.Name
		}
	}
	if service == "radarr" {
		for _, format := range formats {
			if format.LanguageSpecification && scores[format.ID] != 0 && resultingLanguageID != -1 {
				return nil, nil, fmt.Errorf("Radarr profile language must be Any [-1] when language custom format %q has a nonzero score; include language_name \"Any\" in the preview", format.Name)
			}
		}
	}

	body, err := json.Marshal(object)
	if err != nil {
		return nil, nil, fmt.Errorf("encode quality profile %d: %w", profile.ID, err)
	}
	return body, diff, nil
}

func decodeMutableProfile(raw json.RawMessage) (profileMutationView, map[string]any, error) {
	object, err := decodeJSONObject(raw)
	if err != nil {
		return profileMutationView{}, nil, fmt.Errorf("quality profile must be exactly one JSON object")
	}
	var profile profileMutationView
	if err := json.Unmarshal(raw, &profile); err != nil || profile.ID <= 0 || strings.TrimSpace(profile.Name) == "" {
		return profileMutationView{}, nil, fmt.Errorf("quality profile has an unreadable id or name")
	}
	if len(profile.Name) > maxCustomFormatNameBytes {
		return profileMutationView{}, nil, fmt.Errorf("quality profile %d has a name exceeding the 256-byte limit", profile.ID)
	}
	if profile.Items == nil || profile.FormatItems == nil {
		return profileMutationView{}, nil, fmt.Errorf("quality profile %d is missing its complete items or formatItems array", profile.ID)
	}
	if profile.UpgradeAllowed == nil || profile.Cutoff == nil || profile.MinFormatScore == nil || profile.CutoffFormatScore == nil {
		return profileMutationView{}, nil, fmt.Errorf("quality profile %d is missing a required upgrade, cutoff, or score field", profile.ID)
	}
	return profile, object, nil
}

func decodeCustomFormatMutationInfos(raws []json.RawMessage) ([]customFormatMutationInfo, map[int]customFormatMutationInfo, error) {
	formats := make([]customFormatMutationInfo, 0, len(raws))
	byID := make(map[int]customFormatMutationInfo, len(raws))
	names := make(map[string]struct{}, len(raws))
	for _, raw := range raws {
		var view arrCustomFormatView
		if err := json.Unmarshal(raw, &view); err != nil || view.ID <= 0 || strings.TrimSpace(view.Name) == "" || len(view.Name) > maxCustomFormatNameBytes {
			return nil, nil, fmt.Errorf("an existing custom format has an unreadable id or name")
		}
		if _, duplicate := byID[view.ID]; duplicate {
			return nil, nil, fmt.Errorf("multiple custom formats use id %d", view.ID)
		}
		if _, duplicate := names[view.Name]; duplicate {
			return nil, nil, fmt.Errorf("multiple custom formats are named exactly %q", view.Name)
		}
		info := customFormatMutationInfo{ID: view.ID, Name: view.Name}
		for _, specification := range view.Specifications {
			if specification.Implementation == "LanguageSpecification" {
				info.LanguageSpecification = true
			}
		}
		formats = append(formats, info)
		byID[info.ID] = info
		names[info.Name] = struct{}{}
	}
	sort.Slice(formats, func(i, j int) bool { return formats[i].ID < formats[j].ID })
	return formats, byID, nil
}

func profileFormatItemObjects(object map[string]any, formats []customFormatMutationInfo) (map[int]map[string]any, map[int]int64, error) {
	rawItems, ok := object["formatItems"].([]any)
	if !ok {
		return nil, nil, fmt.Errorf("quality profile formatItems must be an array")
	}
	objects := make(map[int]map[string]any, len(rawItems))
	scores := make(map[int]int64, len(rawItems))
	for _, rawItem := range rawItems {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("quality profile contains an unreadable formatItems entry")
		}
		formatID, ok := exactJSONInt(item["format"])
		if !ok || formatID <= 0 {
			return nil, nil, fmt.Errorf("quality profile contains a formatItems entry with an unreadable format id")
		}
		score, ok := exactJSONInt64(item["score"])
		if !ok || score < math.MinInt32 || score > math.MaxInt32 {
			return nil, nil, fmt.Errorf("quality profile contains an unreadable score for custom format %d", formatID)
		}
		if _, duplicate := objects[formatID]; duplicate {
			return nil, nil, fmt.Errorf("quality profile contains custom format %d more than once", formatID)
		}
		objects[formatID] = item
		scores[formatID] = score
	}
	if len(objects) != len(formats) {
		return nil, nil, fmt.Errorf("quality profile has %d formatItems but the service has %d custom formats; refresh the profile in the arr before editing", len(objects), len(formats))
	}
	for _, format := range formats {
		if _, ok := objects[format.ID]; !ok {
			return nil, nil, fmt.Errorf("quality profile is missing custom format %d (%q); refresh it in the arr before editing", format.ID, format.Name)
		}
	}
	return objects, scores, nil
}

func mutateProfileIntField(object map[string]any, profileID int, key, label string, current, desired *int64, diff *[]string) error {
	if desired == nil {
		return nil
	}
	if current == nil {
		return fmt.Errorf("quality profile %d is missing %s", profileID, key)
	}
	if *current != *desired {
		*diff = append(*diff, fmt.Sprintf("%s: %d -> %d", label, *current, *desired))
		object[key] = *desired
	}
	return nil
}

type profileCutoffCandidate struct {
	Name    string
	Allowed bool
}

func profileCutoffIndex(items []arrProfileItemView) (map[int]profileCutoffCandidate, []int, error) {
	if len(items) == 0 {
		return nil, nil, fmt.Errorf("items is empty")
	}
	index := make(map[int]profileCutoffCandidate)
	seenIDs := make(map[int]struct{})
	order := make([]int, 0, len(items))
	var walk func([]arrProfileItemView, bool, bool) error
	walk = func(entries []arrProfileItemView, parentAllowed, topLevel bool) error {
		for _, item := range entries {
			if item.Allowed == nil {
				return fmt.Errorf("a quality item is missing allowed")
			}
			effectiveAllowed := parentAllowed && *item.Allowed
			var id int
			var name string
			switch {
			case item.Quality != nil:
				// Radarr and Sonarr include the real, disallowed "Unknown"
				// quality as ID 0 in every stock profile. It must round-trip, and
				// Chaptarr may even expose an allowed ID-0 quality as a cutoff.
				if len(item.Items) != 0 || item.Quality.ID < 0 || strings.TrimSpace(item.Quality.Name) == "" || len(item.Quality.Name) > maxCustomFormatNameBytes {
					return fmt.Errorf("a quality leaf is malformed")
				}
				id, name = item.Quality.ID, item.Quality.Name
			case item.ID > 0 && strings.TrimSpace(item.Name) != "" && len(item.Name) <= maxCustomFormatNameBytes && len(item.Items) > 0:
				id, name = item.ID, item.Name
			default:
				return fmt.Errorf("a quality group is malformed")
			}
			if _, duplicate := seenIDs[id]; duplicate {
				return fmt.Errorf("quality or group id %d appears more than once", id)
			}
			seenIDs[id] = struct{}{}
			if topLevel {
				index[id] = profileCutoffCandidate{Name: name, Allowed: effectiveAllowed}
				order = append(order, id)
			}
			if item.Quality == nil {
				if err := walk(item.Items, effectiveAllowed, false); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(items, true, true); err != nil {
		return nil, nil, err
	}
	return index, order, nil
}

func allowedProfileCutoffList(index map[int]profileCutoffCandidate, order []int) string {
	values := make([]string, 0, len(order))
	for _, id := range order {
		candidate := index[id]
		if candidate.Allowed {
			values = append(values, fmt.Sprintf("%s [%d]", candidate.Name, id))
		}
	}
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func customFormatNameList(formats []customFormatMutationInfo) string {
	if len(formats) == 0 {
		return "none"
	}
	values := make([]string, 0, len(formats))
	for _, format := range formats {
		values = append(values, fmt.Sprintf("%s [%d]", format.Name, format.ID))
	}
	return joinCapped(values, 30)
}

func resolveProfileLanguage(raws []json.RawMessage, name string) (*resolvedProfileLanguage, error) {
	languages := make([]resolvedProfileLanguage, 0, len(raws))
	ids := make(map[int]struct{}, len(raws))
	names := make(map[string]struct{}, len(raws))
	for _, raw := range raws {
		var language arrIDName
		if err := json.Unmarshal(raw, &language); err != nil || strings.TrimSpace(language.Name) == "" || len(language.Name) > maxCustomFormatNameBytes {
			return nil, fmt.Errorf("Radarr returned a language with an unreadable id or name")
		}
		if _, duplicate := ids[language.ID]; duplicate {
			return nil, fmt.Errorf("Radarr returned duplicate language id %d", language.ID)
		}
		if _, duplicate := names[language.Name]; duplicate {
			return nil, fmt.Errorf("Radarr returned duplicate language name %q", language.Name)
		}
		languages = append(languages, resolvedProfileLanguage{ID: language.ID, Name: language.Name})
		ids[language.ID] = struct{}{}
		names[language.Name] = struct{}{}
	}
	for _, language := range languages {
		if language.Name == name {
			matched := language
			return &matched, nil
		}
	}
	available := make([]string, 0, len(languages))
	for _, language := range languages {
		available = append(available, fmt.Sprintf("%s [%d]", language.Name, language.ID))
	}
	return nil, fmt.Errorf("no Radarr language is named exactly %q; available: %s", name, joinCapped(available, 30))
}

func exactJSONInt(value any) (int, bool) {
	number, ok := exactJSONInt64(value)
	if !ok || int64(int(number)) != number {
		return 0, false
	}
	return int(number), true
}

func exactJSONInt64(value any) (int64, bool) {
	switch value := value.(type) {
	case json.Number:
		number, err := strconv.ParseInt(string(value), 10, 64)
		return number, err == nil
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		if value < math.MinInt64 || value > math.MaxInt64 || math.Trunc(value) != value {
			return 0, false
		}
		return int64(value), true
	default:
		return 0, false
	}
}

// UpdateQualityProfileHelper is the single remote mutation body used by the
// interactive apply tool and any future executor. It calls the supplied guard
// immediately before PUT and verifies the complete canonical profile afterward.
func UpdateQualityProfileHelper(ctx context.Context, client qualityProfileMutator, profileID int, body json.RawMessage, beforeWrite SettingsWriteGuard) error {
	if client == nil || profileID <= 0 {
		return &MutationNotStartedError{Detail: "quality profile mutation target is invalid"}
	}
	bodyObject, err := decodeJSONObject(body)
	bodyID, ok := exactJSONInt(bodyObject["id"])
	if err != nil || !ok || bodyID != profileID {
		return &MutationNotStartedError{Detail: "quality profile mutation body id does not match its route"}
	}
	expectedHash, err := canonicalProfileJSONHash(body)
	if err != nil {
		return &MutationNotStartedError{Detail: "quality profile mutation body is invalid"}
	}
	if err := ctx.Err(); err != nil {
		return &MutationNotStartedError{Detail: err.Error(), Cause: err}
	}
	if beforeWrite != nil {
		if err := beforeWrite(ctx); err != nil {
			return &MutationNotStartedError{Detail: err.Error(), Cause: err}
		}
	}
	if _, err := client.UpdateQualityProfileRawContext(ctx, profileID, body); err != nil {
		return classifySettingsWriteOutcome("the quality profile update may have been accepted", err)
	}
	profiles, err := client.GetQualityProfilesRawContext(ctx)
	if err != nil {
		return &PartialMutationError{Completed: "the quality profile update was accepted", Pending: "reading the updated profile", Err: err}
	}
	updatedRaw, _, err := uniqueRawProfileByID(profiles, profileID)
	if err != nil {
		return &PartialMutationError{Completed: "the quality profile update was accepted", Pending: "finding the updated profile", Err: err}
	}
	actualHash, err := canonicalProfileJSONHash(updatedRaw)
	if err != nil || actualHash != expectedHash {
		if err == nil {
			err = fmt.Errorf("the stored profile differs from the complete object Cantinarr sent")
		}
		return &PartialMutationError{Completed: "the quality profile update was accepted", Pending: "verifying the complete stored profile", Err: err}
	}
	return nil
}

func uniqueRawProfileByID(raws []json.RawMessage, profileID int) (json.RawMessage, string, error) {
	var (
		matched json.RawMessage
		name    string
		count   int
	)
	for _, raw := range raws {
		var head arrIDName
		if err := json.Unmarshal(raw, &head); err != nil || head.ID <= 0 || strings.TrimSpace(head.Name) == "" {
			return nil, "", fmt.Errorf("a quality profile has an unreadable id or name")
		}
		if head.ID == profileID {
			count++
			matched = raw
			name = head.Name
		}
	}
	if count == 0 {
		return nil, "", fmt.Errorf("quality profile %d no longer exists", profileID)
	}
	if count != 1 {
		return nil, "", fmt.Errorf("quality profile id %d is ambiguous", profileID)
	}
	return matched, name, nil
}

func canonicalJSONHash(raw json.RawMessage) ([32]byte, error) {
	var zero [32]byte
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return zero, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return zero, fmt.Errorf("trailing JSON value")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return zero, err
	}
	return sha256.Sum256(encoded), nil
}

// canonicalProfileJSONHash preserves quality-item order (the ranking) while
// normalizing formatItems order by custom-format id, which has no ranking
// meaning and may be normalized differently by compatible arr builds.
func canonicalProfileJSONHash(raw json.RawMessage) ([32]byte, error) {
	var zero [32]byte
	object, err := decodeJSONObject(raw)
	if err != nil {
		return zero, err
	}
	rawFormatItems, ok := object["formatItems"].([]any)
	if !ok {
		return zero, fmt.Errorf("quality profile formatItems must be an array")
	}
	type formatItem struct {
		formatID int
		value    any
	}
	items := make([]formatItem, 0, len(rawFormatItems))
	seen := make(map[int]struct{}, len(rawFormatItems))
	for _, value := range rawFormatItems {
		item, ok := value.(map[string]any)
		if !ok {
			return zero, fmt.Errorf("quality profile contains an unreadable formatItems entry")
		}
		formatID, ok := exactJSONInt(item["format"])
		if !ok || formatID <= 0 {
			return zero, fmt.Errorf("quality profile contains an unreadable custom-format id")
		}
		if _, duplicate := seen[formatID]; duplicate {
			return zero, fmt.Errorf("quality profile contains custom format %d more than once", formatID)
		}
		seen[formatID] = struct{}{}
		items = append(items, formatItem{formatID: formatID, value: value})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].formatID < items[j].formatID })
	normalized := make([]any, len(items))
	for i := range items {
		normalized[i] = items[i].value
	}
	object["formatItems"] = normalized
	encoded, err := json.Marshal(object)
	if err != nil {
		return zero, err
	}
	return sha256.Sum256(encoded), nil
}

func canonicalJSONCollectionHash(raws []json.RawMessage) ([32]byte, error) {
	return canonicalJSONCollectionHashByID(raws, true)
}

func canonicalLanguageCollectionHash(raws []json.RawMessage) ([32]byte, error) {
	return canonicalJSONCollectionHashByID(raws, false)
}

func canonicalJSONCollectionHashByID(raws []json.RawMessage, requirePositiveID bool) ([32]byte, error) {
	var zero [32]byte
	type canonicalItem struct {
		id      int
		encoded []byte
	}
	items := make([]canonicalItem, 0, len(raws))
	seenIDs := make(map[int]struct{}, len(raws))
	for _, raw := range raws {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return zero, err
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return zero, fmt.Errorf("collection item contains trailing JSON")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return zero, err
		}
		object, ok := value.(map[string]any)
		if !ok {
			return zero, fmt.Errorf("collection item is not an object")
		}
		id, ok := exactJSONInt(object["id"])
		if !ok {
			return zero, fmt.Errorf("collection item has an unreadable id")
		}
		if requirePositiveID && id <= 0 {
			return zero, fmt.Errorf("collection item id must be positive")
		}
		if _, duplicate := seenIDs[id]; duplicate {
			return zero, fmt.Errorf("collection contains duplicate id %d", id)
		}
		seenIDs[id] = struct{}{}
		items = append(items, canonicalItem{id: id, encoded: encoded})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	hash := sha256.New()
	for _, item := range items {
		if err := binary.Write(hash, binary.BigEndian, int64(item.id)); err != nil {
			return zero, err
		}
		if err := binary.Write(hash, binary.BigEndian, uint64(len(item.encoded))); err != nil {
			return zero, err
		}
		_, _ = hash.Write(item.encoded)
	}
	copy(zero[:], hash.Sum(nil))
	return zero, nil
}
