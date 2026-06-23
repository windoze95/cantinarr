package push

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// Notifier turns request and content events into push notifications via the
// gateway. It implements request.Notifier. Sends are fire-and-forget so a slow
// or failing gateway never blocks an approval/denial. Every notification is
// filtered by the recipient's per-category preferences before dispatch.
//
// The Notifier holds the push Manager rather than a static client, so it picks
// up a gateway that enrolled after boot and no-ops cleanly while push is
// unconfigured or the gateway is unreachable (Manager.Client() == nil). After
// each send it inspects the gateway's per-device results and drops any local
// token the gateway pruned (token rejected by APNs), keeping push_tokens from
// accumulating dead rows.
type Notifier struct {
	mgr    *Manager
	db     *sql.DB
	prefs  *PrefsStore
	logger *slog.Logger
}

// NewNotifier builds a push notifier over the push Manager. A nil manager (or
// one whose client has not enrolled) makes every method a no-op, so callers can
// wire it unconditionally and let the manager gate on configuration.
func NewNotifier(db *sql.DB, mgr *Manager, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Notifier{mgr: mgr, db: db, prefs: NewPrefsStore(db), logger: logger}
}

// client returns the currently-enrolled gateway client, or nil when push is
// unconfigured or the gateway has not yet come up. nil-receiver safe.
func (n *Notifier) client() *Client {
	if n == nil || n.mgr == nil {
		return nil
	}
	return n.mgr.Client()
}

// NotifyUser pushes the outcome of a request decision to the requesting user.
// Only "request_decision" events produce a notification, and only when the
// requester has opted into that category (off by default).
func (n *Notifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	client := n.client()
	if client == nil || eventType != CategoryRequestDecision {
		return
	}
	if !n.prefs.optedIn(userID, CategoryRequestDecision) {
		return
	}

	title, body := decisionMessage(data)
	if title == "" {
		return
	}

	n.send(client, []int64{userID}, title, body, passthrough(CategoryRequestDecision, data))
}

// NotifyAdmins pushes a new pending request to every admin who has opted into
// the request_pending category (on by default). Only "request_pending" events
// produce a notification.
func (n *Notifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	client := n.client()
	if client == nil || eventType != CategoryRequestPending {
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

	// Set the home-screen icon badge to the live queue depth so admins see the
	// count even while the app is closed (the gateway maps this to aps.badge).
	var opts SendOptions
	if count, ok := intval(data["pending_count"]); ok {
		opts.Badge = &count
	}
	n.sendWithOptions(client, recipients, "New request", title, passthrough(CategoryRequestPending, data), opts)
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
	client := n.client()
	if client == nil || mediaTitle == "" {
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
	n.sendWithOptions(client, recipients, title, body, data, SendOptions{
		CollapseID: fmt.Sprintf("%s:%d", category, tmdbID),
	})
}

// send dispatches a notification in the background with panic recovery, so a
// failed delivery (or a marshalling bug) can never take down the caller.
func (n *Notifier) send(client *Client, userIDs []int64, title, body string, data map[string]any) {
	n.sendWithOptions(client, userIDs, title, body, data, SendOptions{})
}

// sendWithOptions is send with explicit gateway options (e.g. a collapse id).
// After the gateway replies it prunes any local token the gateway reported
// dead, so push_tokens self-cleans without a separate sweep.
func (n *Notifier) sendWithOptions(client *Client, userIDs []int64, title, body string, data map[string]any, opts SendOptions) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				n.logger.Error("push: send panicked", "err", fmt.Sprint(r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := client.SendWithOptions(ctx, userIDs, title, body, data, opts)
		if err != nil {
			n.logger.Error("push: send notification", "err", err, "title", title)
			return
		}
		pruneDeadTokens(n.db, n.logger, resp)
	}()
}

// pruneDeadTokens deletes the local push_tokens row for every result the
// gateway flagged as pruned (token rejected by APNs as unregistered). Matching
// on token keeps it precise even if a device re-registered with a new token in
// the meantime. Best-effort: delete failures are logged, not surfaced. Shared
// by the Notifier (after each send) and the test-push handler.
func pruneDeadTokens(db *sql.DB, logger *slog.Logger, resp *SendResponse) {
	if resp == nil {
		return
	}
	pruned := 0
	for _, r := range resp.Results {
		if !r.Pruned || r.Token == "" {
			continue
		}
		if _, err := db.Exec("DELETE FROM push_tokens WHERE token = ?", r.Token); err != nil {
			logger.Error("push: prune dead token", "err", err, "device_id", r.DeviceID)
			continue
		}
		pruned++
	}
	if pruned > 0 {
		logger.Info("push: pruned dead device tokens", "count", pruned)
	}
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

// intval reads an int from a map value, tolerating the int/int64/float64 forms a
// value may take whether it arrived in-process or via a JSON round-trip.
func intval(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
