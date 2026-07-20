package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func profileDependencyHash(snapshot profileSettingsSnapshot) [sha256.Size]byte {
	material := make([]byte, 0, 1+len(snapshot.CustomFormatHash)+len(snapshot.LanguageHash))
	material = append(material, snapshot.CustomFormatHash[:]...)
	if snapshot.HasLanguages {
		material = append(material, 1)
		material = append(material, snapshot.LanguageHash[:]...)
	} else {
		material = append(material, 0)
	}
	return sha256.Sum256(material)
}

// profileDependencyMatchesStored preserves the dependency scope captured when
// the change was applied. Sonarr includes its language catalog only when a
// changed custom format uses LanguageSpecification, while Radarr always
// includes it. Trying the base snapshot first keeps non-language Sonarr and
// Chaptarr history independent from unrelated language-catalog changes.
func profileDependencyMatchesStored(ctx context.Context, mutator qualityProfileMutator, service string, snapshot *profileSettingsSnapshot, expected string) (bool, error) {
	if hashString(profileDependencyHash(*snapshot)) == expected {
		return true, nil
	}
	if snapshot.HasLanguages || (service != "radarr" && service != "sonarr") {
		return false, nil
	}
	if err := loadProfileSnapshotLanguages(ctx, mutator, snapshot); err != nil {
		return false, err
	}
	return hashString(profileDependencyHash(*snapshot)) == expected, nil
}

func profileSettingFieldChanges(beforeRaw, afterRaw json.RawMessage, customFormats []json.RawMessage, plan profileChangePlan) ([]SettingFieldChange, error) {
	before, beforeObject, err := decodeMutableProfile(beforeRaw)
	if err != nil {
		return nil, err
	}
	after, afterObject, err := decodeMutableProfile(afterRaw)
	if err != nil {
		return nil, err
	}
	changes := make([]SettingFieldChange, 0, 6+len(plan.CustomFormatScores))
	appendField := func(key, label, oldValue, newValue string) {
		if oldValue != newValue {
			changes = append(changes, SettingFieldChange{Key: key, Label: label, Before: oldValue, After: newValue})
		}
	}

	if plan.UpgradeAllowed != nil {
		appendField("upgrade_allowed", "Upgrade policy", onOff(*before.UpgradeAllowed), onOff(*after.UpgradeAllowed))
	}
	if plan.QualityCutoffID != nil {
		beforeCutoffs, _, err := profileCutoffIndex(before.Items)
		if err != nil {
			return nil, err
		}
		afterCutoffs, _, err := profileCutoffIndex(after.Items)
		if err != nil {
			return nil, err
		}
		appendField("quality_cutoff", "Quality cutoff", cutoffDisplay(beforeCutoffs, *before.Cutoff), cutoffDisplay(afterCutoffs, *after.Cutoff))
	}
	if plan.MinFormatScore != nil {
		appendField("min_format_score", "Minimum custom-format score", strconv.FormatInt(*before.MinFormatScore, 10), strconv.FormatInt(*after.MinFormatScore, 10))
	}
	if plan.CutoffFormatScore != nil {
		appendField("cutoff_format_score", "Custom-format cutoff score", strconv.FormatInt(*before.CutoffFormatScore, 10), strconv.FormatInt(*after.CutoffFormatScore, 10))
	}
	if plan.MinUpgradeFormatScore != nil && before.MinUpgradeFormatScore != nil && after.MinUpgradeFormatScore != nil {
		appendField("min_upgrade_format_score", "Minimum upgrade score gain", strconv.FormatInt(*before.MinUpgradeFormatScore, 10), strconv.FormatInt(*after.MinUpgradeFormatScore, 10))
	}
	if len(plan.CustomFormatScores) > 0 {
		formats, _, err := decodeCustomFormatMutationInfos(customFormats)
		if err != nil {
			return nil, err
		}
		_, beforeScores, err := profileFormatItemObjects(beforeObject, formats)
		if err != nil {
			return nil, err
		}
		_, afterScores, err := profileFormatItemObjects(afterObject, formats)
		if err != nil {
			return nil, err
		}
		for _, score := range plan.CustomFormatScores {
			appendField(
				fmt.Sprintf("custom_format_score:%d", score.FormatID),
				"Custom format: "+score.FormatName,
				signedSettingScore(beforeScores[score.FormatID]),
				signedSettingScore(afterScores[score.FormatID]),
			)
		}
	}
	if plan.Language != nil && before.Language != nil && after.Language != nil {
		appendField("language", "Profile language", languageDisplay(before.Language), languageDisplay(after.Language))
	}
	if len(changes) == 0 {
		return nil, fmt.Errorf("profile change history projection is empty")
	}
	return changes, nil
}

