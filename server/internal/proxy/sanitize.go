package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

const (
	// Keep response sanitization memory-bounded. Arr list/history responses are
	// normally far smaller than this; an oversized JSON response is rejected
	// rather than passed through unsanitized.
	maxSanitizedJSONResponseSize int64 = 32 << 20
	redactedValue                      = "[REDACTED]"
	maxJSONSniffBytes                  = 4 << 10
)

var (
	errEncodedJSONResponse     = errors.New("encoded JSON response")
	errJSONResponseTooLarge    = errors.New("JSON response exceeds sanitizer limit")
	errInvalidJSONResponse     = errors.New("invalid JSON response")
	errMalformedJSONMediaType  = errors.New("malformed JSON media type")
	errStreamingJSONResponse   = errors.New("streaming JSON response cannot be sanitized")
	errJSONResponseSniffFailed = errors.New("could not classify upstream response")
	errUnsanitizableUpgrade    = errors.New("protocol upgrade cannot be sanitized")
)

// sanitizeProxyResponse removes credentials that an arr (or one of its
// integrations) embeds in an otherwise user-readable response. In particular,
// history records can contain a downloadUrl copied from an indexer, including
// that indexer's API key.
func sanitizeProxyResponse(resp *http.Response) error {
	sanitizeResponseHeaders(resp.Header)
	// An arr sits behind Cantinarr's authenticated boundary; its responses are
	// per-user and must never be stored by a shared cache. Strip upstream
	// cache negotiation and the nginx/lighttpd internal-redirect controls that
	// would otherwise let a fronting reverse proxy re-serve or re-route the body.
	sanitizeUpstreamControlHeaders(resp.Header)
	enforcePrivateProxyCachePolicy(resp.Header)

	if responseHasNoBody(resp) {
		return nil
	}
	sanitize, err := shouldSanitizeJSON(resp)
	if err != nil {
		return err
	}
	if !sanitize {
		return nil
	}
	if encoding := strings.TrimSpace(resp.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return errEncodedJSONResponse
	}
	if resp.ContentLength > maxSanitizedJSONResponseSize {
		return errJSONResponseTooLarge
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSanitizedJSONResponseSize+1))
	if err != nil {
		return fmt.Errorf("read JSON response: %w", err)
	}
	_ = resp.Body.Close()
	if int64(len(body)) > maxSanitizedJSONResponseSize {
		return errJSONResponseTooLarge
	}
	if len(body) == 0 {
		resp.Body = http.NoBody
		resp.ContentLength = 0
		resp.Header.Set("Content-Length", "0")
		return nil
	}

	sanitized, err := sanitizeJSON(body)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(sanitized))
	resp.ContentLength = int64(len(sanitized))
	resp.Header.Set("Content-Length", strconv.Itoa(len(sanitized)))
	// These validators describe the upstream representation, not the rewritten
	// body. Leaving them in place can make a cache serve or reject the wrong
	// bytes.
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-MD5")
	resp.Header.Del("Digest")
	resp.Header.Del("ETag")
	resp.Header.Del("Last-Modified")
	resp.Header.Del("Trailer")
	resp.Trailer = nil
	return nil
}

// sanitizeUpstreamControlHeaders removes reverse-proxy control headers that an
// arr (or something it proxies) may emit. X-Accel-*/X-Sendfile/X-Lighttpd-* and
// their siblings ask a fronting web server to serve a file or perform an
// internal redirect, so forwarding them from an untrusted upstream is an
// internal-redirect vector rather than a benign header.
func sanitizeUpstreamControlHeaders(header http.Header) {
	for name := range header {
		if isUpstreamControlHeader(name) {
			header.Del(name)
		}
	}
}

func isUpstreamControlHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(name, "x-accel-") || strings.HasPrefix(name, "x-sendfile") ||
		strings.HasPrefix(name, "x-lighttpd-send") {
		return true
	}
	switch name {
	case "x-reproxy-url":
		return true
	default:
		return false
	}
}

func responseHasNoBody(resp *http.Response) bool {
	if resp.Body == nil || resp.Body == http.NoBody {
		return true
	}
	if resp.Request != nil && resp.Request.Method == http.MethodHead {
		return true
	}
	return resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified
}

