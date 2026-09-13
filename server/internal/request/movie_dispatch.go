package request

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/requestquota"
)

func (s *Service) createMovieRequest(r *resolvedRequest, approval bool) (*CreateResponse, error) {
	if r.tmdbID <= 0 {
		return nil, errors.New("tmdb_id is required for movie requests")
	}
	client, _, err := s.resolveRadarr(r.userID, r.instanceID)
	if err != nil {
		return nil, err
	}
	if client == nil && !approval {
		return nil, errors.New("radarr is not configured")
	}
	free, liveStatus := false, StatusRequested
	// An unavailable read can reserve a unit, but can never waive its charge.
	// Delivery retries this read before any write, recovering a lost response.
	if client != nil {
		if existing, e := client.GetMovieByTMDB(r.tmdbID); e == nil && existing != nil && existing.TmdbID == r.tmdbID {
			free = existing.HasFile || existing.Monitored
			if existing.HasFile {
				liveStatus = StatusAvailable
			}
			r.title = existing.Title
		}
	}
	tx, err := s.beginRequest(r.userID, r.authority)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	dupGuard := "instance_id=?"
	if r.instanceIsUserDefault || r.instanceID == "" {
		dupGuard = "(instance_id=? OR instance_id IS NULL)"
	}
	err = tx.QueryRow(`SELECT id FROM request_log WHERE user_id=? AND media_type='movie' AND tmdb_id=? AND (status='pending' OR (? AND status!='denied')) AND `+dupGuard+` ORDER BY id DESC LIMIT 1`, r.userID, r.tmdbID, free, r.instanceID).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	inserted := id == 0
	u := movieUnit(r, id, free)
	if !inserted {
		// Pre-upgrade intent is already accepted; no historical backfill.
		var items int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM request_quota_items WHERE request_id=?`, id).Scan(&items); err != nil {
			return nil, err
		}
		if items == 0 {
			u.Free = true
		}
	}
	preview, _, err := s.Quotas.Price(tx, r.userID, []requestquota.Unit{u})
	if err != nil {
		return nil, err
	}
	if r.previewOnly {
		return &CreateResponse{QuotaPreview: preview}, nil
	}
	if e := preview.Failure(); e != nil {
		return nil, e
	}
	if inserted {
		status, park, state := StatusPending, "delivery", "queued"
		if approval {
			park, state = "", "approval"
		}
		if free {
			status, park, state = liveStatus, "", "complete"
		}
		res, e := tx.Exec(`INSERT INTO request_log(user_id,tmdb_id,instance_id,media_type,title,status,quality_profile_id,park_reason) VALUES (?,?,?,'movie',?,?,?,?)`, r.userID, r.tmdbID, sqlNullStr(r.instanceID), r.title, status, sqlNullInt(r.qualityProfileID), sqlNullStr(park))
		if e != nil {
			return nil, e
		}
		id, err = res.LastInsertId()
		if err != nil {
			return nil, err
		}
		code := ""
		if free {
			code = liveStatus
		}
		if _, err = tx.Exec(`INSERT INTO request_dispatch(request_id,format,state,code) VALUES (?,'',?,?)`, id, state, code); err != nil {
			return nil, err
		}
	} else {
		// Revisited legacy approvals retain their gate and pinned destination.
		if _, err = tx.Exec(`UPDATE request_log SET instance_id=COALESCE(instance_id,?) WHERE id=?`, sqlNullStr(r.instanceID), id); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`INSERT INTO request_dispatch(request_id,format,state) VALUES (?,'','approval') ON CONFLICT(request_id,format) DO NOTHING`, id); err != nil {
			return nil, err
		}
	}
	u.RequestID = id
	if _, err = s.Quotas.Accept(tx, r.userID, []requestquota.Unit{u}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.quotaChanged(r.userID)
	if inserted {
		s.notifyCreated(id, approval && !free)
		if approval && !free && s.notifier != nil {
			pending, _ := s.ListPending()
			s.notifier.NotifyAdmins("request_pending", map[string]interface{}{"request_id": id, "tmdb_id": r.tmdbID, "media_type": "movie", "title": r.title, "instance_id": r.instanceID, "pending_count": len(pending)})
		}
	}
	if !approval && !free {
		s.dispatchRequest(context.Background(), id)
		s.wakeDispatch()
	}
	return s.deliveryResponse(r.userID, []int64{id}, r.title, r.instanceID, nil)
}

func (s *Service) dispatchMovie(ctx context.Context, id int64, token string, r *resolvedRequest) {
	r.beforeMutation = func() error {
		actor := r.userID
		if r.actorID != 0 && s.userIsAdmin(r.actorID) {
			actor = r.actorID
		}
		if _, _, err := s.resolveRadarr(actor, r.instanceID); err != nil {
			return err
		}
		if err := s.checkContentPolicy(r.userID, s.userIsAdmin(r.userID), "movie", r.tmdbID); err != nil {
			return err
		}
		return s.startDelivery(ctx, id, "", token)
	}
	if err := s.checkContentPolicy(r.userID, s.userIsAdmin(r.userID), "movie", r.tmdbID); err != nil {
		s.finishDelivery(id, "", token, "attention", "access_unavailable", nil)
		return
	}
	status, title, err := s.addMovie(r)
	if err != nil {
		s.finishDelivery(id, "", token, "retry", "library_unavailable", err)
		return
	}
	if _, err = s.db.Exec(`UPDATE request_log SET title=? WHERE id=? AND status='pending'`, title, id); err != nil {
		return
	}
	s.finishDelivery(id, "", token, "complete", status, nil)
}

func (s *Service) migrateMovieApproval(id int64, r *resolvedRequest) error {
	_, instanceID, err := s.resolveRadarr(r.userID, r.instanceID)
	if err != nil {
		return err
	}
	if instanceID == "" {
		return fmt.Errorf("radarr is not configured")
	}
	tx, err := requestquota.Begin(s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE request_log SET instance_id=? WHERE id=? AND status='pending' AND park_reason IS NULL`, instanceID, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO request_dispatch(request_id,format,state) SELECT id,'','approval' FROM request_log WHERE id=? AND status='pending' AND park_reason IS NULL ON CONFLICT(request_id,format) DO NOTHING`, id); err != nil {
		return err
	}
	return tx.Commit()
}
