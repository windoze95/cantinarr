package grokoauth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// Options controls only test seams. Production wiring leaves it zero-valued:
// endpoints stay pinned to auth.x.ai and the shared public Grok CLI client.
type Options struct {
	AuthBaseURL string
	ClientID    string
	HTTPClient  *http.Client
	// Clock lets tests step through poll intervals and token expiries.
	Clock func() time.Time
}

// Manager owns encrypted xAI authorizations plus in-flight per-account device
// flows. It is safe for concurrent use.
type Manager struct {
	db         *sql.DB
	cipher     *secrets.Cipher
	httpClient *http.Client
	authBase   string
	clientID   string
	nowFn      func() time.Time

	flowsMu      sync.Mutex
	flows        map[string]*deviceFlow
	accountFlows map[AccountRef]string

	accountMu    sync.Mutex
	accountLocks map[AccountRef]*sync.Mutex

	// pendingAuth holds a rotated token pair whose persist failed. The old
	// refresh token is already consumed upstream at that point, so dropping
	// the pair would force a full re-link; it is retained here and persisted
	// on the next resolution instead.
	pendingMu   sync.Mutex
	pendingAuth map[AccountRef]storedAuth
}

type deviceFlow struct {
	id              string
	actorID         int64
	account         AccountRef
	deviceCode      string
	userCode        string
	verificationURI string
	expiresAt       time.Time
	interval        time.Duration

	mu         sync.Mutex
	nextPollAt time.Time
}

func NewManager(db *sql.DB, cipher *secrets.Cipher, opts Options) *Manager {
	authBase := strings.TrimRight(strings.TrimSpace(opts.AuthBaseURL), "/")
	if authBase == "" {
		authBase = defaultAuthBaseURL
	}
	clientID := strings.TrimSpace(opts.ClientID)
	if clientID == "" {
		clientID = sharedGrokClientID
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Transport: httpx.External(), Timeout: upstreamTimeout}
	}
	nowFn := opts.Clock
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Manager{
		db:           db,
		cipher:       cipher,
		httpClient:   httpClient,
		authBase:     authBase,
		clientID:     clientID,
		nowFn:        nowFn,
		flows:        make(map[string]*deviceFlow),
		accountFlows: make(map[AccountRef]string),
		accountLocks: make(map[AccountRef]*sync.Mutex),
		pendingAuth:  make(map[AccountRef]storedAuth),
	}
}

// Available reports whether Grok OAuth can operate. The flow is pure HTTPS,
// so availability only requires storage and the secrets cipher.
func (m *Manager) Available() bool {
	return m != nil && m.db != nil && m.cipher != nil
}

// HasAccount reports whether the user has a personal xAI authorization.
// Storage errors read as false; resolution paths use AccountExists instead.
func (m *Manager) HasAccount(userID int64) bool {
	connected, err := m.AccountExists(PersonalAccount(userID))
	return err == nil && connected
}

func (m *Manager) now() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
}

// BeginDeviceLogin starts one user-owned xAI device authorization.
func (m *Manager) BeginDeviceLogin(ctx context.Context, userID int64) (DeviceLogin, error) {
	return m.BeginDeviceLoginForAccount(ctx, PersonalAccount(userID), userID)
}

func (m *Manager) BeginDeviceLoginForAccount(ctx context.Context, account AccountRef, actorID int64) (DeviceLogin, error) {
	if !m.Available() || !account.valid() || actorID <= 0 {
		return DeviceLogin{}, ErrInvalidInput
	}
	connected, err := m.AccountExists(account)
	if err != nil {
		return DeviceLogin{}, err
	}
	if connected {
		return DeviceLogin{}, ErrAlreadyConnected
	}
	if err := m.reserveLoginStart(account, actorID); err != nil {
		return DeviceLogin{}, err
	}

	login, flow, err := m.requestDeviceCode(ctx, account, actorID)
	if err != nil {
		m.releaseLoginStart(account)
		return DeviceLogin{}, err
	}
	m.publishLoginFlow(flow)
	return login, nil
}

