// Package remoteaccess classifies inbound connections and optionally requires
// HTTPS outside the server's local network. The policy is disabled when its
// setting is absent, so upgrading an existing installation never changes which
// requests are accepted.
package remoteaccess

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

const settingsKey = "remote_access_security"

// ConnectionSecurity is the server's view of the client-facing transport.
type ConnectionSecurity string

const (
	ConnectionHTTPS          ConnectionSecurity = "https"
	ConnectionLocalHTTP      ConnectionSecurity = "local_http"
	ConnectionRemoteHTTP     ConnectionSecurity = "remote_http"
	ConnectionUntrustedProxy ConnectionSecurity = "untrusted_proxy"
	ConnectionUnknownHTTP    ConnectionSecurity = "unknown_http"
)

// Settings is the persisted, administrator-controlled policy. The zero value
// is the compatibility contract for every existing installation.
type Settings struct {
	RequireHTTPSOutsideLocalNetwork bool     `json:"require_https_outside_local_network"`
	TrustedProxyCIDRs               []string `json:"trusted_proxy_cidrs"`
	TrustCGNATAsLocal               bool     `json:"trust_cgnat_as_local"`
}

// EnvironmentOverrides are deployment-owned values. Pointer booleans
// distinguish an explicit false (the lockout-recovery path) from an unset
// variable that leaves the stored value editable.
type EnvironmentOverrides struct {
	RequireHTTPSOutsideLocalNetwork *bool
	TrustedProxyCIDRs               []string
	TrustedProxyCIDRsSet            bool
	TrustCGNATAsLocal               *bool
}

// ManagedByEnvironment tells the app which controls are read-only.
type ManagedByEnvironment struct {
	RequireHTTPSOutsideLocalNetwork bool `json:"require_https_outside_local_network"`
	TrustedProxyCIDRs               bool `json:"trusted_proxy_cidrs"`
	TrustCGNATAsLocal               bool `json:"trust_cgnat_as_local"`
}

// Status is returned to administrators. WouldCurrentConnectionBeAllowed is a
// preflight for the values in this same response, not a claim about some other
// address the administrator might use later.
type Status struct {
	Settings
	ManagedByEnvironment            ManagedByEnvironment `json:"managed_by_environment"`
	ConnectionSecurity              ConnectionSecurity   `json:"connection_security"`
	HTTPSRequired                   bool                 `json:"https_required"`
	WouldCurrentConnectionBeAllowed bool                 `json:"would_current_connection_be_allowed"`
}

// ErrWouldBlockCurrentConnection prevents an administrator from turning on a
// policy that immediately removes the connection being used to save it.
var ErrWouldBlockCurrentConnection = errors.New("this connection would be blocked; reconnect over HTTPS or a local address first")

type effectiveSettings struct {
	Settings
	trustedProxyPrefixes []netip.Prefix
	managed              ManagedByEnvironment
}

// Service keeps a validated in-memory policy so request classification never
// reads SQLite. Writes update SQLite first and publish the new snapshot only
// after that succeeds.
type Service struct {
	db        *sql.DB
	overrides EnvironmentOverrides

	mu        sync.RWMutex
	stored    Settings
	effective effectiveSettings
}

// NewService loads the stored policy and applies deployment overrides.
func NewService(db *sql.DB, overrides EnvironmentOverrides) (*Service, error) {
	s := &Service{db: db, overrides: overrides}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload re-reads the persisted settings after another transaction changes
// them, notably first-run setup.
func (s *Service) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, err := readSettings(s.db)
	if err != nil {
		return err
	}
	effective, err := applyOverrides(stored, s.overrides)
	if err != nil {
		return err
	}
	s.stored = stored
	s.effective = effective
	return nil
}

