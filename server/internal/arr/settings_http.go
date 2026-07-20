package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

const maxSettingsWriteResponseBytes = 4 << 20

var settingsValidationURLAuthorityPattern = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*:)?//(?:\[[^\]\s]+\]|[^\s/;]+)`)

// SettingsWriteOutcomeUnknownError means a settings request was handed to the
// HTTP transport but Cantinarr could not prove whether the arr applied it.
// Callers must not describe this as a clean failure or blindly retry the write.
type SettingsWriteOutcomeUnknownError struct{ Detail string }

func (e *SettingsWriteOutcomeUnknownError) Error() string                     { return e.Detail }
func (e *SettingsWriteOutcomeUnknownError) SettingsWriteOutcomeUnknown() bool { return true }

// DoSettingsWrite sends one credential-free arr settings object and returns
// its raw response. Unlike the clients' generic request paths, this narrowly
// scoped helper may project typed FluentValidation details from HTTP 400
// bodies; all other upstream error bodies remain discarded.
//
// Do not use this for indexers, download clients, notifications, import lists,
// or any other settings surface that can carry credentials or signed URLs.
func DoSettingsWrite(ctx context.Context, client *http.Client, service, baseURL, apiKey, method, path string, body json.RawMessage) (json.RawMessage, int, error) {
	if len(body) == 0 || !json.Valid(body) {
		return nil, 0, fmt.Errorf("%s settings write: invalid JSON request body", service)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, bytes.NewReader(body))
	if err != nil {
		requestPath, _, _ := strings.Cut(path, "?")
		return nil, 0, fmt.Errorf("%s %s %s: invalid instance request configuration", service, method, requestPath)
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	requestPath, _, _ := strings.Cut(path, "?")
	if err != nil {
		return nil, 0, &SettingsWriteOutcomeUnknownError{Detail: fmt.Sprintf("%s sent %s %s but did not receive a reliable response (%s); the write outcome is unknown, so inspect the live settings before retrying", service, method, requestPath, transporterr.Summarize(err))}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, resp.StatusCode, &SettingsWriteOutcomeUnknownError{Detail: fmt.Sprintf("%s returned status %d after %s %s; the write may already have been applied, so inspect the live settings before retrying", service, resp.StatusCode, method, requestPath)}
		}
		message := fmt.Sprintf("%s %s %s returned status %d", service, method, requestPath, resp.StatusCode)
		if resp.StatusCode == http.StatusBadRequest {
			if details := ReadSettingsValidationDetails(resp.Body); details != "" {
				message += ": " + sanitizeSettingsValidationDetails(details, baseURL, apiKey)
			}
		}
		return nil, resp.StatusCode, fmt.Errorf("%s", message)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSettingsWriteResponseBytes+1))
	if err != nil {
		return nil, resp.StatusCode, &SettingsWriteOutcomeUnknownError{Detail: fmt.Sprintf("%s accepted %s %s with status %d, but its response could not be read; inspect the live settings before retrying", service, method, requestPath, resp.StatusCode)}
	}
	if len(data) > maxSettingsWriteResponseBytes {
		return nil, resp.StatusCode, &SettingsWriteOutcomeUnknownError{Detail: fmt.Sprintf("%s accepted %s %s with status %d, but its response exceeded the safe size limit; inspect the live settings before retrying", service, method, requestPath, resp.StatusCode)}
	}
	return json.RawMessage(data), resp.StatusCode, nil
}

// sanitizeSettingsValidationDetails removes topology and the exact credential
// used for this request after the typed projection. Pattern-based redaction is
// useful but cannot identify a bare random arr API key or a URL authority that
// contains no credential marker.
func sanitizeSettingsValidationDetails(details, baseURL, apiKey string) string {
	if apiKey != "" {
		details = strings.ReplaceAll(details, apiKey, secrets.RedactedValue)
	}
	if parsed, err := url.Parse(baseURL); err == nil {
		for _, value := range []string{parsed.Host, parsed.Hostname()} {
			if value != "" {
				details = strings.ReplaceAll(details, value, secrets.RedactedValue)
			}
		}
	}
	details = settingsValidationURLAuthorityPattern.ReplaceAllString(details, secrets.RedactedValue)
	return secrets.RedactText(details)
}
