// mediaaccess.go — media-server access: the accounts Cantinarr creates on
// Jellyfin/Emby and the shares it sends on Plex.
//
// This is the surface that replaced the old /api/admin/plex/* console. Access
// itself is the instance grant (state.go); this file only answers what a
// granted user can do about it — see their servers, create an account, link
// one they already have, sign in with Plex, ask where a title can be watched —
// and what an admin can do about theirs: tag the Users screen, link/unlink,
// and import a server's existing accounts.
//
// Domain prefix: msv…. Domain-local state is guarded by msvMu; per the
// contract, never take stateMu here.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// msvMinPasswordLength mirrors the real server's floor for an account the
// requester creates on a media server.
const msvMinPasswordLength = 8

// msvMaxImportAccounts caps one import call, matching the server.
const msvMaxImportAccounts = 200

// msvAccount is one Cantinarr user's account on one media server. Presence of
// a row is what "linked" means; the demo never stores a credential.
type msvAccount struct {
	UserID             int
	InstanceID         string
	RemoteUserID       string
	Username           string
	CreatedByCantinarr bool
	Disabled           bool
	// Pending is the Plex shape: the invite is out, the account has not
	// accepted it yet.
	Pending       bool
	Administrator bool
	CreatedAt     time.Time
}

// msvRemoteUser is one account that exists ON a media server, as the server
// itself reports it. This is the roster the admin picker and the import read;
// a row with no matching msvAccount is an account Cantinarr does not know.
type msvRemoteUser struct {
	ID              string
	Name            string
	Email           string
	IsAdministrator bool
	IsDisabled      bool
	Pending         bool
}

var (
	msvMu sync.Mutex

	// msvAccounts is keyed "<userID>|<instanceID>".
	msvAccounts = map[string]*msvAccount{}

	// msvRosters is the per-instance list of accounts the media server holds.
	msvRosters = map[string][]*msvRemoteUser{}

	// msvSignInPins tracks in-flight Plex sign-ins: pin id -> the user who
	// began it. A pin can only be polled by its own beginner.
	msvSignInPins = map[int64]int{}
	// msvSignInResults caches a concluded sign-in so later polls of the same
	// pin answer identically instead of re-running the invite.
	msvSignInResults = map[int64]map[string]any{}
)

func msvKey(userID int, instanceID string) string {
	return strconv.Itoa(userID) + "|" + instanceID
}

func init() {
	now := time.Now()

	// What each media server holds, independent of Cantinarr. Jellyfin has an
	// account for user 2 and two Cantinarr does not know (one of them
	// disabled); Emby holds an account named like the Cantinarr admin, which
	// is what puts the "you already have an account" card on their guide.
	msvRosters[instJellyfin] = []*msvRemoteUser{
		{ID: "jf-1", Name: "jellyfin-admin", IsAdministrator: true},
		{ID: "jf-2", Name: "user"},
		{ID: "jf-3", Name: "morgan"},
		{ID: "jf-4", Name: "sasha", IsDisabled: true},
	}
	msvRosters[instEmby] = []*msvRemoteUser{
		{ID: "emby-1", Name: "emby-admin", IsAdministrator: true},
		{ID: "emby-2", Name: "admin"},
		{ID: "emby-3", Name: "jules"},
	}
	msvRosters[instPlex] = []*msvRemoteUser{
		{ID: "plex-1", Name: plexDemoAccount, Email: plexDemoAccountEmail, IsAdministrator: true},
		{ID: "plex-2", Name: "casey", Email: "casey@example.net"},
	}

	// user (2) already has the Jellyfin account Cantinarr made for them.
	acct := &msvAccount{
		UserID: 2, InstanceID: instJellyfin, RemoteUserID: "jf-2",
		Username: "user", CreatedByCantinarr: true,
		CreatedAt: now.Add(-18 * 24 * time.Hour),
	}
	msvAccounts[msvKey(acct.UserID, acct.InstanceID)] = acct
}

// ─── Cross-domain hooks ─────────────────────────────────

