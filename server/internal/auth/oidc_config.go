package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

var (
	ErrSSORequired     = errors.New("sign in with single sign-on to continue")
	ErrOIDCConfig      = errors.New("single sign-on configuration is missing or unreadable; ask an administrator to check Single sign-on settings")
	ErrOIDCUnavailable = errors.New("the identity provider could not complete sign-in; please try again")
	ErrOIDCFlow        = errors.New("this sign-in attempt is invalid or has expired; please start again")
	ErrOIDCGroups      = errors.New("your identity provider did not supply a permitted group")
	ErrOIDCUnlinked    = errors.New("this identity is not linked to an account; sign in to your existing account and link it in Account settings, or ask for an invitation")
	ErrOIDCConflict    = errors.New("this identity or account already has a different link; existing links cannot be replaced by an invitation")
)

// ClientSecret is accepted only on writes. A nil pointer preserves it; an
// explicit empty string removes it (for providers with a public client).
// Ciphertext is kept in a separate storage envelope and is never returned.
type OIDCConfig struct {
	Enabled          bool     `json:"enabled"`
	Label            string   `json:"label"`
	Issuer           string   `json:"issuer"`
	ClientID         string   `json:"client_id"`
	ClientSecret     *string  `json:"client_secret,omitempty"`
	AdditionalScopes []string `json:"additional_scopes"`
	AllowedGroups    []string `json:"allowed_groups"`
	GroupClaim       string   `json:"group_claim"`
	AutoCreate       bool     `json:"auto_create"`
	SSOOnly          bool     `json:"sso_only"`
	UseProxy         bool     `json:"use_proxy"`
	HasSecret        bool     `json:"has_secret"`
	CallbackURL      string   `json:"callback_url"`
	Tested           bool     `json:"tested"`
}

type oidcStoredConfig struct {
	Config OIDCConfig `json:"config"`
	Secret string     `json:"secret"`
}

type oidcConfiguration struct {
	OIDCConfig
	secret      string
	fingerprint string
	storedRaw   string
}

// SetOIDCCipher is wired once at startup, using the server's existing cipher.
func (s *Service) SetOIDCCipher(cipher *secrets.Cipher) { s.oidcCipher = cipher }

func (s *Service) oidcConfiguration() (oidcConfiguration, error) {
	var stored oidcStoredConfig
	var raw string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = 'oidc_config'").Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return oidcConfiguration{}, ErrOIDCConfig
	}
	if err == nil && (strings.TrimSpace(raw) == "null" || json.Unmarshal([]byte(raw), &stored) != nil) {
		return oidcConfiguration{}, ErrOIDCConfig
	}
	c := oidcConfiguration{OIDCConfig: stored.Config, storedRaw: raw}
	c.ClientSecret = nil
	c.Label = strings.TrimSpace(c.Label)
	if c.Label == "" {
		c.Label = "Single sign-on"
	}
	if c.GroupClaim == "" {
		c.GroupClaim = "groups"
	}
	if c.AdditionalScopes == nil {
		c.AdditionalScopes = []string{}
	}
	if c.AllowedGroups == nil {
		c.AllowedGroups = []string{}
	}
	if stored.Secret != "" {
		if s.oidcCipher == nil || !secrets.IsEncrypted(stored.Secret) {
			return c, ErrOIDCConfig
		}
		c.secret, err = s.oidcCipher.Decrypt(stored.Secret)
		if err != nil {
			return c, ErrOIDCConfig
		}
	}
	c.HasSecret = c.secret != ""
	c.CallbackURL, err = s.oidcCallbackURL()
	if err != nil && (c.Enabled || c.Issuer != "") {
		return c, err
	}
	c.fingerprint = oidcFingerprint(c)
	var tested string
	err = s.db.QueryRow("SELECT value FROM settings WHERE key = 'oidc_tested'").Scan(&tested)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c, ErrOIDCConfig
	}
	c.Tested = tested == c.fingerprint
	if c.Enabled {
		if err := c.validate(); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (s *Service) oidcCallbackURL() (string, error) {
	var raw string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = 'server_settings'").Scan(&raw); err != nil {
		return "", ErrOIDCConfig
	}
	var settings struct {
		ExternalURL string `json:"external_url"`
	}
	if json.Unmarshal([]byte(raw), &settings) != nil {
		return "", ErrOIDCConfig
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(settings.ExternalURL), "/"))
	if err != nil || !oidcURLAllowed(u) || u.Path != "" {
		return "", ErrOIDCConfig
	}
	return u.String() + "/api/auth/oidc/callback", nil
}

