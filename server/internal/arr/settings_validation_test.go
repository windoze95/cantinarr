package arr

import (
	"fmt"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

func TestReadSettingsValidationDetailsAcceptsTypedIssuesAndIgnoresExtras(t *testing.T) {
	const body = `[
		{
			"propertyName": "cutoff",
			"errorMessage": "Cutoff must reference an allowed quality",
			"severity": "error",
			"attemptedValue": "https://indexer.invalid/download?apiKey=attempted-secret&signature=signed-url-secret"
		}
	]`

	got := ReadSettingsValidationDetails(strings.NewReader(body))
	want := "cutoff: Cutoff must reference an allowed quality"
	if got != want {
		t.Fatalf("ReadSettingsValidationDetails() = %q, want %q", got, want)
	}
	for _, secretValue := range []string{"attempted-secret", "signed-url-secret"} {
		if strings.Contains(got, secretValue) {
			t.Fatalf("validation details leaked ignored attemptedValue %q: %s", secretValue, got)
		}
	}
}

func TestReadSettingsValidationDetailsJoinsIssuesAndNormalizesNewlines(t *testing.T) {
	const body = `[
		{"propertyName":"cutoff\r\nquality", "errorMessage":"Must be present\nand allowed"},
		{"propertyName":"formatItems", "errorMessage":"Every custom format is required"}
	]`

	got := ReadSettingsValidationDetails(strings.NewReader(body))
	want := "cutoff quality: Must be present and allowed; formatItems: Every custom format is required"
	if got != want {
		t.Fatalf("ReadSettingsValidationDetails() = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("validation details retained a line break: %q", got)
	}
}

func TestReadSettingsValidationDetailsAcceptsValidationProblemDetails(t *testing.T) {
	const body = `{
		"type":"https://tools.ietf.org/html/rfc9110#section-15.5.1",
		"title":"One or more validation errors occurred.",
		"status":400,
		"traceId":"trace-secret-that-is-never-rendered",
		"errors":{
			"Name":["Name is required"],
			"$.specifications[0].fields":["The JSON value could not be converted to List<Field>.","Use the native fields array"]
		}
	}`

	got := ReadSettingsValidationDetails(strings.NewReader(body))
	want := "$.specifications[0].fields: The JSON value could not be converted to List<Field>.; $.specifications[0].fields: Use the native fields array; Name: Name is required"
	if got != want {
		t.Fatalf("ReadSettingsValidationDetails() = %q, want %q", got, want)
	}
	if strings.Contains(got, "trace-secret") || strings.Contains(got, "rfc9110") {
		t.Fatalf("problem metadata leaked: %s", got)
	}
}

func TestReadSettingsValidationDetailsKeepsModelLevelFailures(t *testing.T) {
	const body = `[{"propertyName":"","errorMessage":"Must contain at least one Condition"}]`
	if got, want := ReadSettingsValidationDetails(strings.NewReader(body)), "settings object: Must contain at least one Condition"; got != want {
		t.Fatalf("ReadSettingsValidationDetails() = %q, want %q", got, want)
	}
}

func TestReadSettingsValidationDetailsRedactsProjectedStrings(t *testing.T) {
	const body = `[
		{
			"propertyName":"X-Api-Key: property-secret\ncutoff",
			"errorMessage":"request https://indexer.invalid/a?token=message-secret&item=7 failed"
		}
	]`

	got := ReadSettingsValidationDetails(strings.NewReader(body))
	for _, secretValue := range []string{"property-secret", "message-secret"} {
		if strings.Contains(got, secretValue) {
			t.Fatalf("validation details leaked %q: %s", secretValue, got)
		}
	}
	if !strings.Contains(got, secrets.RedactedValue) || !strings.Contains(got, "cutoff") || !strings.Contains(got, "item=7") {
		t.Fatalf("validation details lost useful redacted context: %s", got)
	}
}

func TestReadSettingsValidationDetailsRejectsUnsafeOrInvalidBodies(t *testing.T) {
	valid := `[{"propertyName":"cutoff","errorMessage":"must be allowed"}]`
	tooMany := "["
	for i := 0; i <= maxSettingsValidationIssues; i++ {
		if i > 0 {
			tooMany += ","
		}
		tooMany += fmt.Sprintf(`{"propertyName":"field%d","errorMessage":"invalid"}`, i)
	}
	tooMany += "]"

	tests := []struct {
		name string
		body string
	}{
		{name: "empty body", body: ""},
		{name: "empty array", body: "[]"},
		{name: "malformed", body: `[{"propertyName":"cutoff"`},
		{name: "invalid UTF-8", body: "[{\"propertyName\":\"cutoff\",\"errorMessage\":\"\xff\"}]"},
		{name: "non-array object", body: `{"propertyName":"cutoff","errorMessage":"invalid"}`},
		{name: "problem without errors", body: `{"title":"invalid","status":400}`},
		{name: "problem blank message", body: `{"errors":{"field":[" "]}}`},
		{name: "problem wrong value type", body: `{"errors":{"field":"invalid"}}`},
		{name: "problem empty message list", body: `{"errors":{"field":[]}}`},
		{name: "non-array null", body: "null"},
		{name: "trailing document", body: valid + ` {"unexpected":true}`},
		{name: "oversize", body: valid + strings.Repeat(" ", maxSettingsValidationBodyBytes-len(valid)+1)},
		{name: "property type error", body: `[{"propertyName":12,"errorMessage":"invalid"}]`},
		{name: "message type error", body: `[{"propertyName":"cutoff","errorMessage":{"message":"invalid"}}]`},
		{name: "too many issues", body: tooMany},
		{name: "blank message", body: `[{"propertyName":"cutoff","errorMessage":"\n\t"}]`},
		{name: "property too long", body: `[{"propertyName":"` + strings.Repeat("p", maxSettingsValidationFieldBytes+1) + `","errorMessage":"invalid"}]`},
		{name: "message too long", body: `[{"propertyName":"cutoff","errorMessage":"` + strings.Repeat("m", maxSettingsValidationFieldBytes+1) + `"}]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReadSettingsValidationDetails(strings.NewReader(tt.body)); got != "" {
				t.Fatalf("ReadSettingsValidationDetails() = %q, want empty", got)
			}
		})
	}
}