// reserveLoginStart holds the account's single login slot, purging any
// expired flow first so an abandoned attempt cannot block relinking forever.
// The same actor starting over replaces their own pending flow: an app that
// died mid-flow (page refresh, crash) would otherwise be locked out until
// that orphaned flow expires, with no flow id left to cancel it.
func (m *Manager) reserveLoginStart(account AccountRef, actorID int64) error {
	m.flowsMu.Lock()
	defer m.flowsMu.Unlock()
	m.purgeExpiredFlowsLocked()
	if id, busy := m.accountFlows[account]; busy {
		flow, ok := m.flows[id]
		if id == "" || !ok || flow.actorID != actorID {
			return ErrLoginInProgress
		}
		delete(m.flows, id)
		delete(m.accountFlows, account)
	}
	if len(m.flows) >= maxPendingFlows {
		return ErrProvider
	}
	// Reserve with an empty id so a concurrent Begin fails fast while the
	// upstream request is in flight.
	m.accountFlows[account] = ""
	return nil
}

func (m *Manager) releaseLoginStart(account AccountRef) {
	m.flowsMu.Lock()
	if id, ok := m.accountFlows[account]; ok && id == "" {
		delete(m.accountFlows, account)
	}
	m.flowsMu.Unlock()
}

func (m *Manager) publishLoginFlow(flow *deviceFlow) {
	m.flowsMu.Lock()
	m.flows[flow.id] = flow
	m.accountFlows[flow.account] = flow.id
	m.flowsMu.Unlock()
}

func (m *Manager) purgeExpiredFlowsLocked() {
	now := m.now()
	for id, flow := range m.flows {
		if now.After(flow.expiresAt) {
			delete(m.flows, id)
			if m.accountFlows[flow.account] == id {
				delete(m.accountFlows, flow.account)
			}
		}
	}
}

type deviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

func (m *Manager) requestDeviceCode(ctx context.Context, account AccountRef, actorID int64) (DeviceLogin, *deviceFlow, error) {
	form := url.Values{
		"client_id": {m.clientID},
		"scope":     {oauthScope},
	}
	status, body, err := m.postForm(ctx, m.authBase+deviceCodePath, form)
	if err != nil || status != http.StatusOK {
		return DeviceLogin{}, nil, ErrProvider
	}
	var resp deviceCodeResponse
	if err := json.Unmarshal(body, &resp); err != nil || resp.DeviceCode == "" || resp.UserCode == "" {
		return DeviceLogin{}, nil, ErrProvider
	}
	verification := m.trustedVerificationURL(resp.VerificationURIComplete)
	if verification == "" {
		verification = m.trustedVerificationURL(resp.VerificationURI)
	}
	if verification == "" || !safeUserCode(resp.UserCode) {
		return DeviceLogin{}, nil, ErrProvider
	}

	now := m.now()
	lifetime := deviceLoginLifetime
	if resp.ExpiresIn > 0 && time.Duration(resp.ExpiresIn)*time.Second < lifetime {
		lifetime = time.Duration(resp.ExpiresIn) * time.Second
	}
	interval := defaultPollInterval
	if resp.Interval > 0 {
		interval = min(time.Duration(resp.Interval)*time.Second, maxPollInterval)
	}
	flowID, err := randomFlowID()
	if err != nil {
		return DeviceLogin{}, nil, ErrProvider
	}
	flow := &deviceFlow{
		id:              flowID,
		actorID:         actorID,
		account:         account,
		deviceCode:      resp.DeviceCode,
		userCode:        resp.UserCode,
		verificationURI: verification,
		expiresAt:       now.Add(lifetime),
		interval:        interval,
		nextPollAt:      now.Add(interval),
	}
	login := DeviceLogin{
		FlowID:          flowID,
		VerificationURI: verification,
		UserCode:        resp.UserCode,
		ExpiresAt:       flow.expiresAt,
		IntervalSeconds: int(interval / time.Second),
	}
	return login, flow, nil
}

// trustedVerificationURL accepts only HTTPS URLs on xAI's own sign-in hosts,
// so a compromised upstream response can never send a user elsewhere. A
// non-default test base URL trusts its own host too.
func (m *Manager) trustedVerificationURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "https" && (host == "auth.x.ai" || host == "accounts.x.ai") {
		return raw
	}
	if m.authBase != defaultAuthBaseURL {
		if base, err := url.Parse(m.authBase); err == nil && parsed.Scheme == base.Scheme && parsed.Host == base.Host {
			return raw
		}
	}
	return ""
}