// shouldSanitizeJSON classifies every Content-Type value. Explicit JSON is
// always sanitized and malformed JSON-like types fail closed. For an arr API
// response carrying an absent or misleading non-streaming type, it peeks at a
// small prefix and restores those bytes before deciding. Server-sent event and
// structured JSON stream formats fail closed because forwarding them unbuffered
// would bypass the recursive credential scrubber; no arr API Cantinarr proxies
// serves them, and the Flutter client never consumes a proxied stream.
func shouldSanitizeJSON(resp *http.Response) (bool, error) {
	isJSON, isEventStream, isJSONStream, malformedJSON := classifyContentTypes(resp.Header.Values("Content-Type"))
	if malformedJSON {
		return false, errMalformedJSONMediaType
	}
	if isEventStream || isJSONStream {
		return false, errStreamingJSONResponse
	}
	if isJSON {
		return true, nil
	}
	if !isArrAPIResponse(resp) || isKnownOpaqueArrAPIResponse(resp) {
		return false, nil
	}
	// The proxy requested identity encoding. An encoded, ambiguously typed arr
	// API body cannot be structurally sniffed and must not bypass sanitization.
	if encoding := strings.TrimSpace(resp.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return false, errEncodedJSONResponse
	}
	return sniffStructuredJSON(resp)
}

func isKnownOpaqueArrAPIResponse(resp *http.Response) bool {
	if resp.Request == nil || resp.Request.URL == nil {
		return false
	}
	apiPath, ok := arrAPIPathSuffix(resp.Request.URL.Path)
	return ok && (strings.HasPrefix(apiPath, "/api/v1/log/file/") || strings.HasPrefix(apiPath, "/api/v3/log/file/"))
}

func classifyContentTypes(values []string) (isJSON, isEventStream, isJSONStream, malformedJSON bool) {
	for _, headerValue := range values {
		for _, contentType := range splitContentTypeValues(headerValue) {
			mediaType, _, err := mime.ParseMediaType(contentType)
			if err != nil {
				lowered := strings.ToLower(contentType)
				if strings.Contains(lowered, "json") {
					malformedJSON = true
				}
				// A malformed streaming type cannot be classified, so treat it as
				// a stream and fail closed rather than passing it through opaque.
				if strings.Contains(lowered, "event-stream") {
					isEventStream = true
				}
				continue
			}
			mediaType = strings.ToLower(mediaType)
			if mediaType == "text/event-stream" {
				isEventStream = true
				continue
			}
			if isStructuredJSONStreamMediaType(mediaType) {
				isJSONStream = true
				continue
			}
			if mediaType == "application/json" || mediaType == "text/json" || strings.HasSuffix(mediaType, "+json") {
				isJSON = true
				continue
			}
			// A valid but unknown JSON-named media type is not safe to pass as
			// opaque bytes. Known streaming JSON types were handled above.
			if strings.Contains(mediaType, "json") {
				malformedJSON = true
			}
		}
	}
	return isJSON, isEventStream, isJSONStream, malformedJSON
}

// Content-Type is a singleton header, but intermediaries sometimes combine
// duplicates with commas. Split those values without breaking quoted media
// type parameters.
func splitContentTypeValues(value string) []string {
	var values []string
	start := 0
	quoted := false
	escaped := false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\':
			if quoted && !escaped {
				escaped = true
				continue
			}
		case '"':
			if !escaped {
				quoted = !quoted
			}
		case ',':
			if !quoted {
				if part := strings.TrimSpace(value[start:i]); part != "" {
					values = append(values, part)
				}
				start = i + 1
			}
		}
		escaped = false
	}
	if part := strings.TrimSpace(value[start:]); part != "" {
		values = append(values, part)
	}
	return values
}

func isStructuredJSONStreamMediaType(mediaType string) bool {
	switch mediaType {
	case "application/x-ndjson", "application/ndjson", "application/json-seq", "application/stream+json":
		return true
	default:
		return false
	}
}

func isArrAPIResponse(resp *http.Response) bool {
	if resp.Request == nil || resp.Request.URL == nil {
		return false
	}
	_, ok := arrAPIPathSuffix(resp.Request.URL.Path)
	return ok
}

// arrAPIPathSuffix locates a versioned arr API root after any configured
// instance base path. ModifyResponse observes the outbound URL, so an instance
// configured as https://host/sonarr produces /sonarr/api/v3/... here rather
// than the client-facing /api/v3/... path.
func arrAPIPathSuffix(requestPath string) (string, bool) {
	for _, root := range []string{"/api/v1", "/api/v3"} {
		searchFrom := 0
		for searchFrom < len(requestPath) {
			relative := strings.Index(requestPath[searchFrom:], root)
			if relative < 0 {
				break
			}
			start := searchFrom + relative
			end := start + len(root)
			if end == len(requestPath) || requestPath[end] == '/' {
				return requestPath[start:], true
			}
			searchFrom = end
		}
	}
	return "", false
}

type prefixedReadCloser struct {
	io.Reader
	io.Closer
}

