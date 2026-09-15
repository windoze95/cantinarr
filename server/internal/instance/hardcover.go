package instance

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/hardcover"
)

// Hardcover connects Cantinarr's trending feed; Chaptarr manages its own
// metadata credential. API tokens remain write-only for older clients.
const hardcoverAPIURL = hardcover.APIURL

// hardcoverTokenMaxLen bounds a pasted token. Hardcover issues JWTs of a few
// hundred bytes; anything past this is not a token.
const hardcoverTokenMaxLen = 4096

// SupportsHardcover reports whether a service type carries a Hardcover token.
func SupportsHardcover(serviceType string) bool { return serviceType == "chaptarr" }

// HasHardcoverToken reports whether an instance holds a Hardcover token
// without decrypting it: whether the slot is empty is stored metadata, so
// this read never needs the encryption key.
func (s *Store) HasHardcoverToken(id string) (bool, error) {
	var stored string
	err := s.db.QueryRow(
		"SELECT hardcover_token FROM service_instances WHERE id = ?", id,
	).Scan(&stored)
	if err == sql.ErrNoRows {
		return false, fmt.Errorf("instance not found: %s", id)
	}
	if err != nil {
		return false, fmt.Errorf("get hardcover status: %w", err)
	}
	return stored != "", nil
}

// HardcoverToken returns the decrypted Hardcover token for an instance, or
// "" when none is connected.
func (s *Store) HardcoverToken(id string) (string, error) {
	var stored string
	err := s.db.QueryRow(
		"SELECT hardcover_token FROM service_instances WHERE id = ?", id,
	).Scan(&stored)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("instance not found: %s", id)
	}
	if err != nil {
		return "", fmt.Errorf("get hardcover token: %w", err)
	}
	if stored == "" {
		return "", nil
	}
	token, err := s.cipher.Decrypt(stored)
	if err != nil {
		return "", fmt.Errorf("decrypt hardcover token for %s (wrong encryption key?): %w", id, err)
	}
	return token, nil
}

// SetHardcoverToken selects a verified API token, encrypted at rest, and
// releases this instance's OAuth link. Unrelated instance settings stay intact.
func (s *Store) SetHardcoverToken(id, token string) error {
	if token == "" {
		return errors.New("hardcover token is required")
	}
	revision, err := s.beginHardcoverChange(id)
	if err != nil {
		return err
	}
	return s.setHardcoverTokenAtRevision(id, token, revision)
}

// ClearHardcoverToken disconnects either connection method for this instance.
func (s *Store) ClearHardcoverToken(id string) error {
	revision, err := s.beginHardcoverChange(id)
	if err != nil {
		return err
	}
	return s.setHardcoverTokenAtRevision(id, "", revision)
}

// HardcoverStatus answers GET /instances/{id}/hardcover: whether this
// instance type can hold a Hardcover token and whether one is connected. The
// token itself never appears.
func (h *Handler) HardcoverStatus(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.hardcoverInstance(w, r)
	if !ok {
		return
	}
	if !SupportsHardcover(inst.ServiceType) {
		writeHardcoverStatus(w, false, false)
		return
	}
	state, err := h.store.HardcoverState(inst.ID)
	if err != nil {
		writeHardcoverError(w, errHardcoverStorage)
		return
	}
	writeHardcoverJSON(w, state)
}

// SaveHardcoverToken answers PUT /instances/{id}/hardcover with {token}. The
// token is verified against Hardcover before anything is stored -- a token
// Hardcover rejects is a 400 and leaves the previous connection in place; a
// Hardcover that cannot be reached is a 502, so blindness never reads as a
// bad token.
func (h *Handler) SaveHardcoverToken(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.hardcoverInstance(w, r)
	if !ok {
		return
	}
	if !SupportsHardcover(inst.ServiceType) {
		http.Error(w, `{"error":"Hardcover connects to Chaptarr instances only"}`, http.StatusBadRequest)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	token, err := normalizeHardcoverToken(body.Token)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadRequest)
		return
	}
	revision, err := h.store.beginHardcoverChange(inst.ID)
	if err != nil {
		writeHardcoverError(w, err)
		return
	}
	if err := hardcover.NewClientForURL(h.hardcoverAPIURL).VerifyCatalog(r.Context(), token); err != nil {
		if !errors.Is(err, hardcover.ErrUnauthorized) && !errors.Is(err, hardcover.ErrInsufficientScope) {
			err = hardcover.ErrProvider
		}
		writeHardcoverError(w, err)
		return
	}
	if err := h.store.setHardcoverTokenAtRevision(inst.ID, token, revision); err != nil {
		writeHardcoverError(w, err)
		return
	}
	h.hardcover.ForgetUnlinked()
	h.notifyHardcoverChanged(inst.ID)
	h.HardcoverStatus(w, r)
}

// ClearHardcoverToken answers DELETE /instances/{id}/hardcover.
func (h *Handler) ClearHardcoverToken(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.hardcoverInstance(w, r)
	if !ok {
		return
	}
	if !SupportsHardcover(inst.ServiceType) {
		writeHardcoverStatus(w, false, false)
		return
	}
	if err := h.store.ClearHardcoverToken(inst.ID); err != nil {
		http.Error(w, `{"error":"failed to clear hardcover token"}`, http.StatusInternalServerError)
		return
	}
	h.hardcover.ForgetUnlinked()
	h.notifyHardcoverChanged(inst.ID)
	h.HardcoverStatus(w, r)
}

func (h *Handler) hardcoverInstance(w http.ResponseWriter, r *http.Request) (*Instance, bool) {
	w.Header().Set("Cache-Control", "no-store")
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return nil, false
	}
	if claims.Role != auth.RoleAdmin {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return nil, false
	}
	inst, err := h.store.Get(chi.URLParam(r, "instanceID"))
	if err != nil {
		http.Error(w, `{"error":"failed to get instance"}`, http.StatusInternalServerError)
		return nil, false
	}
	if inst == nil {
		http.Error(w, `{"error":"instance not found"}`, http.StatusNotFound)
		return nil, false
	}
	return inst, true
}

func writeHardcoverStatus(w http.ResponseWriter, supported, configured bool) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"supported":  supported,
		"configured": configured,
	})
}

// normalizeHardcoverToken accepts what an admin actually pastes -- Hardcover's
// settings page shows the token with a "Bearer " prefix -- and refuses
// anything that could not be a single header value.
func normalizeHardcoverToken(raw string) (string, error) {
	token := strings.TrimSpace(raw)
	if len(token) > 7 && strings.EqualFold(token[:7], "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	if token == "" {
		return "", errors.New("token is required")
	}
	if len(token) > hardcoverTokenMaxLen || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("that does not look like a Hardcover API token")
	}
	return token, nil
}