func readSettings(q interface {
	QueryRow(query string, args ...any) *sql.Row
}) (Settings, error) {
	var raw string
	err := q.QueryRow("SELECT value FROM settings WHERE key = ?", settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return normalizeSettings(Settings{})
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read remote access security: %w", err)
	}
	var settings Settings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return Settings{}, fmt.Errorf("decode remote access security: %w", err)
	}
	return normalizeSettings(settings)
}

func normalizeSettings(in Settings) (Settings, error) {
	if in.TrustedProxyCIDRs == nil {
		in.TrustedProxyCIDRs = []string{}
	}
	_, canonical, err := parseTrustedProxyCIDRs(in.TrustedProxyCIDRs)
	if err != nil {
		return Settings{}, err
	}
	in.TrustedProxyCIDRs = canonical
	return in, nil
}

func applyOverrides(stored Settings, overrides EnvironmentOverrides) (effectiveSettings, error) {
	next := stored
	managed := ManagedByEnvironment{
		RequireHTTPSOutsideLocalNetwork: overrides.RequireHTTPSOutsideLocalNetwork != nil,
		TrustedProxyCIDRs:               overrides.TrustedProxyCIDRsSet,
		TrustCGNATAsLocal:               overrides.TrustCGNATAsLocal != nil,
	}
	if overrides.RequireHTTPSOutsideLocalNetwork != nil {
		next.RequireHTTPSOutsideLocalNetwork = *overrides.RequireHTTPSOutsideLocalNetwork
	}
	if overrides.TrustedProxyCIDRsSet {
		next.TrustedProxyCIDRs = append([]string(nil), overrides.TrustedProxyCIDRs...)
	}
	if overrides.TrustCGNATAsLocal != nil {
		next.TrustCGNATAsLocal = *overrides.TrustCGNATAsLocal
	}
	normalized, err := normalizeSettings(next)
	if err != nil {
		return effectiveSettings{}, err
	}
	prefixes, _, err := parseTrustedProxyCIDRs(normalized.TrustedProxyCIDRs)
	if err != nil {
		return effectiveSettings{}, err
	}
	return effectiveSettings{
		Settings:             normalized,
		trustedProxyPrefixes: prefixes,
		managed:              managed,
	}, nil
}

func parseTrustedProxyCIDRs(values []string) ([]netip.Prefix, []string, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	canonical := make([]string, 0, len(values))
	seen := make(map[netip.Prefix]bool, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			if addr, addrErr := netip.ParseAddr(value); addrErr == nil {
				bits := 128
				if addr.Is4() || addr.Is4In6() {
					addr = addr.Unmap()
					bits = 32
				}
				prefix = netip.PrefixFrom(addr, bits)
			} else {
				return nil, nil, fmt.Errorf("trusted proxy %q must be an IP address or CIDR", raw)
			}
		}
		addr := prefix.Addr()
		bits := prefix.Bits()
		if addr.Is4In6() {
			if bits < 96 {
				return nil, nil, fmt.Errorf("trusted proxy %q is not a valid IPv4-mapped CIDR", raw)
			}
			addr = addr.Unmap()
			bits -= 96
		}
		prefix = netip.PrefixFrom(addr, bits).Masked()
		if !prefix.IsValid() {
			return nil, nil, fmt.Errorf("trusted proxy %q is not a valid CIDR", raw)
		}
		if prefix.Bits() == 0 {
			return nil, nil, fmt.Errorf("trusted proxy %q cannot trust the entire internet", raw)
		}
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		prefixes = append(prefixes, prefix)
		canonical = append(canonical, prefix.String())
	}
	return prefixes, canonical, nil
}

func (s *Service) snapshot() effectiveSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.effective
	out.TrustedProxyCIDRs = append([]string(nil), out.TrustedProxyCIDRs...)
	out.trustedProxyPrefixes = append([]netip.Prefix(nil), out.trustedProxyPrefixes...)
	return out
}

// StatusFor describes the current request under the effective policy.
func (s *Service) StatusFor(r *http.Request) Status {
	settings := s.snapshot()
	return statusForRequest(r, settings)
}