func oidcURLAllowed(u *url.URL) bool {
	return u != nil && u.User == nil && u.RawQuery == "" && u.Fragment == "" && isAllowedRedirectURI(u.String())
}

func oidcFingerprint(c oidcConfiguration) string {
	copy := c.OIDCConfig
	// Enablement is a policy switch, so a successful test can enable SSO-only.
	// Every provider, claims, signup, transport and callback change needs a new test.
	copy.Enabled, copy.SSOOnly, copy.Tested, copy.HasSecret = false, false, false, false
	copy.ClientSecret = nil
	raw, _ := json.Marshal(copy)
	return hashToken(string(raw) + "\x00" + c.secret)
}

func (c oidcConfiguration) validate() error {
	u, err := url.Parse(c.Issuer)
	if err != nil || !oidcURLAllowed(u) || strings.TrimSpace(c.ClientID) == "" || c.CallbackURL == "" {
		return ErrOIDCConfig
	}
	if len(c.Label) > 80 || len(c.Issuer) > 2048 || len(c.ClientID) > 1024 || len(c.secret) > 16384 || len(c.GroupClaim) > 256 || len(c.AllowedGroups) > 100 || len(c.AdditionalScopes) > 30 {
		return ErrOIDCConfig
	}
	for _, scope := range c.AdditionalScopes {
		if scope == "" || strings.ContainsAny(scope, " \t\r\n") {
			return errors.New("additional scopes must be individual scope names")
		}
	}
	for _, group := range c.AllowedGroups {
		if group == "" || len(group) > 512 {
			return errors.New("allowed groups must be nonempty group names")
		}
	}
	return nil
}

// The same client is injected into discovery, remote keys, token exchange and
// UserInfo. Discovery cannot downgrade any endpoint to cleartext off loopback.
func (c oidcConfiguration) provider(ctx context.Context) (*oidc.Provider, *oauth2.Config, context.Context, error) {
	if err := c.validate(); err != nil {
		return nil, nil, ctx, err
	}
	var transport http.RoundTripper = httpx.Internal()
	if c.UseProxy {
		transport = httpx.External()
	}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !oidcTransportURLAllowed(req.URL) {
			return ErrOIDCUnavailable
		}
		return nil
	}}
	ctx = oidc.ClientContext(ctx, client)
	p, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, nil, ctx, ErrOIDCUnavailable
	}
	var metadata struct {
		JWKS     string `json:"jwks_uri"`
		UserInfo string `json:"userinfo_endpoint"`
	}
	if p.Claims(&metadata) != nil {
		return nil, nil, ctx, ErrOIDCUnavailable
	}
	for _, endpoint := range []string{p.Endpoint().AuthURL, p.Endpoint().TokenURL, metadata.JWKS, metadata.UserInfo} {
		if endpoint == "" {
			continue
		}
		u, e := url.Parse(endpoint)
		if e != nil || !oidcTransportURLAllowed(u) {
			return nil, nil, ctx, ErrOIDCConfig
		}
	}
	scopes := []string{oidc.ScopeOpenID, "profile", "email"}
	for _, scope := range c.AdditionalScopes {
		if !containsString(scopes, scope) {
			scopes = append(scopes, scope)
		}
	}
	return p, &oauth2.Config{ClientID: c.ClientID, ClientSecret: c.secret, Endpoint: p.Endpoint(), RedirectURL: c.CallbackURL, Scopes: scopes}, ctx, nil
}
func oidcTransportURLAllowed(u *url.URL) bool {
	return u != nil && u.User == nil && isAllowedRedirectURI(u.String())
}
func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func (s *Service) saveOIDCConfig(next OIDCConfig, actor *Claims) (OIDCConfig, error) {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	if err := s.AuthorizePermission(context.Background(), actor.UserID, actor.DeviceID, PermissionUsersManage); err != nil {
		return next, err
	}
	current, err := s.oidcConfiguration()
	if err != nil {
		return next, err
	}
	c := oidcConfiguration{OIDCConfig: next, secret: current.secret}
	if next.ClientSecret != nil {
		c.secret = *next.ClientSecret
	}
	c.ClientSecret = nil
	c.Label = strings.TrimSpace(c.Label)
	if c.Label == "" {
		c.Label = "Single sign-on"
	}
	if c.GroupClaim == "" {
		c.GroupClaim = "groups"
	}
	c.Issuer = strings.TrimSpace(c.Issuer)
	c.ClientID = strings.TrimSpace(c.ClientID)
	if c.AdditionalScopes == nil {
		c.AdditionalScopes = []string{}
	}
	if c.AllowedGroups == nil {
		c.AllowedGroups = []string{}
	}
	c.CallbackURL, err = s.oidcCallbackURL()
	if (c.Enabled || c.Issuer != "") && err != nil {
		return next, err
	}
	if c.Enabled || c.Issuer != "" {
		if err = c.validate(); err != nil {
			return next, err
		}
	}
	if !c.Enabled {
		c.SSOOnly = false
	}
	c.fingerprint = oidcFingerprint(c)
	if c.SSOOnly && (!current.Tested || current.fingerprint != c.fingerprint) {
		return next, errors.New("complete Test sign-in with these settings before requiring single sign-on")
	}
	var encrypted string
	if c.secret != "" {
		if s.oidcCipher == nil {
			return next, ErrOIDCConfig
		}
		encrypted, err = s.oidcCipher.Encrypt(c.secret)
		if err != nil {
			return next, ErrOIDCConfig
		}
	}
	c.Tested = false
	c.HasSecret = false
	c.CallbackURL = ""
	raw, err := json.Marshal(oidcStoredConfig{Config: c.OIDCConfig, Secret: encrypted})
	if err != nil {
		return next, ErrOIDCConfig
	}
	tx, err := s.db.Begin()
	if err != nil {
		return next, ErrAuthUnavailable
	}
	defer tx.Rollback()
	if err = oidcActorInTransaction(tx, actor, true); err != nil {
		return next, err
	}
	if c.SSOOnly {
		count, err := oidcRecoveryCount(tx, 0)
		if err != nil {
			return next, err
		}
		if count == 0 {
			return next, errors.New("an administrator must have a local password before requiring single sign-on")
		}
		if _, err = tx.Exec("UPDATE devices SET revoked_at=? WHERE auth_method='local' AND revoked_at IS NULL AND user_id IN (SELECT id FROM users WHERE role != 'admin')", time.Now()); err != nil {
			return next, ErrAuthUnavailable
		}
		if _, err = tx.Exec("DELETE FROM oauth_authorization_codes WHERE auth_method='local' AND user_id IN (SELECT id FROM users WHERE role != 'admin')"); err != nil {
			return next, ErrAuthUnavailable
		}
	}
	policy := "false"
	if c.SSOOnly {
		policy = "true"
	}
	if _, err = tx.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES ('oidc_config',?),('oidc_sso_only',?)", string(raw), policy); err != nil {
		return next, ErrAuthUnavailable
	}
	if err = tx.Commit(); err != nil {
		return next, ErrAuthUnavailable
	}
	s.oidcFlows.clear()
	saved, err := s.oidcConfiguration()
	return saved.OIDCConfig, err
}

