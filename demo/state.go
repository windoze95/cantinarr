// state.go — the core in-memory store: users, devices, refresh tokens,
// connect tokens, and the instance registry. Part of the frozen Stage A
// contract (see contract.md).
//
// Locking rule: every exported accessor acquires stateMu itself. Domain files
// must NEVER lock stateMu directly and must never call one accessor from
// inside another's callback (withUser / withInstance) — use a domain-local
// mutex for domain-local maps instead.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ─── Frozen demo constants ──────────────────────────────

const (
	// demoJWTSecret signs every access token (HS256). Ported from the old demo.
	demoJWTSecret = "demo-jwt-secret-cantinarr"

	// demoConnectTokenStr is the well-known MULTI-USE reviewer connect token,
	// bound to user 2 ("user"). It is exempt from single-use redemption so App
	// Store reviewers can reuse the link. Referenced by docs/store-release.md.
	demoConnectTokenStr = "demo0000000000000000000000000000000000000000000000000000connect1"

	accessTokenTTL  = 15 * time.Minute
	connectTokenTTL = 7 * 24 * time.Hour
	refreshPrefix   = "cnr1."
)

// Frozen instance IDs — every seeded fixture, request row, WS payload, and
// proxy fixture must reference these exact strings.
const (
	instRadarr   = "radarr-1a2b3c4d"
	instSonarr   = "sonarr-5e6f7a8b"
	instChaptarr = "chaptarr-9c0d1e2f"
	instSab      = "sabnzbd-3f4a5b6c"
	instTautulli = "tautulli-7d8e9f0a"

	// Sibling arr libraries. They exist so the multi-instance surfaces have
	// something to show: a requester granted two Radarrs gets the Library
	// chooser, per-library status chips, and additive grants.
	instRadarr4K    = "radarr-2b3c4d5e"
	instSonarrAnime = "sonarr-6f7a8b9c"

	// Media servers. Granted per user, never a global default.
	instJellyfin = "jellyfin-1f2e3d4c"
	instEmby     = "emby-5b6a7988"
	instPlex     = "plex-0a1b2c3d"
)

// plexDemoMachineIdentifier names the Plex Media Server the seeded Plex
// instance shares. Frozen: the instance editor's server picker and the
// media_server_config both quote it.
const plexDemoMachineIdentifier = "d3m0p1exmach1ne0000000000000001"

// Frozen seed device IDs (one device per real seeded user).
const (
	seedDeviceAdmin = "b1a9c8d7-0e2f-4a6b-8c1d-3e5f7a9b0c2d"
	seedDeviceUser  = "d4c3b2a1-5f6e-4d7c-9b8a-1f2e3d4c5b6a"
)

// ─── Store types ────────────────────────────────────────

// DemoUser is one account. Password is stored in plain text (demo-grade) and
// is never serialized. RequireApproval is the per-user request-approval
// override: nil = inherit the global setting, &true / &false = explicit.
type DemoUser struct {
	ID               int
	Username         string
	Role             string
	Password         string
	PlexEmail        string
	PlexInvitedAt    *time.Time
	CreatedAt        time.Time
	AISharedEnabled  bool
	HasPendingInvite bool
	PasswordEnabled  bool
	PasskeyEnabled   bool
	HasPassword      bool
	DefaultInstances map[string]string // service_type -> instance id (per-user pin / chaptarr grant)
	// InstanceGrants are ADDITIVE per-user access grants: service_type ->
	// instance ids. A granted instance appears alongside the user's default so
	// they can choose a library per request. Never nil.
	InstanceGrants  map[string][]string
	RequireApproval *bool
}

// DemoDevice is one signed-in device. Platform and HardwareID are stored but
// not serialized by devicesJSON (the admin list emits exactly six fields).
type DemoDevice struct {
	ID         string
	UserID     int
	Name       string
	Platform   string
	HardwareID string
	CreatedAt  time.Time
	LastSeen   time.Time
}

// DemoInstance is one registered service instance. Secrets (api keys,
// passwords) are never stored or returned — presence is implied.
type DemoInstance struct {
	ID                string
	ServiceType       string
	Name              string
	URL               string
	Username          string
	IsDefault         bool
	MediaDownloads    bool
	MediaPathMappings []map[string]string // {"arr_path": ..., "cantinarr_path": ...}; never nil
	// MediaServerConfig is the jellyfin/emby/plex-only configuration. Zero for
	// every other type. Only PublicAddress ever reaches a requester, and only
	// because an admin typed it.
	MediaServerConfig *DemoMediaServerConfig
}

// DemoMediaServerConfig is the per-instance configuration of a media server.
// LibraryIDs are the library identifiers new accounts may see; empty shares
// every library. MachineIdentifier names the Plex Media Server whose shares
// the instance manages (Plex only). AutoApprove (Plex) grants the server to
// anyone who shares a Plex email and sends their invite at once.
type DemoMediaServerConfig struct {
	PublicAddress     string
	LibraryIDs        []string // never nil
	MachineIdentifier string
	AutoApprove       bool
}

