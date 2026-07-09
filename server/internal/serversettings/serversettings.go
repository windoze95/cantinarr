// Package serversettings stores small, admin-editable, server-wide preferences
// in the settings key/value table (mirroring the remediation/request settings
// pattern). Today it holds only the optional management-portal URL that the
// "update available" banner links to.
package serversettings

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const settingsKey = "server_settings"

// Settings is the server-wide admin preferences blob. It is stored as JSON and
// unmarshalled over the zero value, so adding a field later is migration-free.
type Settings struct {
	// ManagementURL is an optional link to the admin's own container-management
	// portal (e.g. an Unraid or Portainer page). Empty means "not configured".
	ManagementURL string `json:"management_url"`
}

// Service reads and writes the server settings blob.
type Service struct {
	db *sql.DB
}

// NewService returns a settings service backed by the given database.
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// Get returns the stored settings, or the zero value when none are saved.
func (s *Service) Get() Settings {
	var out Settings
	var v string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", settingsKey).Scan(&v); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &out)
	}
	out.ManagementURL = strings.TrimSpace(out.ManagementURL)
	return out
}

// Set validates and persists the settings, returning the stored value.
func (s *Service) Set(in Settings) (Settings, error) {
	in.ManagementURL = strings.TrimSpace(in.ManagementURL)
	if err := validateURL(in.ManagementURL); err != nil {
		return Settings{}, err
	}
	data, err := json.Marshal(in)
	if err != nil {
		return Settings{}, fmt.Errorf("encode server settings: %w", err)
	}
	if _, err := s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", settingsKey, string(data)); err != nil {
		return Settings{}, fmt.Errorf("save server settings: %w", err)
	}
	return in, nil
}

// validateURL accepts an empty string (clears the setting) or an absolute
// http(s) URL; anything else is rejected so the banner never links somewhere
// unusable.
func validateURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("management_url must be an http(s) URL")
	}
	return nil
}
