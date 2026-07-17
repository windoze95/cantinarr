package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const (
	// Keep response sanitization memory-bounded. Arr list/history responses are
	// normally far smaller than this; an oversized JSON response is rejected
	// rather than passed through unsanitized.
	maxSanitizedJSONResponseSize int64 = 32 << 20
	redactedValue                      = "[REDACTED]"
	maxJSONSniffBytes                  = 4 << 10
	maxSanitizedSSEEventSize           = 256 << 10
	// Nested redirect parameters are commonly encoded once or twice. Bound the
	// number of whole-value decoding passes so a value such as %252525... cannot
	// turn one response string into quadratic CPU work. Reaching the bound fails
	// closed instead of treating a deeper encoding as safe.
	maxCredentialDecodePasses = 8
)

var (
	errEncodedJSONResponse     = errors.New("encoded JSON response")
	errJSONResponseTooLarge    = errors.New("JSON response exceeds sanitizer limit")
	errInvalidJSONResponse     = errors.New("invalid JSON response")
	errMalformedJSONMediaType  = errors.New("malformed JSON media type")
	errUnsanitizableStream     = errors.New("streaming response cannot be sanitized")
	errJSONResponseSniffFailed = errors.New("could not classify upstream response")
	errUnclassifiedArrResponse = errors.New("unclassified arr API response")
	errMalformedSSEResponse    = errors.New("malformed server-sent event response")
	errSSEEventTooLarge        = errors.New("server-sent event exceeds sanitizer limit")
	errUnsanitizableUpgrade    = errors.New("protocol upgrade cannot be sanitized")
)

type responseSanitizationMode uint8

const (
	responseModeOpaque responseSanitizationMode = iota
	responseModeJSON
	responseModeSSE
)