// clone copies the config so a caller can never mutate stored state through a
// returned pointer.
func (c *DemoMediaServerConfig) clone() *DemoMediaServerConfig {
	if c == nil {
		return nil
	}
	out := *c
	out.LibraryIDs = append([]string{}, c.LibraryIDs...)
	return &out
}

type demoConnectTokenRec struct {
	Token     string
	UserID    int
	ExpiresAt time.Time
	Redeemed  bool
}

// ─── Store state (guarded by stateMu) ───────────────────

var (
	stateMu sync.Mutex

	demoUsers      = map[int]*DemoUser{}
	demoNextUserID = 4

	demoDevices      = map[string]*DemoDevice{}
	revokedDeviceIDs = map[string]bool{}
	deviceRefresh    = map[string]string{} // device id -> refresh token
	refreshDevice    = map[string]string{} // refresh token -> device id

	demoConnectTokens = map[string]*demoConnectTokenRec{}

	demoInstances []*DemoInstance
)

var (
	errInvalidRefreshToken = errors.New("invalid refresh token")
	errDeviceRevoked       = errors.New("device has been revoked")
	errUserNotFound        = errors.New("user not found")
	errLastAdmin           = errors.New("cannot delete the last admin")
	errInstanceNotFound    = errors.New("instance not found")
)

func init() {
	seedCoreState()
}