func (s *Service) requireLocalSignIn(userID int64) error {
	var allowed bool
	err := s.db.QueryRow(`SELECT role='admin' OR COALESCE((SELECT value FROM settings WHERE key='oidc_sso_only'),'false')='false' FROM users WHERE id=?`, userID).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return ErrAuthUnavailable
	}
	if !allowed {
		return ErrSSORequired
	}
	return nil
}

func protectOIDCRecovery(tx *sql.Tx, userID int64) error {
	var policy string
	if err := tx.QueryRow("SELECT COALESCE((SELECT value FROM settings WHERE key='oidc_sso_only'),'false')").Scan(&policy); err != nil {
		return ErrAuthUnavailable
	}
	if policy == "false" {
		return nil
	}
	count, err := oidcRecoveryCount(tx, userID)
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrLastAdmin
	}
	return nil
}
func oidcRecoveryCount(tx *sql.Tx, excluding int64) (int, error) {
	rows, err := tx.Query("SELECT password_hash FROM users WHERE role='admin' AND id!=?", excluding)
	if err != nil {
		return 0, ErrAuthUnavailable
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var hash string
		if rows.Scan(&hash) != nil {
			return 0, ErrAuthUnavailable
		}
		if _, err = bcrypt.Cost([]byte(hash)); err == nil {
			count++
		}
	}
	return count, rows.Err()
}

func (s *Service) requireConnectSignIn() error {
	var policy string
	err := s.db.QueryRow("SELECT COALESCE((SELECT value FROM settings WHERE key='oidc_sso_only'),'false')").Scan(&policy)
	if err != nil {
		return ErrAuthUnavailable
	}
	if policy != "false" {
		return ErrSSORequired
	}
	return nil
}
