package discordnotify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

type Preferences struct {
	Enabled        bool            `json:"enabled"`
	DiscordIDs     []string        `json:"discord_ids"`
	Events         map[string]bool `json:"events"`
	AllowedEvents  map[string]bool `json:"allowed_events"`
	ServerEnabled  bool            `json:"server_enabled"`
	ServerMentions bool            `json:"server_mentions"`
	BlockedReason  string          `json:"blocked_reason,omitempty"`
}

func adminEvent(kind string) bool {
	return kind == RequestPending || kind == RequestAutoApproved || kind == RequestFailed || kind == IssueCreated
}

func (s *Service) preferences(userID int64) (Preferences, error) {
	p := Preferences{DiscordIDs: []string{}, Events: map[string]bool{}, AllowedEvents: map[string]bool{}}
	var ids, events, role string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id=?`, userID).Scan(&role); err != nil {
		return p, err
	}
	err := s.db.QueryRow(`SELECT enabled,discord_ids,events FROM discord_user_preferences WHERE user_id=?`, userID).Scan(&p.Enabled, &ids, &events)
	if err != nil && err != sql.ErrNoRows {
		return p, err
	}
	if err == nil {
		if json.Unmarshal([]byte(ids), &p.DiscordIDs) != nil || json.Unmarshal([]byte(events), &p.Events) != nil {
			return p, errors.New("invalid saved preferences")
		}
	}
	c, err := s.readConfig(s.db)
	if err != nil {
		return p, err
	}
	p.ServerEnabled, p.ServerMentions = c.Enabled && c.Webhook != "", c.EnableMentions
	for _, kind := range EventKinds {
		p.AllowedEvents[kind] = c.Events[kind] && (!adminEvent(kind) || role == auth.RoleAdmin)
		if adminEvent(kind) && role != auth.RoleAdmin {
			delete(p.Events, kind)
		}
	}
	switch {
	case !p.ServerEnabled:
		p.BlockedReason = "An administrator must enable Discord notifications and save a webhook."
	case !c.EnableMentions:
		p.BlockedReason = "An administrator must enable Discord mentions."
	case !p.Enabled:
		p.BlockedReason = "Enable mentions for your account to receive personal pings."
	case len(p.DiscordIDs) == 0:
		p.BlockedReason = "Add your Discord user ID to receive personal pings."
	}
	return p, nil
}

func (s *Service) savePreferences(userID int64, p Preferences) error {
	if len(p.DiscordIDs) > 10 || !validEvents(p.Events) {
		return errors.New("invalid Discord preferences")
	}
	var role string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id=?`, userID).Scan(&role); err != nil {
		return err
	}
	set := map[string]bool{}
	for _, id := range p.DiscordIDs {
		id = strings.TrimSpace(id)
		if !validDiscordID(id) {
			return errors.New("enter a numeric Discord user ID")
		}
		set[id] = true
	}
	p.DiscordIDs = []string{}
	for id := range set {
		p.DiscordIDs = append(p.DiscordIDs, id)
	}
	sort.Strings(p.DiscordIDs)
	if p.Enabled && len(p.DiscordIDs) == 0 {
		return errors.New("add a Discord user ID before enabling mentions")
	}
	for kind, on := range p.Events {
		if on && adminEvent(kind) && role != auth.RoleAdmin {
			return errors.New("this event is for administrators")
		}
	}
	ids, _ := json.Marshal(p.DiscordIDs)
	events, _ := json.Marshal(p.Events)
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	_, err := s.db.Exec(`INSERT INTO discord_user_preferences(user_id,enabled,discord_ids,events) VALUES(?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET enabled=excluded.enabled,discord_ids=excluded.discord_ids,events=excluded.events`, userID, p.Enabled, string(ids), string(events))
	return err
}

func (s *Service) PreferencesHandler(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		reply(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/test") {
		d, err := s.testMentions(r.Context(), claims.UserID)
		if err != nil {
			reply(w, 400, map[string]string{"error": err.Error()})
			return
		}
		reply(w, 200, d)
		return
	}
	if r.Method == http.MethodPut {
		var p struct {
			Enabled    bool            `json:"enabled"`
			DiscordIDs []string        `json:"discord_ids"`
			Events     map[string]bool `json:"events"`
		}
		if err := decodeBody(w, r, &p); err != nil {
			reply(w, 400, map[string]string{"error": "Invalid Discord preferences."})
			return
		}
		if err := s.savePreferences(claims.UserID, Preferences{Enabled: p.Enabled, DiscordIDs: p.DiscordIDs, Events: p.Events}); err != nil {
			reply(w, 400, map[string]string{"error": "Check your Discord IDs and event choices."})
			return
		}
	}
	p, err := s.preferences(claims.UserID)
	if err != nil {
		reply(w, 500, map[string]string{"error": "Discord preferences could not be read."})
		return
	}
	reply(w, 200, p)
}

func (s *Service) testMentions(ctx context.Context, userID int64) (Delivery, error) {
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	p, err := s.preferences(userID)
	if err != nil {
		return Delivery{}, errors.New("Discord preferences could not be read")
	}
	if p.BlockedReason != "" {
		return Delivery{}, errors.New(p.BlockedReason)
	}
	c, err := s.readConfig(s.db)
	if err != nil {
		return Delivery{}, err
	}
	if s.now().Unix() < c.NotBefore {
		return Delivery{Status: "pending", Detail: "Discord asked us to wait. Try again later.", NextAttemptAt: c.NotBefore}, nil
	}
	mentions := []string{}
	for _, id := range p.DiscordIDs {
		mentions = append(mentions, "<@"+id+">")
	}
	result := send(ctx, s.client, webhookDestination(c), withAppearance(c, map[string]any{"content": strings.Join(mentions, " ") + " Cantinarr mention test. Your saved Discord IDs are mentioned here.", "allowed_mentions": map[string]any{"parse": []string{}, "users": p.DiscordIDs, "roles": []string{}}}))
	d := Delivery{Status: result.Status, Detail: result.Detail, Attempts: 1, UpdatedAt: s.now().Unix()}
	if result.Status == "pending" {
		d.NextAttemptAt = retryAt(s.now(), result.RetryAfter)
		err = s.cooldown(c.Webhook, d.NextAttemptAt)
	}
	return d, err
}

func (s *Service) userMentions(ids []int64, kind string, actorID int64) ([]string, error) {
	set := map[string]bool{}
	for _, id := range ids {
		if id == actorID {
			continue
		}
		p, err := s.preferences(id)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		if p.BlockedReason != "" || !p.Events[kind] || !p.AllowedEvents[kind] {
			continue
		}
		for _, discordID := range p.DiscordIDs {
			if validDiscordID(discordID) {
				set[discordID] = true
			}
		}
	}
	out := []string{}
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
