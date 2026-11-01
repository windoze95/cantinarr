package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

const mcpSessionPrefix = "mcp-session-"
const maxMCPRequestBodyBytes = 1 << 20
const maxStreamsPerMCPSession = 2

var errUnknownMCPSession = errors.New("unknown MCP session")
var errMCPSessionStreamsFull = errors.New("MCP stream capacity reached")

// boundedSessionIDs replaces mcp-go's stateful manager. That manager retains
// terminated IDs forever, even when the library's idle sweeper expires them.
// Here termination drops an ID and both global and per-user admission are
// bounded, including simultaneous first requests.
type boundedSessionIDs struct {
	mu             sync.Mutex
	max            int
	maxPerUser     int
	active         map[string]int64
	activeByUser   map[int64]int
	pending        int
	pendingByUser  map[int64]int
	streams        map[string]map[*mcpStream]struct{}
	streamCount    int
	streamsByUser  map[int64]int
	streamLifetime time.Duration
}

type mcpStream struct{ cancel context.CancelFunc }

type sessionReservation struct {
	userID   int64
	id       string
	used     bool
	released bool
}

func newBoundedSessionIDs(max, maxPerUser int) *boundedSessionIDs {
	return &boundedSessionIDs{
		max: max, maxPerUser: maxPerUser,
		active:       make(map[string]int64),
		activeByUser: make(map[int64]int), pendingByUser: make(map[int64]int),
		streams: make(map[string]map[*mcpStream]struct{}), streamsByUser: make(map[int64]int),
		streamLifetime: mcpSessionIdleTTL,
	}
}

func (s *boundedSessionIDs) reserve(userID int64) (*sessionReservation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.active)+s.pending >= s.max || s.activeByUser[userID]+s.pendingByUser[userID] >= s.maxPerUser {
		return nil, false
	}
	s.pending++
	s.pendingByUser[userID]++
	return &sessionReservation{userID: userID}, true
}

func (s *boundedSessionIDs) release(reservation *sessionReservation) {
	if reservation == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if reservation.released || reservation.used {
		return
	}
	reservation.released = true
	s.pending--
	decrementUserCount(s.pendingByUser, reservation.userID)
}

func (s *boundedSessionIDs) generate(reservation *sessionReservation) string {
	if reservation == nil {
		// Only POSTs admitted by sessionAdmission and carrying a request-
		// scoped manager may mint sessions. An initialize request with an old
		// session header cannot bypass the capacity or per-user budget.
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if reservation.released || reservation.used {
		return ""
	}
	reservation.used = true
	s.pending--
	decrementUserCount(s.pendingByUser, reservation.userID)
	for {
		id := mcpSessionPrefix + uuid.NewString()
		if _, exists := s.active[id]; exists {
			continue
		}
		s.active[id] = reservation.userID
		s.activeByUser[reservation.userID]++
		reservation.id = id
		return id
	}
}

func (s *boundedSessionIDs) reservationID(reservation *sessionReservation) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return reservation.id
}

// Generate is the fallback manager method used without a request reservation.
// It never issues an unowned ID; the request-scoped manager below calls
// generate with the reservation that admission attached to its own request.
func (*boundedSessionIDs) Generate() string { return "" }