// msvAccountFor returns the account row for a user on an instance, or nil.
func msvAccountFor(userID int, instanceID string) *msvAccount {
	msvMu.Lock()
	defer msvMu.Unlock()
	a := msvAccounts[msvKey(userID, instanceID)]
	if a == nil {
		return nil
	}
	out := *a
	return &out
}

// msvUserInviteState reports whether a user holds a Plex share on any Plex
// instance, and when it was sent. users_admin derives plex_invited_at from
// this — the live account row, not a stored Plex field.
func msvUserInviteState(userID int) (sentAt *time.Time) {
	// Snapshot under msvMu, then resolve instances outside it: instanceByID
	// takes the state lock, and taking it from inside msvMu would be the one
	// lock order nothing else in this file uses.
	type row struct {
		instanceID string
		at         time.Time
	}
	rows := []row{}
	msvMu.Lock()
	for _, a := range msvAccounts {
		if a.UserID == userID {
			rows = append(rows, row{a.InstanceID, a.CreatedAt})
		}
	}
	msvMu.Unlock()
	for _, r := range rows {
		inst := instanceByID(r.instanceID)
		if inst == nil || inst.ServiceType != servicePlex {
			continue
		}
		at := r.at
		if sentAt == nil || at.After(*sentAt) {
			sentAt = &at
		}
	}
	return sentAt
}

// msvDropInstance forgets every account on a deleted instance.
func msvDropInstance(instanceID string) {
	msvMu.Lock()
	defer msvMu.Unlock()
	delete(msvRosters, instanceID)
	for key, a := range msvAccounts {
		if a.InstanceID == instanceID {
			delete(msvAccounts, key)
		}
	}
}

// msvRosterFor returns a copy of an instance's roster, seeding an empty one
// for an admin-created media server so the picker is honest rather than
// missing.
func msvRosterFor(instanceID string) []*msvRemoteUser {
	msvMu.Lock()
	defer msvMu.Unlock()
	out := []*msvRemoteUser{}
	for _, u := range msvRosters[instanceID] {
		cp := *u
		out = append(out, &cp)
	}
	return out
}

// ─── Rendering ──────────────────────────────────────────

// msvAccountView is the per-account block inside a server view. verified:false
// would mean the answer came from Cantinarr's stored row because the server
// was unreachable — the demo always reaches its own fixtures, so it is true.
func msvAccountView(a *msvAccount) map[string]any {
	if a == nil {
		return nil
	}
	return map[string]any{
		"username":      a.Username,
		"disabled":      a.Disabled,
		"pending":       a.Pending,
		"administrator": a.Administrator,
		"verified":      true,
	}
}

// msvPublicAddress is the client-reachable sign-in address a granted user is
// shown. Plex always answers app.plex.tv unless an admin typed another.
func msvPublicAddress(inst *DemoInstance) string {
	if inst.MediaServerConfig != nil && inst.MediaServerConfig.PublicAddress != "" {
		return inst.MediaServerConfig.PublicAddress
	}
	if inst.ServiceType == servicePlex {
		return plexPublicAddress
	}
	return ""
}

// msvExistingAccount reports that this server confirmed an account named like
// the caller (case-insensitively) that no Cantinarr user is linked to, while
// the caller has none here: creating one would collide, so the guide leads
// with signing in to link it instead. False covers absence and blindness
// alike — it never claims absence. Account servers only.
func msvExistingAccount(inst *DemoInstance, u *DemoUser) bool {
	if inst.ServiceType == servicePlex || u == nil {
		return false
	}
	msvMu.Lock()
	defer msvMu.Unlock()
	if msvAccounts[msvKey(u.ID, inst.ID)] != nil {
		return false
	}
	linked := map[string]bool{}
	for _, a := range msvAccounts {
		if a.InstanceID == inst.ID {
			linked[a.RemoteUserID] = true
		}
	}
	for _, remote := range msvRosters[inst.ID] {
		if linked[remote.ID] {
			continue
		}
		if strings.EqualFold(remote.Name, u.Username) {
			return true
		}
	}
	return false
}

