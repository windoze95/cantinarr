package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// RedactedValue is used anywhere diagnostic data contained an access-bearing
// value. It is deliberately conspicuous so operators can still understand why
// a URL, header, or provider response looks incomplete.
const RedactedValue = "[REDACTED]"

var (
	urlCandidatePattern            = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*:)?//[^\s<>"']+|/[^\s<>"']*\?[^\s<>"']+`)
	assignmentPattern              = regexp.MustCompile(`(?i)[a-z][a-z0-9_.-]{0,80}["']?[ \t]*[:=][ \t]*`)
	bareProviderCredentialPatterns = []*regexp.Regexp{
		// OpenAI project/legacy keys and Anthropic sk-ant-* keys share the
		// sk- prefix. Require a non-trivial suffix to avoid scrubbing prose.
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`),
		// Google API keys use AIza..., while newer Gemini credentials may use
		// the AQ. prefix.
		regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{12,}\b`),
		regexp.MustCompile(`\bAQ\.[A-Za-z0-9_-]{12,}\b`),
	}
)

// RedactText removes credentials from structured JSON and from free-form
// diagnostic text. It covers nested strings, URL userinfo/query parameters,
// and header/assignment forms such as Authorization, X-Api-Key, and password.
// It is intended for trust-boundary output, not for values that must later be
// used as credentials.
func RedactText(text string) string {
	if text == "" {
		return ""
	}
	if value, ok := decodeSingleJSON(text); ok {
		encoded, err := json.Marshal(redactJSONValue(value))
		if err == nil {
			return string(encoded)
		}
	}
	return redactFreeform(text)
}

// RedactJSONValue returns a JSON-compatible, recursively redacted copy of v.
// Callers should fail closed (drop the value) when v cannot be represented as
// JSON instead of forwarding an unsanitized provider-specific object.
func RedactJSONValue(v any) (any, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	value, ok := decodeSingleJSON(string(encoded))
	if !ok {
		return nil, errors.New("value is not a single JSON document")
	}
	return redactJSONValue(value), nil
}

// RedactError returns an error with only its scrubbed human-readable message.
// Deliberately do not wrap the original: its Error method may expose an
// upstream response body or credential-bearing URL through a later formatter.
func RedactError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(RedactText(err.Error()))
}

func decodeSingleJSON(text string) (any, bool) {
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return value, true
	}
	return nil, false
}

func redactJSONValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		// Many APIs encode settings as {name/key, value} pairs. The sensitive
		// name is data in that shape, rather than an object key.
		for _, discriminator := range []string{"name", "key"} {
			if dynamicName, ok := stringMapValue(value, discriminator); ok && isSensitiveName(dynamicName, true) {
				for actualKey := range value {
					if strings.EqualFold(actualKey, "value") {
						value[actualKey] = RedactedValue
					}
				}
			}
		}
		for key, child := range value {
			if isSensitiveName(key, false) {
				value[key] = RedactedValue
				continue
			}
			value[key] = redactJSONValue(child)
		}
		return value
	case []any:
		for i, child := range value {
			value[i] = redactJSONValue(child)
		}
		return value
	case string:
		return redactFreeform(value)
	default:
		return value
	}
}

func stringMapValue(value map[string]any, name string) (string, bool) {
	for key, raw := range value {
		if strings.EqualFold(key, name) {
			text, ok := raw.(string)
			return text, ok
		}
	}
	return "", false
}

func redactFreeform(text string) string {
	text = urlCandidatePattern.ReplaceAllStringFunc(text, redactURLCredentials)
	text = redactAssignments(text)
	for _, pattern := range bareProviderCredentialPatterns {
		text = pattern.ReplaceAllString(text, RedactedValue)
	}
	return text
}