// sniffStructuredJSON peeks through leading JSON whitespace (and an optional
// UTF-8 BOM), then restores every byte to the response body. More than 4 KiB of
// leading whitespace is treated as JSON-like and therefore fails closed unless
// the complete body parses as JSON.
func sniffStructuredJSON(resp *http.Response) (bool, error) {
	original := resp.Body
	prefix := make([]byte, 0, maxJSONSniffBytes)
	buffer := make([]byte, 512)
	restore := func() {
		if len(prefix) == 0 {
			return
		}
		resp.Body = &prefixedReadCloser{
			Reader: io.MultiReader(bytes.NewReader(prefix), original),
			Closer: original,
		}
	}

	for len(prefix) < maxJSONSniffBytes {
		remaining := maxJSONSniffBytes - len(prefix)
		readBuffer := buffer
		if remaining < len(readBuffer) {
			readBuffer = readBuffer[:remaining]
		}
		n, err := original.Read(readBuffer)
		if n > 0 {
			prefix = append(prefix, readBuffer[:n]...)
			if found, jsonLike := structuredJSONStart(prefix); found {
				restore()
				return jsonLike, nil
			}
		}
		if err == io.EOF {
			restore()
			return false, nil
		}
		if err != nil {
			restore()
			return false, errJSONResponseSniffFailed
		}
		if n == 0 {
			restore()
			return false, nil
		}
	}
	restore()
	return true, nil
}

func structuredJSONStart(prefix []byte) (found, jsonLike bool) {
	start := 0
	bom := []byte{0xef, 0xbb, 0xbf}
	if len(prefix) < len(bom) && bytes.Equal(prefix, bom[:len(prefix)]) {
		return false, false
	}
	if bytes.HasPrefix(prefix, bom) {
		start = len(bom)
	}
	for _, b := range prefix[start:] {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[', '"', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 't', 'f', 'n':
			return true, true
		default:
			return true, false
		}
	}
	return false, false
}

func sanitizeJSON(body []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errInvalidJSONResponse
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errInvalidJSONResponse
	}
	return json.Marshal(sanitizeJSONValue(value))
}

func sanitizeJSONValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		// Provider schemas often represent settings as {name/key, value}
		// pairs. In that shape the sensitive name is data rather than an object
		// key, so redact its sibling value explicitly.
		for _, discriminator := range []string{"name", "key"} {
			if dynamicName, ok := stringMapValue(value, discriminator); ok && isSensitiveQueryName(dynamicName) {
				for actualKey := range value {
					if strings.EqualFold(actualKey, "value") {
						value[actualKey] = redactedValue
					}
				}
			}
		}
		for key, child := range value {
			if isSensitiveObjectName(key) {
				value[key] = redactedValue
				continue
			}
			value[key] = sanitizeJSONValue(child)
		}
		return value
	case []any:
		for i, child := range value {
			value[i] = sanitizeJSONValue(child)
		}
		return value
	case string:
		return sanitizeURLCredentials(value)
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