// msvServerView is one card on the media-server access guide.
func msvServerView(inst *DemoInstance, u *DemoUser) map[string]any {
	return map[string]any{
		"instance_id":      inst.ID,
		"service_type":     inst.ServiceType,
		"name":             inst.Name,
		"kind":             mediaServerKindFor(inst.ServiceType),
		"public_address":   msvPublicAddress(inst),
		"account":          msvAccountView(msvAccountFor(u.ID, inst.ID)),
		"existing_account": msvExistingAccount(inst, u),
	}
}

// msvAdminAccountRow is the admin-facing account row. Note the key is
// "username", not "remote_username" — the import result is the one that uses
// the other spelling.
func msvAdminAccountRow(a *msvAccount) map[string]any {
	name := a.InstanceID
	serviceType := ""
	if inst := instanceByID(a.InstanceID); inst != nil {
		name = inst.Name
		serviceType = inst.ServiceType
	}
	return map[string]any{
		"user_id":              a.UserID,
		"instance_id":          a.InstanceID,
		"instance_name":        name,
		"service_type":         serviceType,
		"remote_user_id":       a.RemoteUserID,
		"username":             a.Username,
		"created_by_cantinarr": a.CreatedByCantinarr,
		"disabled":             a.Disabled,
		"created_at":           a.CreatedAt.Format(time.RFC3339),
	}
}

// ─── Routes ─────────────────────────────────────────────

// registerMediaAccess mounts both halves: the self-scoped guide surface every
// authenticated user reaches, and the admin surface behind users:manage.
func registerMediaAccess(r chi.Router) {
	r.Get("/media-servers", msvHandleList)
	r.Get("/media-servers/watch", msvHandleWatch)
	r.Post("/media-servers/{instanceID}/account", msvHandleCreateAccount)
	r.Post("/media-servers/{instanceID}/account/link", msvHandleLinkOwnAccount)
	r.Post("/media-servers/plex/sign-in/begin", msvHandlePlexSignInBegin)
	r.Post("/media-servers/plex/sign-in/check", msvHandlePlexSignInCheck)

	admin := r.With(requireAdmin)
	admin.Get("/admin/media-servers/accounts", msvHandleAdminAccounts)
	admin.Get("/admin/media-servers/{instanceID}/users", msvHandleAdminRemoteUsers)
	admin.Post("/admin/media-servers/{instanceID}/import", msvHandleAdminImport)
	admin.Put("/admin/users/{userID}/media-servers/{instanceID}/account", msvHandleAdminLink)
	admin.Delete("/admin/users/{userID}/media-servers/{instanceID}/account", msvHandleAdminUnlink)
}

// msvUserServers lists the media servers a user holds, in registry order.
func msvUserServers(u *DemoUser) []*DemoInstance {
	out := []*DemoInstance{}
	if u == nil {
		return out
	}
	seen := map[string]bool{}
	for _, st := range mediaServerTypes() {
		for _, id := range visibleInstanceIDs(u, st) {
			if seen[id] {
				continue
			}
			if inst := instanceByID(id); inst != nil {
				seen[id] = true
				out = append(out, inst)
			}
		}
	}
	return out
}

