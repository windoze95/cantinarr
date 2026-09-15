// adminsettings.go — three server-wide admin settings that live beside the
// credential screens: the outbound proxy the server would send its own
// internet traffic through, the Discord webhook new requests are announced
// on, and the master push-notification policy every delivery is checked
// against. Secrets are write-only everywhere here: the demo answers presence
// booleans, never a stored value.
//
// Prefix: adms…
package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

var (
	admsMu sync.Mutex

	// admsProxy is the stored outbound proxy. Password is never echoed.
	admsProxy struct {
		URL         string
		Username    string
		HasPassword bool
	}

	// admsDiscord is the request-alert webhook and its switches.
	admsDiscord = struct {
		Enabled             bool
		HasWebhook          bool
		IncludeAutoApproved bool
	}{Enabled: true, HasWebhook: true, IncludeAutoApproved: false}

	// admsDeliveries is the recent-delivery log the screen shows under the
	// switches, newest first.
	admsDeliveries []map[string]any

	// admsPushPolicy is the server-wide master: the switch plus one flag per
	// category. A category off here stops that delivery for everyone, and
	// never overrides a recipient's own preference.
	admsPushPolicy = struct {
		Enabled    bool
		Categories map[string]bool
	}{Enabled: true, Categories: map[string]bool{}}
)

// admsPushCategories are the preference columns a policy flag exists for —
// the server keys its policy by column, so categories that share a column
// (agent_autoapproval_paused, profile_change_pending) share one switch.
var admsPushCategories = []string{
	"request_decision",
	"request_pending",
	"request_auto_approved",
	"new_movie",
	"new_episode",
	"new_book",
	"new_music",
	"issue_created",
	"agent_action_pending",
	"plex_access_request",
	"media_server_access",
	"issue_report_update",
	"agent_digest",
	"content_upgraded",
}

func init() {
	for _, category := range admsPushCategories {
		admsPushPolicy.Categories[category] = true
	}
	now := time.Now()
	admsDeliveries = []map[string]any{
		{"request_id": 4, "status": "sent", "detail": "Announced in #requests.",
			"updated_at": now.Add(-3 * time.Hour).Unix()},
		{"request_id": 3, "status": "sent", "detail": "Announced in #requests.",
			"updated_at": now.Add(-27 * time.Hour).Unix()},
		{"request_id": 2, "status": "failed",
			"detail":     "Discord answered 429 Too Many Requests; the next attempt is queued.",
			"updated_at": now.Add(-40 * time.Hour).Unix()},
	}
}

func registerAdminSettings(r chi.Router) {
	admin := r.With(requireAdmin)
	admin.Get("/admin/outbound-proxy", admsProxyHandler)
	admin.Put("/admin/outbound-proxy", admsProxyHandler)
	admin.Post("/admin/outbound-proxy/test", admsProxyTestHandler)

	admin.Get("/admin/discord-notifications", admsDiscordHandler)
	admin.Put("/admin/discord-notifications", admsDiscordHandler)
	admin.Delete("/admin/discord-notifications", admsDiscordHandler)
	admin.Post("/admin/discord-notifications/test", admsDiscordTestHandler)

	admin.Get("/admin/push-notifications", admsPushPolicyHandler)
	admin.Put("/admin/push-notifications", admsPushPolicyHandler)
}

// ─── Outbound proxy ─────────────────────────────────────

func admsProxyView() map[string]any {
	return map[string]any{
		"url":          admsProxy.URL,
		"username":     admsProxy.Username,
		"has_password": admsProxy.HasPassword,
	}
}

// admsValidProxyURL accepts the shapes the real server stores: an http,
// https, socks5, or socks5h origin with a host and no embedded credentials.
func admsValidProxyURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return u.Path == "" || u.Path == "/"
	}
	return false
}

