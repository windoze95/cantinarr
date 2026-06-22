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
// gateway never blocks an approval/denial. Every notification is filtered by
// the recipient's per-category preferences before dispatch.
type Notifier struct {
	client *Client
	prefs  *PrefsStore
	logger *slog.Logger
}

// NewNotifier builds a push notifier. A nil client makes every method a no-op,
// so callers can wire it unconditionally and gate on configuration elsewhere.
func NewNotifier(db *sql.DB, client *Client, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Notifier{client: client, prefs: NewPrefsStore(db), logger: logger}
}

// NotifyUser pushes the outcome of a request decision to the requesting user.
// Only "request_decision" events produce a notification, and only when the
// requester has opted into that category (off by default).
func (n *Notifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	if n == nil || n.client == nil || eventType != CategoryRequestDecision {
		return
	}
	if !n.prefs.optedIn(userID, CategoryRequestDecision) {
		return
	}

	title, body := decisionMessage(data)
	if title == "" {
		return
	}

	n.send([]int64{userID}, title, body, passthrough(CategoryRequestDecision, data))
}

// NotifyAdmins pushes a new pending request to every admin who has opted into
// the request_pending category (on by default). Only "request_pending" events
// produce a notification.
func (n *Notifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	if n == nil || n.client == nil || eventType != CategoryRequestPending {
		return
	}

	title := str(data["title"])
	if title == "" {
		title = "a title"
	}

	recipients, err := n.prefs.usersOptedInto(CategoryRequestPending)
	if err != nil {
		n.logger.Error("push: resolve request_pending recipients", "err", err)
		return
	}
	if len(recipients) == 0 {
		return
	}

	n.send(recipients, "New request", title, passthrough(CategoryRequestPending, data))
}

// NotifyNewMovie pushes a "movie became available" alert to every user opted
// into the new_movie category (on by default). A collapse id keeps repeat
// availability pings for the same movie from stacking on-device.
func (n *Notifier) NotifyNewMovie(title string, tmdbID int) {
	n.notifyNewContent(CategoryNewMovie, "movie", "New movie available", title+" is ready to watch", title, tmdbID)
}

// NotifyNewEpisode pushes a "new episode available" alert to every user opted
// into the new_episode category (on by default).
func (n *Notifier) NotifyNewEpisode(seriesTitle string, tmdbID int) {
	n.notifyNewContent(CategoryNewEpisode, "tv", "New episode available", "New on "+seriesTitle, seriesTitle, tmdbID)
}

// notifyNewContent is the shared body for the new-content notifications: it
// resolves the opted-in audience for the category and dispatches one collapsed
// push carrying the media identity for tap routing.
func (n *Notifier) notifyNewContent(category, mediaType, title, body, mediaTitle string, tmdbID int) {
	if n == nil || n.client == nil || mediaTitle == "" {
		return
	}
	recipients, err := n.prefs.usersOptedInto(category)
	if err != nil {
		n.logger.Error("push: resolve new-content recipients", "err", err, "category", category)
		return
	}
	if len(recipients) == 0 {
		return
	}
	data := map[string]any{
		"type":       category,
		"tmdb_id":    tmdbID,
		"media_type": mediaType,
	}
	n.sendWithOptions(recipients, title, body, data, SendOptions{
		CollapseID: fmt.Sprintf("%s:%d", category, tmdbID),
	})
}

// send dispatches a notification in the background with panic recovery, so a
// failed delivery (or a marshalling bug) can never take down the caller.
func (n *Notifier) send(userIDs []int64, title, body string, data map[string]any) {
	n.sendWithOptions(userIDs, title, body, data, SendOptions{})
}

// sendWithOptions is send with explicit gateway options (e.g. a collapse id).
func (n *Notifier) sendWithOptions(userIDs []int64, title, body string, data map[string]any, opts SendOptions) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				n.logger.Error("push: send panicked", "err", fmt.Sprint(r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := n.client.SendWithOptions(ctx, userIDs, title, body, data, opts); err != nil {
			n.logger.Error("push: send notification", "err", err, "title", title)
		}
	}()
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
