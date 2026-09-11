package request

import (
	"fmt"
	"github.com/windoze95/cantinarr-server/internal/bookdiscovery"
	"github.com/windoze95/cantinarr-server/internal/musicdiscovery"
	"strings"
	"time"
)

// CatalogRef is public metadata identity, never an arr-native identifier.
type CatalogRef struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type DeliveryState struct {
	RequestID     int64      `json:"request_id"`
	Format        string     `json:"format,omitempty"`
	State         string     `json:"state"`
	Attempts      int        `json:"attempts"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	Code          string     `json:"code,omitempty"`
	Message       string     `json:"message"`
	CanManage     bool       `json:"can_manage"`
	CanCancel     bool       `json:"can_cancel"`
}

const dispatchLease = 5 * time.Minute

func (s *Service) deliveryInstance(userID int64, mediaType, requested string) (string, error) {
	if mediaType == "book" {
		client, id, err := s.resolveChaptarr(userID, requested)
		if err != nil {
			return "", err
		}
		if client == nil {
			return "", fmt.Errorf("chaptarr is not configured for you")
		}
		return id, nil
	}
	if mediaType == "music" {
		client, id, err := s.resolveLidarr(userID, requested)
		if err != nil {
			return "", err
		}
		if client == nil {
			return "", fmt.Errorf("lidarr is not configured for you")
		}
		return id, nil
	}
	return "", fmt.Errorf("delivery is only supported for books and music")
}

func validateCatalogRef(mediaType string, ref *CatalogRef) error {
	if ref == nil {
		return nil
	}
	if mediaType == "book" && ref.Provider == "openlibrary" {
		if id := bookdiscovery.WorkID(ref.ID); id != "" {
			ref.ID = id
			return nil
		}
	}
	if mediaType == "music" && (ref.Provider == "musicbrainz" || ref.Provider == "musicbrainz_release") && musicdiscovery.ValidID(ref.ID) {
		return nil
	}
	return fmt.Errorf("invalid catalog reference for this media type")
}

func (s *Service) createCatalogRequest(userID int64, req *CreateRequest, eff effective) (*CreateResponse, error) {
	if req.MediaType == "book" && req.CatalogRef != nil {
		return nil, bookdiscovery.ErrRetired
	}
	if err := validateCatalogRef(req.MediaType, req.CatalogRef); err != nil {
		return nil, err
	}
	instanceID, err := s.deliveryInstance(userID, req.MediaType, strings.TrimSpace(req.InstanceID))
	if err != nil {
		return nil, err
	}
	provider, sourceID := "", ""
	if req.CatalogRef != nil {
		provider, sourceID = req.CatalogRef.Provider, req.CatalogRef.ID
	}
	// Music source callers cannot supply their own native mapping.
	foreignID := req.ForeignID
	if provider != "" {
		foreignID = ""
	}
	if req.Title == "" {
		if req.MediaType == "book" {
			return nil, fmt.Errorf("title is required to add a new book")
		}
		return nil, fmt.Errorf("title is required to add a new album")
	}
	formats := []string{""}
	if req.MediaType == "book" {
		formats = expandBookFormat(normalizeBookFormat(req.BookFormat))
	}
	lockKey := instanceID + "\x00" + provider + ":" + sourceID + ":" + foreignID
	if req.MediaType == "music" {
		lockKey = instanceID + "\x00music-intake"
	}
	lock := s.bookLock(lockKey)
	lock.Lock()
	ids, inserted, err := s.saveDelivery(userID, req, instanceID, foreignID, provider, sourceID, formats, eff.RequiresApproval)
	lock.Unlock()
	if err != nil {
		return nil, err
	}
	if inserted && eff.RequiresApproval && s.notifier != nil {
		s.notifier.NotifyAdmins("request_pending", map[string]interface{}{"media_type": req.MediaType, "title": req.Title, "instance_id": instanceID, "foreign_id": foreignID})
	}
	response, err := s.deliveryResponse(userID, ids, req.Title, instanceID, req.CatalogRef)
	if err == nil {
		savedDeliveryState(response)
	}
	s.wakeDispatch()

	return response, err
}

// saveDelivery atomically records intent, subscriptions and each format's job.
// A double submission shares pending work, including its approval decision.
func (s *Service) saveDelivery(userID int64, req *CreateRequest, instanceID, foreignID, provider, sourceID string, formats []string, approval bool) ([]int64, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	covered := map[string]int64{}
	legacy := map[int64]struct{ format, park string }{}
	query := `SELECT r.id,COALESCE(r.book_format,'both'),COALESCE(r.park_reason,''),EXISTS(SELECT 1 FROM request_dispatch d WHERE d.request_id=r.id) FROM request_log r
 WHERE r.media_type=? AND COALESCE(r.instance_id,'')=? AND (?!='' OR COALESCE(r.foreign_id,'')=?)
 AND COALESCE(r.catalog_provider,'')=? AND COALESCE(r.catalog_id,'')=? AND r.status='pending'
 AND (r.media_type='book' OR r.user_id=?) ORDER BY r.id`
	args := []any{req.MediaType, instanceID, provider, foreignID, provider, sourceID, userID}
	if req.MediaType == "music" {
		where, identityArgs, e := musicIdentityWhere(tx, instanceID, foreignID, provider, sourceID)
		if e != nil {
			return nil, false, e
		}
		query = `SELECT r.id,COALESCE(r.book_format,'both'),COALESCE(r.park_reason,''),EXISTS(SELECT 1 FROM request_dispatch d WHERE d.request_id=r.id) FROM request_log r WHERE r.media_type='music' AND r.instance_id=? AND r.user_id=? AND r.status='pending' AND ` + where + ` ORDER BY r.id`
		args = append([]any{instanceID, userID}, identityArgs...)
	}
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	for rows.Next() {
		var id int64
		var format, park string
		var dispatched bool
		if err = rows.Scan(&id, &format, &park, &dispatched); err != nil {
			rows.Close()
			return nil, false, err
		}
		if !dispatched {
			legacy[id] = struct{ format, park string }{format, park}
		}
		if req.MediaType == "book" {
			for _, f := range expandBookFormat(format) {
				covered[f] = id
			}
		} else {
			covered[""] = id
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	for id, row := range legacy {
		// Revisited pre-upgrade requests retain their approval/import gate.
		state := "approval"
		if row.park == bookParkReasonAuthorImport {
			state = "waiting_library"
		}
		formats := []string{""}
		if req.MediaType == "book" {
			formats = expandBookFormat(row.format)
		}
		for _, format := range formats {
			if _, err = tx.Exec(`INSERT INTO request_dispatch(request_id,format,state) VALUES (?,?,?)`, id, format, state); err != nil {
				return nil, false, err
			}
		}
	}
	missing := []string{}
	for _, f := range formats {
		if covered[f] == 0 {
			missing = append(missing, f)
		}
	}
	inserted := len(missing) > 0
	var createdID int64
	if inserted {
		format := missing[0]
		if len(missing) == 2 {
			format = BookFormatBoth
		}
		park, state := "delivery", "queued"
		if approval {
			park, state = "", "approval"
		}
		res, e := tx.Exec(`INSERT INTO request_log(user_id,tmdb_id,foreign_id,instance_id,media_type,title,book_format,status,search_term,park_reason,catalog_provider,catalog_id)
		 VALUES (?,0,?,?,?,?,?,'pending',?,?,?,?)`, userID, sqlNullStr(foreignID), instanceID, req.MediaType, req.Title, sqlNullStr(format), sqlNullStr(req.SearchTerm), sqlNullStr(park), sqlNullStr(provider), sqlNullStr(sourceID))
		if e != nil {
			return nil, false, e
		}
		id, e := res.LastInsertId()
		if e != nil {
			return nil, false, e
		}
		createdID = id
		for _, f := range missing {
			if _, e = tx.Exec(`INSERT INTO request_dispatch(request_id,format,state) VALUES (?,?,?)`, id, f, state); e != nil {
				return nil, false, e
			}
			covered[f] = id
		}
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, f := range formats {
		id := covered[f]
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
		if req.MediaType == "book" {
			_, err = tx.Exec(`INSERT INTO book_request_waiters(request_id,user_id,book_format) VALUES (?,?,?)
		 ON CONFLICT(request_id,user_id) DO UPDATE SET book_format=CASE WHEN book_request_waiters.book_format=excluded.book_format THEN excluded.book_format ELSE 'both' END`, id, userID, f)
			if err != nil {
				return nil, false, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	s.notifyCreated(createdID, approval)
	return ids, inserted, nil
}