// isSensitiveObjectName deliberately normalizes common casing/separator variants.
// Suffix matching covers provider-specific names such as prowlarrApiKey and
// webhookToken without treating an ordinary field named "key" as a secret.
func isSensitiveObjectName(name string) bool {
	n := normalizeSensitiveName(name)
	if n == "auth" || n == "cookie" || n == "setcookie" {
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
	return false
}

// Query parameters are access-bearing more often than ordinary object fields,
// so their rule is intentionally stricter: a bare key or signed-URL credential
// is redacted without forcing every JSON property named "key" to disappear.
func isSensitiveQueryName(name string) bool {
	if isSensitiveObjectName(name) {
		return true
	}
	n := normalizeSensitiveName(name)
	switch n {
	case "key", "sig", "signature", "authkey", "accesskey", "awsaccesskeyid":
		return true
	}
	for _, suffix := range []string{"signature", "credential", "credentials", "authkey", "accesskey", "signingkey"} {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return false
}

func normalizeSensitiveName(name string) string {
	var normalized strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

// sanitizeURLCredentials strips URL userinfo and rewrites sensitive query
// values. The targeted userinfo rewrite preserves the rest of the URL spelling;
// query redaction preserves parameter order, duplicate keys, and fragments.
// Relative URLs are supported because arr history records occasionally contain
// them.
func sanitizeURLCredentials(raw string) string {
	raw = stripURLUserinfo(raw)
	return redactURLQuery(raw)
}

func stripURLUserinfo(raw string) string {
	authorityStart := -1
	if strings.HasPrefix(raw, "//") {
		authorityStart = 2
	} else if delimiter := strings.Index(raw, "://"); delimiter > 0 && isURLScheme(raw[:delimiter]) {
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
	userinfoEnd := strings.LastIndexByte(authority, '@')
	if userinfoEnd < 0 {
		return raw
	}
	return raw[:authorityStart] + authority[userinfoEnd+1:] + raw[authorityEnd:]
}

func isURLScheme(value string) bool {
	if value == "" || !isASCIIAlpha(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		b := value[i]
		if !isASCIIAlpha(b) && (b < '0' || b > '9') && b != '+' && b != '-' && b != '.' {
			return false
		}
	}
	return true
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

// redactURLQuery rewrites only sensitive query values while preserving the URL
// spelling, parameter order, duplicate keys, and fragment.
func redactURLQuery(raw string) string {
	question := strings.IndexByte(raw, '?')
	if question < 0 {
		return raw
	}
	queryEnd := len(raw)
	if fragment := strings.IndexByte(raw[question+1:], '#'); fragment >= 0 {
		queryEnd = question + 1 + fragment
	}
	rawQuery := raw[question+1 : queryEnd]
	if rawQuery == "" {
		return raw
	}

	parts := strings.Split(rawQuery, "&")
	changed := false
	for i, part := range parts {
		key, _, _ := strings.Cut(part, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err != nil || !isSensitiveQueryName(decodedKey) {
			continue
		}
		changed = true
		parts[i] = key + "=" + url.QueryEscape(redactedValue)
	}
	if !changed {
		return raw
	}
	return raw[:question+1] + strings.Join(parts, "&") + raw[queryEnd:]
}

// enforcePrivateProxyCachePolicy marks a proxy-generated error response as
// uncacheable and strips upstream shared-cache directives so an intermediary
// cannot serve it to another client.
func enforcePrivateProxyCachePolicy(header http.Header) {
	for name := range header {
		if isUpstreamSharedCacheDirective(name) {
			header.Del(name)
		}
	}
	header.Set("Cache-Control", "private, no-store")
	header.Set("Pragma", "no-cache")
}

func isUpstreamSharedCacheDirective(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "cdn-cache-control" || strings.HasSuffix(name, "-cdn-cache-control") {
		return true
	}
	switch name {
	case "age", "akamai-cache-control", "cache-control", "edge-control", "expires", "pragma", "surrogate-control", "x-cache-control", "x-edge-control":
		return true
	default:
		return false
	}
}

func sanitizeResponseHeaders(header http.Header) {
	for name := range header {
		if isSensitiveQueryName(name) {
			header.Del(name)
		}
	}
	rewriteResponseHeader(header, "Location", sanitizeDirectURLHeader)
	rewriteResponseHeader(header, "Content-Location", sanitizeDirectURLHeader)
	rewriteResponseHeader(header, "Link", sanitizeLinkHeader)
	rewriteResponseHeader(header, "Refresh", sanitizeRefreshHeader)
}

func rewriteResponseHeader(header http.Header, name string, sanitize func(string) (string, bool)) {
	values := header.Values(name)
	if len(values) == 0 {
		return
	}
	header.Del(name)
	for _, value := range values {
		if sanitized, ok := sanitize(value); ok {
			header.Add(name, sanitized)
		}
	}
}

func sanitizeDirectURLHeader(value string) (string, bool) {
	return sanitizeURLCredentials(strings.TrimSpace(value)), true
}

// Link URLs are the URI references enclosed in angle brackets. Malformed Link
// values are dropped rather than risking an uninspected credential-bearing URL.
func sanitizeLinkHeader(value string) (string, bool) {
	var sanitized strings.Builder
	rest := value
	foundURL := false
	for {
		open := strings.IndexByte(rest, '<')
		if open < 0 {
			if !foundURL {
				return "", false
			}
			sanitized.WriteString(rest)
			return sanitized.String(), true
		}
		sanitized.WriteString(rest[:open+1])
		rest = rest[open+1:]
		close := strings.IndexByte(rest, '>')
		if close < 0 {
			return "", false
		}
		sanitized.WriteString(sanitizeURLCredentials(strings.TrimSpace(rest[:close])))
		sanitized.WriteByte('>')
		rest = rest[close+1:]
		foundURL = true
	}
}

// Refresh permits either a numeric delay or "delay; url=...". Invalid values
// are removed because browser parsing of malformed refresh syntax is permissive
// and could otherwise expose a credential-bearing URL we failed to recognize.
func sanitizeRefreshHeader(value string) (string, bool) {
	value = strings.TrimSpace(value)
	delay, assignment, hasURL := strings.Cut(value, ";")
	if _, err := strconv.ParseFloat(strings.TrimSpace(delay), 64); err != nil {
		return "", false
	}
	if !hasURL {
		return value, true
	}

	name, rawURL, ok := strings.Cut(assignment, "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(name), "url") {
		return "", false
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", false
	}
	quote := byte(0)
	if rawURL[0] == '\'' || rawURL[0] == '"' {
		quote = rawURL[0]
		if len(rawURL) < 2 || rawURL[len(rawURL)-1] != quote {
			return "", false
		}
		rawURL = rawURL[1 : len(rawURL)-1]
	}
	sanitizedURL := sanitizeURLCredentials(rawURL)
	if quote != 0 {
		sanitizedURL = string(quote) + sanitizedURL + string(quote)
	}
	return strings.TrimSpace(delay) + "; url=" + sanitizedURL, true
}
