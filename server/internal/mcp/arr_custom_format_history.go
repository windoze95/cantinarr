package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

func customFormatSettingFieldChanges(plan customFormatUpsertPlan) ([]SettingFieldChange, error) {
	if plan.Action == "created" {
		after, err := decodeJSONObject(plan.AfterRaw)
		if err != nil {
			return nil, err
		}
		changes := []SettingFieldChange{{
			Key: "presence", Label: "Custom format", Before: "Not present",
			After: customFormatDefinitionSummary(plan.AfterRaw),
		}}
		keys := make([]string, 0, len(after))
		for key := range after {
			if key != "id" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			changes = append(changes, SettingFieldChange{
				Key: "field:" + key, Label: customFormatFieldLabel(key),
				Before: "Not present", After: customFormatValueDisplay(key, after[key]),
			})
		}
		return changes, nil
	}
	before, err := decodeJSONObject(plan.BeforeRaw)
	if err != nil {
		return nil, err
	}
	after, err := decodeJSONObject(plan.AfterRaw)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(before)+len(after))
	seen := make(map[string]struct{}, len(before)+len(after))
	for key := range before {
		if key != "id" {
			seen[key] = struct{}{}
		}
	}
	for key := range after {
		if key != "id" {
			seen[key] = struct{}{}
		}
	}
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	changes := make([]SettingFieldChange, 0, len(keys))
	for _, key := range keys {
		if customFormatValuesEquivalent(key, before[key], after[key]) {
			continue
		}
		changes = append(changes, SettingFieldChange{
			Key: "field:" + key, Label: customFormatFieldLabel(key),
			Before: customFormatValueDisplay(key, before[key]),
			After:  customFormatValueDisplay(key, after[key]),
		})
	}
	if len(changes) == 0 {
		return nil, fmt.Errorf("custom format history projection is empty")
	}
	return changes, nil
}

func customFormatCurrentFieldValues(raw json.RawMessage, exists bool, fields []SettingFieldChange, beforeRaw, afterRaw json.RawMessage, plannedAfter bool) (map[string]string, map[string]string, error) {
	values := make(map[string]string, len(fields))
	states := make(map[string]string, len(fields))
	current := make(map[string]any)
	if exists {
		var err error
		current, err = decodeJSONObject(raw)
		if err != nil {
			return nil, nil, err
		}
	}
	before, beforeExists, err := decodeCustomFormatHistoryObject(beforeRaw)
	if err != nil {
		return nil, nil, err
	}
	after, afterExists, err := decodeCustomFormatHistoryObject(afterRaw)
	if err != nil || !afterExists {
		return nil, nil, fmt.Errorf("stored custom-format after snapshot is invalid")
	}
	for _, field := range fields {
		switch {
		case field.Key == "presence":
			if exists {
				values[field.Key] = customFormatDefinitionSummary(raw)
			} else {
				values[field.Key] = "Not present"
			}
			states[field.Key] = settingCurrentState(exists == afterExists, exists == beforeExists)
		case strings.HasPrefix(field.Key, "field:"):
			key := strings.TrimPrefix(field.Key, "field:")
			currentValue, currentFieldExists := current[key]
			if exists {
				values[field.Key] = customFormatValueDisplay(key, currentValue)
			} else {
				values[field.Key] = "Not present"
			}
			matchesAfter := customFormatHistoryFieldMatches(
				current, exists, currentValue, currentFieldExists,
				after, afterExists, key, plannedAfter,
			)
			matchesBefore := customFormatHistoryFieldMatches(
				current, exists, currentValue, currentFieldExists,
				before, beforeExists, key, false,
			)
			states[field.Key] = settingCurrentState(matchesAfter, matchesBefore)
		default:
			return nil, nil, fmt.Errorf("unsupported stored custom-format field %q", field.Key)
		}
	}
	return values, states, nil
}

func decodeCustomFormatHistoryObject(raw json.RawMessage) (map[string]any, bool, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false, nil
	}
	object, err := decodeJSONObject(raw)
	return object, err == nil, err
}

func customFormatHistoryFieldMatches(current map[string]any, currentExists bool, currentValue any, currentFieldExists bool, target map[string]any, targetExists bool, key string, targetContainsPlan bool) bool {
	if currentExists != targetExists {
		return false
	}
	if !currentExists {
		return true
	}
	targetValue, targetFieldExists := target[key]
	if currentFieldExists != targetFieldExists {
		return false
	}
	if !currentFieldExists {
		return true
	}
	if targetContainsPlan {
		return jsonValueContains(currentValue, targetValue)
	}
	return customFormatValuesEquivalent(key, currentValue, targetValue)
}

func customFormatObjectContainsPlan(currentRaw, plannedRaw json.RawMessage) bool {
	current, currentErr := decodeJSONObject(currentRaw)
	planned, plannedErr := decodeJSONObject(plannedRaw)
	return currentErr == nil && plannedErr == nil && jsonValueContains(current, planned)
}

func settingCurrentState(matchesAfter, matchesBefore bool) string {
	if matchesAfter {
		return "matches_applied"
	}
	if matchesBefore {
		return "matches_before"
	}
	return "different"
}

func customFormatDefinitionSummary(raw json.RawMessage) string {
	object, err := decodeJSONObject(raw)
	if err != nil {
		return "Created"
	}
	if values, ok := object["specifications"].([]any); ok {
		return fmt.Sprintf("%d matching rule%s", len(values), pluralSuffix(len(values)))
	}
	return "Created"
}

func customFormatValueDisplay(key string, value any) string {
	if value == nil {
		return "Not set"
	}
	switch value := value.(type) {
	case string:
		if len(value) <= 512 {
			return value
		}
		return value[:509] + "..."
	case bool:
		return onOff(value)
	case json.Number:
		return value.String()
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "Changed"
	}
	if len(encoded) > 512 {
		if key == "specifications" {
			if values, ok := value.([]any); ok {
				return fmt.Sprintf("%d matching rule%s (%d-byte definition)", len(values), pluralSuffix(len(values)), len(encoded))
			}
		}
		return fmt.Sprintf("Changed (%d-byte value)", len(encoded))
	}
	return string(encoded)
}

func customFormatFieldLabel(key string) string {
	switch key {
	case "includeCustomFormatWhenRenaming":
		return "Use in file renaming"
	case "specifications":
		return "Matching rules"
	case "name":
		return "Name"
	}
	var out strings.Builder
	for i, r := range key {
		if unicode.IsUpper(r) && i > 0 {
			out.WriteByte(' ')
		}
		out.WriteRune(unicode.ToLower(r))
	}
	label := strings.TrimSpace(out.String())
	if label == "" {
		return "Setting"
	}
	runes := []rune(label)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