// sanitizeProxyResponse removes credentials that an arr (or one of its
// integrations) embeds in an otherwise user-readable response. In particular,
// history records can contain a downloadUrl copied from an indexer, including
// that indexer's API key.
func sanitizeProxyResponse(resp *http.Response) error {
	sanitizeResponseHeaders(resp.Header)
	enforcePrivateProxyCachePolicy(resp.Header)

	if responseHasNoBody(resp) {
		return nil
	}
	mode, err := proxyResponseSanitizationMode(resp)
	if err != nil {
		return err
	}
	if mode == responseModeOpaque {
		return nil
	}
	if mode == responseModeSSE {
		return sanitizeSSEResponse(resp)
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
	clearRewrittenRepresentationHeaders(resp)
	return nil
}

func clearRewrittenRepresentationHeaders(resp *http.Response) {
	resp.Header.Del("Accept-Ranges")
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Digest")
	resp.Header.Del("Content-MD5")
	resp.Header.Del("Content-Range")
	resp.Header.Del("Digest")
	resp.Header.Del("ETag")
	resp.Header.Del("Last-Modified")
	resp.Header.Del("Repr-Digest")
	resp.Header.Del("Trailer")
	resp.Trailer = nil
}

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

func responseHasNoBody(resp *http.Response) bool {
	if resp.Body == nil || resp.Body == http.NoBody {
		return true
	}
	if resp.Request != nil && resp.Request.Method == http.MethodHead {
		return true
	}
	return resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified
}

// shouldSanitizeJSON is kept as the narrow classifier used by unit tests. The
// response modifier uses proxyResponseSanitizationMode so intended SSE can take
// its separate bounded streaming sanitizer path.
func shouldSanitizeJSON(resp *http.Response) (bool, error) {
	mode, err := proxyResponseSanitizationMode(resp)
	return mode == responseModeJSON, err
}

// proxyResponseSanitizationMode classifies every Content-Type value. Explicit
// JSON is always sanitized and malformed JSON-like types fail closed. Declared
// SSE is accepted only on the exact versioned arr event endpoints and is still
// rewritten event-by-event. Structured JSON streams remain unsupported. For a
// versioned arr API response with an absent or misleading type, only a body
// that sniffs as JSON may proceed; non-empty XSSI/SSE/text prefixes fail closed
// unless the route is explicitly known to be opaque.
func proxyResponseSanitizationMode(resp *http.Response) (responseSanitizationMode, error) {
	isJSON, isEventStream, isJSONStream, hasOtherType, malformedJSON := classifyContentTypes(resp.Header.Values("Content-Type"))
	if malformedJSON {
		return responseModeOpaque, errMalformedJSONMediaType
	}
	if isJSONStream {
		return responseModeOpaque, errUnsanitizableStream
	}
	if isEventStream {
		if isJSON || hasOtherType || !isIntendedArrEventResponse(resp) {
			return responseModeOpaque, errUnsanitizableStream
		}
		if encoding := strings.TrimSpace(resp.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
			return responseModeOpaque, errUnsanitizableStream
		}
		return responseModeSSE, nil
	}
	if isJSON {
		return responseModeJSON, nil
	}
	if !isArrAPIResponse(resp) {
		return responseModeOpaque, nil
	}
	// The proxy requested identity encoding. An encoded, ambiguously typed arr
	// API body cannot be structurally sniffed and must not bypass sanitization.
	if encoding := strings.TrimSpace(resp.Header.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return responseModeOpaque, errEncodedJSONResponse
	}
	knownOpaque := isKnownOpaqueArrAPIResponse(resp)
	var jsonLike, nonEmpty bool
	var err error
	if knownOpaque {
		jsonLike, nonEmpty, err = sniffStructuredJSONObjectOrArray(resp)
	} else {
		jsonLike, nonEmpty, err = sniffStructuredJSON(resp)
	}
	if err != nil {
		return responseModeOpaque, err
	}
	if jsonLike {
		return responseModeJSON, nil
	}
	if knownOpaque {
		return responseModeOpaque, nil
	}
	if nonEmpty {
		return responseModeOpaque, errUnclassifiedArrResponse
	}
	return responseModeOpaque, nil
}

func isKnownOpaqueArrAPIResponse(resp *http.Response) bool {
	if resp.Request == nil || resp.Request.URL == nil {
		return false
	}
	apiPath, ok := arrAPIPathSuffix(resp.Request.URL.Path)
	if !ok {
		return false
	}
	lowerAPIPath := strings.ToLower(apiPath)
	for _, prefix := range []string{
		"/api/v1/log/file/",
		"/api/v3/log/file/",
		"/api/v1/mediacover/",
		"/api/v3/mediacover/",
	} {
		if strings.HasPrefix(lowerAPIPath, prefix) {
			return true
		}
	}
	return false
}

func isIntendedArrEventResponse(resp *http.Response) bool {
	if resp.Request == nil || resp.Request.URL == nil {
		return false
	}
	apiPath, ok := arrAPIPathSuffix(resp.Request.URL.Path)
	lowerAPIPath := strings.ToLower(apiPath)
	return ok && (lowerAPIPath == "/api/v1/events" || lowerAPIPath == "/api/v3/events")
}

func classifyContentTypes(values []string) (isJSON, isEventStream, isJSONStream, hasOtherType, malformedJSON bool) {
	for _, headerValue := range values {
		for _, contentType := range splitContentTypeValues(headerValue) {
			mediaType, _, err := mime.ParseMediaType(contentType)
			if err != nil {
				lowerContentType := strings.ToLower(contentType)
				if strings.Contains(lowerContentType, "event-stream") {
					isEventStream = true
				}
				if strings.Contains(lowerContentType, "json") {
					malformedJSON = true
				}
				hasOtherType = true
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
				continue
			}
			hasOtherType = true
		}
	}
	return isJSON, isEventStream, isJSONStream, hasOtherType, malformedJSON
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
	// Kestrel/Servarr routing is case-insensitive and normalizes dot segments.
	// Classify the effective route the upstream sees rather than a spelling that
	// could otherwise disguise an API response as an opaque non-arr resource.
	hadTrailingSlash := len(requestPath) > 1 &&
		(strings.HasSuffix(requestPath, "/") || strings.HasSuffix(requestPath, "\\"))
	requestPath = path.Clean(strings.ReplaceAll(requestPath, "\\", "/"))
	if hadTrailingSlash && requestPath != "/" {
		requestPath += "/"
	}
	lowerRequestPath := strings.ToLower(requestPath)
	bestStart := -1
	for _, root := range []string{"/api/v1", "/api/v3"} {
		searchFrom := 0
		for searchFrom < len(lowerRequestPath) {
			relative := strings.Index(lowerRequestPath[searchFrom:], root)
			if relative < 0 {
				break
			}
			start := searchFrom + relative
			end := start + len(root)
			if end == len(requestPath) || requestPath[end] == '/' {
				if bestStart < 0 || start < bestStart {
					bestStart = start
				}
				break
			}
			searchFrom = end
		}
	}
	if bestStart < 0 {
		return "", false
	}
	return requestPath[bestStart:], true
}

type prefixedReadCloser struct {
	io.Reader
	io.Closer
}

// sniffStructuredJSON peeks through leading JSON whitespace (and an optional
// UTF-8 BOM), then restores every byte to the response body. More than 4 KiB of
// leading whitespace is treated as JSON-like and therefore fails closed unless
// the complete body parses as JSON.
func sniffStructuredJSON(resp *http.Response) (jsonLike, nonEmpty bool, err error) {
	return sniffJSONPrefix(resp, structuredJSONStart)
}

// Opaque cover/log routes still need to recognize a mislabeled structured API
// error, but a log line commonly begins with a JSON scalar-looking timestamp.
// Limit the fallback to the object/array shapes used by Servarr error bodies so
// genuine text and binary streams remain byte-identical and unbuffered.
func sniffStructuredJSONObjectOrArray(resp *http.Response) (jsonLike, nonEmpty bool, err error) {
	return sniffJSONPrefix(resp, structuredJSONObjectOrArrayStart)
}

func sniffJSONPrefix(resp *http.Response, classify func([]byte) (found, jsonLike bool)) (jsonLike, nonEmpty bool, err error) {
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
			if found, jsonLike := classify(prefix); found {
				restore()
				return jsonLike, true, nil
			}
		}
		if err == io.EOF {
			restore()
			return false, len(prefix) > 0, nil
		}
		if err != nil {
			restore()
			return false, len(prefix) > 0, errJSONResponseSniffFailed
		}
		if n == 0 {
			restore()
			return false, len(prefix) > 0, errJSONResponseSniffFailed
		}
	}
	restore()
	return true, true, nil
}

