package seerrcompat

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Seerr keeps one identity per user and names it by type: a Plex user's
// plexUsername, a Jellyfin or Emby user's jellyfinUsername, a local user's
// username. Integrators join that name against the media server's own user
// list (Maintainerr compares "who requested it" with "who watched it" this
// way), so the name here must be the one the media server knows.
//
// Cantinarr users can hold several linked accounts at once. The view fills
// every field it can and picks userType by the same precedence Seerr's own
// import would: a Plex identity, then Jellyfin, then Emby, else local.

type userView struct {
	id               int64
	username         string
	role             string
	createdAt        time.Time
	email            string
	plexUsername     string
	plexID           int64
	jellyfinUsername string
	jellyfinUserID   string
	embyUsername     string
	embyUserID       string
	requestCount     int
}

// loadUsers reads every user with their linked media-server identities and
// movie/TV request counts, keyed by id.
func loadUsers(db *sql.DB) (map[int64]*userView, error) {
	rows, err := db.Query(`SELECT u.id, u.username, u.role, u.created_at, u.plex_email,
		COALESCE(p.username, ''), COALESCE(p.email, ''), COALESCE(p.plex_account_id, 0)
		FROM users u LEFT JOIN plex_identities p ON p.user_id = u.id ORDER BY u.id`)
	if err != nil {
		return nil, fmt.Errorf("load users: %w", err)
	}
	defer rows.Close()
	users := map[int64]*userView{}
	for rows.Next() {
		var (
			u          userView
			plexEmail  string
			identEmail string
		)
		if err := rows.Scan(&u.id, &u.username, &u.role, &u.createdAt, &plexEmail, &u.plexUsername, &identEmail, &u.plexID); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.email = firstNonEmpty(identEmail, plexEmail)
		users[u.id] = &u
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	accounts, err := db.Query(`SELECT a.user_id, i.service_type, a.remote_user_id, a.remote_username
		FROM user_media_server_accounts a JOIN service_instances i ON i.id = a.instance_id
		WHERE i.service_type IN ('plex', 'jellyfin', 'emby') AND a.disabled_at IS NULL
		ORDER BY a.created_at, a.instance_id`)
	if err != nil {
		return nil, fmt.Errorf("load media-server accounts: %w", err)
	}
	defer accounts.Close()
	for accounts.Next() {
		var (
			userID                int64
			serviceType, remoteID string
			remoteName            string
		)
		if err := accounts.Scan(&userID, &serviceType, &remoteID, &remoteName); err != nil {
			return nil, fmt.Errorf("scan media-server account: %w", err)
		}
		u, ok := users[userID]
		if !ok {
			continue
		}
		switch serviceType {
		case "plex":
			// A Plex share row is keyed by the invitee's email; the username
			// arrives once the share is accepted. An email is not a username.
			if u.plexUsername == "" && remoteName != "" && !strings.Contains(remoteName, "@") {
				u.plexUsername = remoteName
			}
			if u.email == "" && strings.Contains(remoteID, "@") {
				u.email = remoteID
			}
		case "jellyfin":
			if u.jellyfinUsername == "" {
				u.jellyfinUsername, u.jellyfinUserID = remoteName, remoteID
			}
		case "emby":
			if u.embyUsername == "" {
				u.embyUsername, u.embyUserID = remoteName, remoteID
			}
		}
	}
	if err := accounts.Err(); err != nil {
		return nil, err
	}

	counts, err := db.Query(`SELECT user_id, COUNT(*) FROM request_log WHERE media_type IN ('movie', 'tv') GROUP BY user_id`)
	if err != nil {
		return nil, fmt.Errorf("count requests: %w", err)
	}
	defer counts.Close()
	for counts.Next() {
		var userID int64
		var n int
		if err := counts.Scan(&userID, &n); err != nil {
			return nil, fmt.Errorf("scan request count: %w", err)
		}
		if u, ok := users[userID]; ok {
			u.requestCount = n
		}
	}
	return users, counts.Err()
}

// seerr renders the view in Seerr's shape.
func (u *userView) seerr() seerrUser {
	out := seerrUser{
		ID:           u.id,
		Email:        u.email,
		Username:     stringOrNull(u.username),
		UserType:     userTypeLocal,
		Permissions:  permissionRequest,
		CreatedAt:    u.createdAt.UTC(),
		UpdatedAt:    u.createdAt.UTC(),
		RequestCount: u.requestCount,
		DisplayName:  u.username,
	}
	if u.role == "admin" {
		out.Permissions = permissionAdmin
	}
	if u.plexUsername != "" || u.plexID != 0 {
		out.UserType = userTypePlex
		out.PlexUsername = stringOrNull(u.plexUsername)
		if u.plexID != 0 {
			id := u.plexID
			out.PlexID = &id
		}
	}
	// Seerr files Emby accounts under the Jellyfin fields (its Emby support
	// rides the Jellyfin code path), so an integrator reads one field for both.
	switch {
	case u.jellyfinUsername != "":
		out.JellyfinUsername = stringOrNull(u.jellyfinUsername)
		out.JellyfinUserID = stringOrNull(u.jellyfinUserID)
		if out.UserType == userTypeLocal {
			out.UserType = userTypeJellyfin
		}
	case u.embyUsername != "":
		out.JellyfinUsername = stringOrNull(u.embyUsername)
		out.JellyfinUserID = stringOrNull(u.embyUserID)
		if out.UserType == userTypeLocal {
			out.UserType = userTypeEmby
		}
	}
	return out
}

// sortedUsers returns the views in id order.
func sortedUsers(users map[int64]*userView) []*userView {
	out := make([]*userView, 0, len(users))
	for _, u := range users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// unknownUser stands in for a requester whose account is gone (request rows
// keep a NULL user after deletion); Seerr never answers a request without a
// requestedBy, so neither does this.
func unknownUser(id int64) seerrUser {
	return seerrUser{ID: id, UserType: userTypeLocal, DisplayName: "Deleted user", Permissions: 0}
}

func stringOrNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