func seedCoreState() {
	stateMu.Lock()
	defer stateMu.Unlock()

	now := time.Now()

	// Users. IDs, usernames, and flags are frozen contract.
	demoUsers[1] = &DemoUser{
		ID: 1, Username: "admin", Role: roleAdmin, Password: "demo",
		CreatedAt:       time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC),
		PasswordEnabled: true, PasskeyEnabled: true, HasPassword: true,
		AISharedEnabled:  true,
		DefaultInstances: map[string]string{},
		// The admin holds an Emby grant with no account on it yet, so the
		// guide's "I already have an account" card has something to link.
		InstanceGrants: map[string][]string{serviceEmby: {instEmby}},
	}
	requireApproval := true
	demoUsers[2] = &DemoUser{
		ID: 2, Username: "user", Role: roleUser, Password: "demo",
		CreatedAt:       time.Date(2026, 5, 2, 10, 30, 0, 0, time.UTC),
		PasswordEnabled: true, PasskeyEnabled: false, HasPassword: true,
		AISharedEnabled:  true,
		DefaultInstances: map[string]string{serviceChaptarr: instChaptarr},
		// Additive grants: the second Radarr and the second Sonarr sit
		// ALONGSIDE the global defaults, which is what puts the Library
		// chooser and the sibling status chips on screen.
		InstanceGrants: map[string][]string{
			serviceRadarr:   {instRadarr4K},
			serviceSonarr:   {instSonarrAnime},
			serviceJellyfin: {instJellyfin},
		},
		RequireApproval: &requireApproval,
	}
	demoUsers[3] = &DemoUser{
		ID: 3, Username: "riley", Role: roleUser, Password: "",
		CreatedAt:       time.Date(2026, 7, 20, 18, 45, 0, 0, time.UTC),
		PasswordEnabled: false, PasskeyEnabled: false, HasPassword: false,
		HasPendingInvite: true,
		PlexEmail:        "riley@example.net", // shared, never invited — drives the Plex badge demo
		DefaultInstances: map[string]string{},
		InstanceGrants:   map[string][]string{servicePlex: {instPlex}},
	}

	// One device per real user, with a stable refresh token each.
	seedDevice := &DemoDevice{
		ID: seedDeviceAdmin, UserID: 1, Name: "Admin's iPhone", Platform: "ios",
		HardwareID: "seed-hw-admin-0001",
		CreatedAt:  demoUsers[1].CreatedAt, LastSeen: now.Add(-5 * time.Minute),
	}
	demoDevices[seedDevice.ID] = seedDevice
	lockedEnsureRefreshToken(seedDevice.ID)

	seedDevice = &DemoDevice{
		ID: seedDeviceUser, UserID: 2, Name: "Pixel 9", Platform: "android",
		HardwareID: "seed-hw-user-0001",
		CreatedAt:  demoUsers[2].CreatedAt, LastSeen: now.Add(-40 * time.Minute),
	}
	demoDevices[seedDevice.ID] = seedDevice
	lockedEnsureRefreshToken(seedDevice.ID)

	// Connect tokens: the well-known multi-use reviewer token (user 2, never
	// expires in practice) plus a pending single-use invite for riley (backs
	// riley's has_pending_invite:true).
	demoConnectTokens[demoConnectTokenStr] = &demoConnectTokenRec{
		Token: demoConnectTokenStr, UserID: 2, ExpiresAt: now.AddDate(10, 0, 0),
	}
	demoConnectTokens[randomHex(32)] = &demoConnectTokenRec{
		Token: "", UserID: 3, ExpiresAt: now.Add(connectTokenTTL),
	}

	// Instance registry (IDs frozen above).
	demoInstances = []*DemoInstance{
		{
			ID: instRadarr, ServiceType: serviceRadarr, Name: "Radarr",
			URL: "http://radarr:7878", IsDefault: true, MediaDownloads: true,
			MediaPathMappings: []map[string]string{{"arr_path": "/movies", "cantinarr_path": "/media/movies"}},
		},
		{
			ID: instSonarr, ServiceType: serviceSonarr, Name: "Sonarr",
			URL: "http://sonarr:8989", IsDefault: true, MediaDownloads: true,
			MediaPathMappings: []map[string]string{{"arr_path": "/tv", "cantinarr_path": "/media/tv"}},
		},
		{
			ID: instChaptarr, ServiceType: serviceChaptarr, Name: "Chaptarr",
			URL: "http://chaptarr:8787", IsDefault: false, MediaDownloads: true, // chaptarr is NEVER default
			MediaPathMappings: []map[string]string{{"arr_path": "/books", "cantinarr_path": "/media/books"}},
		},
		{
			ID: instSab, ServiceType: serviceSabnzbd, Name: "SABnzbd",
			URL: "http://sabnzbd:8080", IsDefault: true, MediaDownloads: false,
			MediaPathMappings: []map[string]string{},
		},
		{
			ID: instTautulli, ServiceType: serviceTautulli, Name: "Tautulli",
			URL: "http://tautulli:8181", IsDefault: true, MediaDownloads: false,
			MediaPathMappings: []map[string]string{},
		},
		// Sibling arr libraries. Neither carries the global default flag —
		// they reach a requester through an additive grant, which is exactly
		// the shape the Library chooser exists for.
		{
			ID: instRadarr4K, ServiceType: serviceRadarr, Name: "Radarr 4K",
			URL: "http://radarr-4k:7878", IsDefault: false, MediaDownloads: true,
			MediaPathMappings: []map[string]string{{"arr_path": "/movies-4k", "cantinarr_path": "/media/movies-4k"}},
		},
		{
			ID: instSonarrAnime, ServiceType: serviceSonarr, Name: "Sonarr Anime",
			URL: "http://sonarr-anime:8989", IsDefault: false, MediaDownloads: true,
			MediaPathMappings: []map[string]string{{"arr_path": "/anime", "cantinarr_path": "/media/anime"}},
		},
		// Media servers. Never a global default — access is the grant.
		{
			ID: instJellyfin, ServiceType: serviceJellyfin, Name: "Jellyfin",
			URL: "http://jellyfin:8096", IsDefault: false, MediaDownloads: false,
			MediaPathMappings: []map[string]string{},
			MediaServerConfig: &DemoMediaServerConfig{
				PublicAddress: "https://jellyfin.demo.example",
				LibraryIDs:    []string{"jf-lib-movies", "jf-lib-shows"},
			},
		},
		{
			ID: instEmby, ServiceType: serviceEmby, Name: "Emby",
			URL: "http://emby:8096", IsDefault: false, MediaDownloads: false,
			MediaPathMappings: []map[string]string{},
			MediaServerConfig: &DemoMediaServerConfig{
				PublicAddress: "https://emby.demo.example",
				LibraryIDs:    []string{},
			},
		},
		{
			ID: instPlex, ServiceType: servicePlex, Name: "Demo Plex",
			URL: "https://plex.tv", IsDefault: false, MediaDownloads: false,
			MediaPathMappings: []map[string]string{},
			MediaServerConfig: &DemoMediaServerConfig{
				PublicAddress:     plexPublicAddress,
				LibraryIDs:        []string{"1", "2"},
				MachineIdentifier: plexDemoMachineIdentifier,
			},
		},
	}
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ─── Users ──────────────────────────────────────────────