func (s *boundedSessionIDs) Validate(sessionID string) (bool, error) {
	if !validMCPSessionID(sessionID) {
		return false, errUnknownMCPSession
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.active[sessionID]; !exists {
		return false, errUnknownMCPSession
	}
	return false, nil
}

func (s *boundedSessionIDs) Terminate(sessionID string) (bool, error) {
	s.mu.Lock()
	var canceled []context.CancelFunc
	if userID, exists := s.active[sessionID]; exists {
		for stream := range s.streams[sessionID] {
			canceled = append(canceled, stream.cancel)
			s.streamCount--
			decrementUserCount(s.streamsByUser, userID)
		}
		delete(s.streams, sessionID)
		delete(s.active, sessionID)
		decrementUserCount(s.activeByUser, userID)
	}
	s.mu.Unlock()
	for _, cancel := range canceled {
		cancel()
	}
	return false, nil
}

// openStream admits one live GET stream. Session IDs alone do not bound SSE
// goroutines: several GETs may use the same ID, and DELETE must cancel them.
func (s *boundedSessionIDs) openStream(sessionID string, userID int64, parent context.Context) (context.Context, func(), error) {
	// A long-lived GET must periodically reconnect through OAuth and device
	// checks, even when heartbeat traffic keeps the session ID itself alive.
	ctx, cancel := context.WithTimeout(parent, s.streamLifetime)
	s.mu.Lock()
	owner, exists := s.active[sessionID]
	if !exists || owner != userID {
		s.mu.Unlock()
		cancel()
		return nil, nil, errUnknownMCPSession
	}
	if s.streamCount >= s.max || s.streamsByUser[userID] >= s.maxPerUser || len(s.streams[sessionID]) >= maxStreamsPerMCPSession {
		s.mu.Unlock()
		cancel()
		return nil, nil, errMCPSessionStreamsFull
	}
	stream := &mcpStream{cancel: cancel}
	if s.streams[sessionID] == nil {
		s.streams[sessionID] = make(map[*mcpStream]struct{})
	}
	s.streams[sessionID][stream] = struct{}{}
	s.streamCount++
	s.streamsByUser[userID]++
	s.mu.Unlock()
	return ctx, func() { s.closeStream(sessionID, userID, stream) }, nil
}

func (s *boundedSessionIDs) closeStream(sessionID string, userID int64, stream *mcpStream) {
	s.mu.Lock()
	if _, exists := s.streams[sessionID][stream]; exists {
		delete(s.streams[sessionID], stream)
		if len(s.streams[sessionID]) == 0 {
			delete(s.streams, sessionID)
		}
		s.streamCount--
		decrementUserCount(s.streamsByUser, userID)
	}
	s.mu.Unlock()
	stream.cancel()
}

func decrementUserCount(counts map[int64]int, userID int64) {
	if counts[userID] <= 1 {
		delete(counts, userID)
	} else {
		counts[userID]--
	}
}

func (s *boundedSessionIDs) validateOwner(sessionID string, userID int64) error {
	if !validMCPSessionID(sessionID) {
		return errUnknownMCPSession
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	owner, exists := s.active[sessionID]
	if !exists || owner != userID {
		return errUnknownMCPSession
	}
	return nil
}

func validMCPSessionID(sessionID string) bool {
	if len(sessionID) != len(mcpSessionPrefix)+36 || sessionID[:len(mcpSessionPrefix)] != mcpSessionPrefix {
		return false
	}
	_, err := uuid.Parse(sessionID[len(mcpSessionPrefix):])
	return err == nil
}

type reservationKey struct{}

// mcp-go resolves its SessionIdManager for each request. The request-scoped
// manager lets Generate consume only that request's reservation, so two
// simultaneous initializations cannot take each other's capacity slots.
type sessionIDResolver struct{ ids *boundedSessionIDs }

func (r sessionIDResolver) ResolveSessionIdManager(req *http.Request) server.SessionIdManager {
	if req == nil {
		return r.ids // idle sweeper
	}
	reservation, _ := req.Context().Value(reservationKey{}).(*sessionReservation)
	return reservedSessionIDs{ids: r.ids, reservation: reservation}
}

type reservedSessionIDs struct {
	ids         *boundedSessionIDs
	reservation *sessionReservation
}

func (s reservedSessionIDs) Generate() string                  { return s.ids.generate(s.reservation) }
func (s reservedSessionIDs) Validate(id string) (bool, error)  { return s.ids.Validate(id) }
func (s reservedSessionIDs) Terminate(id string) (bool, error) { return s.ids.Terminate(id) }

// sessionAdmission checks capacity before mcp-go can generate or register a
// session. It also rejects unknown GET IDs: mcp-go's GET path skips its
// SessionIdManager.Validate call and can otherwise register arbitrary IDs.
type sessionAdmission struct {
	next http.Handler
	ids  *boundedSessionIDs
}

func (a sessionAdmission) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID := int64(0)
	if claims := auth.GetClaims(r.Context()); claims != nil {
		userID = claims.UserID
	}
	switch r.Method {
	case http.MethodPost:
		// The transport reads the whole JSON body itself. Inspect it within the
		// same size bound so only initialize requests reserve capacity and an
		// initialize bearing an old session ID cannot mint an empty ID.
		if r.Body == nil {
			r.Body = http.NoBody
		}
		originalBody := r.Body
		defer originalBody.Close()
		body, err := io.ReadAll(io.LimitReader(originalBody, maxMCPRequestBodyBytes+1))
		if err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				http.Error(w, "MCP request body too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "Could not read MCP request body", http.StatusBadRequest)
			}
			return
		}
		if len(body) > maxMCPRequestBodyBytes {
			w.Header().Set("Connection", "close")
			http.Error(w, "MCP request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var message struct {
			Method mcp.MCPMethod `json:"method"`
		}
		_ = json.Unmarshal(body, &message) // mcp-go owns malformed-JSON responses.
		id := r.Header.Get("Mcp-Session-Id")
		if id != "" {
			if err := a.ids.validateOwner(id, userID); err != nil {
				http.Error(w, "Invalid session ID", http.StatusNotFound)
				return
			}
			if message.Method == mcp.MethodInitialize {
				http.Error(w, "Initialize must not reuse a session ID", http.StatusBadRequest)
				return
			}
		} else if message.Method == mcp.MethodInitialize {
			reservation, ok := a.ids.reserve(userID)
			if !ok {
				w.Header().Set("Retry-After", "60")
				w.Header().Set("Cache-Control", "no-store")
				http.Error(w, "MCP session capacity reached", http.StatusTooManyRequests)
				return
			}
			defer a.ids.release(reservation)
			r = r.WithContext(context.WithValue(r.Context(), reservationKey{}, reservation))
			// mcp-go calls Generate before JSON-RPC validation. It can return a
			// JSON-RPC error with HTTP 200 and still register the new session.
			// Inspect the initialize response, then use its DELETE path to
			// remove all transport state when initialization failed.
			response := httptest.NewRecorder()
			success := false
			defer func() {
				if !success {
					if mintedID := a.ids.reservationID(reservation); mintedID != "" {
						a.cleanupFailedInitialize(r, mintedID)
					}
				}
			}()
			a.next.ServeHTTP(response, r)
			success = successfulInitialize(response, a.ids.reservationID(reservation))
			if !success {
				response.Header().Del("Mcp-Session-Id")
			}
			writeMCPResponse(w, response)
			return
		}
	case http.MethodGet:
		id := r.Header.Get("Mcp-Session-Id")
		if id == "" {
			http.Error(w, "MCP session ID required", http.StatusBadRequest)
			return
		}
		streamCtx, closeStream, err := a.ids.openStream(id, userID, r.Context())
		if err != nil {
			if errors.Is(err, errUnknownMCPSession) {
				http.Error(w, "Invalid session ID", http.StatusNotFound)
			} else {
				w.Header().Set("Retry-After", "60")
				w.Header().Set("Cache-Control", "no-store")
				http.Error(w, "MCP stream capacity reached", http.StatusTooManyRequests)
			}
			return
		}
		defer closeStream()
		r = r.WithContext(streamCtx)
	case http.MethodDelete:
		id := r.Header.Get("Mcp-Session-Id")
		if id == "" {
			http.Error(w, "MCP session ID required", http.StatusBadRequest)
			return
		}
		if err := a.ids.validateOwner(id, userID); err != nil {
			http.Error(w, "Invalid session ID", http.StatusNotFound)
			return
		}
	}
	a.next.ServeHTTP(w, r)
}

func successfulInitialize(response *httptest.ResponseRecorder, sessionID string) bool {
	if response.Code != http.StatusOK || sessionID == "" || response.Header().Get("Mcp-Session-Id") != sessionID {
		return false
	}
	var message struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &message); err != nil {
		return false
	}
	return len(message.Result) > 0 && !bytes.Equal(message.Result, []byte("null")) && len(message.Error) == 0
}

func writeMCPResponse(w http.ResponseWriter, response *httptest.ResponseRecorder) {
	for key, values := range response.Header() {
		w.Header()[key] = append([]string(nil), values...)
	}
	w.WriteHeader(response.Code)
	_, _ = w.Write(response.Body.Bytes())
	if response.Flushed {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func (a sessionAdmission) cleanupFailedInitialize(r *http.Request, sessionID string) {
	deleteRequest := r.Clone(context.WithoutCancel(r.Context()))
	deleteRequest.Method = http.MethodDelete
	deleteRequest.Body = http.NoBody
	deleteRequest.Header.Set("Mcp-Session-Id", sessionID)
	a.next.ServeHTTP(httptest.NewRecorder(), deleteRequest)
	// The transport's DELETE path terminates the ID and clears all session
	// maps. If that path ever fails, leave the ID charged against capacity
	// until the idle sweeper can clean it rather than admitting leaked state.
}
