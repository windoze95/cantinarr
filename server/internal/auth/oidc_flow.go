package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

const oidcAttemptTTL = 10 * time.Minute
const oidcHandoffTTL = time.Minute
const oidcMaxFlows = 2048

type oidcBeginRequest struct {
	Client     string     `json:"client"`
	Challenge  string     `json:"challenge"`
	Invitation string     `json:"invitation,omitempty"`
	DeviceName string     `json:"device_name"`
	HardwareID string     `json:"hardware_id"`
	OAuth      url.Values `json:"oauth,omitempty"`
}
type oidcAttempt struct {
	Generation uint64
	Request    oidcBeginRequest
	Purpose    string
	Actor      *Claims
	Config     oidcConfiguration
	State      string
	CookieHash string
	Nonce      string
	Verifier   string
	Expires    time.Time
	Started    bool
}
type oidcPrincipal struct {
	Subject string
	Name    string
}
type oidcHandoff struct {
	Attempt   oidcAttempt
	Principal oidcPrincipal
	Expires   time.Time
	Error     error
}
type oidcConsent struct {
	UserID      int64
	Issuer      string
	OAuth       url.Values
	Expires     time.Time
	Fingerprint string
}
type oidcFlowStore struct {
	generation uint64
	mu         sync.Mutex
	attempts   map[string]oidcAttempt
	handoffs   map[string]oidcHandoff
	consents   map[string]oidcConsent
}

func newOIDCFlowStore() *oidcFlowStore {
	return &oidcFlowStore{attempts: map[string]oidcAttempt{}, handoffs: map[string]oidcHandoff{}, consents: map[string]oidcConsent{}}
}
func (f *oidcFlowStore) clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation++
	clear(f.attempts)
	clear(f.handoffs)
	clear(f.consents)
}
func (f *oidcFlowStore) prune() {
	now := time.Now()
	for key, v := range f.attempts {
		if now.After(v.Expires) {
			delete(f.attempts, key)
		}
	}
	for key, v := range f.handoffs {
		if now.After(v.Expires) {
			delete(f.handoffs, key)
		}
	}
	for key, v := range f.consents {
		if now.After(v.Expires) {
			delete(f.consents, key)
		}
	}
}
func (f *oidcFlowStore) full() bool {
	return len(f.attempts)+len(f.handoffs)+len(f.consents) >= oidcMaxFlows
}
func validOIDCChallenge(challenge string) bool {
	b, e := base64.RawURLEncoding.DecodeString(challenge)
	return e == nil && len(b) == 32 && len(challenge) == 43
}
func (s *Service) beginOIDC(req oidcBeginRequest, purpose string, actor *Claims) (map[string]string, error) {
	if req.Client != "web" && req.Client != "mobile" && req.Client != "mcp" || !validOIDCChallenge(req.Challenge) || len(req.DeviceName) > 200 || len(req.HardwareID) > 200 || len(req.Invitation) > 128 {
		return nil, ErrOIDCFlow
	}
	if req.Client == "mcp" && purpose != "mcp" {
		return nil, ErrOIDCFlow
	}
	if purpose != "login" && req.Invitation != "" {
		return nil, ErrOIDCFlow
	}
	c, err := s.oidcConfiguration()
	if err != nil {
		return nil, err
	}
	if !c.Enabled && purpose != "test" {
		return nil, errors.New("single sign-on is disabled")
	}
	if err = c.validate(); err != nil {
		return nil, err
	}
	if purpose == "link" || purpose == "test" {
		if actor == nil {
			return nil, ErrInvalidCredentials
		}
		if _, err = s.authoritativeSession(context.Background(), actor.UserID, actor.DeviceID); err != nil {
			return nil, err
		}
	}
	if req.Invitation != "" {
		if _, err = s.oidcInvitation(req.Invitation); err != nil {
			return nil, err
		}
	}
	state, err := randomURLToken(32)
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	a := oidcAttempt{Request: req, Purpose: purpose, Actor: actor, Config: c, State: state, Nonce: nonce, Verifier: oauth2.GenerateVerifier(), Expires: time.Now().Add(oidcAttemptTTL)}
	f := s.oidcFlows
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prune()
	if f.full() {
		return nil, errors.New("too many sign-in attempts; please try again shortly")
	}
	a.Generation = f.generation
	f.attempts[state] = a
	base := strings.TrimSuffix(c.CallbackURL, "/callback")
	return map[string]string{"start_url": base + "/start?flow=" + url.QueryEscape(state), "flow": state}, nil
}
func oidcCookieName(state string) string { return "cantinarr_oidc_" + hashToken(state)[:16] }
func (s *Service) currentOIDCAttempt(a oidcAttempt) error {
	s.oidcFlows.mu.Lock()
	current := s.oidcFlows.generation
	s.oidcFlows.mu.Unlock()
	if current != a.Generation {
		return ErrOIDCFlow
	}
	c, err := s.oidcConfiguration()
	if err != nil {
		return err
	}
	if c.fingerprint != a.Config.fingerprint || c.Enabled != a.Config.Enabled || c.SSOOnly != a.Config.SSOOnly {
		return ErrOIDCFlow
	}
	return nil
}