// allUsers returns every account ordered by id ascending.
func allUsers() []*DemoUser {
	stateMu.Lock()
	defer stateMu.Unlock()
	out := make([]*DemoUser, 0, len(demoUsers))
	for _, u := range demoUsers {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// userByID returns the account with the given id, or nil.
func userByID(id int) *DemoUser {
	stateMu.Lock()
	defer stateMu.Unlock()
	return demoUsers[id]
}

// userByName returns the account with the given username, or nil.
func userByName(name string) *DemoUser {
	stateMu.Lock()
	defer stateMu.Unlock()
	return lockedUserByName(name)
}

func lockedUserByName(name string) *DemoUser {
	for _, u := range demoUsers {
		if u.Username == name {
			return u
		}
	}
	return nil
}

// createInvitedUser creates (or returns the existing) account for a connect
// link: role user, no password, password/passkey sign-in disabled.
func createInvitedUser(name string) *DemoUser {
	stateMu.Lock()
	defer stateMu.Unlock()
	if u := lockedUserByName(name); u != nil {
		return u
	}
	u := &DemoUser{
		ID: demoNextUserID, Username: name, Role: roleUser,
		CreatedAt:        time.Now(),
		DefaultInstances: map[string]string{},
		InstanceGrants:   map[string][]string{},
	}
	demoNextUserID++
	demoUsers[u.ID] = u
	return u
}

// deleteUser removes an account plus its devices, refresh tokens, and connect
// tokens. Errors: errUserNotFound, errLastAdmin. The self-delete check is the
// handler's job (it knows the caller).
func deleteUser(id int) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	u := demoUsers[id]
	if u == nil {
		return errUserNotFound
	}
	if u.Role == roleAdmin {
		admins := 0
		for _, other := range demoUsers {
			if other.Role == roleAdmin {
				admins++
			}
		}
		if admins <= 1 {
			return errLastAdmin
		}
	}
	for devID, d := range demoDevices {
		if d.UserID == id {
			delete(demoDevices, devID)
			if rt, ok := deviceRefresh[devID]; ok {
				delete(refreshDevice, rt)
				delete(deviceRefresh, devID)
			}
			revokedDeviceIDs[devID] = true
		}
	}
	for tok, ct := range demoConnectTokens {
		if ct.UserID == id {
			delete(demoConnectTokens, tok)
		}
	}
	delete(demoUsers, id)
	return nil
}

// withUser runs fn on the user under the state lock; reports whether the user
// exists. Use it for cross-domain field mutations (role, flags, plex fields,
// DefaultInstances, RequireApproval). fn must not call other state accessors.
func withUser(id int, fn func(*DemoUser)) bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	u := demoUsers[id]
	if u == nil {
		return false
	}
	fn(u)
	return true
}

// setUserPassword stores a new password (and flips HasPassword true); reports
// whether the user exists.
func setUserPassword(id int, password string) bool {
	return withUser(id, func(u *DemoUser) {
		u.Password = password
		u.HasPassword = true
	})
}

// setUserPlexEmail stores the shared Plex email. On a CHANGED address it also
// clears PlexInvitedAt. Returns (changed, ok).
func setUserPlexEmail(id int, email string) (changed, ok bool) {
	ok = withUser(id, func(u *DemoUser) {
		if u.PlexEmail != email {
			changed = true
			u.PlexEmail = email
			u.PlexInvitedAt = nil
		}
	})
	return changed, ok
}

// permissionsFor returns the role's permission list, sorted lexicographically
// (exactly what the real server serializes). Unknown roles get [].
func permissionsFor(role string) []string {
	switch role {
	case roleAdmin:
		return []string{
			"admin:*", "ai:chat", "ai_tools:manage", "arr:browse", "arr:read",
			"arr:search", "credentials:manage", "downloads:manage", "downloads:read",
			"instances:manage", "mcp:access", "media:discover", "media:download",
			"media:request", "monitoring:read", "remediation:manage",
			"requests:manage", "system:read", "users:manage",
		}
	case roleUser:
		return []string{
			"ai:chat", "arr:browse", "mcp:access", "media:discover",
			"media:download", "media:request",
		}
	default:
		return []string{}
	}
}

// userSummaryJSON renders one GET /api/admin/users element (auth.UserSummary):
// id, username, role, permissions, created_at, device_count, has_password,
// password_enabled, passkey_enabled, ai_shared_enabled, has_pending_invite,
// plex_email always present; plex_invited_at only when set.
func userSummaryJSON(u *DemoUser) map[string]any {
	stateMu.Lock()
	deviceCount := 0
	for _, d := range demoDevices {
		if d.UserID == u.ID {
			deviceCount++
		}
	}
	stateMu.Unlock()
	out := map[string]any{
		"id":                 u.ID,
		"username":           u.Username,
		"role":               u.Role,
		"permissions":        permissionsFor(u.Role),
		"created_at":         u.CreatedAt,
		"device_count":       deviceCount,
		"has_password":       u.HasPassword,
		"password_enabled":   u.PasswordEnabled,
		"passkey_enabled":    u.PasskeyEnabled,
		"ai_shared_enabled":  u.AISharedEnabled,
		"has_pending_invite": u.HasPendingInvite,
		"plex_email":         u.PlexEmail,
	}
	if at := userPlexInvitedAt(u); at != nil {
		out["plex_invited_at"] = *at
	}
	return out
}

// userPlexInvitedAt is when this user's Plex share was sent, derived from the
// live account row rather than a stored field — the share is the truth, and a
// stored copy of it would drift the moment an admin unshared in Plex.
func userPlexInvitedAt(u *DemoUser) *time.Time {
	if u == nil {
		return nil
	}
	if at := msvUserInviteState(u.ID); at != nil {
		return at
	}
	return u.PlexInvitedAt
}

