package secrets

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRedactTextScrubsNestedCredentials(t *testing.T) {
	secretValues := []string{
		"query-api-secret",
		"signed-value",
		"nested-token",
		"basic-secret",
		"cookie-secret",
		"password-secret",
		"userinfo-secret",
	}
	input := `{
  "downloadUrl":"https://indexer.invalid/get?id=4&apiKey=query-api-secret&signature=signed-value",
  "nested":{"message":"request failed: token=nested-token; Authorization: Basic basic-secret"},
  "headers":{"Cookie":"session=cookie-secret","ordinary":"kept"},
  "settings":[{"name":"password","value":"password-secret"}],
  "url":"https://user:userinfo-secret@example.invalid/path?safe=1"
}`

	got := RedactText(input)
	for _, secret := range secretValues {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted output leaked %q: %s", secret, got)
		}
	}
	for _, want := range []string{"[REDACTED]", `"ordinary":"kept"`, "safe=1", "example.invalid"} {
		if !strings.Contains(got, want) {
			t.Errorf("redacted output missing useful value %q: %s", want, got)
		}
	}
}

func TestRedactTextScrubsPrefixedErrorBodyAndHeaders(t *testing.T) {
	input := "upstream 502 body={\"error\":\"download https://idx.invalid/a?apikey=url-secret&item=7\",\"detail\":\"nope\"}\n" +
		"Authorization: Bearer bearer-secret\nX-Api-Key: header-secret\npassword='quoted secret'\n"
	got := RedactText(input)
	for _, secret := range []string{"url-secret", "bearer-secret", "header-secret", "quoted secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted output leaked %q: %s", secret, got)
		}
	}
	for _, want := range []string{"upstream 502", "item=7", "detail", RedactedValue} {
		if !strings.Contains(got, want) {
			t.Errorf("redacted output missing diagnosis %q: %s", want, got)
		}
	}
}

func TestRedactTextScrubsBareProviderCredentials(t *testing.T) {
	credentials := []string{
		"sk-proj-Synthetic_OpenAI_Key_123456789",
		"sk-SyntheticLegacyOpenAIKey123456789",
		"sk-ant-api03-Synthetic_Anthropic_Key_123456789",
		"AIzaSyntheticGoogleAPIKey1234567890",
		"AQ.Synthetic_Gemini_Key_1234567890",
	}
	input := "provider rejected credentials: " + strings.Join(credentials, " | ") + " (request_id=req-safe)"
	got := RedactText(input)
	for _, credential := range credentials {
		if strings.Contains(got, credential) {
			t.Fatalf("redacted output leaked bare provider credential %q: %s", credential, got)
		}
	}
	if strings.Count(got, RedactedValue) != len(credentials) || !strings.Contains(got, "request_id=req-safe") {
		t.Fatalf("bare provider redaction lost diagnostic context: %s", got)
	}
}

func TestRedactErrorDoesNotWrapCredentialBearingBody(t *testing.T) {
	original := errors.New(`arr request failed: {"downloadUrl":"https://idx.invalid/get?token=error-secret"}`)
	redacted := RedactError(original)
	if redacted == nil {
		t.Fatal("RedactError returned nil")
	}
	if strings.Contains(redacted.Error(), "error-secret") {
		t.Fatalf("redacted error leaked secret: %v", redacted)
	}
	if errors.Is(redacted, original) {
		t.Fatal("redacted error retained the credential-bearing original in its unwrap chain")
	}
}

func TestRedactJSONValueScrubsStructuredOutput(t *testing.T) {
	got, err := RedactJSONValue(map[string]any{
		"results": []any{map[string]any{
			"title": "useful",
			"links": []string{"/download?access_key=structured-secret&id=8"},
		}},
		"api_token": "field-secret",
	})
	if err != nil {
		t.Fatalf("RedactJSONValue: %v", err)
	}
	text := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(toJSON(t, got)), "\\u0026", "&"), "\\u003d", "=")
	for _, secret := range []string{"structured-secret", "field-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("structured output leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "useful") || !strings.Contains(text, "id=8") {
		t.Fatalf("structured output lost useful fields: %s", text)
	}
}

func toJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
