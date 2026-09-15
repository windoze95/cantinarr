package push

import "fmt"

// RequestCreated consumes the request service's new-submission event. Pending
// requests already have their own actionable notification. Later approvals,
// retries, and shared subscriptions never produce this creation event.
func (n *Notifier) RequestCreated(requestID int64, requiresApproval bool) {
	if n == nil || requiresApproval {
		return
	}
	client := n.client()
	if client == nil {
		return
	}
	var title, mediaType, foreignID, instanceID, format, username string
	var tmdbID int
	err := n.db.QueryRow(`SELECT r.title, r.media_type, COALESCE(r.tmdb_id,0),
		COALESCE(r.foreign_id,''), COALESCE(r.instance_id,''), COALESCE(r.book_format,''), u.username
		FROM request_log r JOIN users u ON u.id=r.user_id WHERE r.id=?`, requestID).
		Scan(&title, &mediaType, &tmdbID, &foreignID, &instanceID, &format, &username)
	if err != nil {
		n.logger.Error("push: read new request", "request_id", requestID, "err", err)
		return
	}
	users, err := n.prefs.usersOptedInto(CategoryRequestAutoApproved)
	if err != nil {
		n.logger.Error("push: resolve auto-approved recipients", "err", err)
		return
	}
	n.send(client, users, "Request automatically approved", fmt.Sprintf("%s requested %s", username, title), map[string]any{
		"type": CategoryRequestAutoApproved, "request_id": requestID,
		"title": title, "media_type": mediaType, "tmdb_id": tmdbID,
		"foreign_id": foreignID, "instance_id": instanceID, "book_format": format,
	})
}