// userAuthJSON renders the TokenResponse "user" object: id, username, role,
// permissions, password_enabled, passkey_enabled, plex_email, created_at;
// plex_invited_at only when set. (GET /api/auth/me is a different, wider
// shape — auth.go builds that one itself.)
func userAuthJSON(u *DemoUser) map[string]any {
	out := map[string]any{
		"id":               u.ID,
		"username":         u.Username,
		"role":             u.Role,
		"permissions":      permissionsFor(u.Role),
		"password_enabled": u.PasswordEnabled,
		"passkey_enabled":  u.PasskeyEnabled,
		"plex_email":       u.PlexEmail,
		"created_at":       u.CreatedAt,
	}
	if at := userPlexInvitedAt(u); at != nil {
		out["plex_invited_at"] = *at
	}
	return out
}

// ─── Devices & refresh tokens ───────────────────────────

// deviceUpsert reuses the user's non-revoked device with a matching non-empty
// hardwareID (refreshing name/platform/last-seen) or creates a new UUID
// device with a fresh stable refresh token. Empty name becomes
// "Unknown Device".
func deviceUpsert(userID int, hardwareID, name, platform string) *DemoDevice {
	stateMu.Lock()
	defer stateMu.Unlock()
	if name == "" {
		name = "Unknown Device"
	}
	now := time.Now()
	if hardwareID != "" {
		for _, d := range demoDevices {
			if d.UserID == userID && d.HardwareID == hardwareID {
				d.Name = name
				if platform != "" {
					d.Platform = platform
				}
				d.LastSeen = now
				return d
			}
		}
	}
	d := &DemoDevice{
		ID: uuid.NewString(), UserID: userID, Name: name, Platform: platform,
		HardwareID: hardwareID, CreatedAt: now, LastSeen: now,
	}
	demoDevices[d.ID] = d
	lockedEnsureRefreshToken(d.ID)
	return d
}

func lockedEnsureRefreshToken(deviceID string) string {
	if rt, ok := deviceRefresh[deviceID]; ok {
		return rt
	}
	rt := refreshPrefix + randomHex(32)
	deviceRefresh[deviceID] = rt
	refreshDevice[rt] = deviceID
	return rt
}

// devicesJSON renders GET /api/admin/devices: every non-revoked device,
// ordered by last_seen_at DESC, exactly six fields per row.
func devicesJSON() []map[string]any {
	stateMu.Lock()
	defer stateMu.Unlock()
	devs := make([]*DemoDevice, 0, len(demoDevices))
	for _, d := range demoDevices {
		devs = append(devs, d)
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].LastSeen.After(devs[j].LastSeen) })
	out := make([]map[string]any, 0, len(devs))
	for _, d := range devs {
		username := ""
		if u := demoUsers[d.UserID]; u != nil {
			username = u.Username
		}
		out = append(out, map[string]any{
			"id":           d.ID,
			"user_id":      d.UserID,
			"username":     username,
			"device_name":  d.Name,
			"created_at":   d.CreatedAt,
			"last_seen_at": d.LastSeen,
		})
	}
	return out
}

// revokeDevice removes a device and kills its refresh token; its access
// tokens fail middleware auth from now on. Reports whether the id existed.
func revokeDevice(id string) bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	d := demoDevices[id]
	if d == nil {
		return false
	}
	delete(demoDevices, id)
	revokedDeviceIDs[id] = true
	// Keep refreshDevice[rt] pointing at the id so a later refresh can answer
	// "device has been revoked" (not "invalid refresh token").
	return true
}

// deviceRevoked reports whether a device id has been revoked (or belonged to
// a deleted user). requireAuth checks this so revocation kills live access
// tokens immediately.
func deviceRevoked(id string) bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	return revokedDeviceIDs[id]
}

// ─── Sessions ───────────────────────────────────────────

type demoClaims struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	DeviceID string `json:"device_id"`
	jwt.RegisteredClaims
}

// mintAccessToken signs a 15-minute HS256 access JWT with user_id, username,
// role, device_id, exp, iat claims.
func mintAccessToken(u *DemoUser, deviceID string) (string, error) {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, &demoClaims{
		UserID: u.ID, Username: u.Username, Role: u.Role, DeviceID: deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	})
	return tok.SignedString([]byte(demoJWTSecret))
}

