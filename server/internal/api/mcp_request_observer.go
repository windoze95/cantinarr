package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5/middleware"
)

const (
	maxMCPObservedRequestBytes  int64 = 64 << 10
	maxMCPObservedResponseBytes       = 16 << 10
	maxMCPObservedMethodBytes         = 128
	maxMCPObservedClientBytes         = 80
)

// mcpRequestObserver records enough protocol metadata to diagnose negotiation,
// discovery, and fallback behavior without logging bearer tokens, session IDs,
// tool arguments, client capabilities, resource URIs, or response content.
//
// Request bodies are inspected only when their declared size is small and are
// restored before the next handler reads them. Response bytes are retained only
// in a bounded in-memory buffer so JSON-RPC failures can be classified; the
// bytes themselves are never written to logs.
func mcpRequestObserver(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		observed := observeMCPRequest(r)

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		responseSample := &mcpObservationBuffer{limit: maxMCPObservedResponseBytes}
		ww.Tee(responseSample)
		next.ServeHTTP(ww, r)

		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		outcome := classifyMCPOutcome(r.Context(), status, responseSample.Bytes())

		fields := []string{
			"method=" + strconv.Quote(observed.method),
			"protocol=" + strconv.Quote(observed.protocol),
			"era=" + strconv.Quote(observed.era),
			"lifecycle=" + strconv.Quote(observed.lifecycle),
			"capabilities=" + strconv.Quote(observed.capabilities),
		}
		if observed.target != "" {
			fields = append(fields, "target="+strconv.Quote(observed.target))
		}
		if observed.clientName != "" {
			fields = append(fields, "client_name="+strconv.Quote(observed.clientName))
		}
		if observed.clientVersion != "" {
			fields = append(fields, "client_version="+strconv.Quote(observed.clientVersion))
		}
		if observed.metadataMismatch {
			fields = append(fields, "metadata_mismatch=true")
		}
		fields = append(fields,
			fmt.Sprintf("status=%d", status),
			"outcome="+strconv.Quote(outcome),
			fmt.Sprintf("duration_ms=%d", time.Since(started).Milliseconds()),
		)
		log.Printf("mcp: %s", strings.Join(fields, " "))
	})
}

type observedMCPRequest struct {
	method           string
	target           string
	protocol         string
	clientName       string
	clientVersion    string
	capabilities     string
	era              string
	lifecycle        string
	metadataMismatch bool
}

type mcpObservedImplementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type mcpObservedEnvelope struct {
	Method string `json:"method"`
	Params struct {
		Name            string                    `json:"name"`
		URI             string                    `json:"uri"`
		ProtocolVersion string                    `json:"protocolVersion"`
		ClientInfo      mcpObservedImplementation `json:"clientInfo"`
		Capabilities    json.RawMessage           `json:"capabilities"`
		Meta            struct {
			ProtocolVersion    string                    `json:"io.modelcontextprotocol/protocolVersion"`
			ClientInfo         mcpObservedImplementation `json:"io.modelcontextprotocol/clientInfo"`
			ClientCapabilities json.RawMessage           `json:"io.modelcontextprotocol/clientCapabilities"`
		} `json:"_meta"`
	} `json:"params"`
}

func observeMCPRequest(r *http.Request) observedMCPRequest {
	headerMethod := strings.TrimSpace(r.Header.Get("Mcp-Method"))
	headerProtocol := strings.TrimSpace(r.Header.Get("MCP-Protocol-Version"))

	body, bodyAvailable := observedMCPRequestBody(r)
	var envelope mcpObservedEnvelope
	bodyValid := bodyAvailable && json.Unmarshal(body, &envelope) == nil

	bodyMethod := ""
	bodyProtocol := ""
	clientInfo := mcpObservedImplementation{}
	capabilities := "unknown"
	if bodyValid {
		bodyMethod = strings.TrimSpace(envelope.Method)
		bodyProtocol = strings.TrimSpace(envelope.Params.Meta.ProtocolVersion)
		clientInfo = envelope.Params.Meta.ClientInfo
		if len(envelope.Params.Meta.ClientCapabilities) > 0 {
			capabilities = "present"
		}
		if bodyMethod == "initialize" {
			if bodyProtocol == "" {
				bodyProtocol = strings.TrimSpace(envelope.Params.ProtocolVersion)
			}
			if clientInfo.Name == "" && clientInfo.Version == "" {
				clientInfo = envelope.Params.ClientInfo
			}
			if len(envelope.Params.Capabilities) > 0 {
				capabilities = "legacy"
			}
		} else if capabilities == "unknown" {
			capabilities = "missing"
		}
	}
	if capabilities == "missing" && r.Header.Get("Mcp-Session-Id") != "" {
		capabilities = "session"
	}

	method := bodyMethod
	if method == "" {
		method = headerMethod
	}
	protocol := headerProtocol
	if protocol == "" {
		protocol = bodyProtocol
	}

	metadataMismatch := headerMethod != "" && bodyMethod != "" && headerMethod != bodyMethod
	if headerProtocol != "" && bodyProtocol != "" && headerProtocol != bodyProtocol {
		metadataMismatch = true
	}
	headerName := strings.TrimSpace(r.Header.Get("Mcp-Name"))
	bodyName := ""
	switch bodyMethod {
	case "tools/call", "prompts/get":
		bodyName = envelope.Params.Name
	case "resources/read":
		bodyName = envelope.Params.URI
	}
	if headerName != "" && bodyName != "" && headerName != bodyName {
		metadataMismatch = true
	}

	method = safeMCPIdentifier(method, maxMCPObservedMethodBytes, true)
	protocol = safeMCPIdentifier(protocol, 32, false)
	clientName := safeMCPClientValue(clientInfo.Name)
	clientVersion := safeMCPClientValue(clientInfo.Version)

	target := ""
	if bodyValid && (bodyMethod == "tools/call" || bodyMethod == "prompts/get") {
		target = safeMCPIdentifier(envelope.Params.Name, maxMCPObservedMethodBytes, false)
	} else if headerMethod == "tools/call" || headerMethod == "prompts/get" {
		target = safeMCPIdentifier(headerName, maxMCPObservedMethodBytes, false)
	}

	return observedMCPRequest{
		method:           fallbackMCPObservation(method),
		target:           target,
		protocol:         fallbackMCPObservation(protocol),
		clientName:       clientName,
		clientVersion:    clientVersion,
		capabilities:     capabilities,
		era:              mcpProtocolEra(protocol, method, r),
		lifecycle:        mcpLifecycle(method, r.Method),
		metadataMismatch: metadataMismatch,
	}
}

