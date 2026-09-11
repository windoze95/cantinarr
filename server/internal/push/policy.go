package push

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const policyKey = "push_notification_policy"

// Policy allows delivery; it never overrides a recipient's preferences.
// Keys are preference columns, so related agent/report events share a switch.
type Policy struct {
	Enabled    bool            `json:"enabled"`
	Categories map[string]bool `json:"categories"`
}

func defaultPolicy() Policy {
	p := Policy{Enabled: true, Categories: make(map[string]bool)}
	for _, category := range categoryColumn {
		p.Categories[category.column] = true
	}
	return p
}

type policyReader interface{ QueryRow(string, ...any) *sql.Row }

func readPolicy(db policyReader) (Policy, error) {
	p := defaultPolicy()
	var raw string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", policyKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return Policy{}, err
	}
	loaded := &p
	if err := json.Unmarshal([]byte(raw), &loaded); err != nil {
		return Policy{}, err
	}
	if loaded == nil || p.Categories == nil {
		return Policy{}, errors.New("missing push categories")
	}
	return p, nil
}

func (s *PrefsStore) Policy() (Policy, error) { return readPolicy(s.db) }

func (s *PrefsStore) setPolicy(enabled *bool, categories map[string]bool) error {
	known := defaultPolicy().Categories
	for key := range categories {
		if _, ok := known[key]; !ok {
			return fmt.Errorf("unknown push category %q", key)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := readPolicy(tx)
	if err != nil {
		return err
	}
	if enabled != nil {
		p.Enabled = *enabled
	}
	for key, value := range categories {
		p.Categories[key] = value
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", policyKey, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

func adminCategory(category string) bool {
	switch category {
	case CategoryRequestPending, CategoryRequestAutoApproved, CategoryIssueCreated,
		CategoryAgentActionPending, CategoryAgentAutoApprovalPaused, CategoryProfileChangePending,
		CategoryPlexAccessRequest, CategoryAgentDigest, CategoryContentUpgraded:
		return true
	}
	return false
}

func preferenceCategory(event string) string {
	switch event {
	case EventIssueQuestion, EventIssueFixConfirm, EventIssueClosed:
		return CategoryIssueReportUpdate
	case EventAutoDispatchDisabled:
		return CategoryAgentActionPending
	}
	return event
}

// filterDelivery only narrows the audience already authorized by the caller.
// Unknown categories and unreadable policy fail closed, including at the final
// asynchronous send boundary. Tokens and library observation keep working while
// muted, so re-enabling does not replay a backlog or require device enrollment.
func (s *PrefsStore) filterDelivery(userIDs []int64, category string) ([]int64, error) {
	category = preferenceCategory(category)
	col, ok := categoryColumn[category]
	if !ok {
		return nil, fmt.Errorf("unknown push category %q", category)
	}
	p, err := s.Policy()
	if err != nil {
		return nil, err
	}
	if !p.Enabled || !p.Categories[col.column] || len(userIDs) == 0 {
		return nil, nil
	}
	def := 0
	if col.defaultVal {
		def = 1
	}
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		args[i] = id
	}
	query := fmt.Sprintf(`SELECT u.id FROM users u
		LEFT JOIN notification_prefs p ON p.user_id=u.id
		WHERE COALESCE(p.push_enabled, 1)=1 AND COALESCE(p.%s, %d)=1
		AND u.id IN (%s)`, col.column, def, strings.TrimSuffix(strings.Repeat("?,", len(args)), ","))
	if adminCategory(category) {
		query += " AND u.role='admin'"
	}
	return s.queryUserIDs(query, args...)
}

type preferencesResponse struct {
	Prefs
	ServerPolicy Policy `json:"server_policy"`
}

func (h *Handler) preferences(w http.ResponseWriter, userID int64) {
	prefs, err := h.prefs.Get(userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load preferences"})
		return
	}
	policy, err := h.prefs.Policy()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load server notification settings"})
		return
	}
	writeJSON(w, http.StatusOK, preferencesResponse{Prefs: prefs, ServerPolicy: policy})
}

// ServerPolicy is mounted behind admin:*; personal preference writes cannot
// modify it even if a caller echoes the read-only server_policy metadata.
func (h *Handler) ServerPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		var patch struct {
			Enabled    *bool           `json:"enabled"`
			Categories map[string]bool `json:"categories"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		d.DisallowUnknownFields()
		var extra any
		if err := d.Decode(&patch); err != nil || d.Decode(&extra) != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid push notification settings"})
			return
		}
		for key := range patch.Categories {
			if _, ok := defaultPolicy().Categories[key]; !ok {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown notification category"})
				return
			}
		}
		if err := h.prefs.setPolicy(patch.Enabled, patch.Categories); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save server notification settings"})
			return
		}
	}
	p, err := h.prefs.Policy()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load server notification settings"})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

var (
	errPushDisabledByServer = errors.New("Push notifications are turned off by the server.")
	errPushDisabledForUser  = errors.New("Push notifications are turned off for this account.")
)

func (s *PrefsStore) checkMasters(userID int64) error {
	p, err := s.Policy()
	if err != nil {
		return err
	}
	if !p.Enabled {
		return errPushDisabledByServer
	}
	prefs, err := s.Get(userID)
	if err != nil {
		return err
	}
	if !prefs.PushEnabled {
		return errPushDisabledForUser
	}
	return nil
}

func writeTestPolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errPushDisabledByServer):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "push_disabled_by_server", "error": errPushDisabledByServer.Error()})
	case errors.Is(err, errPushDisabledForUser):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "push_disabled_for_user", "error": errPushDisabledForUser.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load notification settings"})
	}
}