// parseAccessClaims validates an access JWT (HS256, demoJWTSecret) and
// returns its claims.
func parseAccessClaims(tokenStr string) (*demoClaims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &demoClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(demoJWTSecret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := tok.Claims.(*demoClaims)
	if !ok || !tok.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

// issueSession builds the full TokenResponse map: a fresh access_token, the
// device's STABLE cnr1. refresh_token (never rotates), the user object, and
// device_id.
func issueSession(u *DemoUser, d *DemoDevice) map[string]any {
	stateMu.Lock()
	rt := lockedEnsureRefreshToken(d.ID)
	stateMu.Unlock()
	at, err := mintAccessToken(u, d.ID)
	if err != nil {
		at = "" // never happens with HS256 + a static secret
	}
	return map[string]any{
		"access_token":  at,
		"refresh_token": rt,
		"user":          userAuthJSON(u),
		"device_id":     d.ID,
	}
}

// refreshSession redeems an opaque cnr1. refresh token: a new access token,
// the SAME refresh token echoed back verbatim, fresh user + device_id. Bumps
// the device's last-seen. Errors (both mean 401): errDeviceRevoked,
// errInvalidRefreshToken.
func refreshSession(refreshToken string) (map[string]any, error) {
	stateMu.Lock()
	deviceID, ok := refreshDevice[refreshToken]
	if !ok {
		stateMu.Unlock()
		return nil, errInvalidRefreshToken
	}
	if revokedDeviceIDs[deviceID] {
		stateMu.Unlock()
		return nil, errDeviceRevoked
	}
	d := demoDevices[deviceID]
	if d == nil {
		stateMu.Unlock()
		return nil, errInvalidRefreshToken
	}
	u := demoUsers[d.UserID]
	if u == nil {
		stateMu.Unlock()
		return nil, errInvalidRefreshToken
	}
	d.LastSeen = time.Now()
	stateMu.Unlock()
	return issueSession(u, d), nil
}

// ─── Connect tokens ─────────────────────────────────────

// mintConnectToken finds-or-creates the named user, marks them
// has_pending_invite, and returns a single-use 7-day connect link
// "cantinarr://connect?token=<64hex>&server=<urlencoded DEMO_SERVER_URL>".
func mintConnectToken(username string) (link string, expiresAt time.Time) {
	u := createInvitedUser(username)
	stateMu.Lock()
	token := randomHex(32)
	expiresAt = time.Now().Add(connectTokenTTL)
	demoConnectTokens[token] = &demoConnectTokenRec{Token: token, UserID: u.ID, ExpiresAt: expiresAt}
	u.HasPendingInvite = true
	stateMu.Unlock()
	origin, _ := connectLinkOrigin()
	link = fmt.Sprintf("cantinarr://connect?token=%s&server=%s", token, url.QueryEscape(origin))
	return link, expiresAt
}

// grantInstanceToUser adds an access grant, no-op when the user already holds
// it. Additive: it never moves anyone's default or touches a sibling.
func grantInstanceToUser(userID int, instanceID string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	u := demoUsers[userID]
	inst := lockedInstanceByID(instanceID)
	if u == nil || inst == nil {
		return
	}
	if u.InstanceGrants == nil {
		u.InstanceGrants = map[string][]string{}
	}
	for _, id := range u.InstanceGrants[inst.ServiceType] {
		if id == instanceID {
			return
		}
	}
	u.InstanceGrants[inst.ServiceType] = append(u.InstanceGrants[inst.ServiceType], instanceID)
}

// redeemConnectToken redeems a connect token for its user. Single-use, except
// the well-known reviewer token which never burns. Error messages are shown
// verbatim in the app: "connect token not found",
// "this link has already been used", "this link has expired".
func redeemConnectToken(token string) (*DemoUser, error) {
	stateMu.Lock()
	defer stateMu.Unlock()
	ct := demoConnectTokens[token]
	if ct == nil {
		return nil, errors.New("connect token not found")
	}
	isReviewerToken := token == demoConnectTokenStr
	if !isReviewerToken && ct.Redeemed {
		return nil, errors.New("this link has already been used")
	}
	if time.Now().After(ct.ExpiresAt) {
		return nil, errors.New("this link has expired")
	}
	u := demoUsers[ct.UserID]
	if u == nil {
		return nil, errors.New("connect token not found")
	}
	if !isReviewerToken {
		ct.Redeemed = true
		u.HasPendingInvite = false
	}
	return u, nil
}

// ─── Instances ──────────────────────────────────────────

// instanceByID returns the instance with the given id, or nil.
func instanceByID(id string) *DemoInstance {
	stateMu.Lock()
	defer stateMu.Unlock()
	return lockedInstanceByID(id)
}

func lockedInstanceByID(id string) *DemoInstance {
	for _, inst := range demoInstances {
		if inst.ID == id {
			return inst
		}
	}
	return nil
}

// allInstances returns every registered instance in stable seed order
// (radarr, sonarr, chaptarr, sabnzbd, tautulli, then any created later).
func allInstances() []*DemoInstance {
	stateMu.Lock()
	defer stateMu.Unlock()
	out := make([]*DemoInstance, len(demoInstances))
	copy(out, demoInstances)
	return out
}

// withInstance runs fn on the instance under the state lock; reports whether
// it exists. fn must not call other state accessors.
func withInstance(id string, fn func(*DemoInstance)) bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	inst := lockedInstanceByID(id)
	if inst == nil {
		return false
	}
	fn(inst)
	return true
}

// registerInstance adds an admin-created instance to the shared registry so
// config, defaults, pins, and the proxy all see it.
func registerInstance(inst *DemoInstance) {
	stateMu.Lock()
	defer stateMu.Unlock()
	demoInstances = append(demoInstances, inst)
}

// removeInstance deletes an instance from the shared registry; reports
// whether it existed.
func removeInstance(id string) bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	for idx, inst := range demoInstances {
		if inst.ID == id {
			demoInstances = append(demoInstances[:idx], demoInstances[idx+1:]...)
			return true
		}
	}
	return false
}

