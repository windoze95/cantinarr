package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var admsDiscordEventKinds = []string{
	"request_pending", "request_auto_approved", "request_approved", "request_denied",
	"request_available", "request_failed", "issue_created", "issue_comment",
	"issue_resolved", "issue_reopened",
}

func admsValidEvents(events map[string]bool) bool {
	for key := range events {
		known := false
		for _, kind := range admsDiscordEventKinds {
			if key == kind {
				known = true
				break
			}
		}
		if !known {
			return false
		}
	}
	return true
}

func admsAdminEvent(kind string) bool {
	return kind == "request_pending" || kind == "request_auto_approved" ||
		kind == "request_failed" || kind == "issue_created"
}

func admsValidDiscordID(id string) bool {
	if len(id) < 17 || len(id) > 20 || id[0] == '0' {
		return false
	}
	for _, ch := range id {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(id, 10, 64)
	return err == nil
}

func admsValidAvatarURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

type demoDiscordPrefs struct {
	Enabled bool
	IDs     []string
	Events  map[string]bool
}

var demoDiscordPrefsMu sync.Mutex
var demoDiscordPrefsByUser = map[int]demoDiscordPrefs{}

func demoDiscordPrefsView(u *DemoUser) map[string]any {
	demoDiscordPrefsMu.Lock()
	p := demoDiscordPrefsByUser[u.ID]
	demoDiscordPrefsMu.Unlock()
	admsMu.Lock()
	serverEnabled := admsDiscord.Enabled && admsDiscord.HasWebhook
	serverMentions := admsDiscord.EnableMentions
	serverEvents := map[string]bool{}
	for k, v := range admsDiscord.Events {
		serverEvents[k] = v
	}
	admsMu.Unlock()
	allowed := map[string]bool{}
	for _, kind := range admsDiscordEventKinds {
		allowed[kind] = serverEvents[kind] && (!admsAdminEvent(kind) || u.Role == roleAdmin)
	}
	events := map[string]bool{}
	for k, v := range p.Events {
		if !admsAdminEvent(k) || u.Role == roleAdmin {
			events[k] = v
		}
	}
	blocked := ""
	switch {
	case !serverEnabled:
		blocked = "An administrator must enable Discord notifications and save a webhook."
	case !serverMentions:
		blocked = "An administrator must enable Discord mentions."
	case !p.Enabled:
		blocked = "Enable mentions for your account to receive personal pings."
	case len(p.IDs) == 0:
		blocked = "Add your Discord user ID to receive personal pings."
	}
	return map[string]any{
		"enabled": p.Enabled, "discord_ids": append([]string{}, p.IDs...),
		"events": events, "allowed_events": allowed,
		"server_enabled": serverEnabled, "server_mentions": serverMentions,
		"blocked_reason": blocked,
	}
}

func demoDiscordPrefsHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if r.Method == http.MethodPut {
		var body struct {
			Enabled    bool            `json:"enabled"`
			DiscordIDs []string        `json:"discord_ids"`
			Events     map[string]bool `json:"events"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&body) != nil ||
			len(body.DiscordIDs) > 10 || !admsValidEvents(body.Events) {
			writeErr(w, http.StatusBadRequest, "Check your Discord IDs and event choices.")
			return
		}
		set := map[string]bool{}
		for _, raw := range body.DiscordIDs {
			id := strings.TrimSpace(raw)
			if !admsValidDiscordID(id) {
				writeErr(w, http.StatusBadRequest, "Check your Discord IDs and event choices.")
				return
			}
			set[id] = true
		}
		ids := []string{}
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if body.Enabled && len(ids) == 0 {
			writeErr(w, http.StatusBadRequest, "Check your Discord IDs and event choices.")
			return
		}
		for kind, on := range body.Events {
			if on && admsAdminEvent(kind) && u.Role != roleAdmin {
				writeErr(w, http.StatusBadRequest, "Check your Discord IDs and event choices.")
				return
			}
		}
		demoDiscordPrefsMu.Lock()
		demoDiscordPrefsByUser[u.ID] = demoDiscordPrefs{body.Enabled, ids, body.Events}
		demoDiscordPrefsMu.Unlock()
	}
	view := demoDiscordPrefsView(u)
	if r.Method == http.MethodPost {
		if blocked := view["blocked_reason"].(string); blocked != "" {
			writeErr(w, http.StatusBadRequest, blocked)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "sent", "detail": "Simulated: the demo server never contacts Discord.",
			"attempts": 1, "updated_at": time.Now().Unix(),
		})
		return
	}
	writeJSON(w, http.StatusOK, view)
}