func structuredJSONObjectOrArrayStart(prefix []byte) (found, jsonLike bool) {
	found, _ = structuredJSONStart(prefix)
	if !found {
		return false, false
	}
	start := 0
	bom := []byte{0xef, 0xbb, 0xbf}
	if bytes.HasPrefix(prefix, bom) {
		start = len(bom)
	}
	for _, b := range prefix[start:] {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			return true, true
		default:
			return true, false
		}
	}
	return false, false
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

// sanitizedSSEReadCloser emits at most one already-sanitized event per source
// read cycle. It deliberately has no background worker: closing the downstream
// response closes the upstream body directly, which also interrupts a blocked
// read without leaving a goroutine behind.
type sanitizedSSEReadCloser struct {
	source    io.ReadCloser
	reader    *bufio.Reader
	pending   []byte
	closeOnce sync.Once
}

func sanitizeSSEResponse(resp *http.Response) error {
	body := &sanitizedSSEReadCloser{
		source: resp.Body,
		reader: bufio.NewReader(resp.Body),
	}

	// Validate and sanitize the first complete event before ModifyResponse
	// succeeds. That gives malformed declarations a 502 instead of committing a
	// successful response whose first bytes could not be inspected safely.
	lines, eof, err := readSSEEvent(body.reader)
	if err != nil {
		_ = body.Close()
		return err
	}
	if eof {
		_ = body.Close()
		resp.Body = http.NoBody
		resp.ContentLength = 0
		resp.Header.Set("Content-Length", "0")
		clearRewrittenRepresentationHeaders(resp)
		return nil
	}
	body.pending, err = sanitizeSSEEvent(lines, true)
	if err != nil {
		_ = body.Close()
		return err
	}

	resp.Body = body
	resp.ContentLength = -1
	resp.Header.Del("Content-Length")
	clearRewrittenRepresentationHeaders(resp)
	return nil
}

func (b *sanitizedSSEReadCloser) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(b.pending) == 0 {
		lines, eof, err := readSSEEvent(b.reader)
		if err != nil {
			_ = b.Close()
			return 0, err
		}
		if eof {
			_ = b.Close()
			return 0, io.EOF
		}
		b.pending, err = sanitizeSSEEvent(lines, false)
		if err != nil {
			_ = b.Close()
			return 0, err
		}
	}

	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *sanitizedSSEReadCloser) Close() error {
	b.closeOnce.Do(func() {
		// Do not surface an upstream-provided close error. ReverseProxy does not
		// need it, and a hostile ReadCloser must not get error text into logs.
		_ = b.source.Close()
	})
	return nil
}

// readSSEEvent reads through one blank-line delimiter. Both input and output
// are bounded per event, and LF, CRLF, and bare CR line endings are accepted as
// required by the event-stream grammar. A partial final event is rejected.
func readSSEEvent(reader *bufio.Reader) (lines [][]byte, eof bool, err error) {
	line := make([]byte, 0, 128)
	eventBytes := 0
	sawByte := false

	finishLine := func() bool {
		if len(line) == 0 {
			return true
		}
		lines = append(lines, line)
		line = make([]byte, 0, 128)
		return false
	}

	for {
		b, readErr := reader.ReadByte()
		if readErr != nil {
			if readErr == io.EOF && !sawByte {
				return nil, true, nil
			}
			return nil, false, errMalformedSSEResponse
		}
		sawByte = true
		eventBytes++
		if eventBytes > maxSanitizedSSEEventSize {
			return nil, false, errSSEEventTooLarge
		}

		switch b {
		case '\n':
			if finishLine() {
				return lines, false, nil
			}
		case '\r':
			next, peekErr := reader.Peek(1)
			if peekErr == nil && next[0] == '\n' {
				_, _ = reader.ReadByte()
				eventBytes++
				if eventBytes > maxSanitizedSSEEventSize {
					return nil, false, errSSEEventTooLarge
				}
			} else if peekErr != nil && peekErr != io.EOF {
				return nil, false, errMalformedSSEResponse
			}
			if finishLine() {
				return lines, false, nil
			}
		default:
			line = append(line, b)
		}
	}
}

type sanitizedSSEField struct {
	name  string
	value string
}