func statusForRequest(r *http.Request, settings effectiveSettings) Status {
	connection := classify(r, settings)
	allowed := !settings.RequireHTTPSOutsideLocalNetwork ||
		connection == ConnectionHTTPS || connection == ConnectionLocalHTTP
	return Status{
		Settings:                        settings.Settings,
		ManagedByEnvironment:            settings.managed,
		ConnectionSecurity:              connection,
		HTTPSRequired:                   settings.RequireHTTPSOutsideLocalNetwork && !allowed,
		WouldCurrentConnectionBeAllowed: allowed,
	}
}

// CanEnableFrom reports whether enabling the stored policy from this request
// is safe. It is used by first-run setup before the account transaction starts.
func (s *Service) CanEnableFrom(r *http.Request) bool {
	s.mu.RLock()
	stored := s.stored
	s.mu.RUnlock()
	stored.RequireHTTPSOutsideLocalNetwork = true
	effective, err := applyOverrides(stored, s.overrides)
	if err != nil {
		return false
	}
	status := statusForRequest(r, effective)
	return status.WouldCurrentConnectionBeAllowed
}

// StoreSetupPreference writes the first-run choice inside the account setup
// transaction, so the account and its transport policy commit together.
func (s *Service) StoreSetupPreference(tx *sql.Tx, enabled bool) error {
	stored, err := readSettings(tx)
	if err != nil {
		return err
	}
	stored.RequireHTTPSOutsideLocalNetwork = enabled
	data, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("encode remote access security: %w", err)
	}
	if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", settingsKey, string(data)); err != nil {
		return fmt.Errorf("save remote access security: %w", err)
	}
	return nil
}

// Update saves administrator settings. Fields managed by the environment keep
// their stored values, preventing a temporary override from silently changing
// what takes effect after the variable is removed.
func (s *Service) Update(r *http.Request, requested Settings) (Status, error) {
	normalized, err := normalizeSettings(requested)
	if err != nil {
		return Status{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.overrides.RequireHTTPSOutsideLocalNetwork != nil {
		normalized.RequireHTTPSOutsideLocalNetwork = s.stored.RequireHTTPSOutsideLocalNetwork
	}
	if s.overrides.TrustedProxyCIDRsSet {
		normalized.TrustedProxyCIDRs = append([]string(nil), s.stored.TrustedProxyCIDRs...)
	}
	if s.overrides.TrustCGNATAsLocal != nil {
		normalized.TrustCGNATAsLocal = s.stored.TrustCGNATAsLocal
	}

	effective, err := applyOverrides(normalized, s.overrides)
	if err != nil {
		return Status{}, err
	}
	status := statusForRequest(r, effective)
	if effective.RequireHTTPSOutsideLocalNetwork && !status.WouldCurrentConnectionBeAllowed {
		return Status{}, ErrWouldBlockCurrentConnection
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return Status{}, fmt.Errorf("encode remote access security: %w", err)
	}
	if _, err := s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", settingsKey, string(data)); err != nil {
		return Status{}, fmt.Errorf("save remote access security: %w", err)
	}
	s.stored = normalized
	s.effective = effective
	return status, nil
}

// GetSettings serves the administrator settings and current-connection
// preflight. Authentication and administrator permission are applied by the
// API router.
func (s *Service) GetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.StatusFor(r))
}

// UpdateSettings validates and persists the administrator settings.
func (s *Service) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var requested Settings
	if err := json.NewDecoder(r.Body).Decode(&requested); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	status, err := s.Update(r, requested)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, ErrWouldBlockCurrentConnection) {
			code = http.StatusConflict
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// Middleware enforces the effective policy. Compatibility mode returns before
// any classification branch and is therefore behaviorally identical to the
// pre-feature server.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settings := s.snapshot()
		if !settings.RequireHTTPSOutsideLocalNetwork {
			next.ServeHTTP(w, r)
			return
		}
		connection := classify(r, settings)
		if connection == ConnectionHTTPS || connection == ConnectionLocalHTTP || informationalRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Upgrade", "TLS/1.2")
		writeJSON(w, http.StatusUpgradeRequired, map[string]any{
			"error":               "https_required",
			"connection_security": connection,
			"https_required":      true,
		})
	})
}

func informationalRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.URL.Path == "/api/auth/status" || r.URL.Path == "/api/health" {
		return true
	}
	for _, prefix := range []string{"/api", "/oauth", "/mcp", "/passkeys", "/.well-known"} {
		if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
			return false
		}
	}
	// The embedded Flutter application and its client-side routes remain
	// reachable so they can explain how to reconnect securely.
	return true
}

func classify(r *http.Request, settings effectiveSettings) ConnectionSecurity {
	peer, ok := remoteAddr(r.RemoteAddr)
	if !ok || peer.IsUnspecified() {
		if r.TLS != nil {
			return ConnectionHTTPS
		}
		return ConnectionUnknownHTTP
	}
	hasForwarding := hasForwardingHeaders(r)
	if contains(settings.trustedProxyPrefixes, peer) {
		proto, protoOK := rightmostForwardedValue(r.Header.Get("X-Forwarded-Proto"))
		if !protoOK {
			return ConnectionUnknownHTTP
		}
		switch strings.ToLower(proto) {
		case "https":
			return ConnectionHTTPS
		case "http":
		default:
			return ConnectionUnknownHTTP
		}
		client, ok := forwardedClient(r.Header.Get("X-Forwarded-For"), settings.trustedProxyPrefixes)
		if !ok {
			return ConnectionUnknownHTTP
		}
		return classifyHTTPAddr(client, settings.TrustCGNATAsLocal)
	}
	if r.TLS != nil {
		return ConnectionHTTPS
	}
	if hasForwarding {
		return ConnectionUntrustedProxy
	}
	return classifyHTTPAddr(peer, settings.TrustCGNATAsLocal)
}

func hasForwardingHeaders(r *http.Request) bool {
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Real-IP", "X-Forwarded-Proto", "X-Forwarded-Host"} {
		if strings.TrimSpace(r.Header.Get(name)) != "" {
			return true
		}
	}
	return false
}

func rightmostForwardedValue(raw string) (string, bool) {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return "", false
	}
	value := strings.TrimSpace(parts[len(parts)-1])
	return value, value != ""
}

func forwardedClient(raw string, trusted []netip.Prefix) (netip.Addr, bool) {
	parts := strings.Split(raw, ",")
	addresses := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		addr, ok := parseForwardedAddr(strings.TrimSpace(part))
		if !ok {
			return netip.Addr{}, false
		}
		addresses = append(addresses, addr)
	}
	if len(addresses) == 0 {
		return netip.Addr{}, false
	}
	for i := len(addresses) - 1; i >= 0; i-- {
		if !contains(trusted, addresses[i]) {
			return addresses[i], true
		}
	}
	return addresses[0], true
}

func remoteAddr(raw string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(raw))
	if err != nil {
		host = strings.Trim(strings.TrimSpace(raw), "[]")
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func parseForwardedAddr(raw string) (netip.Addr, bool) {
	raw = strings.Trim(raw, "\"")
	if addr, err := netip.ParseAddr(strings.Trim(raw, "[]")); err == nil {
		return addr.Unmap(), true
	}
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func contains(prefixes []netip.Prefix, addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

func classifyHTTPAddr(addr netip.Addr, trustCGNAT bool) ConnectionSecurity {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsUnspecified() {
		return ConnectionUnknownHTTP
	}
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || (trustCGNAT && cgnatPrefix.Contains(addr)) {
		return ConnectionLocalHTTP
	}
	return ConnectionRemoteHTTP
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
