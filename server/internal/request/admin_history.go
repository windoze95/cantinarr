package request

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

// History decisions describe saved intent, never current library availability.
// In particular an approved dispatch may still be stored as pending while it
// waits for a library. Missing reviewer metadata is not proof of auto-approval.
const historyDecisionSQL = `CASE
 WHEN r.status = 'denied' AND r.approved_by IS NULL AND r.deny_reason = 'Cancelled' THEN 'cancelled'
 WHEN r.status = 'denied' THEN 'denied'
 WHEN r.approved_by IS NOT NULL THEN 'approved'
 WHEN r.status = 'pending' AND COALESCE(r.park_reason, '') IN ('delivery', 'author_import') THEN 'approved'
 WHEN r.status = 'pending' THEN 'pending'
 WHEN r.status IN ('requested', 'available', 'downloading', 'partial', 'unavailable') THEN 'approved'
 ELSE 'unknown' END`

// Older book decisions materialized each subscriber's own history row. Only
// active/shared dispatch rows still represent the subscribers on their owner.
const historySharedSQL = `(r.status = 'pending' OR EXISTS (SELECT 1 FROM request_dispatch d WHERE d.request_id = r.id))`

type HistoryRequester struct {
	UserID     int64  `json:"user_id"`
	Username   string `json:"username"`
	BookFormat string `json:"book_format,omitempty"`
}

type AdminHistoryItem struct {
	ID              int64              `json:"id"`
	TmdbID          int                `json:"tmdb_id"`
	ForeignID       string             `json:"foreign_id,omitempty"`
	CatalogProvider string             `json:"catalog_provider,omitempty"`
	MediaType       string             `json:"media_type"`
	Title           string             `json:"title"`
	PosterPath      string             `json:"poster_path,omitempty"`
	InstanceID      string             `json:"instance_id,omitempty"`
	InstanceName    string             `json:"instance_name,omitempty"`
	SeasonScope     string             `json:"season_scope,omitempty"`
	BookFormat      string             `json:"book_format,omitempty"`
	Decision        string             `json:"decision"`
	DecidedBy       string             `json:"decided_by,omitempty"`
	DecidedAt       *time.Time         `json:"decided_at,omitempty"`
	DenyReason      string             `json:"deny_reason,omitempty"`
	RequestedAt     time.Time          `json:"requested_at"`
	Requesters      []HistoryRequester `json:"requesters"`
}

type AdminHistoryPage struct {
	Requests   []AdminHistoryItem `json:"requests"`
	Requesters []HistoryRequester `json:"requesters"`
	NextBefore int64              `json:"next_before,omitempty"`
}

type adminHistoryFilter struct {
	Query, MediaType, Decision string
	UserID, Before             int64
	Limit                      int
}

