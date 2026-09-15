// sso.go — the two federated sign-in providers: OpenID Connect and Plex.
// Both are admin-configurable here and both answer their sign-in flows, but
// the demo never talks to a real identity provider, so every flow endpoint
// answers one clear, verbatim refusal instead of pretending to federate. The
// configuration round-trips so the Settings screens are real, and
// `/api/auth/status` reflects what an admin turned on.
//
// SSO-only is refused on purpose: the demo must stay signable-in with the
// published credentials, and a stored `sso_only` would lock every visitor out
// of a server nobody can reconfigure.
//
// Prefix: sso…
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
)

const (
	ssoOIDCUnavailable = "Single sign-on is configured here for the tour, but the demo server never contacts an identity provider. Sign in with the demo credentials instead."
	ssoPlexUnavailable = "Plex sign-in is configured here for the tour, but the demo server never contacts plex.tv. Sign in with the demo credentials instead."
	ssoOnlyRefused     = "The demo server keeps password sign-in available, so SSO-only cannot be turned on here."
)

// ssoOIDCConfig mirrors the server's OIDCConfig. The secret is never echoed —
// has_secret is the only thing said about it.
type ssoOIDCConfig struct {
	Enabled          bool     `json:"enabled"`
	Label            string   `json:"label"`
	Issuer           string   `json:"issuer"`
	ClientID         string   `json:"client_id"`
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

// ssoPlexConfig mirrors the server's PlexConfig.
type ssoPlexConfig struct {
	Enabled    bool `json:"enabled"`
	AutoCreate bool `json:"auto_create"`
}

var (
	ssoMu   sync.Mutex
	ssoOIDC = ssoOIDCConfig{
		Label:            "",
		AdditionalScopes: []string{},
		AllowedGroups:    []string{},
		GroupClaim:       "groups",
	}
	ssoPlex ssoPlexConfig
)

// ssoOIDCView answers the stored configuration with the callback URL derived
// from the advertised server URL, exactly as the real server derives it.
func ssoOIDCView() ssoOIDCConfig {
	c := ssoOIDC
	if c.AdditionalScopes == nil {
		c.AdditionalScopes = []string{}
	}
	if c.AllowedGroups == nil {
		c.AllowedGroups = []string{}
	}
	c.CallbackURL = strings.TrimSuffix(demoServerURL, "/") + "/api/auth/oidc/callback"
	return c
}

// ssoStatusFlags decorates GET /api/auth/status with whatever an admin turned
// on, so the sign-in screen and the settings screen never disagree.
func ssoStatusFlags(out map[string]any) {
	ssoMu.Lock()
	defer ssoMu.Unlock()
	if ssoPlex.Enabled {
		out["plex_available"] = true
	}
	if ssoOIDC.Enabled {
		out["sso_available"] = true
		out["sso_provider"] = ssoOIDC.Label
		out["sso_origin"] = strings.TrimSuffix(demoServerURL, "/")
		// Never sso_only: see the file header.
	}
}

// ─── Routes ─────────────────────────────────────────────

// registerSSOPublic mounts the unauthenticated halves of both flows. Mount it
// on the same public router as /api/auth.
func registerSSOPublic(r chi.Router) {
	r.Post("/auth/oidc/begin", ssoOIDCUnavailableHandler)
	r.Get("/auth/oidc/start", ssoOIDCUnavailableHandler)
	r.Get("/auth/oidc/callback", ssoOIDCUnavailableHandler)
	r.Post("/auth/oidc/exchange", ssoOIDCUnavailableHandler)
	r.Post("/auth/oidc/mcp/begin", ssoOIDCUnavailableHandler)

	r.Post("/auth/plex/begin", ssoPlexUnavailableHandler)
	r.Post("/auth/plex/check", ssoPlexUnavailableHandler)
	r.Post("/auth/plex/cancel", ssoPlexUnavailableHandler)
	r.Post("/auth/plex/exchange", ssoPlexUnavailableHandler)
	r.Post("/auth/plex/mcp/begin", ssoPlexUnavailableHandler)
}

// registerSSO mounts the authenticated halves: a person's own linked
// identities, and the admin configuration screens.
func registerSSO(r chi.Router) {
	r.Get("/auth/oidc/identities", ssoOIDCIdentitiesHandler)
	r.Post("/auth/oidc/link", ssoOIDCUnavailableHandler)
	r.Delete("/auth/oidc/identities", ssoOIDCUnlinkHandler)
	r.Get("/auth/plex/identities", ssoPlexIdentitiesHandler)
	r.Post("/auth/plex/link", ssoPlexUnavailableHandler)
	r.Delete("/auth/plex/identities", ssoPlexUnlinkHandler)

	admin := r.With(requireAdmin)
	admin.Get("/admin/oidc", ssoOIDCConfigHandler)
	admin.Put("/admin/oidc", ssoOIDCConfigHandler)
	admin.Post("/admin/oidc/validate", ssoOIDCUnavailableHandler)
	admin.Post("/admin/oidc/test", ssoOIDCUnavailableHandler)
	admin.Get("/admin/users/{userID}/oidc", ssoOIDCIdentitiesHandler)
	admin.Delete("/admin/users/{userID}/oidc", ssoOIDCUnlinkHandler)

	admin.Get("/admin/plex-auth", ssoPlexConfigHandler)
	admin.Put("/admin/plex-auth", ssoPlexConfigHandler)
	admin.Get("/admin/plex-auth/candidates", ssoPlexCandidatesHandler)
	admin.Post("/admin/plex-auth/confirm", ssoPlexConfirmHandler)
	admin.Get("/admin/users/{userID}/plex", ssoPlexIdentitiesHandler)
	admin.Delete("/admin/users/{userID}/plex", ssoPlexUnlinkHandler)
}

// ssoNoStore matches the real server's headers on every identity surface:
// nothing here may be cached or referred onward.
func ssoNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func ssoOIDCUnavailableHandler(w http.ResponseWriter, _ *http.Request) {
	ssoNoStore(w)
	writeErr(w, http.StatusServiceUnavailable, ssoOIDCUnavailable)
}

func ssoPlexUnavailableHandler(w http.ResponseWriter, _ *http.Request) {
	ssoNoStore(w)
	writeErr(w, http.StatusServiceUnavailable, ssoPlexUnavailable)
}

// ─── Identities ─────────────────────────────────────────

// ssoIdentityTarget is the user whose identities are being read: the path's
// user on an admin route, else the caller.
func ssoIdentityTarget(w http.ResponseWriter, r *http.Request) (*DemoUser, bool) {
	if raw := chi.URLParam(r, "userID"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid user id")
			return nil, false
		}
		u := userByID(id)
		if u == nil {
			writeErr(w, http.StatusNotFound, "user not found")
			return nil, false
		}
		return u, true
	}
	return userFrom(r), true
}