func (h *Handler) OIDCStart(w http.ResponseWriter, r *http.Request) {
	oidcHeaders(w)
	state := r.URL.Query().Get("flow")
	f := h.service.oidcFlows
	f.mu.Lock()
	f.prune()
	a, ok := f.attempts[state]
	if !ok || a.Started {
		f.mu.Unlock()
		oidcHTTPError(w, ErrOIDCFlow)
		return
	}
	a.Started = true
	cookie, err := randomURLToken(32)
	if err != nil {
		f.mu.Unlock()
		oidcHTTPError(w, ErrAuthUnavailable)
		return
	}
	a.CookieHash = hashToken(cookie)
	f.attempts[state] = a
	f.mu.Unlock()
	if err = h.service.currentOIDCAttempt(a); err != nil {
		oidcHTTPError(w, err)
		return
	}
	_, oauth, _, err := a.Config.provider(r.Context())
	if err != nil {
		oidcHTTPError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcCookieName(state), Value: cookie, Path: "/api/auth/oidc", HttpOnly: true, Secure: strings.HasPrefix(a.Config.CallbackURL, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: int(oidcAttemptTTL.Seconds())})
	http.Redirect(w, r, oauth.AuthCodeURL(state, oidc.Nonce(a.Nonce), oauth2.S256ChallengeOption(a.Verifier)), http.StatusFound)
}
func (h *Handler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	oidcHeaders(w)
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(oidcCookieName(state))
	if err != nil {
		oidcHTTPError(w, ErrOIDCFlow)
		return
	}
	f := h.service.oidcFlows
	f.mu.Lock()
	f.prune()
	a, ok := f.attempts[state]
	if !ok || !a.Started || subtle.ConstantTimeCompare([]byte(a.CookieHash), []byte(hashToken(cookie.Value))) != 1 {
		f.mu.Unlock()
		oidcHTTPError(w, ErrOIDCFlow)
		return
	}
	delete(f.attempts, state)
	f.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: oidcCookieName(state), Value: "", Path: "/api/auth/oidc", HttpOnly: true, Secure: strings.HasPrefix(a.Config.CallbackURL, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	if err = h.service.currentOIDCAttempt(a); err != nil {
		oidcHTTPError(w, err)
		return
	}
	var principal oidcPrincipal
	if r.URL.Query().Get("error") != "" {
		err = errors.New("sign-in was cancelled or refused by the identity provider")
	} else {
		principal, err = a.verify(r.Context(), r.URL.Query().Get("code"))
	}
	handoff := oidcHandoff{Attempt: a, Principal: principal, Expires: time.Now().Add(oidcHandoffTTL)}
	if err != nil {
		handoff.Error = err
	}
	code, err := randomURLToken(32)
	if err != nil {
		oidcHTTPError(w, ErrAuthUnavailable)
		return
	}
	f.mu.Lock()
	f.prune()
	if f.full() {
		f.mu.Unlock()
		oidcHTTPError(w, ErrOIDCUnavailable)
		return
	}
	f.handoffs[hashToken(code)] = handoff
	f.mu.Unlock()
	values := url.Values{"code": {code}, "flow": {state}}
	base := strings.TrimSuffix(a.Config.CallbackURL, "/api/auth/oidc/callback")
	target := base + "/#/oidc/return?" + values.Encode()
	if a.Request.Client == "mobile" {
		target = "cantinarr://oidc?" + values.Encode()
	}
	if a.Request.Client == "mcp" {
		q := a.Request.OAuth
		q.Set("oidc_code", code)
		q.Set("oidc_flow", state)
		target = base + "/oauth/authorize?" + q.Encode()
	}
	http.Redirect(w, r, target, http.StatusFound)
}
func (a oidcAttempt) verify(ctx context.Context, code string) (oidcPrincipal, error) {
	var out oidcPrincipal
	if code == "" {
		return out, ErrOIDCFlow
	}
	provider, oauth, ctx, err := a.Config.provider(ctx)
	if err != nil {
		return out, err
	}
	token, err := oauth.Exchange(ctx, code, oauth2.VerifierOption(a.Verifier))
	if err != nil {
		return out, ErrOIDCUnavailable
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return out, ErrOIDCUnavailable
	}
	id, err := provider.Verifier(&oidc.Config{ClientID: a.Config.ClientID}).Verify(ctx, raw)
	if err != nil || id.Subject == "" || id.Nonce != a.Nonce {
		return out, ErrOIDCFlow
	}
	var claims map[string]any
	if id.Claims(&claims) != nil {
		return out, ErrOIDCFlow
	}
	// Audience membership is verified by go-oidc. For multi-audience tokens,
	// also require an explicit authorized party for this client.
	if azp, present := claims["azp"]; present {
		if v, ok := azp.(string); !ok || v != a.Config.ClientID {
			return out, ErrOIDCFlow
		}
	} else if len(id.Audience) > 1 {
		return out, ErrOIDCFlow
	}
	// Never let UserInfo replace a signed claim. Fetch it only to fill a missing
	// name/group claim, and validate sub before using any of its fields.
	_, hasGroups := claims[a.Config.GroupClaim]
	if (len(a.Config.AllowedGroups) > 0 && !hasGroups) || claimName(claims) == "" {
		var metadata struct {
			UserInfo string `json:"userinfo_endpoint"`
		}
		_ = provider.Claims(&metadata)
		if metadata.UserInfo != "" {
			info, e := provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
			if e != nil {
				return out, ErrOIDCUnavailable
			}
			if info.Subject != id.Subject {
				return out, ErrOIDCFlow
			}
			var extra map[string]any
			if info.Claims(&extra) != nil {
				return out, ErrOIDCFlow
			}
			for k, v := range extra {
				if _, present := claims[k]; !present {
					claims[k] = v
				}
			}
		}
	}
	if len(a.Config.AllowedGroups) > 0 {
		groups, ok := claims[a.Config.GroupClaim].([]any)
		if !ok {
			return out, ErrOIDCGroups
		}
		allowed := false
		for _, value := range groups {
			group, ok := value.(string)
			if !ok || group == "" {
				return out, ErrOIDCGroups
			}
			if containsString(a.Config.AllowedGroups, group) {
				allowed = true
			}
		}
		if !allowed {
			return out, ErrOIDCGroups
		}
	}
	return oidcPrincipal{Subject: id.Subject, Name: claimName(claims)}, nil
}
func claimName(claims map[string]any) string {
	for _, key := range []string{"preferred_username", "name", "email"} {
		if v, ok := claims[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (s *Service) exchangeOIDC(code, verifier, flow string) (any, error) {
	if len(verifier) < 43 || len(verifier) > 128 {
		return nil, ErrOIDCFlow
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	f := s.oidcFlows
	f.mu.Lock()
	f.prune()
	hand, ok := f.handoffs[hashToken(code)]
	if !ok || hand.Attempt.State != flow || !verifyPKCES256(verifier, hand.Attempt.Request.Challenge) {
		f.mu.Unlock()
		return nil, ErrOIDCFlow
	}
	delete(f.handoffs, hashToken(code))
	f.mu.Unlock()
	a := hand.Attempt
	if err := s.currentOIDCAttempt(a); err != nil {
		return nil, err
	}
	if hand.Error != nil {
		return nil, hand.Error
	}
	if a.Purpose == "test" || a.Purpose == "link" {
		if a.Actor == nil {
			return nil, ErrOIDCFlow
		}
		if _, err := s.authoritativeSession(context.Background(), a.Actor.UserID, a.Actor.DeviceID); err != nil {
			return nil, err
		}
	}
	if a.Purpose == "test" {
		if err := s.AuthorizePermission(context.Background(), a.Actor.UserID, a.Actor.DeviceID, PermissionUsersManage); err != nil {
			return nil, err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return nil, ErrAuthUnavailable
		}
		defer tx.Rollback()
		if err = oidcAttemptInTransaction(tx, a); err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES ('oidc_tested',?)", a.Config.fingerprint); err != nil {
			return nil, ErrAuthUnavailable
		}
		if err = tx.Commit(); err != nil {
			return nil, ErrAuthUnavailable
		}
		return map[string]string{"status": "tested", "message": "Test sign-in succeeded. No account was created or linked."}, nil
	}
	session, err := s.commitOIDCIdentity(a, hand.Principal)
	if err != nil {
		return nil, err
	}
	if a.Purpose == "link" {
		return map[string]string{"status": "linked"}, nil
	}
	if a.Purpose == "mcp" {
		ticket, err := randomURLToken(32)
		if err != nil {
			return nil, ErrAuthUnavailable
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.prune()
		if f.full() {
			return nil, ErrOIDCUnavailable
		}
		f.consents[hashToken(ticket)] = oidcConsent{UserID: session.User.ID, Issuer: a.Config.Issuer, OAuth: a.Request.OAuth, Expires: time.Now().Add(oidcHandoffTTL), Fingerprint: a.Config.fingerprint}
		return map[string]string{"status": "authenticated", "consent": ticket, "username": session.User.Username}, nil
	}
	return session, nil
}

func (s *Service) oidcInvitation(token string) (*ConnectToken, error) {
	var ct ConnectToken
	err := s.db.QueryRow("SELECT token,user_id,created_by,expires_at,redeemed_at FROM connect_tokens WHERE token=?", token).Scan(&ct.Token, &ct.UserID, &ct.CreatedBy, &ct.ExpiresAt, &ct.RedeemedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	if ct.RedeemedAt != nil {
		return nil, ErrTokenRedeemed
	}
	if time.Now().After(ct.ExpiresAt) {
		return nil, ErrTokenExpired
	}
	return &ct, nil
}
func (s *Service) commitOIDCIdentity(a oidcAttempt, p oidcPrincipal) (*TokenResponse, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	defer tx.Rollback()
	if err = oidcAttemptInTransaction(tx, a); err != nil {
		return nil, err
	}
	var linked int64
	err = tx.QueryRow("SELECT user_id FROM oidc_identities WHERE issuer=? AND subject=?", a.Config.Issuer, p.Subject).Scan(&linked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAuthUnavailable
	}
	intended := int64(0)
	if a.Purpose == "link" {
		intended = a.Actor.UserID
	}
	if a.Request.Invitation != "" {
		var expires time.Time
		var redeemed sql.NullTime
		err = tx.QueryRow("SELECT user_id,expires_at,redeemed_at FROM connect_tokens WHERE token=?", a.Request.Invitation).Scan(&intended, &expires, &redeemed)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTokenNotFound
		}
		if err != nil {
			return nil, ErrAuthUnavailable
		}
		if redeemed.Valid {
			return nil, ErrTokenRedeemed
		}
		if time.Now().After(expires) {
			return nil, ErrTokenExpired
		}
	}
	if intended != 0 && linked != 0 && linked != intended {
		return nil, ErrOIDCConflict
	}
	if intended != 0 {
		var subject string
		err = tx.QueryRow("SELECT subject FROM oidc_identities WHERE user_id=? AND issuer=?", intended, a.Config.Issuer).Scan(&subject)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAuthUnavailable
		}
		if err == nil && subject != p.Subject {
			return nil, ErrOIDCConflict
		}
	}
	if linked == 0 {
		if intended == 0 {
			if !a.Config.AutoCreate {
				return nil, ErrOIDCUnlinked
			}
			// A claim is only a display-name suggestion, never an account lookup key.
			name := oidcUsername(p.Name)
			for i := 0; i < 20; i++ {
				candidate := name
				if i > 0 {
					suffix, e := randomURLToken(6)
					if e != nil {
						return nil, ErrAuthUnavailable
					}
					candidate += "-" + suffix
				}
				result, e := tx.Exec("INSERT INTO users(username,password_hash,role) VALUES (?,'','user') ON CONFLICT(username) DO NOTHING", candidate)
				if e != nil {
					return nil, ErrAuthUnavailable
				}
				n, e := result.RowsAffected()
				if e != nil {
					return nil, ErrAuthUnavailable
				}
				if n == 1 {
					intended, _ = result.LastInsertId()
					break
				}
			}
			if intended == 0 {
				return nil, ErrAuthUnavailable
			}
		}
		if _, err = tx.Exec("INSERT INTO oidc_identities(issuer,subject,user_id) VALUES (?,?,?)", a.Config.Issuer, p.Subject, intended); err != nil {
			return nil, ErrOIDCConflict
		}
		linked = intended
	}
	if a.Request.Invitation != "" {
		result, e := tx.Exec("UPDATE connect_tokens SET redeemed_at=? WHERE token=? AND redeemed_at IS NULL", time.Now(), a.Request.Invitation)
		if e != nil {
			return nil, ErrAuthUnavailable
		}
		n, e := result.RowsAffected()
		if e != nil || n != 1 {
			return nil, ErrTokenRedeemed
		}
	}
	user, err := scanUserRecord(tx.QueryRow(userSelect+" WHERE u.id=?", linked))
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	response := &TokenResponse{User: userWithPermissions(user)}
	if a.Purpose == "login" {
		device := uuid.NewString()
		name := a.Request.DeviceName
		if name == "" {
			name = "Single sign-on"
		}
		// Never upgrade a local device: its old refresh tokens stay local.
		if _, err = tx.Exec("INSERT INTO devices(id,user_id,device_name,hardware_id,auth_method,oidc_issuer) VALUES (?,?,?,?,'oidc',?)", device, user.ID, name, a.Request.HardwareID, a.Config.Issuer); err != nil {
			return nil, ErrAuthUnavailable
		}
		response.DeviceID = device
		response.AccessToken, err = s.signAccessToken(user, device)
		if err != nil {
			return nil, err
		}
		response.RefreshToken, err = newOpaqueRefreshToken()
		if err != nil {
			return nil, ErrAuthUnavailable
		}
		if _, err = tx.Exec("INSERT INTO refresh_tokens(token_hash,device_id,user_id,expires_at) VALUES (?,?,?,?)", hashToken(response.RefreshToken), device, user.ID, refreshNeverExpires); err != nil {
			return nil, ErrAuthUnavailable
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, ErrAuthUnavailable
	}
	response.User.PlexInvitedAt = s.plexInvitedAt(user.ID)
	return response, nil
}
func oidcUsername(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-", r) {
			b.WriteRune(r)
		} else if unicode.IsSpace(r) {
			b.WriteByte('-')
		}
		if b.Len() >= 48 {
			break
		}
	}
	if b.Len() == 0 {
		return "sso-user"
	}
	return b.String()
}

type OIDCIdentity struct {
	Issuer    string    `json:"issuer"`
	Subject   string    `json:"subject"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Service) oidcIdentities(userID int64) ([]OIDCIdentity, error) {
	rows, err := s.db.Query("SELECT issuer,subject,created_at FROM oidc_identities WHERE user_id=? ORDER BY issuer", userID)
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	defer rows.Close()
	out := []OIDCIdentity{}
	for rows.Next() {
		var id OIDCIdentity
		if rows.Scan(&id.Issuer, &id.Subject, &id.CreatedAt) != nil {
			return nil, ErrAuthUnavailable
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *Service) unlinkOIDC(actor *Claims, userID int64, issuer string) error {
	if actor == nil {
		return ErrInvalidCredentials
	}
	self := actor.UserID == userID
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return ErrAuthUnavailable
	}
	defer tx.Rollback()
	if err = oidcActorInTransaction(tx, actor, !self); err != nil {
		return err
	}
	if self {
		var allowed bool
		err = tx.QueryRow(`SELECT (role='admin' OR COALESCE((SELECT value FROM settings WHERE key='oidc_sso_only'),'false')='false')
  AND ((password_hash!='' AND (password_enabled=1 OR role='admin')) OR ((passkey_enabled=1 OR role='admin') AND EXISTS(SELECT 1 FROM webauthn_credentials WHERE user_id=?))) FROM users WHERE id=?`, userID, userID).Scan(&allowed)
		if err != nil {
			return ErrAuthUnavailable
		}
		if !allowed {
			return errors.New("add a permitted local sign-in method before unlinking single sign-on")
		}
	}
	if _, err = tx.Exec("UPDATE devices SET revoked_at=? WHERE user_id=? AND auth_method='oidc' AND oidc_issuer=? AND revoked_at IS NULL", time.Now(), userID, issuer); err != nil {
		return ErrAuthUnavailable
	}
	if _, err = tx.Exec("DELETE FROM oauth_authorization_codes WHERE user_id=? AND oidc_issuer=?", userID, issuer); err != nil {
		return ErrAuthUnavailable
	}
	if _, err = tx.Exec("DELETE FROM oidc_identities WHERE user_id=? AND issuer=?", userID, issuer); err != nil {
		return ErrAuthUnavailable
	}
	if err = tx.Commit(); err != nil {
		return ErrAuthUnavailable
	}
	// A completed callback must not silently re-link after an explicit unlink.
	s.oidcFlows.clear()
	return nil
}

// These reads share the account/session write transaction. Revocation, role
// changes and External Address edits cannot slip between the check and commit.
func oidcAttemptInTransaction(tx *sql.Tx, a oidcAttempt) error {
	var raw, external string
	if err := tx.QueryRow("SELECT value FROM settings WHERE key='oidc_config'").Scan(&raw); err != nil {
		return ErrOIDCConfig
	}
	if raw != a.Config.storedRaw {
		return ErrOIDCFlow
	}
	if err := tx.QueryRow("SELECT value FROM settings WHERE key='server_settings'").Scan(&external); err != nil {
		return ErrOIDCConfig
	}
	var settings struct {
		ExternalURL string `json:"external_url"`
	}
	if json.Unmarshal([]byte(external), &settings) != nil {
		return ErrOIDCConfig
	}
	if strings.TrimRight(strings.TrimSpace(settings.ExternalURL), "/")+"/api/auth/oidc/callback" != a.Config.CallbackURL {
		return ErrOIDCFlow
	}
	if a.Purpose == "test" || a.Purpose == "link" {
		return oidcActorInTransaction(tx, a.Actor, a.Purpose == "test")
	}
	return nil
}
func oidcActorInTransaction(tx *sql.Tx, actor *Claims, admin bool) error {
	if actor == nil {
		return ErrInvalidCredentials
	}
	var role string
	var permitted bool
	err := tx.QueryRow(`SELECT u.role,(u.role='admin' OR d.auth_method='oidc' OR COALESCE((SELECT value FROM settings WHERE key='oidc_sso_only'),'false')='false')
 FROM users u JOIN devices d ON d.user_id=u.id WHERE u.id=? AND d.id=? AND d.revoked_at IS NULL`, actor.UserID, actor.DeviceID).Scan(&role, &permitted)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return ErrAuthUnavailable
	}
	if !permitted {
		return ErrSSORequired
	}
	if admin && role != RoleAdmin {
		return ErrPermissionDenied
	}
	return nil
}
