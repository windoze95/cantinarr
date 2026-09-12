package request

import "database/sql"

// CreationObserver receives new saved submissions, independently of approval
// prompts and per-recipient pushes. Implementations must not perform network
// I/O here or change the outcome of the accepted media request.
type CreationObserver interface {
	RequestCreated(requestID int64, requiresApproval bool)
}

// CreationObservers keeps each delivery channel independent of the others'
// configuration. Each observer records local work or dispatches asynchronously.
type CreationObservers []CreationObserver

func (observers CreationObservers) RequestCreated(id int64, approval bool) {
	for _, observer := range observers {
		if observer != nil {
			observer.RequestCreated(id, approval)
		}
	}
}

func (s *Service) SetCreationObserver(observer CreationObserver) { s.creationObserver = observer }

func (s *Service) notifyCreated(id int64, approval bool) {
	if id > 0 && s.creationObserver != nil {
		s.creationObserver.RequestCreated(id, approval)
	}
}

// Movie/TV history is per call, even when the arr reports existing work.
// Identify a retry before inserting that history; never dedupe by title, or
// confuse a sibling library, another requester, or new season selection.
func (s *Service) isNewSubmission(r *resolvedRequest) bool {
	if r.newWork {
		return true
	}
	var status string
	err := s.db.QueryRow(`SELECT status FROM request_log WHERE user_id=? AND media_type=? AND tmdb_id=? AND COALESCE(instance_id,'')=? AND COALESCE(season_scope,'')=? AND COALESCE(quality_profile_id,0)=? ORDER BY id DESC LIMIT 1`, r.userID, r.mediaType, r.tmdbID, r.instanceID, r.seasonScope, r.qualityProfileID).Scan(&status)
	return err == sql.ErrNoRows || (err == nil && status == StatusDenied)
}