// No account in the demo signed in through a provider, so both lists are
// empty — and empty here means "nothing is linked", not "this could not be
// read": a demo that has never federated has nothing to show.
func ssoOIDCIdentitiesHandler(w http.ResponseWriter, r *http.Request) {
	ssoNoStore(w)
	if _, ok := ssoIdentityTarget(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": []any{}})
}

func ssoPlexIdentitiesHandler(w http.ResponseWriter, r *http.Request) {
	ssoNoStore(w)
	if _, ok := ssoIdentityTarget(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": []any{}})
}

func ssoOIDCUnlinkHandler(w http.ResponseWriter, r *http.Request) {
	ssoNoStore(w)
	if _, ok := ssoIdentityTarget(w, r); !ok {
		return
	}
	writeErr(w, http.StatusBadRequest, "no linked single sign-on identity")
}

func ssoPlexUnlinkHandler(w http.ResponseWriter, r *http.Request) {
	ssoNoStore(w)
	if _, ok := ssoIdentityTarget(w, r); !ok {
		return
	}
	writeErr(w, http.StatusBadRequest, "no linked Plex account")
}

// ─── Admin configuration ────────────────────────────────

func ssoOIDCConfigHandler(w http.ResponseWriter, r *http.Request) {
	ssoNoStore(w)
	ssoMu.Lock()
	defer ssoMu.Unlock()
	if r.Method == http.MethodPut {
		var body struct {
			ssoOIDCConfig
			ClientSecret *string `json:"client_secret"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid single sign-on configuration")
			return
		}
		if body.SSOOnly {
			writeErr(w, http.StatusBadRequest, ssoOnlyRefused)
			return
		}
		if body.Enabled && (strings.TrimSpace(body.Issuer) == "" || strings.TrimSpace(body.ClientID) == "") {
			writeErr(w, http.StatusBadRequest, "an issuer and client ID are required")
			return
		}
		next := body.ssoOIDCConfig
		next.SSOOnly = false
		next.HasSecret = ssoOIDC.HasSecret
		if body.ClientSecret != nil {
			next.HasSecret = strings.TrimSpace(*body.ClientSecret) != ""
		}
		// A configuration change always un-tests itself: the stored "tested"
		// flag describes the settings that were tested, not these.
		next.Tested = false
		if next.AdditionalScopes == nil {
			next.AdditionalScopes = []string{}
		}
		if next.AllowedGroups == nil {
			next.AllowedGroups = []string{}
		}
		ssoOIDC = next
	}
	writeJSON(w, http.StatusOK, ssoOIDCView())
}

func ssoPlexConfigHandler(w http.ResponseWriter, r *http.Request) {
	ssoNoStore(w)
	ssoMu.Lock()
	defer ssoMu.Unlock()
	if r.Method == http.MethodPut {
		var body ssoPlexConfig
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid Plex sign-in configuration")
			return
		}
		ssoPlex = body
	}
	writeJSON(w, http.StatusOK, ssoPlex)
}

// ssoPlexCandidatesHandler lists the accounts a Plex sign-in would adopt:
// every Cantinarr user who already holds a linked account on a Plex instance,
// with the Plex account that link points at. Confirming them is what lets a
// later sign-in recognise the person instead of creating a second account.
func ssoPlexCandidatesHandler(w http.ResponseWriter, _ *http.Request) {
	ssoNoStore(w)
	candidates := []map[string]any{}
	for _, u := range allUsers() {
		for _, inst := range allInstances() {
			if inst.ServiceType != servicePlex {
				continue
			}
			acct := msvAccountFor(u.ID, inst.ID)
			if acct == nil {
				continue
			}
			email := ""
			for _, remote := range msvRosterFor(inst.ID) {
				if remote.ID == acct.RemoteUserID {
					email = remote.Email
					break
				}
			}
			candidates = append(candidates, map[string]any{
				"user_id":         u.ID,
				"username":        u.Username,
				"plex_account_id": ssoPlexAccountID(acct.RemoteUserID),
				"email":           email,
				"plex_username":   acct.Username,
				"servers": []map[string]any{{
					"instance_id": inst.ID,
					"name":        inst.Name,
				}},
				"confirmed": ssoPlexConfirmed[u.ID],
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": candidates})
}

// ssoPlexConfirmed records which candidates an admin accepted this session.
var ssoPlexConfirmed = map[int]bool{}

// ssoPlexAccountID derives a stable numeric Plex account id from the demo's
// opaque remote user id, so the same row always reports the same number.
func ssoPlexAccountID(remoteUserID string) int {
	sum := 0
	for _, c := range remoteUserID {
		sum = sum*31 + int(c)
		sum %= 9_000_000
	}
	return 1_000_000 + sum
}

func ssoPlexConfirmHandler(w http.ResponseWriter, r *http.Request) {
	ssoNoStore(w)
	var body struct {
		Mappings []struct {
			UserID    int `json:"user_id"`
			AccountID int `json:"plex_account_id"`
		} `json:"mappings"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&body) != nil {
		writeErr(w, http.StatusBadRequest, "invalid Plex account mapping")
		return
	}
	ssoMu.Lock()
	defer ssoMu.Unlock()
	for _, m := range body.Mappings {
		if userByID(m.UserID) == nil {
			writeErr(w, http.StatusBadRequest, "unknown user id")
			return
		}
		ssoPlexConfirmed[m.UserID] = true
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
}