// msvHandleList — GET /api/media-servers. A BARE array: the app reads
// resp.data as a list and treats anything else as empty.
func msvHandleList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	out := []map[string]any{}
	for _, inst := range msvUserServers(u) {
		out = append(out, msvServerView(inst, u))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// msvHandleWatch — GET /api/media-servers/watch. One entry per server that was
// actually asked; an empty list means nobody was eligible, which is absence of
// an eligible server, not absence of the title. Plex is never asked — Cantinarr
// holds no Plex library read — and a server the user has no account on is not
// asked either.
func msvHandleWatch(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	mediaType := r.URL.Query().Get("media_type")
	if mediaType != mediaTypeMovie && mediaType != mediaTypeTV {
		writeErr(w, http.StatusBadRequest, "media_type must be movie or tv")
		return
	}
	tmdbID := queryInt(r, "tmdb_id", 0)
	tvdbID := queryInt(r, "tvdb_id", 0)
	if tmdbID <= 0 && tvdbID <= 0 {
		writeErr(w, http.StatusBadRequest, "tmdb_id or tvdb_id required")
		return
	}

	status, _ := requestStatusForTmdb(tmdbID, mediaType)
	found := status == statusAvailable || status == statusPartial

	out := []map[string]any{}
	for _, inst := range msvUserServers(u) {
		if inst.ServiceType == servicePlex {
			continue
		}
		address := msvPublicAddress(inst)
		if address == "" {
			continue
		}
		acct := msvAccountFor(u.ID, inst.ID)
		if acct == nil || acct.Disabled {
			continue
		}
		entry := map[string]any{
			"instance_id":  inst.ID,
			"service_type": inst.ServiceType,
			"name":         inst.Name,
			"state":        "missing",
		}
		if found {
			entry["state"] = "found"
			entry["url"] = fmt.Sprintf("%s/web/index.html#!/details?id=%s-%d",
				strings.TrimRight(address, "/"), mediaType, tmdbID)
		}
		out = append(out, entry)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// msvResolveOwn resolves the addressed media server for a self-service call.
// Every "not for you" case answers with the SAME bytes — unknown instance,
// ungranted instance, wrong type — so the endpoint never becomes a probe for
// which instances exist.
func msvResolveOwn(w http.ResponseWriter, r *http.Request) (*DemoUser, *DemoInstance, bool) {
	u := userFrom(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return nil, nil, false
	}
	id := chi.URLParam(r, "instanceID")
	inst := instanceByID(id)
	if inst == nil || !isMediaServerType(inst.ServiceType) || !userCanSeeInstance(u, id) {
		writeErr(w, http.StatusForbidden, "that server is not available to you")
		return nil, nil, false
	}
	return u, inst, true
}

// msvWriteCodedErr writes {"error":…,"code":…} — the app switches on the code.
func msvWriteCodedErr(w http.ResponseWriter, status int, msg, code string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// msvHandleCreateAccount — POST /api/media-servers/{instanceID}/account.
// One endpoint, two shapes: a password creates an account on Jellyfin/Emby,
// an email asks for a Plex invite. Sending both is a client bug, not a choice.
func msvHandleCreateAccount(w http.ResponseWriter, r *http.Request) {
	u, inst, ok := msvResolveOwn(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	password := strings.TrimSpace(req.Password)
	email := strings.TrimSpace(req.Email)
	if password != "" && email != "" {
		writeErr(w, http.StatusBadRequest, "send a password or an email, not both")
		return
	}
	if email != "" {
		msvRequestInvite(w, u, inst, email)
		return
	}
	if inst.ServiceType == servicePlex {
		msvWriteCodedErr(w, http.StatusBadRequest,
			"this server invites by email; share the email your invite should go to", "wrong_kind")
		return
	}
	if len(password) < msvMinPasswordLength {
		writeErr(w, http.StatusBadRequest,
			fmt.Sprintf("password must be at least %d characters", msvMinPasswordLength))
		return
	}
	if len(password) > 1024 {
		writeErr(w, http.StatusBadRequest, "password is too long")
		return
	}
	if msvAccountFor(u.ID, inst.ID) != nil {
		msvWriteCodedErr(w, http.StatusConflict, "you already have an account on this server", "account_exists")
		return
	}
	// A name the server already holds is the collision the guide's
	// "I already have an account" card exists to route around.
	for _, remote := range msvRosterFor(inst.ID) {
		if strings.EqualFold(remote.Name, u.Username) {
			msvWriteCodedErr(w, http.StatusConflict,
				"that name is already taken on this server; ask your admin to link it to you", "name_taken")
			return
		}
	}

	created := msvCreateAccount(u, inst, nil, u.Username, true, false)
	writeJSON(w, http.StatusCreated, map[string]any{
		"username":       created.Username,
		"public_address": msvPublicAddress(inst),
		"pending":        created.Pending,
		"administrator":  created.Administrator,
	})
}

// msvRequestInvite is the Plex half of create: share an email, get a share.
func msvRequestInvite(w http.ResponseWriter, u *DemoUser, inst *DemoInstance, email string) {
	if inst.ServiceType != servicePlex {
		msvWriteCodedErr(w, http.StatusBadRequest, "this server takes a password, not an email", "wrong_kind")
		return
	}
	if !strings.Contains(email, "@") || len(email) > 254 {
		msvWriteCodedErr(w, http.StatusBadRequest, "enter a valid email address", "invalid_email")
		return
	}
	if msvAccountFor(u.ID, inst.ID) != nil {
		msvWriteCodedErr(w, http.StatusConflict, "you already have an account on this server", "account_exists")
		return
	}
	msvMu.Lock()
	for _, a := range msvAccounts {
		if a.InstanceID == inst.ID && strings.EqualFold(a.Username, email) {
			msvMu.Unlock()
			msvWriteCodedErr(w, http.StatusConflict,
				"that email already has access through another account; ask your admin", "name_taken")
			return
		}
	}
	msvMu.Unlock()

	setUserPlexEmail(u.ID, email)
	created := msvCreateAccount(u, inst, nil, email, true, true)
	// The user hears "check your email"; admins hear that access was asked for.
	wsToUser(u.ID, evtPlexInviteSent, map[string]any{})
	wsToAdmins(evtPlexAccessRequest, map[string]any{
		"user_id": u.ID, "username": u.Username, "invite_state": "sent",
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"username":       created.Username,
		"public_address": msvPublicAddress(inst),
		"pending":        created.Pending,
		"administrator":  created.Administrator,
	})
}

// msvCreateAccount records a new account row, minting a remote id when the
// caller has none (an account Cantinarr just made rather than adopted).
// msvCreateAccount records a new account row. remote is nil when Cantinarr is
// making the account rather than adopting one, in which case the roster gains
// the row too — the server now holds it, so every later read must see it.
func msvCreateAccount(u *DemoUser, inst *DemoInstance, remote *msvRemoteUser, username string, byCantinarr, pending bool) *msvAccount {
	msvMu.Lock()
	defer msvMu.Unlock()
	if remote == nil {
		remote = &msvRemoteUser{
			ID:      fmt.Sprintf("%s-%s", strings.Split(inst.ID, "-")[0], randomHex(4)),
			Name:    username,
			Pending: pending,
		}
		msvRosters[inst.ID] = append(msvRosters[inst.ID], remote)
	}
	a := &msvAccount{
		UserID: u.ID, InstanceID: inst.ID, RemoteUserID: remote.ID,
		Username: username, CreatedByCantinarr: byCantinarr, Pending: pending,
		Disabled: remote.IsDisabled, Administrator: remote.IsAdministrator,
		CreatedAt: time.Now(),
	}
	msvAccounts[msvKey(u.ID, inst.ID)] = a
	out := *a
	return &out
}

// msvHandleLinkOwnAccount — POST /api/media-servers/{instanceID}/account/link.
// Proves an account is yours by signing in to the media server with it. A bad
// password answers 400, deliberately not 401: a 401 here would look to the
// app's interceptor like the Cantinarr session expired.
func msvHandleLinkOwnAccount(w http.ResponseWriter, r *http.Request) {
	u, inst, ok := msvResolveOwn(w, r)
	if !ok {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "username and password required")
		return
	}
	if len(username) > 256 {
		writeErr(w, http.StatusBadRequest, "username is too long")
		return
	}
	if len(req.Password) > 1024 {
		writeErr(w, http.StatusBadRequest, "password is too long")
		return
	}
	if inst.ServiceType == servicePlex {
		msvWriteCodedErr(w, http.StatusBadRequest,
			"this server invites by email; sign in with Plex or share your email", "wrong_kind")
		return
	}
	if msvAccountFor(u.ID, inst.ID) != nil {
		msvWriteCodedErr(w, http.StatusConflict, "you already have an account on this server", "account_exists")
		return
	}

	var match *msvRemoteUser
	for _, remote := range msvRosterFor(inst.ID) {
		if strings.EqualFold(remote.Name, username) {
			match = remote
			break
		}
	}
	// The demo accepts any password for a name the server holds — there is no
	// real credential to check — but keeps every other verdict honest.
	if match == nil {
		msvWriteCodedErr(w, http.StatusBadRequest,
			"wrong username or password for this server", "bad_credentials")
		return
	}
	if match.IsDisabled {
		msvWriteCodedErr(w, http.StatusBadRequest,
			"that account can't sign in right now; it may be turned off. Ask your admin", "account_refused")
		return
	}
	msvMu.Lock()
	for _, a := range msvAccounts {
		if a.InstanceID == inst.ID && a.RemoteUserID == match.ID {
			msvMu.Unlock()
			msvWriteCodedErr(w, http.StatusConflict,
				"that account is already linked to another user; ask your admin", "remote_already_linked")
			return
		}
	}
	msvMu.Unlock()

	created := msvCreateAccount(u, inst, match, match.Name, false, false)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"username":       created.Username,
		"public_address": msvPublicAddress(inst),
		"pending":        created.Pending,
		"administrator":  created.Administrator,
	})
}

// msvPlexInstance returns the Plex instance this user holds, or nil.
func msvPlexInstance(u *DemoUser) *DemoInstance {
	for _, inst := range msvUserServers(u) {
		if inst.ServiceType == servicePlex {
			return inst
		}
	}
	return nil
}

// msvHandlePlexSignInBegin — POST /api/media-servers/plex/sign-in/begin.
// Signing in with Plex proves which plex.tv account is yours, so the share can
// go to the right address without anyone typing it.
func msvHandlePlexSignInBegin(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	pinID, code, url := plexBeginPin()
	msvMu.Lock()
	msvSignInPins[pinID] = u.ID
	msvMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"pin_id": pinID, "code": code, "url": url})
}