func profileCurrentFieldValues(profileRaw json.RawMessage, customFormats []json.RawMessage, fields []SettingFieldChange) (map[string]string, error) {
	profile, object, err := decodeMutableProfile(profileRaw)
	if err != nil {
		return nil, err
	}
	cutoffs, _, err := profileCutoffIndex(profile.Items)
	if err != nil {
		return nil, err
	}
	formats, _, err := decodeCustomFormatMutationInfos(customFormats)
	if err != nil {
		return nil, err
	}
	_, scores, err := profileFormatItemObjects(object, formats)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(fields))
	for _, field := range fields {
		switch field.Key {
		case "upgrade_allowed":
			values[field.Key] = onOff(*profile.UpgradeAllowed)
		case "quality_cutoff":
			values[field.Key] = cutoffDisplay(cutoffs, *profile.Cutoff)
		case "min_format_score":
			values[field.Key] = strconv.FormatInt(*profile.MinFormatScore, 10)
		case "cutoff_format_score":
			values[field.Key] = strconv.FormatInt(*profile.CutoffFormatScore, 10)
		case "min_upgrade_format_score":
			if profile.MinUpgradeFormatScore == nil {
				values[field.Key] = "Unavailable"
			} else {
				values[field.Key] = strconv.FormatInt(*profile.MinUpgradeFormatScore, 10)
			}
		case "language":
			if profile.Language == nil {
				values[field.Key] = "Unavailable"
			} else {
				values[field.Key] = languageDisplay(profile.Language)
			}
		default:
			if idText, ok := strings.CutPrefix(field.Key, "custom_format_score:"); ok {
				id, parseErr := strconv.Atoi(idText)
				if parseErr != nil {
					return nil, fmt.Errorf("invalid stored custom-format field key")
				}
				if score, exists := scores[id]; exists {
					values[field.Key] = signedSettingScore(score)
				} else {
					values[field.Key] = "Removed"
				}
				continue
			}
			return nil, fmt.Errorf("unsupported stored profile field %q", field.Key)
		}
	}
	return values, nil
}

func withCurrentSettingValues(fields []SettingFieldChange, values, states map[string]string) []SettingFieldChange {
	result := make([]SettingFieldChange, len(fields))
	for i, field := range fields {
		current := values[field.Key]
		field.Current = &current
		if state, ok := states[field.Key]; ok {
			field.CurrentState = state
			result[i] = field
			continue
		}
		switch current {
		case field.After:
			field.CurrentState = "matches_applied"
		case field.Before:
			field.CurrentState = "matches_before"
		default:
			field.CurrentState = "different"
		}
		result[i] = field
	}
	return result
}

func cutoffDisplay(candidates map[int]profileCutoffCandidate, id int) string {
	if candidate, ok := candidates[id]; ok {
		return fmt.Sprintf("%s [%d]", candidate.Name, id)
	}
	return fmt.Sprintf("Unknown [%d]", id)
}

func languageDisplay(language *arrIDName) string {
	return fmt.Sprintf("%s [%d]", language.Name, language.ID)
}

func signedSettingScore(value int64) string {
	if value > 0 {
		return fmt.Sprintf("+%d", value)
	}
	return strconv.FormatInt(value, 10)
}
