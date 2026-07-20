package arr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const (
	maxSettingsValidationBodyBytes  = 64 * 1024
	maxSettingsValidationIssues     = 20
	maxSettingsValidationFieldBytes = 512
)

type settingsValidationIssue struct {
	PropertyName string `json:"propertyName"`
	ErrorMessage string `json:"errorMessage"`
}

// ReadSettingsValidationDetails extracts the safe, useful portion of an arr
// settings validation response. Callers should use it only for HTTP 400
// responses and fall back to a generic status error when it returns empty.
func ReadSettingsValidationDetails(r io.Reader) string {
	if r == nil {
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(r, maxSettingsValidationBodyBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxSettingsValidationBodyBytes || !utf8.Valid(body) {
		return ""
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	var document json.RawMessage
	if err := decoder.Decode(&document); err != nil || len(document) == 0 {
		return ""
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ""
	}

	if details := projectFluentValidationIssues(document); details != "" {
		return details
	}
	return projectValidationProblemDetails(document)
}

func projectFluentValidationIssues(document json.RawMessage) string {
	var issues []settingsValidationIssue
	if err := json.Unmarshal(document, &issues); err != nil || issues == nil || len(issues) == 0 || len(issues) > maxSettingsValidationIssues {
		return ""
	}
	details := make([]string, 0, len(issues))
	for _, issue := range issues {
		propertyName, ok := projectSettingsValidationProperty(issue.PropertyName)
		if !ok {
			return ""
		}
		errorMessage, ok := projectSettingsValidationText(issue.ErrorMessage)
		if !ok {
			return ""
		}
		details = append(details, fmt.Sprintf("%s: %s", propertyName, errorMessage))
	}

	return strings.Join(details, "; ")
}

func projectValidationProblemDetails(document json.RawMessage) string {
	var problem struct {
		Errors map[string][]string `json:"errors"`
	}
	if err := json.Unmarshal(document, &problem); err != nil || len(problem.Errors) == 0 {
		return ""
	}
	keys := make([]string, 0, len(problem.Errors))
	count := 0
	for key, messages := range problem.Errors {
		if len(messages) == 0 {
			return ""
		}
		count += len(messages)
		if count > maxSettingsValidationIssues {
			return ""
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	details := make([]string, 0, count)
	for _, key := range keys {
		propertyName, ok := projectSettingsValidationProperty(key)
		if !ok {
			return ""
		}
		for _, message := range problem.Errors[key] {
			errorMessage, ok := projectSettingsValidationText(message)
			if !ok {
				return ""
			}
			details = append(details, fmt.Sprintf("%s: %s", propertyName, errorMessage))
		}
	}
	return strings.Join(details, "; ")
}

func projectSettingsValidationProperty(value string) (string, bool) {
	if len(value) > maxSettingsValidationFieldBytes {
		return "", false
	}
	if strings.TrimSpace(value) == "" {
		return "settings object", true
	}
	return projectSettingsValidationText(value)
}

func projectSettingsValidationText(value string) (string, bool) {
	if len(value) > maxSettingsValidationFieldBytes {
		return "", false
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "", false
	}
	return secrets.RedactText(value), true
}