func admsProxyHandler(w http.ResponseWriter, r *http.Request) {
	admsMu.Lock()
	defer admsMu.Unlock()
	if r.Method == http.MethodPut {
		var body struct {
			URL      string `json:"url"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid outbound proxy settings")
			return
		}
		trimmed := strings.TrimSpace(body.URL)
		if trimmed == "" {
			// An empty URL clears everything, credentials included.
			admsProxy.URL, admsProxy.Username, admsProxy.HasPassword = "", "", false
			writeJSON(w, http.StatusOK, admsProxyView())
			return
		}
		if !admsValidProxyURL(trimmed) {
			writeErr(w, http.StatusBadRequest,
				"the proxy address must be scheme://host:port using http, https, socks5, or socks5h, with no username or password in the URL")
			return
		}
		usernameChanged := strings.TrimSpace(body.Username) != admsProxy.Username
		admsProxy.URL = trimmed
		admsProxy.Username = strings.TrimSpace(body.Username)
		switch {
		case body.Password != "":
			admsProxy.HasPassword = true
		case usernameChanged || admsProxy.Username == "":
			// A blank password only keeps the stored one when the username
			// did not move; otherwise the pair no longer belongs together.
			admsProxy.HasPassword = false
		}
	}
	writeJSON(w, http.StatusOK, admsProxyView())
}

// admsProxyTestHandler mirrors instMgmtHandleTest: the demo never dials
// anything, so a well-formed proxy always passes and a malformed one fails
// for the reason the real server would give.
func admsProxyTestHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
		writeErr(w, http.StatusBadRequest, "invalid outbound proxy settings")
		return
	}
	if !admsValidProxyURL(strings.TrimSpace(body.URL)) {
		writeErr(w, http.StatusBadRequest,
			"the proxy address must be scheme://host:port using http, https, socks5, or socks5h, with no username or password in the URL")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ─── Discord request alerts ─────────────────────────────

func admsDiscordView() map[string]any {
	recent := admsDeliveries
	if recent == nil {
		recent = []map[string]any{}
	}
	return map[string]any{
		"enabled":               admsDiscord.Enabled,
		"has_webhook":           admsDiscord.HasWebhook,
		"include_auto_approved": admsDiscord.IncludeAutoApproved,
		"recent":                recent,
	}
}

func admsDiscordHandler(w http.ResponseWriter, r *http.Request) {
	admsMu.Lock()
	defer admsMu.Unlock()
	switch r.Method {
	case http.MethodDelete:
		admsDiscord.Enabled = false
		admsDiscord.HasWebhook = false
		admsDiscord.IncludeAutoApproved = false
	case http.MethodPut:
		var body struct {
			Enabled             bool    `json:"enabled"`
			IncludeAutoApproved bool    `json:"include_auto_approved"`
			WebhookURL          *string `json:"webhook_url"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid Discord settings")
			return
		}
		if body.WebhookURL != nil {
			if !admsValidDiscordWebhook(*body.WebhookURL) {
				writeErr(w, http.StatusBadRequest,
					"the webhook address must be a https://discord.com/api/webhooks/… URL")
				return
			}
			admsDiscord.HasWebhook = true
		}
		if body.Enabled && !admsDiscord.HasWebhook {
			writeErr(w, http.StatusBadRequest, "add a webhook address first")
			return
		}
		admsDiscord.Enabled = body.Enabled
		admsDiscord.IncludeAutoApproved = body.IncludeAutoApproved
	}
	writeJSON(w, http.StatusOK, admsDiscordView())
}

func admsValidDiscordWebhook(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" {
		return false
	}
	if u.Host != "discord.com" && u.Host != "discordapp.com" && u.Host != "canary.discord.com" {
		return false
	}
	return strings.HasPrefix(u.Path, "/api/webhooks/")
}

// admsDiscordTestHandler answers one simulated delivery. It says plainly
// that nothing left the building — a demo that reported a real "Delivered"
// would be claiming a message somebody could go look for.
func admsDiscordTestHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WebhookURL *string `json:"webhook_url"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
		writeErr(w, http.StatusBadRequest, "invalid Discord settings")
		return
	}
	admsMu.Lock()
	defer admsMu.Unlock()
	if body.WebhookURL != nil && !admsValidDiscordWebhook(*body.WebhookURL) {
		writeErr(w, http.StatusBadRequest,
			"the webhook address must be a https://discord.com/api/webhooks/… URL")
		return
	}
	if body.WebhookURL == nil && !admsDiscord.HasWebhook {
		writeErr(w, http.StatusBadRequest, "add a webhook address first")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "sent",
		"detail":     "Simulated: the demo server never contacts Discord.",
		"updated_at": time.Now().Unix(),
	})
}

// ─── Push notification policy ───────────────────────────

func admsPushPolicyView() map[string]any {
	categories := map[string]bool{}
	for _, category := range admsPushCategories {
		categories[category] = admsPushPolicy.Categories[category]
	}
	return map[string]any{"enabled": admsPushPolicy.Enabled, "categories": categories}
}

func admsPushPolicyHandler(w http.ResponseWriter, r *http.Request) {
	admsMu.Lock()
	defer admsMu.Unlock()
	if r.Method == http.MethodPut {
		var body struct {
			Enabled    *bool           `json:"enabled"`
			Categories map[string]bool `json:"categories"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid push notification settings")
			return
		}
		known := map[string]bool{}
		for _, category := range admsPushCategories {
			known[category] = true
		}
		for key := range body.Categories {
			if !known[key] {
				writeErr(w, http.StatusBadRequest, "unknown notification category")
				return
			}
		}
		if body.Enabled != nil {
			admsPushPolicy.Enabled = *body.Enabled
		}
		for key, value := range body.Categories {
			admsPushPolicy.Categories[key] = value
		}
	}
	writeJSON(w, http.StatusOK, admsPushPolicyView())
}