// msvHandlePlexSignInCheck — POST /api/media-servers/plex/sign-in/check. The
// app polls this every few seconds; the answer is cached once concluded, so a
// later poll never re-sends an invite.
func msvHandlePlexSignInCheck(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		PinID int64 `json:"pin_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PinID == 0 {
		writeErr(w, http.StatusBadRequest, "pin_id required")
		return
	}
	msvMu.Lock()
	cached, hasCached := msvSignInResults[req.PinID]
	owner, known := msvSignInPins[req.PinID]
	msvMu.Unlock()
	if hasCached && owner == u.ID {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, cached)
		return
	}
	if !known || owner != u.ID {
		msvWriteCodedErr(w, http.StatusNotFound,
			"the Plex sign-in has expired; start again", "pin_expired")
		return
	}

	approved, found := plexPollPin(req.PinID)
	if !found {
		msvMu.Lock()
		delete(msvSignInPins, req.PinID)
		msvMu.Unlock()
		msvWriteCodedErr(w, http.StatusNotFound,
			"the Plex sign-in has expired; start again", "pin_expired")
		return
	}
	if !approved {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"linked": false})
		return
	}

	// Approved. The account plex.tv reports is this user's; what happens next
	// depends on whether they already hold a share.
	email := plexDemoAccountEmail
	if u.PlexEmail != "" {
		email = u.PlexEmail
	}
	result := map[string]any{
		"linked":   true,
		"username": plexDemoAccount,
		"email":    email,
	}
	inst := msvPlexInstance(u)
	switch {
	case inst == nil:
		// No Plex grant: the address is recorded and an admin hears about it.
		setUserPlexEmail(u.ID, email)
		wsToAdmins(evtPlexAccessRequest, map[string]any{
			"user_id": u.ID, "username": u.Username, "invite_state": "",
		})
		result["invite_state"] = ""
	case msvAccountFor(u.ID, inst.ID) != nil:
		result["invite_state"] = "claimed"
	default:
		setUserPlexEmail(u.ID, email)
		msvCreateAccount(u, inst, nil, email, true, true)
		wsToUser(u.ID, evtPlexInviteSent, map[string]any{})
		result["invite_state"] = "sent"
	}
	msvMu.Lock()
	msvSignInResults[req.PinID] = result
	msvMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

// ─── Admin surface ──────────────────────────────────────

// msvHandleAdminAccounts — GET /api/admin/media-servers/accounts. A BARE
// array, sorted so the Users screen renders stably.
func msvHandleAdminAccounts(w http.ResponseWriter, _ *http.Request) {
	msvMu.Lock()
	rows := make([]*msvAccount, 0, len(msvAccounts))
	for _, a := range msvAccounts {
		cp := *a
		rows = append(rows, &cp)
	}
	msvMu.Unlock()
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UserID != rows[j].UserID {
			return rows[i].UserID < rows[j].UserID
		}
		return rows[i].InstanceID < rows[j].InstanceID
	})
	out := []map[string]any{}
	for _, a := range rows {
		out = append(out, msvAdminAccountRow(a))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// msvResolveAdminInstance resolves an addressed media server for an admin
// call. Admins get the specific answer — they are the ones who can fix it.
func msvResolveAdminInstance(w http.ResponseWriter, r *http.Request) (*DemoInstance, bool) {
	inst := instanceByID(chi.URLParam(r, "instanceID"))
	if inst == nil {
		writeErr(w, http.StatusNotFound, "instance not found")
		return nil, false
	}
	if !isMediaServerType(inst.ServiceType) {
		writeErr(w, http.StatusBadRequest, "not a media server instance")
		return nil, false
	}
	return inst, true
}

// msvHandleAdminRemoteUsers — GET /api/admin/media-servers/{id}/users. Wrapped
// in "users" (unlike the accounts list, which is bare).
func msvHandleAdminRemoteUsers(w http.ResponseWriter, r *http.Request) {
	inst, ok := msvResolveAdminInstance(w, r)
	if !ok {
		return
	}
	out := []map[string]any{}
	for _, remote := range msvRosterFor(inst.ID) {
		out = append(out, map[string]any{
			"id":               remote.ID,
			"name":             remote.Name,
			"is_administrator": remote.IsAdministrator,
			"is_disabled":      remote.IsDisabled,
			"pending":          remote.Pending,
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

// msvHandleAdminImport — POST /api/admin/media-servers/{id}/import. Turns
// picked media-server accounts into granted, linked Cantinarr users. Every
// row reports its own outcome; one failure never abandons the rest.
func msvHandleAdminImport(w http.ResponseWriter, r *http.Request) {
	inst, ok := msvResolveAdminInstance(w, r)
	if !ok {
		return
	}
	var req struct {
		RemoteUserIDs []string `json:"remote_user_ids"`
		ServerURL     string   `json:"server_url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.RemoteUserIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "remote_user_ids required")
		return
	}
	if len(req.RemoteUserIDs) > msvMaxImportAccounts {
		writeErr(w, http.StatusBadRequest, "too many accounts at once")
		return
	}
	// A saved external address wins over whatever the app believes its own
	// address is — that is the whole point of configuring one.
	origin := "app"
	if extAddr := externalAddress(); extAddr != "" {
		origin = "external_address"
	} else if strings.TrimSpace(req.ServerURL) == "" {
		writeErr(w, http.StatusBadRequest, "server_url required")
		return
	}

	roster := map[string]*msvRemoteUser{}
	for _, remote := range msvRosterFor(inst.ID) {
		roster[remote.ID] = remote
	}
	results := []map[string]any{}
	for _, remoteID := range req.RemoteUserIDs {
		row := map[string]any{
			"remote_user_id": remoteID, "remote_username": "",
			"created": false, "linked": false,
		}
		remote := roster[remoteID]
		if remote == nil {
			row["error"] = "not_found"
			results = append(results, row)
			continue
		}
		row["remote_username"] = remote.Name

		msvMu.Lock()
		var takenBy int
		for _, a := range msvAccounts {
			if a.InstanceID == inst.ID && a.RemoteUserID == remoteID {
				takenBy = a.UserID
			}
		}
		msvMu.Unlock()
		if takenBy != 0 {
			row["error"] = "already_linked"
			results = append(results, row)
			continue
		}

		// Find or invite the Cantinarr account this media-server user maps to.
		u := userByName(remote.Name)
		created := false
		if u == nil {
			u = createInvitedUser(remote.Name)
			created = true
		} else if msvAccountFor(u.ID, inst.ID) != nil {
			row["error"] = "user_has_account"
			results = append(results, row)
			continue
		}

		grantInstanceToUser(u.ID, inst.ID)
		msvMu.Lock()
		msvAccounts[msvKey(u.ID, inst.ID)] = &msvAccount{
			UserID: u.ID, InstanceID: inst.ID, RemoteUserID: remote.ID,
			Username: remote.Name, CreatedByCantinarr: false,
			Disabled: remote.IsDisabled, Administrator: remote.IsAdministrator,
			CreatedAt: time.Now(),
		}
		msvMu.Unlock()

		row["user_id"] = u.ID
		row["username"] = u.Username
		row["created"] = created
		row["linked"] = true
		row["origin_source"] = origin
		if created {
			// A brand-new account needs a way in; an existing one already has
			// its own sign-in and gets no link.
			link, _ := mintConnectToken(u.Username)
			row["link"] = link
		}
		results = append(results, row)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// msvHandleAdminLink — PUT /api/admin/users/{userID}/media-servers/{id}/account.
func msvHandleAdminLink(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil || userID <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	u := userByID(userID)
	if u == nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	inst, ok := msvResolveAdminInstance(w, r)
	if !ok {
		return
	}
	var req struct {
		RemoteUserID string `json:"remote_user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	remoteID := strings.TrimSpace(req.RemoteUserID)
	if remoteID == "" {
		writeErr(w, http.StatusBadRequest, "remote_user_id required")
		return
	}
	var match *msvRemoteUser
	for _, remote := range msvRosterFor(inst.ID) {
		if remote.ID == remoteID {
			match = remote
			break
		}
	}
	if match == nil {
		writeErr(w, http.StatusNotFound, "remote user not found")
		return
	}
	if msvAccountFor(u.ID, inst.ID) != nil {
		msvWriteCodedErr(w, http.StatusConflict, "user already has an account on this server", "account_exists")
		return
	}
	msvMu.Lock()
	for _, a := range msvAccounts {
		if a.InstanceID == inst.ID && a.RemoteUserID == remoteID {
			msvMu.Unlock()
			msvWriteCodedErr(w, http.StatusConflict,
				"that account is already linked to another user", "remote_already_linked")
			return
		}
	}
	row := &msvAccount{
		UserID: u.ID, InstanceID: inst.ID, RemoteUserID: match.ID,
		Username: match.Name, CreatedByCantinarr: false,
		Disabled: match.IsDisabled, Pending: match.Pending,
		Administrator: match.IsAdministrator, CreatedAt: time.Now(),
	}
	msvAccounts[msvKey(u.ID, inst.ID)] = row
	msvMu.Unlock()
	// Linking an account without the grant would hand someone an account they
	// cannot see; the grant is what access actually is.
	grantInstanceToUser(u.ID, inst.ID)
	writeJSON(w, http.StatusOK, msvAdminAccountRow(row))
}

// msvHandleAdminUnlink — DELETE the same path. 204, no body. The grant is left
// alone: forgetting the link is not the same decision as revoking access.
func msvHandleAdminUnlink(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil || userID <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	instanceID := chi.URLParam(r, "instanceID")
	msvMu.Lock()
	_, ok := msvAccounts[msvKey(userID, instanceID)]
	if ok {
		delete(msvAccounts, msvKey(userID, instanceID))
	}
	msvMu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "no linked account")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