// observedMCPRequestBody reads and restores a bounded request body. Unknown
// length and oversized bodies are deliberately left untouched; modern clients
// still provide their routing method and protocol version in HTTP headers.
func observedMCPRequestBody(r *http.Request) ([]byte, bool) {
	if r.Body == nil || r.ContentLength <= 0 || r.ContentLength > maxMCPObservedRequestBytes {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMCPObservedRequestBytes+1))
	r.Body = struct {
		io.Reader
		io.Closer
	}{
		Reader: io.MultiReader(bytes.NewReader(body), r.Body),
		Closer: r.Body,
	}
	if err != nil || int64(len(body)) > maxMCPObservedRequestBytes {
		return nil, false
	}
	return body, true
}

func safeMCPIdentifier(value string, limit int, allowSlash bool) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > limit {
		return ""
	}
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_' || char == '-' || char == '.' || (allowSlash && char == '/') {
			continue
		}
		return ""
	}
	return value
}

func safeMCPClientValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxMCPObservedClientBytes || !utf8.ValidString(value) {
		return ""
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return ""
		}
	}
	return value
}

func fallbackMCPObservation(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func mcpProtocolEra(protocol, method string, r *http.Request) string {
	protocolDate, protocolDateErr := time.Parse("2006-01-02", protocol)
	modernProtocolDate := time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)
	if protocolDateErr == nil && !protocolDate.Before(modernProtocolDate) {
		return "modern"
	}
	if method == "initialize" || method == "notifications/initialized" || r.Header.Get("Mcp-Session-Id") != "" || r.Method == http.MethodGet || r.Method == http.MethodDelete {
		return "legacy"
	}
	if protocol != "unknown" {
		return "legacy"
	}
	return "unknown"
}

func mcpLifecycle(method, httpMethod string) string {
	switch {
	case httpMethod == http.MethodOptions:
		return "preflight"
	case httpMethod == http.MethodGet:
		return "legacy_stream"
	case httpMethod == http.MethodDelete:
		return "legacy_termination"
	case method == "server/discover":
		return "discovery"
	case method == "initialize":
		return "initialization"
	case method == "notifications/initialized":
		return "initialized"
	case method == "subscriptions/listen":
		return "subscription"
	case method == "notifications/cancelled":
		return "cancellation"
	case strings.HasPrefix(method, "notifications/"):
		return "notification"
	default:
		return "request"
	}
}

func classifyMCPOutcome(ctx context.Context, status int, response []byte) string {
	if ctx.Err() == context.Canceled {
		return "cancelled"
	}
	if ctx.Err() == context.DeadlineExceeded {
		return "deadline_exceeded"
	}
	if code, ok := observedJSONRPCErrorCode(response); ok {
		switch code {
		case -32601:
			return "method_not_found"
		case -32022:
			return "unsupported_protocol"
		default:
			return "jsonrpc_error"
		}
	}
	switch {
	case status >= 200 && status < 400:
		return "ok"
	case status == http.StatusUnauthorized:
		return "unauthorized"
	case status == http.StatusForbidden:
		return "forbidden"
	case status >= 400 && status < 500:
		return "rejected"
	default:
		return "server_error"
	}
}

func observedJSONRPCErrorCode(response []byte) (int, bool) {
	decode := func(payload []byte) (int, bool) {
		var envelope struct {
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(bytes.TrimSpace(payload), &envelope) != nil || envelope.Error == nil {
			return 0, false
		}
		return envelope.Error.Code, true
	}
	if code, ok := decode(response); ok {
		return code, true
	}
	for _, line := range bytes.Split(response, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		if code, ok := decode(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))); ok {
			return code, true
		}
	}
	return 0, false
}

type mcpObservationBuffer struct {
	bytes.Buffer
	limit int
}

func (w *mcpObservationBuffer) Write(payload []byte) (int, error) {
	written := len(payload)
	remaining := w.limit - w.Len()
	if remaining > 0 {
		if len(payload) > remaining {
			payload = payload[:remaining]
		}
		_, _ = w.Buffer.Write(payload)
	}
	return written, nil
}