func sanitizeSSEEvent(lines [][]byte, firstEvent bool) ([]byte, error) {
	fields := make([]sanitizedSSEField, 0, len(lines))
	dataValues := make([][]byte, 0, len(lines))
	bom := []byte{0xef, 0xbb, 0xbf}

	for i, rawLine := range lines {
		line := rawLine
		if firstEvent && i == 0 && bytes.HasPrefix(line, bom) {
			line = line[len(bom):]
		}
		if !utf8.Valid(line) || bytes.IndexByte(line, 0) >= 0 {
			return nil, errMalformedSSEResponse
		}
		if len(line) == 0 {
			continue
		}
		if line[0] == ':' {
			// Comments can be useful as keepalives, but their content has no
			// application semantics and may contain arbitrary upstream text.
			fields = append(fields, sanitizedSSEField{name: ":"})
			continue
		}

		name := line
		value := []byte(nil)
		if colon := bytes.IndexByte(line, ':'); colon >= 0 {
			name = line[:colon]
			value = line[colon+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}

		switch string(name) {
		case "data":
			dataValues = append(dataValues, value)
		case "event":
			eventName := string(value)
			if eventName != "" && !isSafeSSEEventName(eventName) {
				eventName = redactedValue
			}
			fields = append(fields, sanitizedSSEField{name: "event", value: eventName})
		case "id":
			id := sanitizeURLCredentials(string(value))
			if id != "" && !isSafeSSEEventID(id) {
				id = redactedValue
			}
			fields = append(fields, sanitizedSSEField{name: "id", value: id})
		case "retry":
			if len(value) == 0 || !isASCIIDigits(value) {
				return nil, errMalformedSSEResponse
			}
			if _, parseErr := strconv.ParseUint(string(value), 10, 64); parseErr != nil {
				return nil, errMalformedSSEResponse
			}
			fields = append(fields, sanitizedSSEField{name: "retry", value: string(value)})
		default:
			// Unknown fields are ignored by conforming EventSource clients. Drop
			// them instead of forwarding arbitrary extension data.
		}
	}

	var output bytes.Buffer
	for _, field := range fields {
		if field.name == ":" {
			output.WriteString(":\n")
			continue
		}
		output.WriteString(field.name)
		output.WriteByte(':')
		if field.value != "" {
			output.WriteByte(' ')
			output.WriteString(field.value)
		}
		output.WriteByte('\n')
	}
	if len(dataValues) > 0 {
		joinedData := bytes.Join(dataValues, []byte{'\n'})
		sanitizedData, sanitizeErr := sanitizeJSON(joinedData)
		if sanitizeErr != nil {
			sanitizedData = []byte(redactedValue)
		}
		output.WriteString("data: ")
		output.Write(sanitizedData)
		output.WriteByte('\n')
	}
	output.WriteByte('\n')
	if output.Len() > maxSanitizedSSEEventSize {
		return nil, errSSEEventTooLarge
	}
	return output.Bytes(), nil
}

func isSafeSSEEventName(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if !isASCIIAlpha(b) && (b < '0' || b > '9') && b != '_' && b != '-' && b != '.' {
			return false
		}
	}
	return true
}

