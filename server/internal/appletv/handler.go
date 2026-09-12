package appletv

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const pairingLifetime = 5 * time.Minute
const operationLifetime = 40 * time.Second
const confirmationLifetime = 30 * time.Second

// TitleAuthorizer resolves canonical metadata and requires a current media
// server match under this user's own access. A cached availability flag is
// never sufficient authority for a physical TV action.
type TitleAuthorizer func(context.Context, int64, string, int64) error

type Handler struct {
	db            *sql.DB
	cipher        *secrets.Cipher
	runner        Runner
	authorize     auth.PermissionAuthorizer
	title         TitleAuthorizer
	mu            sync.Mutex
	pairings      map[string]*pairingSession
	busy          map[string]bool
	confirmations map[string]confirmation
	pairingStarts int
}

type pairingSession struct {
	mu           sync.Mutex
	userID       int64
	deviceID     string
	device       Device
	conversation Conversation
	cancel       context.CancelFunc
	timer        *time.Timer
	deadline     time.Time
}

type confirmation struct {
	id       string
	userID   int64
	deviceID string
	revision int64
	title    titleRequest
	deadline time.Time
}

type storedDevice struct {
	Device
	credentials string
	revision    int64
}

type titleRequest struct {
	MediaType string `json:"media_type"`
	TMDBID    int64  `json:"tmdb_id"`
}

type caller struct {
	userID   int64
	deviceID string
	admin    bool
}

func NewHandler(database *sql.DB, cipher *secrets.Cipher, runner Runner, authorize auth.PermissionAuthorizer, title TitleAuthorizer) *Handler {
	return &Handler{db: database, cipher: cipher, runner: runner, authorize: authorize, title: title,
		pairings: map[string]*pairingSession{}, busy: map[string]bool{}, confirmations: map[string]confirmation{}}
}

func (h *Handler) Available() bool { return h != nil && h.runner != nil && h.runner.Available() }

func (h *Handler) Register(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/discover", h.discover)
	r.Post("/pairings", h.begin)
	r.Post("/pairings/{pairingID}/complete", h.complete)
	r.Delete("/pairings/{pairingID}", h.cancelPairing)
	r.Patch("/{tvID}", h.update)
	r.Delete("/{tvID}", h.forget)
	r.Get("/{tvID}/grants", h.grants)
	r.Put("/{tvID}/grants", h.grants)
	r.Post("/{tvID}/check", h.check)
	r.Post("/{tvID}/open", h.open)
	r.Post("/{tvID}/confirm-open", h.confirm)
}

func (h *Handler) Close() {
	h.mu.Lock()
	sessions := h.pairings
	h.pairings = map[string]*pairingSession{}
	h.confirmations = map[string]confirmation{}
	h.mu.Unlock()
	for _, session := range sessions {
		session.timer.Stop()
		session.cancel()
		session.conversation.Close()
	}
}

func randomID() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure random source unavailable")
	}
	return hex.EncodeToString(b[:])
}

func (h *Handler) identity(ctx context.Context, adminOnly bool) (caller, error) {
	claims := auth.GetClaims(ctx)
	if claims == nil {
		return caller{}, problem("unauthorized")
	}
	c := caller{userID: claims.UserID, deviceID: claims.DeviceID}
	permission := auth.PermissionMediaDiscover
	if adminOnly {
		permission = auth.PermissionAdmin
	}
	if h.authorize == nil {
		return c, problem("unavailable")
	}
	if err := h.authorize(ctx, c.userID, c.deviceID, permission); err != nil {
		if errors.Is(err, auth.ErrAuthUnavailable) {
			return c, problem("unavailable")
		}
		return c, problem("not_available")
	}
	var role string
	var child bool
	err := h.db.QueryRowContext(ctx, `SELECT role, EXISTS(SELECT 1 FROM user_content_policies WHERE user_id=users.id) FROM users WHERE id=?`, c.userID).Scan(&role, &child)
	if err != nil {
		return c, problem("unavailable")
	}
	if child {
		return c, problem("not_available")
	}
	c.admin = role == auth.RoleAdmin
	if adminOnly && !c.admin {
		return c, problem("not_available")
	}
	return c, nil
}