// visibleInstances returns the instances a user may see: admins get all;
// a regular user gets every access-granted instance plus their effective
// default, across the grantable service types (radarr, sonarr, chaptarr and
// the three media servers). Grants are ADDITIVE — a granted sibling sits
// beside the default rather than replacing it — so renderers must mark
// is_default per user with effectiveInstanceFor, not blanket-true.
func visibleInstances(u *DemoUser) []*DemoInstance {
	if u != nil && u.Role == roleAdmin {
		return allInstances()
	}
	out := []*DemoInstance{}
	if u == nil {
		return out
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	seen := map[string]bool{}
	for _, st := range grantableServiceTypes() {
		for _, id := range lockedVisibleInstanceIDs(u, st) {
			if seen[id] {
				continue
			}
			if inst := lockedInstanceByID(id); inst != nil {
				seen[id] = true
				out = append(out, inst)
			}
		}
	}
	// Keep the registry's stable seed order rather than the service-type walk
	// order, so a user's list reads like a subset of the admin's.
	order := map[string]int{}
	for idx, inst := range demoInstances {
		order[inst.ID] = idx
	}
	sort.Slice(out, func(i, j int) bool { return order[out[i].ID] < order[out[j].ID] })
	return out
}

// grantableServiceTypes are the types an admin can grant per user. Mirrors
// the server's instance.grantableServiceTypes.
func grantableServiceTypes() []string {
	return append([]string{serviceRadarr, serviceSonarr, serviceChaptarr}, mediaServerTypes()...)
}

// effectiveInstanceFor resolves the user's effective instance for a service
// type: per-user pin -> (chaptarr and media servers) first grant, no fallback
// -> global default -> first instance of that type.
func effectiveInstanceFor(u *DemoUser, serviceType string) *DemoInstance {
	stateMu.Lock()
	defer stateMu.Unlock()
	return lockedInstanceByID(lockedEffectiveInstanceID(u, serviceType))
}

// effectiveInstanceIDFor is effectiveInstanceFor by id ("" when none).
func effectiveInstanceIDFor(u *DemoUser, serviceType string) string {
	stateMu.Lock()
	defer stateMu.Unlock()
	return lockedEffectiveInstanceID(u, serviceType)
}

func lockedEffectiveInstanceID(u *DemoUser, serviceType string) string {
	if u != nil {
		if pinned, ok := u.DefaultInstances[serviceType]; ok && pinned != "" {
			if inst := lockedInstanceByID(pinned); inst != nil && inst.ServiceType == serviceType {
				return inst.ID
			}
		}
	}
	// Chaptarr and the media servers have NO global fallback — access is the
	// grant. The first grant stands in as the default so a client that reads
	// one instance per type still picks a real one.
	if serviceType == serviceChaptarr || isMediaServerType(serviceType) {
		if u != nil {
			for _, id := range u.InstanceGrants[serviceType] {
				if inst := lockedInstanceByID(id); inst != nil && inst.ServiceType == serviceType {
					return inst.ID
				}
			}
		}
		return ""
	}
	first := ""
	for _, inst := range demoInstances {
		if inst.ServiceType != serviceType {
			continue
		}
		if inst.IsDefault {
			return inst.ID
		}
		if first == "" {
			first = inst.ID
		}
	}
	return first
}

// grantedInstanceIDs is the user's explicit grants for a service type plus
// their per-user pin, in stable registry order. Never nil.
func grantedInstanceIDs(u *DemoUser, serviceType string) []string {
	stateMu.Lock()
	defer stateMu.Unlock()
	return lockedGrantedInstanceIDs(u, serviceType)
}

func lockedGrantedInstanceIDs(u *DemoUser, serviceType string) []string {
	out := []string{}
	if u == nil {
		return out
	}
	seen := map[string]bool{}
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		inst := lockedInstanceByID(id)
		if inst == nil || inst.ServiceType != serviceType {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, id := range u.InstanceGrants[serviceType] {
		add(id)
	}
	add(u.DefaultInstances[serviceType])
	return out
}

// visibleInstanceIDs is grantedInstanceIDs plus the effective default. Never
// nil. Mirrors the server's instance.VisibleInstanceIDs.
func visibleInstanceIDs(u *DemoUser, serviceType string) []string {
	stateMu.Lock()
	defer stateMu.Unlock()
	return lockedVisibleInstanceIDs(u, serviceType)
}

func lockedVisibleInstanceIDs(u *DemoUser, serviceType string) []string {
	out := lockedGrantedInstanceIDs(u, serviceType)
	def := lockedEffectiveInstanceID(u, serviceType)
	if def == "" {
		return out
	}
	for _, id := range out {
		if id == def {
			return out
		}
	}
	return append(out, def)
}

// userCanSeeInstance reports whether a non-admin holds this instance. Admins
// see everything.
func userCanSeeInstance(u *DemoUser, id string) bool {
	if u == nil {
		return false
	}
	if u.Role == roleAdmin {
		return instanceByID(id) != nil
	}
	inst := instanceByID(id)
	if inst == nil {
		return false
	}
	for _, visible := range visibleInstanceIDs(u, inst.ServiceType) {
		if visible == id {
			return true
		}
	}
	return false
}

// userInstanceGrants returns a copy of the user's grant map, keyed by service
// type. Always a map, never nil; empty types are omitted.
func userInstanceGrants(u *DemoUser) map[string][]string {
	stateMu.Lock()
	defer stateMu.Unlock()
	out := map[string][]string{}
	if u == nil {
		return out
	}
	for _, st := range grantableServiceTypes() {
		ids := []string{}
		for _, id := range u.InstanceGrants[st] {
			if inst := lockedInstanceByID(id); inst != nil && inst.ServiceType == st {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			out[st] = ids
		}
	}
	return out
}

// setUserInstanceGrants replaces the grants for exactly the listed service
// types, leaving every other type alone.
func setUserInstanceGrants(userID int, grants map[string][]string) bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	u := demoUsers[userID]
	if u == nil {
		return false
	}
	if u.InstanceGrants == nil {
		u.InstanceGrants = map[string][]string{}
	}
	for st, ids := range grants {
		if len(ids) == 0 {
			delete(u.InstanceGrants, st)
			continue
		}
		u.InstanceGrants[st] = append([]string{}, ids...)
	}
	return true
}

// instanceGrantRows lists every grant row for the addressed instance's SERVICE
// TYPE — not just that instance — sorted by user id then instance id, which is
// what the instance editor's assignment section reads.
func instanceGrantRows(serviceType string) []struct {
	UserID     int
	InstanceID string
} {
	stateMu.Lock()
	defer stateMu.Unlock()
	type row = struct {
		UserID     int
		InstanceID string
	}
	out := []row{}
	for _, u := range demoUsers {
		for _, id := range lockedGrantedInstanceIDs(u, serviceType) {
			out = append(out, row{UserID: u.ID, InstanceID: id})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UserID != out[j].UserID {
			return out[i].UserID < out[j].UserID
		}
		return out[i].InstanceID < out[j].InstanceID
	})
	return out
}

// setInstanceGrantUsers is the replace-set for ONE instance: every listed user
// gains the grant, every unlisted user loses it. Sibling instances of the same
// type are untouched — grants are additive, so assigning one library never
// moves anyone off another.
func setInstanceGrantUsers(instanceID string, userIDs []int) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	inst := lockedInstanceByID(instanceID)
	if inst == nil {
		return errInstanceNotFound
	}
	want := map[int]bool{}
	for _, id := range userIDs {
		if demoUsers[id] == nil {
			return fmt.Errorf("unknown user id: %d", id)
		}
		want[id] = true
	}
	for _, u := range demoUsers {
		if u.InstanceGrants == nil {
			u.InstanceGrants = map[string][]string{}
		}
		kept := []string{}
		held := false
		for _, id := range u.InstanceGrants[inst.ServiceType] {
			if id == instanceID {
				held = true
				continue
			}
			kept = append(kept, id)
		}
		if want[u.ID] {
			kept = append(kept, instanceID)
		} else if !held {
			continue
		}
		if len(kept) == 0 {
			delete(u.InstanceGrants, inst.ServiceType)
			continue
		}
		u.InstanceGrants[inst.ServiceType] = kept
	}
	return nil
}

// dropInstanceGrants strips every grant pointing at a deleted instance.
func dropInstanceGrants(instanceID string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	for _, u := range demoUsers {
		for st, ids := range u.InstanceGrants {
			kept := ids[:0:0]
			for _, id := range ids {
				if id != instanceID {
					kept = append(kept, id)
				}
			}
			if len(kept) == 0 {
				delete(u.InstanceGrants, st)
				continue
			}
			u.InstanceGrants[st] = kept
		}
	}
}
