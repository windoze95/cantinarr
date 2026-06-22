package push

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// Notifier turns request events into push notifications via the gateway. It
// implements request.Notifier. Sends are fire-and-forget so a slow or failing
// gateway never blocks an approval/denial.
type Notifier struct {
	db     *sql.DB
	client *Client
	logger *slog.Logger
}

// NewNotifier builds a push notifier. A nil client makes every method a no-op,
// so callers can wire it unconditionally and gate on configuration elsewhere.
func NewNotifier(db *sql.DB, client *Client, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Notifier{db: db, client: client, logger: logger}
}

// NotifyUser pushes the outcome of a request decision to the requesting user.
// Only "request_decision" events produce a notification.
func (n *Notifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	if n == nil || n.client == nil || eventType != "request_decision" {
		return
	}

	title, body := decisionMessage(data)
	if title == "" {
		return
	}

	n.send([]int64{userID}, title, body, passthrough("request_decision", data))
}

// NotifyAdmins pushes a new pending request to every admin. Only
// "request_pending" events produce a notification.
func (n *Notifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	if n == nil || n.client == nil || eventType != "request_pending" {
		return
	}

	title := str(data["title"])
	if title == "" {
		title = "a title"
	}

	adminIDs, err := n.adminIDs()
	if err != nil {
		n.logger.Error("push: resolve admin ids", "err", err)
		return
	}
	if len(adminIDs) == 0 {
		return
	}

	n.send(adminIDs, "New request", title, passthrough("request_pending", data))
}

// send dispatches a notification in the background with panic recovery, so a
// failed delivery (or a marshalling bug) can never take down the caller.
func (n *Notifier) send(userIDs []int64, title, body string, data map[string]any) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				n.logger.Error("push: send panicked", "err", fmt.Sprint(r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := n.client.Send(ctx, userIDs, title, body, data); err != nil {
			n.logger.Error("push: send notification", "err", err, "title", title)
		}
	}()
}

// adminIDs returns the ids of all admin users.
func (n *Notifier) adminIDs() ([]int64, error) {
	rows, err := n.db.Query("SELECT id FROM users WHERE role = 'admin'")
	if err != nil {
		return nil, fmt.Errorf("query admin ids: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan admin id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// decisionMessage derives the alert title/body from a request_decision payload.
// An unrecognized decision yields an empty title so no notification is sent.
func decisionMessage(data map[string]interface{}) (title, body string) {
	mediaTitle := str(data["title"])
	if mediaTitle == "" {
		mediaTitle = "Your request"
	}
	switch str(data["decision"]) {
	case "approved":
		return "Request approved", mediaTitle + " is on the way"
	case "denied":
		body = mediaTitle + " was denied"
		if reason := str(data["reason"]); reason != "" {
			body += ": " + reason
		}
		return "Request denied", body
	default:
		return "", ""
	}
}

// passthrough copies the event fields the client uses to route a notification
// tap (type + media identity) into the APNs custom-data payload.
func passthrough(eventType string, data map[string]interface{}) map[string]any {
	out := map[string]any{"type": eventType}
	if v, ok := data["tmdb_id"]; ok {
		out["tmdb_id"] = v
	}
	if v := str(data["media_type"]); v != "" {
		out["media_type"] = v
	}
	return out
}

// str reads a string value from a map[string]interface{}, returning "" when the
// key is absent or not a string.
func str(v interface{}) string {
	s, _ := v.(string)
	return s
}