func (h *Handler) target(ctx context.Context, id string, adminOnly bool) (caller, storedDevice, error) {
	c, err := h.identity(ctx, adminOnly)
	var d storedDevice
	if err != nil {
		return c, d, err
	}
	err = h.db.QueryRowContext(ctx, `SELECT id,name,address,identifier,credentials,revision FROM apple_tv_devices WHERE id=? AND (? OR EXISTS(SELECT 1 FROM apple_tv_grants WHERE tv_id=apple_tv_devices.id AND user_id=?))`, id, c.admin, c.userID).Scan(&d.ID, &d.Name, &d.Address, &d.Identifier, &d.credentials, &d.revision)
	if errors.Is(err, sql.ErrNoRows) {
		return c, d, problem("not_available")
	}
	if err != nil {
		return c, d, problem("unavailable")
	}
	return c, d, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	c, err := h.identity(r.Context(), false)
	if err != nil {
		fail(w, err)
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `SELECT id,name,address,identifier FROM apple_tv_devices WHERE ? OR EXISTS(SELECT 1 FROM apple_tv_grants WHERE tv_id=apple_tv_devices.id AND user_id=?) ORDER BY name,id`, c.admin, c.userID)
	if err != nil {
		fail(w, problem("unavailable"))
		return
	}
	defer rows.Close()
	devices := []Device{}
	for rows.Next() {
		var d Device
		if rows.Scan(&d.ID, &d.Name, &d.Address, &d.Identifier) != nil {
			fail(w, problem("unavailable"))
			return
		}
		if !c.admin {
			d.Address, d.Identifier = "", ""
		}
		devices = append(devices, d)
	}
	if rows.Err() != nil {
		fail(w, problem("unavailable"))
		return
	}
	respond(w, http.StatusOK, map[string]any{"supported": h.Available(), "devices": devices})
}

func (h *Handler) discover(w http.ResponseWriter, r *http.Request) {
	if _, err := h.identity(r.Context(), true); err != nil {
		fail(w, err)
		return
	}
	var body struct {
		Address string `json:"address"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !validAddress(body.Address, true) {
		fail(w, problem("invalid_request"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), operationLifetime)
	defer cancel()
	conversation, err := h.start(ctx, Command{Action: "discover", Address: body.Address})
	if err != nil {
		fail(w, err)
		return
	}
	defer conversation.Close()
	reply, err := conversation.Receive()
	if err == nil && reply.State != "complete" {
		err = problem("unavailable")
	}
	if err == nil {
		_, err = h.identity(r.Context(), true)
	}
	if err != nil {
		fail(w, err)
		return
	}
	if reply.Devices == nil {
		reply.Devices = []Device{}
	}
	respond(w, 200, map[string]any{"devices": reply.Devices, "scope": reply.Scope})
}

func (h *Handler) start(ctx context.Context, command Command) (Conversation, error) {
	if !h.Available() {
		return nil, problem("unsupported")
	}
	return h.runner.Start(ctx, command)
}

func validAddress(address string, empty bool) bool {
	if address == "" {
		return empty
	}
	if len(address) > 253 || strings.ContainsAny(address, "/:@?#% \\[]\t\r\n") {
		return false
	}
	for _, c := range address {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		fail(w, problem("invalid_request"))
		return false
	}
	return true
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type failure string

func (e failure) Error() string { return string(e) }
func problem(code string) error {
	if _, ok := messages[code]; !ok {
		code = "unavailable"
	}
	return failure(code)
}

// Title lookup errors are exported without upstream detail or private hosts.
var ErrTitleUnavailable = problem("title_unavailable")
var ErrLookupUnavailable = problem("lookup_unavailable")

var messages = map[string]string{
	"unauthorized": "Sign in to continue.", "not_available": "This Apple TV is not available to your account.",
	"unavailable": "Apple TV control is temporarily unavailable.", "unsupported": "The Apple TV helper is not installed on this server.",
	"invalid_request": "Check the submitted Apple TV details.", "invalid_pin": "Enter the four-digit PIN shown on the TV.",
	"pairing_failed": "Pairing failed. Start again with a new PIN.", "pairing_expired": "This pairing attempt expired or was cancelled. Start again.",
	"needs_pairing": "Pair this Apple TV again.", "identity_changed": "A different device answered at this address. Check the TV address.",
	"infuse_missing": "Install Infuse on this Apple TV, then try again.",
	"unreachable":    "The server could not connect to the Apple TV. Check its address and network.",
	"timeout":        "The Apple TV did not answer in time. Check the TV before trying again.",
	"launch_failed":  "The Apple TV rejected the request.", "busy": "An action is already in progress on this TV.",
	"title_unavailable":    "This title is not available through your connected media servers.",
	"lookup_unavailable":   "Could not verify your current access to this title. Try again.",
	"confirmation_expired": "This confirmation expired or was already used. Open the title again if needed.",
}

func fail(w http.ResponseWriter, err error) {
	code := "unavailable"
	var f failure
	if errors.As(err, &f) {
		code = string(f)
	}
	status := http.StatusServiceUnavailable
	switch code {
	case "unauthorized":
		status = 401
	case "not_available", "title_unavailable":
		status = 403
	case "invalid_request", "invalid_pin":
		status = 400
	case "pairing_expired", "confirmation_expired":
		status = 404
	case "busy", "identity_changed", "needs_pairing":
		status = 409
	case "launch_failed", "pairing_failed", "infuse_missing":
		status = 502
	case "timeout":
		status = 504
	}
	respond(w, status, map[string]string{"code": code, "error": messages[code]})
}