func isSafeSSEEventID(value string) bool {
	if len(value) > 256 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func isASCIIDigits(value []byte) bool {
	for _, b := range value {
		if b < '0' || b > '9' {
			return false
		}
	}
	return len(value) > 0
}

func sanitizeJSON(body []byte) ([]byte, error) {
	return sanitizeJSONWithOutputLimit(body, maxSanitizedJSONResponseSize)
}

func sanitizeJSONWithOutputLimit(body []byte, outputLimit int64) ([]byte, error) {
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
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	// HTML escaping can expand each '<', '>', or '&' sixfold. These responses
	// are JSON API entities, not inline scripts, so disable that expansion and
	// enforce a post-redaction output bound before installing the body.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(sanitizeJSONValue(value)); err != nil {
		return nil, errInvalidJSONResponse
	}
	result := encoded.Bytes()
	if len(result) > 0 && result[len(result)-1] == '\n' {
		result = result[:len(result)-1]
	}
	if int64(len(result)) > outputLimit {
		return nil, errJSONResponseTooLarge
	}
	return result, nil
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
	if n == "auth" || n == "cookie" || n == "cookie2" || n == "dpop" || n == "setcookie" || n == "setcookie2" {
		return true
	}
	for _, suffix := range []string{
		"apikey", "password", "passwd", "passphrase", "privatekey",
		"secretaccesskey", "secretkey", "secret", "token",
		"authorization", "credential", "credentials", "jwt", "assertion",
	} {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	for _, prefix := range []string{
		"secretaccesskey", "authorization", "credentials", "credential",
		"setcookie", "cookie", "passphrase", "password", "passwd",
		"privatekey", "secretkey", "apikey", "secret", "token",
	} {
		if !strings.HasPrefix(n, prefix) {
			continue
		}
		qualifier := strings.TrimPrefix(n, prefix)
		switch qualifier {
		case "value", "values", "header", "headers", "json", "blob", "string", "text", "raw", "data", "field", "param", "parameter", "hash", "ciphertext":
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
	raw = sanitizeURLReference(raw)
	return sanitizeEmbeddedURLReferences(raw)
}

func sanitizeURLReference(raw string) string {
	raw = normalizeWHATWGURLWhitespace(raw)
	if !looksLikeURLReferenceSyntax(raw) {
		return raw
	}
	raw = normalizeWHATWGURLSlashes(raw)
	raw = stripURLUserinfo(raw)
	return redactURLQuery(raw)
}

// Browsers trim leading/trailing C0 whitespace and remove ASCII tab/newline
// characters while parsing URLs. Apply the same preprocessing only when the
// result is recognizably a URL reference so ordinary prose is not rewritten.
func normalizeWHATWGURLWhitespace(raw string) string {
	candidate := strings.TrimFunc(raw, func(r rune) bool { return r >= 0 && r <= 0x20 })
	if strings.ContainsAny(candidate, "\t\r\n") {
		candidate = strings.Map(func(r rune) rune {
			if r == '\t' || r == '\r' || r == '\n' {
				return -1
			}
			return r
		}, candidate)
	}
	if looksLikeURLReferenceSyntax(candidate) {
		return candidate
	}
	return raw
}

// sanitizeEmbeddedURLReferences scrubs credential-bearing URLs embedded in arr
// history/error prose without altering the surrounding message.
func sanitizeEmbeddedURLReferences(raw string) string {
	var output strings.Builder
	cursor := 0
	searchFrom := 0
	changed := false
	for {
		start, colon, ok := findEmbeddedURLReference(raw, searchFrom)
		if !ok {
			break
		}
		end := embeddedURLReferenceEnd(raw, colon)
		candidate := raw[start:end]
		sanitized := sanitizeURLReference(candidate)
		if sanitized != candidate {
			changed = true
		}
		output.WriteString(raw[cursor:start])
		output.WriteString(sanitized)
		cursor = end
		searchFrom = end
	}
	if !changed {
		return raw
	}
	output.WriteString(raw[cursor:])
	return output.String()
}

// embeddedURLReferenceEnd stops at either a prose delimiter or the beginning
// of another recognizable URL. Without the latter boundary, a safe first URL
// could consume a comma-, semicolon-, or Unicode-whitespace-separated second
// URL and prevent its userinfo from ever reaching the scrubber.
func embeddedURLReferenceEnd(raw string, colon int) int {
	nextStart := len(raw)
	if start, _, ok := findEmbeddedURLReference(raw, colon+1); ok {
		nextStart = start
	}
	end := colon + 1
	for end < nextStart && !isEmbeddedURLTerminator(raw[end]) {
		end++
	}
	return end
}

func findEmbeddedURLReference(raw string, searchFrom int) (start, colon int, ok bool) {
	schemeStart, schemeColon, schemeOK := findEmbeddedSchemeReference(raw, searchFrom)
	// A protocol-relative reference can win only if it starts before the first
	// scheme reference. Do not rescan the entire tail looking for // after an
	// already-nearer https: candidate; repeated absolute URLs would otherwise
	// make this helper quadratic even though each scheme search is short.
	relativeSearch := raw
	if schemeOK {
		relativeSearch = raw[:schemeStart]
	}
	relativeStart, relativeMarker, relativeOK := findEmbeddedProtocolRelativeReference(relativeSearch, searchFrom)
	if relativeOK && (!schemeOK || relativeStart < schemeStart) {
		return relativeStart, relativeMarker, true
	}
	return schemeStart, schemeColon, schemeOK
}

func findEmbeddedSchemeReference(raw string, searchFrom int) (start, colon int, ok bool) {
	for candidateColon := searchFrom; candidateColon < len(raw); candidateColon++ {
		if raw[candidateColon] != ':' {
			continue
		}
		candidateStart := candidateColon
		for candidateStart > 0 {
			b := raw[candidateStart-1]
			if isURLSchemeByte(b) || b == '\t' || b == '\r' || b == '\n' {
				candidateStart--
				continue
			}
			break
		}
		cleanScheme := strings.Map(func(r rune) rune {
			if r == '\t' || r == '\r' || r == '\n' {
				return -1
			}
			return r
		}, raw[candidateStart:candidateColon])
		if !isURLScheme(cleanScheme) {
			// A decorated URL can immediately follow a digit or punctuation
			// that is legal *inside* a scheme but not as its first byte (for
			// example "2https://user:pass@host"). Retry at the first alpha
			// suffix rather than letting that prefix hide the real URL.
			foundSuffix := false
			for suffixStart := candidateStart + 1; suffixStart < candidateColon; suffixStart++ {
				if !isASCIIAlpha(raw[suffixStart]) {
					continue
				}
				cleanSuffix := strings.Map(func(r rune) rune {
					if r == '\t' || r == '\r' || r == '\n' {
						return -1
					}
					return r
				}, raw[suffixStart:candidateColon])
				if isURLScheme(cleanSuffix) {
					candidateStart = suffixStart
					cleanScheme = cleanSuffix
					foundSuffix = true
					break
				}
			}
			if !foundSuffix {
				continue
			}
		}
		hasAuthoritySlash := candidateColon+1 < len(raw) && (raw[candidateColon+1] == '/' || raw[candidateColon+1] == '\\')
		if hasAuthoritySlash || isWHATWGSpecialScheme(cleanScheme) {
			return candidateStart, candidateColon, true
		}
	}
	return 0, 0, false
}

// Protocol-relative references have no scheme colon for the scanner to find.
// Recognize them when they begin a value or follow prose/query punctuation,
// but not the authority slashes belonging to a preceding "https:" scheme or
// a doubled slash embedded in an ordinary URL path.
func findEmbeddedProtocolRelativeReference(raw string, searchFrom int) (start, marker int, ok bool) {
	for i := searchFrom; i+1 < len(raw); i++ {
		if (raw[i] != '/' && raw[i] != '\\') || (raw[i+1] != '/' && raw[i+1] != '\\') {
			continue
		}
		if i > 0 {
			previous := raw[i-1]
			if previous == ':' || previous == '/' || previous == '\\' || isURLSchemeByte(previous) || previous == '@' {
				continue
			}
		}
		return i, i + 1, true
	}
	return 0, 0, false
}

func isEmbeddedURLTerminator(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n', '"', '\'', '<', '>', '(', ')', '[', ']', '{', '}':
		return true
	default:
		return b < 0x20 || b == 0x7f
	}
}

// WHATWG special-scheme URLs treat backslashes as slashes and accept mixed or
// missing authority slashes. Normalize that narrow parsing surface before
// locating userinfo so strings such as https:/\user:pass@host cannot bypass
// the credential scrubber. Protocol-relative mixed slashes receive the same
// treatment because browsers resolve them against a special-scheme base URL.
func normalizeWHATWGURLSlashes(raw string) string {
	if colon := strings.IndexByte(raw, ':'); colon > 0 && isURLScheme(raw[:colon]) && isWHATWGSpecialScheme(raw[:colon]) {
		rest := raw[colon+1:]
		for len(rest) > 0 && (rest[0] == '/' || rest[0] == '\\') {
			rest = rest[1:]
		}
		return raw[:colon+1] + "//" + normalizeURLPathBackslashes(rest)
	}

	slashes := 0
	for slashes < len(raw) && (raw[slashes] == '/' || raw[slashes] == '\\') {
		slashes++
	}
	if slashes >= 2 {
		return "//" + normalizeURLPathBackslashes(raw[slashes:])
	}
	return raw
}

func normalizeURLPathBackslashes(value string) string {
	end := len(value)
	if boundary := strings.IndexAny(value, "?#"); boundary >= 0 {
		end = boundary
	}
	if !strings.Contains(value[:end], "\\") {
		return value
	}
	return strings.ReplaceAll(value[:end], "\\", "/") + value[end:]
}

func isWHATWGSpecialScheme(value string) bool {
	switch strings.ToLower(value) {
	case "ftp", "file", "http", "https", "ws", "wss":
		return true
	default:
		return false
	}
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
	if boundary := strings.IndexAny(raw[authorityStart:], "/\\?#"); boundary >= 0 {
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
		if !isURLSchemeByte(b) {
			return false
		}
	}
	return true
}

func isURLSchemeByte(b byte) bool {
	return isASCIIAlpha(b) || b >= '0' && b <= '9' || b == '+' || b == '-' || b == '.'
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

// redactURLQuery rewrites only sensitive query values while preserving the URL
// spelling, parameter order, duplicate keys, and fragment.
func redactURLQuery(raw string) string {
	question := strings.IndexByte(raw, '?')
	fragment := strings.IndexByte(raw, '#')
	if question >= 0 && (fragment < 0 || question < fragment) {
		queryEnd := len(raw)
		if fragment >= 0 {
			queryEnd = fragment
		}
		if redacted, changed := redactCredentialParameters(raw[question+1 : queryEnd]); changed {
			raw = raw[:question+1] + redacted + raw[queryEnd:]
		}
	}

	// OAuth-style credentials are commonly returned in URL fragments. The
	// fragment is not sent to the upstream host, but it is still exposed to the
	// proxy client, so apply the same conservative parameter scrub there.
	fragment = strings.IndexByte(raw, '#')
	if fragment >= 0 {
		if redacted, changed := redactCredentialParameters(raw[fragment+1:]); changed {
			raw = raw[:fragment+1] + redacted
		}
	}
	return raw
}

// redactCredentialParameters preserves spelling and ordering while treating
// both ampersand and semicolon as possible separators. Although modern Go URL
// parsing rejects unescaped semicolons, upstream applications and browsers are
// not uniformly strict, so accepting only '&' here would leave a parser gap.
func redactCredentialParameters(raw string) (string, bool) {
	var output strings.Builder
	output.Grow(len(raw))
	changed := false
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i < len(raw) && raw[i] != '&' && raw[i] != ';' {
			continue
		}
		part := raw[start:i]
		key, value, hasValue := strings.Cut(part, "=")
		if queryKeyIsSensitive(key) || hasValue && parameterValueContainsCredentials(value) {
			part = key + "=" + url.QueryEscape(redactedValue)
			changed = true
		}
		output.WriteString(part)
		if i < len(raw) {
			output.WriteByte(raw[i])
		}
		start = i + 1
	}
	return output.String(), changed
}

func parameterValueContainsCredentials(raw string) bool {
	decoded := raw
	for pass := 0; pass < maxCredentialDecodePasses; pass++ {
		if containsURLUserinfo(decoded) || containsSensitiveParameterKey(decoded) {
			return true
		}
		next, err := url.QueryUnescape(decoded)
		if err != nil || next == decoded {
			return false
		}
		decoded = next
	}
	return true
}

func containsURLUserinfo(raw string) bool {
	candidate := normalizeWHATWGURLSlashes(normalizeWHATWGURLWhitespace(raw))
	if stripURLUserinfo(candidate) != candidate {
		return true
	}
	searchFrom := 0
	for {
		start, colon, ok := findEmbeddedURLReference(raw, searchFrom)
		if !ok {
			return false
		}
		end := embeddedURLReferenceEnd(raw, colon)
		candidate = normalizeWHATWGURLSlashes(normalizeWHATWGURLWhitespace(raw[start:end]))
		if stripURLUserinfo(candidate) != candidate {
			return true
		}
		searchFrom = end
	}
}

func containsSensitiveParameterKey(raw string) bool {
	regions := []string{raw}
	if question := strings.IndexByte(raw, '?'); question >= 0 {
		end := len(raw)
		if fragment := strings.IndexByte(raw[question+1:], '#'); fragment >= 0 {
			end = question + 1 + fragment
		}
		regions = append(regions, raw[question+1:end])
	}
	if fragment := strings.IndexByte(raw, '#'); fragment >= 0 {
		regions = append(regions, raw[fragment+1:])
	}
	for _, region := range regions {
		start := 0
		for i := 0; i <= len(region); i++ {
			if i < len(region) && region[i] != '&' && region[i] != ';' {
				continue
			}
			part := region[start:i]
			key, _, _ := strings.Cut(part, "=")
			if queryKeyIsSensitive(key) {
				return true
			}
			start = i + 1
		}
	}
	return false
}

// Repeated decoding closes double-encoding gaps such as API%254bEY. The pass
// count is fixed so adversarially deep encodings cannot impose quadratic work.
// Malformed, irreducible, or deeper-than-supported encodings fail closed.
func queryKeyIsSensitive(raw string) bool {
	decoded := raw
	for pass := 0; pass < maxCredentialDecodePasses; pass++ {
		next, err := url.QueryUnescape(decoded)
		if err != nil {
			return true
		}
		if next == decoded {
			return isSensitiveQueryName(decoded)
		}
		decoded = next
	}
	return true
}

func sanitizeResponseHeaders(header http.Header) {
	for name := range header {
		if isSensitiveQueryName(name) || isUpstreamResponseControlHeader(name) {
			header.Del(name)
		}
	}
	rewriteResponseHeader(header, "Location", sanitizeDirectURLHeader)
	rewriteResponseHeader(header, "Content-Location", sanitizeDirectURLHeader)
	rewriteResponseHeader(header, "Link", sanitizeLinkHeader)
	rewriteResponseHeader(header, "Refresh", sanitizeRefreshHeader)

	// Vendor/extension headers sometimes carry download or callback URLs under
	// names the proxy has never seen. Inspect values that are unambiguously URL
	// references instead of relying on a brittle header-name allowlist.
	for name, values := range header {
		for i, value := range values {
			if looksLikeURLReference(value) {
				header[name][i] = sanitizeURLCredentials(value)
			}
		}
	}
}

// These headers are instructions to a reverse proxy, not representation
// metadata for Cantinarr's client. Forwarding them lets an arr control the
// public proxy's internal redirects, local-file offload, buffering, charset,
// rate limits, or cache lifetime under Cantinarr's origin.
func isUpstreamResponseControlHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(name, "x-accel-") ||
		strings.HasPrefix(name, "x-sendfile") ||
		strings.HasPrefix(name, "x-lighttpd-send-") {
		return true
	}
	switch name {
	case "x-reproxy-url":
		return true
	default:
		return false
	}
}

func looksLikeURLReference(value string) bool {
	value = normalizeWHATWGURLWhitespace(value)
	if looksLikeURLReferenceSyntax(value) {
		return true
	}
	_, _, ok := findEmbeddedURLReference(value, 0)
	return ok
}

func looksLikeURLReferenceSyntax(value string) bool {
	if value == "" {
		return false
	}
	if value[0] == '/' || value[0] == '\\' || value[0] == '?' || value[0] == '#' {
		return true
	}
	colon := strings.IndexByte(value, ':')
	return colon > 0 && isURLScheme(value[:colon])
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

// Link is parsed strictly enough to reject arbitrary prefix/suffix text and to
// retain only the small set of parameters Cantinarr clients use. URL-valued,
// sensitive, and unknown extension parameters are dropped; malformed values
// fail closed instead of becoming an informational-header bypass.
func sanitizeLinkHeader(value string) (string, bool) {
	parts, ok := splitLinkHeaderValues(value)
	if !ok || len(parts) == 0 {
		return "", false
	}
	sanitized := make([]string, 0, len(parts))
	for _, part := range parts {
		clean, ok := sanitizeLinkValue(part)
		if !ok {
			return "", false
		}
		sanitized = append(sanitized, clean)
	}
	return strings.Join(sanitized, ", "), true
}

func splitLinkHeaderValues(value string) ([]string, bool) {
	parts := make([]string, 0, 2)
	start := 0
	inTarget := false
	inQuote := false
	escaped := false
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b == 0 || b == '\r' || b == '\n' || b < 0x20 && b != '\t' {
			return nil, false
		}
		if inQuote {
			if escaped {
				escaped = false
				continue
			}
			if b == '\\' {
				escaped = true
				continue
			}
			if b == '"' {
				inQuote = false
			}
			continue
		}
		if inTarget {
			if b == '<' || b == '"' {
				return nil, false
			}
			if b == '>' {
				inTarget = false
			}
			continue
		}
		switch b {
		case '"':
			inQuote = true
		case '<':
			inTarget = true
		case '>':
			return nil, false
		case ',':
			part := strings.TrimSpace(value[start:i])
			if part == "" {
				return nil, false
			}
			parts = append(parts, part)
			start = i + 1
		}
	}
	if inTarget || inQuote || escaped {
		return nil, false
	}
	part := strings.TrimSpace(value[start:])
	if part == "" {
		return nil, false
	}
	return append(parts, part), true
}

func sanitizeLinkValue(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 3 || value[0] != '<' {
		return "", false
	}
	close := strings.IndexByte(value, '>')
	if close <= 1 {
		return "", false
	}
	target := value[1:close]
	for i := 0; i < len(target); i++ {
		if target[i] == '<' || target[i] == '"' || target[i] <= 0x20 || target[i] == 0x7f {
			return "", false
		}
	}

	var output strings.Builder
	output.WriteByte('<')
	output.WriteString(sanitizeURLCredentials(target))
	output.WriteByte('>')

	position := close + 1
	for {
		for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
			position++
		}
		if position == len(value) {
			return output.String(), true
		}
		if value[position] != ';' {
			return "", false
		}
		position++
		for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
			position++
		}
		nameStart := position
		for position < len(value) && isHTTPTokenByte(value[position]) {
			position++
		}
		if position == nameStart {
			return "", false
		}
		name := value[nameStart:position]
		for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
			position++
		}

		rawValue := ""
		if position < len(value) && value[position] == '=' {
			position++
			for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
				position++
			}
			valueStart := position
			if position < len(value) && value[position] == '"' {
				position++
				escaped := false
				for position < len(value) {
					b := value[position]
					if b == '\r' || b == '\n' || b == 0 {
						return "", false
					}
					position++
					if escaped {
						escaped = false
						continue
					}
					if b == '\\' {
						escaped = true
						continue
					}
					if b == '"' {
						break
					}
				}
				if position == valueStart+1 || value[position-1] != '"' || escaped {
					return "", false
				}
			} else {
				for position < len(value) && isHTTPTokenByte(value[position]) {
					position++
				}
				if position == valueStart {
					return "", false
				}
			}
			rawValue = value[valueStart:position]
		}

		if safeValue, ok := sanitizeRetainedLinkParameter(name, rawValue); ok {
			output.WriteString("; ")
			output.WriteString(name)
			if safeValue != "" {
				output.WriteByte('=')
				output.WriteString(safeValue)
			}
		}
	}
}