// ListHistory is also protected by requests:manage in the router. Check claims
// here so mounting this aggregate title surface elsewhere cannot expose it.
func (h *Handler) ListHistory(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !h.service.userIsAdmin(claims.UserID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}
	f := adminHistoryFilter{Query: strings.TrimSpace(r.URL.Query().Get("q")), MediaType: r.URL.Query().Get("media_type"), Decision: r.URL.Query().Get("decision"), Limit: 50}
	valid := utf8.RuneCountInString(f.Query) <= 200
	switch f.MediaType {
	case "", "movie", "tv", "book", "music":
	default:
		valid = false
	}
	switch f.Decision {
	case "", "pending", "approved", "denied", "cancelled", "unknown":
	default:
		valid = false
	}
	for key, target := range map[string]*int64{"user_id": &f.UserID, "before": &f.Before} {
		if raw := r.URL.Query().Get(key); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n <= 0 {
				valid = false
			}
			*target = n
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			valid = false
		}
		f.Limit = n
	}
	if !valid {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid history filter"})
		return
	}
	page, err := h.service.adminHistory(r.Context(), f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read request history"})
		return
	}
	// Artwork can perform a bounded provider read after the DB snapshot closes.
	// A role change during that read must not release another user's titles.
	if !h.service.userIsAdmin(claims.UserID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Service) adminHistory(ctx context.Context, f adminHistoryFilter) (*AdminHistoryPage, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	where := []string{"1 = 1"}
	args := []any{}
	if f.Before > 0 {
		where, args = append(where, "r.id < ?"), append(args, f.Before)
	}
	if f.Query != "" {
		// Literal substring: a title containing % or _ is not a wildcard.
		where, args = append(where, "instr(lower(r.title), lower(?)) > 0"), append(args, f.Query)
	}
	if f.MediaType != "" {
		where, args = append(where, "r.media_type = ?"), append(args, f.MediaType)
	}
	if f.Decision != "" {
		where, args = append(where, "("+historyDecisionSQL+") = ?"), append(args, f.Decision)
	}
	if f.UserID > 0 {
		where = append(where, `(r.user_id = ? OR (r.media_type = 'book' AND `+historySharedSQL+` AND EXISTS (SELECT 1 FROM book_request_waiters bw WHERE bw.request_id = r.id AND bw.user_id = ?)))`)
		args = append(args, f.UserID, f.UserID)
	}
	args = append(args, f.Limit+1)
	rows, err := tx.QueryContext(ctx, `SELECT r.id, r.tmdb_id, COALESCE(r.foreign_id, ''), COALESCE(r.catalog_provider, ''), r.media_type, r.title,
 COALESCE(r.instance_id, ''), COALESCE(i.name, ''), COALESCE(r.season_scope, ''), COALESCE(r.book_format, ''),
 `+historyDecisionSQL+`, COALESCE(a.username, ''), r.decided_at, COALESCE(r.deny_reason, ''), r.requested_at,
 COALESCE(r.user_id, 0), COALESCE(u.username, '')
 FROM request_log r LEFT JOIN users u ON u.id = r.user_id LEFT JOIN users a ON a.id = r.approved_by
 LEFT JOIN service_instances i ON i.id = r.instance_id
 WHERE `+strings.Join(where, " AND ")+` ORDER BY r.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	page := &AdminHistoryPage{Requests: []AdminHistoryItem{}, Requesters: []HistoryRequester{}}
	for rows.Next() {
		var item AdminHistoryItem
		var requester HistoryRequester
		var decided sql.NullTime
		if err = rows.Scan(&item.ID, &item.TmdbID, &item.ForeignID, &item.CatalogProvider, &item.MediaType, &item.Title, &item.InstanceID, &item.InstanceName,
			&item.SeasonScope, &item.BookFormat, &item.Decision, &item.DecidedBy, &decided, &item.DenyReason, &item.RequestedAt,
			&requester.UserID, &requester.Username); err != nil {
			rows.Close()
			return nil, err
		}
		if decided.Valid {
			item.DecidedAt = &decided.Time
		}
		requester.BookFormat = item.BookFormat
		item.Requesters = []HistoryRequester{requester}
		page.Requests = append(page.Requests, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(page.Requests) > f.Limit {
		page.Requests = page.Requests[:f.Limit]
		page.NextBefore = page.Requests[len(page.Requests)-1].ID
	}
	for i := range page.Requests {
		item := &page.Requests[i]
		if item.MediaType != "book" {
			continue
		}
		subscribers, err := tx.QueryContext(ctx, `SELECT bw.user_id, COALESCE(u.username, ''), bw.book_format
 FROM book_request_waiters bw JOIN request_log r ON r.id = bw.request_id LEFT JOIN users u ON u.id = bw.user_id
 WHERE r.id = ? AND bw.user_id != COALESCE(r.user_id, 0) AND `+historySharedSQL+` ORDER BY bw.user_id`, item.ID)
		if err != nil {
			return nil, err
		}
		for subscribers.Next() {
			var user HistoryRequester
			if err = subscribers.Scan(&user.UserID, &user.Username, &user.BookFormat); err != nil {
				break
			}
			item.Requesters = append(item.Requesters, user)
		}
		readErr := subscribers.Err()
		subscribers.Close()
		if err != nil {
			return nil, err
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	// Options describe the whole saved history, so filtering/paging never makes
	// the chosen requester disappear from the filter control.
	users, err := tx.QueryContext(ctx, `SELECT u.id, u.username FROM users u WHERE u.id IN (
 SELECT user_id FROM request_log UNION SELECT bw.user_id FROM book_request_waiters bw
 JOIN request_log r ON r.id = bw.request_id WHERE r.media_type = 'book' AND `+historySharedSQL+`)
 ORDER BY lower(u.username), u.id`)
	if err != nil {
		return nil, err
	}
	for users.Next() {
		var user HistoryRequester
		if err = users.Scan(&user.UserID, &user.Username); err != nil {
			break
		}
		page.Requesters = append(page.Requesters, user)
	}
	readErr := users.Err()
	users.Close()
	if err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	posters := make([]PendingRequest, len(page.Requests))
	for i, item := range page.Requests {
		posters[i] = PendingRequest{TmdbID: item.TmdbID, MediaType: item.MediaType}
	}
	s.attachPosterPaths(posters)
	for i := range page.Requests {
		page.Requests[i].PosterPath = posters[i].PosterPath
	}
	return page, nil
}