// redactAssignments handles both headers and fragments embedded in a larger
// error body. Valid JSON is handled structurally first, but upstream failures
// often prefix a JSON body with prose and therefore need this conservative
// scanner too.
func redactAssignments(text string) string {
	matches := assignmentPattern.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var out strings.Builder
	last := 0
	for _, match := range matches {
		if match[0] < last {
			continue
		}
		prefix := text[match[0]:match[1]]
		key := assignmentKey(prefix)
		if !isSensitiveName(key, true) {
			continue
		}
		valueStart := match[1]
		valueEnd := assignmentValueEnd(text, valueStart, normalizeName(key))
		out.WriteString(text[last:valueStart])
		out.WriteString(RedactedValue)
		last = valueEnd
	}
	if last == 0 {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

func assignmentKey(prefix string) string {
	end := strings.IndexAny(prefix, ":=")
	if end < 0 {
		return ""
	}
	key := strings.TrimSpace(prefix[:end])
	return strings.Trim(key, "\"'")
}

func assignmentValueEnd(text string, start int, normalizedKey string) int {
	if start >= len(text) {
		return start
	}
	if text[start] == '\'' || text[start] == '"' {
		quote := text[start]
		escaped := false
		for i := start + 1; i < len(text); i++ {
			if text[i] == quote && !escaped {
				return i + 1
			}
			if text[i] == '\\' && !escaped {
				escaped = true
			} else {
				escaped = false
			}
		}
		return len(text)
	}

	// Authentication and cookie headers commonly contain spaces or multiple
	// attributes. Redact the whole logical field, stopping before a JSON/object
	// separator so a surrounding diagnostic remains useful.
	wide := normalizedKey == "authorization" || strings.HasSuffix(normalizedKey, "authorization") ||
		normalizedKey == "auth" || normalizedKey == "cookie" || normalizedKey == "setcookie"
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '\r', '\n':
			return i
		case ',', '}', ']':
			if wide {
				return i
			}
		case '&', '#', ';', '<', '>', '"', '\'', ' ', '\t':
			if !wide {
				return i
			}
		}
	}
	return len(text)
}

func isSensitiveName(name string, query bool) bool {
	n := normalizeName(name)
	if n == "auth" || n == "cookie" || n == "setcookie" || n == "proxyauthorization" {
		return true
	}
	for _, suffix := range []string{
		"apikey", "password", "passwd", "passphrase", "privatekey",
		"secretaccesskey", "secretkey", "secret", "token",
		"authorization", "credential", "credentials",
	} {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	if query {
		switch n {
		case "key", "sig", "signature", "authkey", "accesskey", "awsaccesskeyid":
			return true
		}
		for _, suffix := range []string{"signature", "authkey", "accesskey", "signingkey"} {
			if strings.HasSuffix(n, suffix) {
				return true
			}
		}
	}
	return false
}

func normalizeName(name string) string {
	var normalized strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

func redactURLCredentials(raw string) string {
	raw = stripURLUserinfo(raw)
	question := strings.IndexByte(raw, '?')
	if question < 0 {
		return raw
	}
	queryEnd := len(raw)
	if fragment := strings.IndexByte(raw[question+1:], '#'); fragment >= 0 {
		queryEnd = question + 1 + fragment
	}
	parts := strings.Split(raw[question+1:queryEnd], "&")
	changed := false
	for i, part := range parts {
		key, _, _ := strings.Cut(part, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err != nil || !isSensitiveName(decodedKey, true) {
			continue
		}
		parts[i] = key + "=" + url.QueryEscape(RedactedValue)
		changed = true
	}
	if !changed {
		return raw
	}
	return raw[:question+1] + strings.Join(parts, "&") + raw[queryEnd:]
}

func stripURLUserinfo(raw string) string {
	authorityStart := -1
	if strings.HasPrefix(raw, "//") {
		authorityStart = 2
	} else if delimiter := strings.Index(raw, "://"); delimiter > 0 {
		authorityStart = delimiter + 3
	}
	if authorityStart < 0 {
		return raw
	}
	authorityEnd := len(raw)
	if boundary := strings.IndexAny(raw[authorityStart:], "/?#"); boundary >= 0 {
		authorityEnd = authorityStart + boundary
	}
	authority := raw[authorityStart:authorityEnd]
	if userinfoEnd := strings.LastIndexByte(authority, '@'); userinfoEnd >= 0 {
		return raw[:authorityStart] + authority[userinfoEnd+1:] + raw[authorityEnd:]
	}
	return raw
}