// Link parameters are attacker-controlled response text under Cantinarr's
// public origin. Merely allowlisting their names is insufficient: rel and
// integrity, for example, can legally contain arbitrary strings or URLs. Keep
// only the finite parameter semantics used for pagination and preload hints.
// Values outside these enumerations (including integrity and extension
// relation URLs) are dropped rather than forwarded as a covert secret field.
func sanitizeRetainedLinkParameter(name, rawValue string) (string, bool) {
	name = strings.ToLower(name)
	if isSensitiveQueryName(name) {
		return "", false
	}
	value, ok := linkParameterText(rawValue)
	if !ok {
		return "", false
	}
	lowerValue := strings.ToLower(value)

	switch name {
	case "rel":
		relations := strings.Fields(lowerValue)
		if len(relations) == 0 {
			return "", false
		}
		for _, relation := range relations {
			switch relation {
			case "alternate", "author", "canonical", "dns-prefetch", "first", "help", "icon", "last", "license", "manifest", "modulepreload", "next", "pingback", "preconnect", "prefetch", "preload", "prev", "search", "stylesheet", "tag":
			default:
				return "", false
			}
		}
	case "as":
		switch lowerValue {
		case "audio", "audioworklet", "document", "embed", "fetch", "font", "frame", "iframe", "image", "manifest", "object", "paintworklet", "report", "script", "serviceworker", "sharedworker", "style", "track", "video", "webidentity", "worker", "xslt":
		default:
			return "", false
		}
	case "crossorigin":
		if rawValue == "" {
			return "", true
		}
		switch lowerValue {
		case "anonymous", "use-credentials":
		default:
			return "", false
		}
	case "fetchpriority":
		switch lowerValue {
		case "auto", "high", "low":
		default:
			return "", false
		}
	case "referrerpolicy":
		switch lowerValue {
		case "no-referrer", "no-referrer-when-downgrade", "origin", "origin-when-cross-origin", "same-origin", "strict-origin", "strict-origin-when-cross-origin", "unsafe-url":
		default:
			return "", false
		}
	default:
		return "", false
	}
	return rawValue, true
}

func linkParameterText(rawValue string) (string, bool) {
	if rawValue == "" {
		return "", true
	}
	if rawValue[0] != '"' {
		return rawValue, true
	}
	if len(rawValue) < 2 || rawValue[len(rawValue)-1] != '"' {
		return "", false
	}

	var value strings.Builder
	escaped := false
	for i := 1; i < len(rawValue)-1; i++ {
		b := rawValue[i]
		if escaped {
			value.WriteByte(b)
			escaped = false
			continue
		}
		if b == '\\' {
			escaped = true
			continue
		}
		if b == '"' || b == 0 || b == '\r' || b == '\n' || b < 0x20 && b != '\t' {
			return "", false
		}
		value.WriteByte(b)
	}
	if escaped {
		return "", false
	}
	return value.String(), true
}

func isHTTPTokenByte(b byte) bool {
	if isASCIIAlpha(b) || b >= '0' && b <= '9' {
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
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
