package discordnotify

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
)

func loadRequestAlert(db reader, id int64) (requestAlert, error) {
	a := requestAlert{}
	a.Subject.RequestID = id
	err := db.QueryRow(`SELECT r.title,r.media_type,u.username,r.user_id,COALESCE(r.book_format,''),r.tmdb_id,COALESCE(r.foreign_id,''),COALESCE(r.instance_id,''),COALESCE(i.name,'') FROM request_log r JOIN users u ON u.id=r.user_id LEFT JOIN service_instances i ON i.id=r.instance_id WHERE r.id=?`, id).Scan(&a.Title, &a.MediaType, &a.Username, &a.UserID, &a.BookFormat, &a.Subject.TmdbID, &a.Subject.ForeignID, &a.Subject.InstanceID, &a.Subject.Library)
	a.Subject.Title, a.Subject.MediaType = a.Title, a.MediaType
	return a, err
}

func (s *Service) queueEvent(key string, a requestAlert) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := s.readConfig(tx)
	if err != nil || !c.Enabled || !c.Events[eventKind(a)] {
		return err
	}
	if err = s.insertEvent(tx, c, key, a, s.now().Unix()); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

func (s *Service) insertEvent(tx *sql.Tx, c configuration, key string, a requestAlert, due int64) error {
	payload, err := json.Marshal(a)
	if err != nil {
		return err
	}
	var requestID any
	if a.Subject.RequestID > 0 && a.Kind != RequestAvailable {
		requestID = a.Subject.RequestID
	}
	_, err = tx.Exec(`INSERT INTO discord_notifications(request_id,event_key,revision,payload,created_at,updated_at,next_attempt_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(event_key) DO NOTHING`, requestID, key, c.Revision, string(payload), s.now().Unix(), s.now().Unix(), max(due, c.NotBefore))
	return err
}

func (s *Service) recordError(err error) {
	if err == nil {
		return
	}
	s.errorMu.Lock()
	s.enqueueError = "A Discord event could not be queued. Check the server's database and logs."
	s.errorMu.Unlock()
	slog.Error("Discord event could not be queued")
}

// NotifyUser consumes committed request transitions from the existing fan-out.
// Repeated fan-out to shared-book subscribers produces one channel event.
func (s *Service) NotifyUser(_ int64, kind string, data map[string]interface{}) {
	id := integer(data["request_id"])
	if id <= 0 {
		return
	}
	a, err := loadRequestAlert(s.db, id)
	if err != nil {
		s.recordError(err)
		return
	}
	switch kind {
	case "request_decision":
		decision, _ := data["decision"].(string)
		if decision == "approved" {
			a.Kind = RequestApproved
		} else if decision == "denied" {
			a.Kind = RequestDenied
		} else {
			return
		}
		_ = s.db.QueryRow(`SELECT COALESCE(approved_by,0) FROM request_log WHERE id=?`, id).Scan(&a.ActorID)
		// Legacy parked-book completion can refresh a subscriber using the
		// decision event without any human decision. It is not an approval.
		if a.Kind == RequestApproved && a.ActorID == 0 {
			return
		}
		s.recordError(s.queueEvent(fmt.Sprintf("decision:%d:%s", id, a.Kind), a))
	case "request_updated":
		if data["delivery_state"] == "attention" {
			s.requestFailed(id, a)
		}
	}
	s.WakeAvailability()
}

func (s *Service) NotifyAdmins(_ string, _ map[string]interface{}) {}

func (s *Service) requestFailed(id int64, a requestAlert) {
	var stamp string
	err := s.db.QueryRow(`SELECT format || ':' || code || ':' || last_attempt_at || ':' || attempts FROM request_dispatch WHERE request_id=? AND state='attention' AND code IN ('retry_limit','import_failed') ORDER BY format LIMIT 1`, id).Scan(&stamp)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		s.recordError(err)
		return
	}
	a.Kind = RequestFailed
	s.recordError(s.queueEvent(fmt.Sprintf("failed:%d:%s", id, stamp), a))
}

// ReportChanged is called at explicit committed report transitions, never at a
// generic thread refresh. Only user reports belong in this shared channel.
func (s *Service) ReportChanged(kind string, id, actorID, occurrence int64) {
	if kind != IssueCreated && kind != IssueComment && kind != IssueResolved && kind != IssueReopened {
		return
	}
	a := requestAlert{Kind: kind, IssueID: id, ActorID: actorID}
	a.Subject.IssueID = id
	err := s.db.QueryRow(`SELECT i.title,i.media_type,COALESCE(i.tmdb_id,0),COALESCE(i.instance_id,''),i.reporter_id,u.username FROM issues i JOIN users u ON u.id=i.reporter_id WHERE i.id=? AND i.source='user'`, id).Scan(&a.Title, &a.MediaType, &a.Subject.TmdbID, &a.Subject.InstanceID, &a.UserID, &a.Username)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		s.recordError(err)
		return
	}
	a.Subject.Title, a.Subject.MediaType = a.Title, a.MediaType
	if kind == IssueCreated {
		a.ActorID = a.UserID
	}
	s.recordError(s.queueEvent(fmt.Sprintf("report:%d:%s:%d", id, kind, occurrence), a))
}

func integer(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}