// safeUserCode keeps the code display-safe: short and printable ASCII.
func safeUserCode(code string) bool {
	if len(code) == 0 || len(code) > 32 {
		return false
	}
	for _, r := range code {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// CheckDeviceLogin polls only a flow owned by the authenticated user.
func (m *Manager) CheckDeviceLogin(ctx context.Context, userID int64, flowID string) (DeviceLoginCheck, error) {
	return m.CheckDeviceLoginForAccount(ctx, PersonalAccount(userID), userID, flowID)
}

func (m *Manager) CheckDeviceLoginForAccount(ctx context.Context, account AccountRef, actorID int64, flowID string) (DeviceLoginCheck, error) {
	if !m.Available() || !account.valid() {
		return DeviceLoginCheck{}, ErrInvalidInput
	}
	flow, err := m.lookupFlow(account, actorID, flowID)
	if err != nil {
		return DeviceLoginCheck{}, err
	}

	flow.mu.Lock()
	defer flow.mu.Unlock()
	now := m.now()
	if now.After(flow.expiresAt) {
		m.removeFlow(flow)
		return DeviceLoginCheck{}, ErrFlowExpired
	}
	if now.Before(flow.nextPollAt) {
		return DeviceLoginCheck{Status: LoginPending}, nil
	}

	tokens, pollErr := m.pollDeviceToken(ctx, flow)
	switch {
	case pollErr == nil:
	case errors.Is(pollErr, errAuthorizationPending):
		return DeviceLoginCheck{Status: LoginPending}, nil
	case errors.Is(pollErr, errAccessDenied):
		m.removeFlow(flow)
		return DeviceLoginCheck{Status: LoginFailed, Error: "xAI did not approve the connection."}, nil
	case IsCode(pollErr, CodeFlowExpired):
		m.removeFlow(flow)
		return DeviceLoginCheck{}, ErrFlowExpired
	default:
		return DeviceLoginCheck{}, pollErr
	}

	email, plan := identityFromIDToken(tokens.IDToken)
	auth := storedAuth{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ObtainedAt:   now.Unix(),
		ExpiresAt:    tokenExpiryUnix(tokens, now),
	}
	if err := m.saveAccount(flow.account, auth, email, plan); err != nil {
		m.removeFlow(flow)
		return DeviceLoginCheck{}, err
	}
	m.removeFlow(flow)
	return DeviceLoginCheck{
		Status:  LoginConnected,
		Account: AccountStatus{Connected: true, Email: email, PlanType: plan},
	}, nil
}

func (m *Manager) lookupFlow(account AccountRef, actorID int64, flowID string) (*deviceFlow, error) {
	m.flowsMu.Lock()
	defer m.flowsMu.Unlock()
	flow, ok := m.flows[flowID]
	if !ok || flow.account != account || flow.actorID != actorID {
		return nil, ErrFlowNotFound
	}
	return flow, nil
}

func (m *Manager) removeFlow(flow *deviceFlow) {
	m.flowsMu.Lock()
	delete(m.flows, flow.id)
	if m.accountFlows[flow.account] == flow.id {
		delete(m.accountFlows, flow.account)
	}
	m.flowsMu.Unlock()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type tokenErrorResponse struct {
	Error string `json:"error"`
}

var (
	errAuthorizationPending = &Error{Code: Code("authorization_pending"), message: "authorization pending"}
	errAccessDenied         = &Error{Code: Code("access_denied"), message: "access denied"}
)

// pollDeviceToken performs one upstream device-token poll, honoring the
// server-directed interval (RFC 8628: slow_down stretches it by 5 seconds).
func (m *Manager) pollDeviceToken(ctx context.Context, flow *deviceFlow) (tokenResponse, error) {
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {flow.deviceCode},
		"client_id":   {m.clientID},
	}
	status, body, err := m.postForm(ctx, m.authBase+tokenPath, form)
	if err != nil {
		return tokenResponse{}, ErrProvider
	}
	flow.nextPollAt = m.now().Add(flow.interval)
	if status == http.StatusOK {
		var tokens tokenResponse
		if err := json.Unmarshal(body, &tokens); err != nil || tokens.AccessToken == "" {
			return tokenResponse{}, ErrProvider
		}
		return tokens, nil
	}
	var oauthErr tokenErrorResponse
	_ = json.Unmarshal(body, &oauthErr)
	switch oauthErr.Error {
	case "authorization_pending":
		return tokenResponse{}, errAuthorizationPending
	case "slow_down":
		flow.interval = min(flow.interval+5*time.Second, maxPollInterval)
		flow.nextPollAt = m.now().Add(flow.interval)
		return tokenResponse{}, errAuthorizationPending
	case "access_denied":
		return tokenResponse{}, errAccessDenied
	case "expired_token":
		return tokenResponse{}, ErrFlowExpired
	default:
		return tokenResponse{}, ErrProvider
	}
}

// CancelDeviceLogin cancels one pending flow owned by the caller.
func (m *Manager) CancelDeviceLogin(userID int64, flowID string) error {
	return m.CancelDeviceLoginForAccount(PersonalAccount(userID), userID, flowID)
}

func (m *Manager) CancelDeviceLoginForAccount(account AccountRef, actorID int64, flowID string) error {
	if !m.Available() || !account.valid() {
		return ErrInvalidInput
	}
	flow, err := m.lookupFlow(account, actorID, flowID)
	if err != nil {
		return err
	}
	m.removeFlow(flow)
	return nil
}

// Unlink deletes the caller's encrypted xAI authorization and any pending
// device flow. It does not affect another Cantinarr user.
func (m *Manager) Unlink(userID int64) error {
	return m.UnlinkAccount(PersonalAccount(userID))
}

func (m *Manager) UnlinkAccount(account AccountRef) error {
	if !m.Available() || !account.valid() {
		return ErrInvalidInput
	}
	m.flowsMu.Lock()
	if id, ok := m.accountFlows[account]; ok {
		delete(m.accountFlows, account)
		delete(m.flows, id)
	}
	m.flowsMu.Unlock()
	m.dropPending(account)
	return m.deleteAccount(account)
}

// StatusForAccount returns safe status for a personal or shared authorization.
func (m *Manager) StatusForAccount(account AccountRef) (AccountStatus, error) {
	if !m.Available() || !account.valid() {
		return AccountStatus{}, ErrInvalidInput
	}
	status, found, err := m.accountMetadata(account)
	if err != nil {
		return AccountStatus{}, err
	}
	if !found {
		return AccountStatus{Connected: false}, nil
	}
	return status, nil
}

// AccessToken returns a bearer token for api.x.ai, refreshing it first when
// it is expiring. xAI rotates refresh tokens on every use, so refreshes are
// serialized per account and the rotated pair is persisted before returning.
// A pair whose persist failed is retained in memory and re-persisted here,
// because its predecessor is already consumed upstream. The not-expiring fast
// path deliberately skips the account lock: a token read during a concurrent
// refresh stays valid until its own expiry, while a queued read would stall
// every shared-account turn behind one slow upstream refresh.
func (m *Manager) AccessToken(ctx context.Context, account AccountRef) (string, error) {
	if !m.Available() || !account.valid() {
		return "", ErrInvalidInput
	}
	now := m.now()
	if _, ok := m.peekPending(account); !ok {
		record, found, err := m.loadAccount(account)
		if err != nil {
			return "", err
		}
		if !found {
			return "", ErrNotConnected
		}
		if !tokenExpiring(record.auth, now) {
			return record.auth.AccessToken, nil
		}
	}

	lock := m.lockForAccount(account)
	lock.Lock()
	defer lock.Unlock()

	record, found, err := m.loadAccount(account)
	if err != nil {
		return "", err
	}
	if !found {
		m.dropPending(account)
		return "", ErrNotConnected
	}
	if pending, ok := m.peekPending(account); ok {
		// The stored row still holds the consumed predecessor; the pending
		// pair is authoritative. Keep trying to land it on disk.
		record.auth = pending
		if err := m.saveRefreshedAuth(account, pending); err == nil {
			m.dropPending(account)
		}
	}
	now = m.now()
	if !tokenExpiring(record.auth, now) {
		return record.auth.AccessToken, nil
	}
	if record.auth.RefreshToken == "" {
		if record.auth.ExpiresAt > now.Unix() {
			return record.auth.AccessToken, nil
		}
		return "", ErrReloginRequired
	}
	tokens, err := m.refreshTokens(ctx, record.auth.RefreshToken)
	if err != nil {
		return "", err
	}
	auth := storedAuth{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ObtainedAt:   now.Unix(),
		ExpiresAt:    tokenExpiryUnix(tokens, now),
	}
	if auth.RefreshToken == "" {
		auth.RefreshToken = record.auth.RefreshToken
	}
	if err := m.saveRefreshedAuth(account, auth); err != nil {
		// The old refresh token is already burned upstream; losing this pair
		// would demand a full re-link over a transient storage error.
		m.putPending(account, auth)
	} else {
		m.dropPending(account)
	}
	return auth.AccessToken, nil
}

func (m *Manager) peekPending(account AccountRef) (storedAuth, bool) {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	auth, ok := m.pendingAuth[account]
	return auth, ok
}

func (m *Manager) putPending(account AccountRef, auth storedAuth) {
	m.pendingMu.Lock()
	m.pendingAuth[account] = auth
	m.pendingMu.Unlock()
}

func (m *Manager) dropPending(account AccountRef) {
	m.pendingMu.Lock()
	delete(m.pendingAuth, account)
	m.pendingMu.Unlock()
}

// tokenExpiryUnix derives when the granted access token stops working.
// expires_in is optional in RFC 6749 token responses, so absent that field
// the JWT exp claim is used, then a conservative default — a token stored as
// never-expiring would pin this account to its first access token forever
// and strand a perfectly good refresh token.
func tokenExpiryUnix(tokens tokenResponse, now time.Time) int64 {
	if tokens.ExpiresIn > 0 {
		return now.Add(time.Duration(tokens.ExpiresIn) * time.Second).Unix()
	}
	if exp := jwtExpiry(tokens.AccessToken); exp > now.Unix() {
		return exp
	}
	return now.Add(defaultTokenLifetime).Unix()
}

func jwtExpiry(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0
	}
	return claims.Exp
}

func (m *Manager) lockForAccount(account AccountRef) *sync.Mutex {
	m.accountMu.Lock()
	defer m.accountMu.Unlock()
	lock, ok := m.accountLocks[account]
	if !ok {
		lock = &sync.Mutex{}
		m.accountLocks[account] = lock
	}
	return lock
}

// tokenExpiring adapts the proactive-refresh skew to the token lifetime:
// subscription tokens run hours, device-flow tokens can run ~15 minutes, and
// refreshing the latter a full skew early would rotate on every resolution.
func tokenExpiring(auth storedAuth, now time.Time) bool {
	if auth.ExpiresAt <= 0 {
		return false
	}
	skew := refreshSkew
	if lifetime := auth.ExpiresAt - auth.ObtainedAt; auth.ObtainedAt > 0 && lifetime > 0 &&
		time.Duration(lifetime)*time.Second <= shortTokenLifetime {
		skew = shortTokenSkew
	}
	return !now.Before(time.Unix(auth.ExpiresAt, 0).Add(-skew))
}

func (m *Manager) refreshTokens(ctx context.Context, refreshToken string) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {m.clientID},
	}
	status, body, err := m.postForm(ctx, m.authBase+tokenPath, form)
	if err != nil {
		return tokenResponse{}, ErrProvider
	}
	if status != http.StatusOK {
		var oauthErr tokenErrorResponse
		_ = json.Unmarshal(body, &oauthErr)
		if oauthErr.Error == "invalid_grant" {
			return tokenResponse{}, ErrReloginRequired
		}
		return tokenResponse{}, ErrProvider
	}
	var tokens tokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil || tokens.AccessToken == "" {
		return tokenResponse{}, ErrProvider
	}
	return tokens, nil
}

func (m *Manager) postForm(ctx context.Context, endpoint string, form url.Values) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, upstreamTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// identityFromIDToken extracts display metadata from the OIDC id_token
// without verifying it. The token arrived over pinned HTTPS from the issuer
// itself and only ever labels the connection in the UI; it authorizes nothing.
func identityFromIDToken(idToken string) (email, plan string) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims struct {
		Email    string `json:"email"`
		Plan     string `json:"plan"`
		PlanType string `json:"plan_type"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	plan = claims.PlanType
	if plan == "" {
		plan = claims.Plan
	}
	return claims.Email, plan
}

func randomFlowID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func marshalAuth(auth storedAuth) ([]byte, error) {
	if auth.AccessToken == "" {
		return nil, ErrStorage
	}
	return json.Marshal(auth)
}

func unmarshalAuth(raw []byte, auth *storedAuth) error {
	return json.Unmarshal(raw, auth)
}
