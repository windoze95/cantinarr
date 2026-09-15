package request

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
)

// migrateTVApproval is lazy: upgrading does not execute or approve old work.
func (s *Service) migrateTVApproval(id int64, r *resolvedRequest) error {
	target, err := s.prepareTVTarget(r)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(target)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE request_log SET instance_id=?,tvdb_id=? WHERE id=? AND status='pending' AND park_reason IS NULL`, r.instanceID, target.Match.TVDBID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("request is not waiting for approval")
	}
	if _, err = tx.Exec(`INSERT INTO request_tv_targets(request_id,snapshot) VALUES (?,?) ON CONFLICT(request_id) DO NOTHING`, id, string(raw)); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO request_dispatch(request_id,format,state) VALUES (?,'','approval') ON CONFLICT(request_id,format) DO NOTHING`, id); err != nil {
		return err
	}
	return tx.Commit()
}

type tvApproval struct{ previous, next *TVRequestTarget }

// Provider reads finish before the approval transaction begins.
func (s *Service) reviewTVApproval(id int64, r *resolvedRequest, override *DecisionOverride) (*tvApproval, error) {
	target, _, _, err := s.loadTVTarget(id)
	if err != nil {
		return nil, err
	}
	client, _, err := s.resolveSonarr(r.userID, r.instanceID)
	if err != nil {
		return nil, err
	}
	current, err := s.resolveTVMatch(client, r.tmdbID)
	if err != nil {
		return nil, err
	}
	if current.Revision != target.Match.Revision {
		return nil, ErrTVMatchStale
	}
	if override == nil {
		return &tvApproval{target, target}, nil
	}
	if override.SeasonScope != "" {
		if !validSeasonScope(override.SeasonScope) {
			return nil, errors.New("invalid season scope")
		}
		r.seasonScope = override.SeasonScope
		r.seasonNumbers = nil
	}
	if len(override.Seasons) > 0 {
		for _, n := range override.Seasons {
			if n <= 0 {
				return nil, tvMatchFailure("tv_seasons_unmapped")
			}
		}
		r.seasonNumbers = normalizeSeasonNumbers(override.Seasons)
		r.seasonScope = encodeSeasonNumbers(r.seasonNumbers)
	}
	if override.QualityProfileID != 0 {
		r.qualityProfileID = override.QualityProfileID
	}
	if override.SeasonScope == "" && len(override.Seasons) == 0 {
		return &tvApproval{target, target}, nil
	}
	next, err := s.prepareTVTarget(r)
	if err != nil {
		return nil, err
	}
	if next.Match.Revision != target.Match.Revision {
		return nil, ErrTVMatchStale
	}
	return &tvApproval{target, next}, nil
}

func (s *Service) applyTVApproval(tx *sql.Tx, id int64, r *resolvedRequest, review *tvApproval) error {
	previous, _ := json.Marshal(review.previous)
	next, _ := json.Marshal(review.next)
	res, err := tx.Exec(`UPDATE request_tv_targets SET snapshot=? WHERE request_id=? AND snapshot=?`, string(next), id, string(previous))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrTVMatchStale
	}
	var repair bool
	var itemCount int
	if err = tx.QueryRow(`SELECT repair_of IS NOT NULL FROM request_tv_targets WHERE request_id=?`, id).Scan(&repair); err != nil {
		return err
	}
	if err = tx.QueryRow(`SELECT COUNT(*) FROM request_quota_items WHERE request_id=?`, id).Scan(&itemCount); err != nil {
		return err
	}
	for _, n := range review.previous.SourceSeasons {
		if !slices.Contains(review.next.SourceSeasons, n) {
			if err = s.Quotas.Release(tx, id, 0, "", fmt.Sprintf("%d:%d", r.tmdbID, n), "approval_reduced"); err != nil {
				return err
			}
		}
	}
	units := s.tvUnits(r, review.next, id)
	for i := range units {
		// Unchanged legacy approvals and corrective repairs are free.
		if repair || (itemCount == 0 && slices.Contains(review.previous.SourceSeasons, review.next.SourceSeasons[i])) {
			units[i].Free = true
		}
	}
	if _, err = s.Quotas.Accept(tx, r.userID, units); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE request_log SET season_scope=?,quality_profile_id=? WHERE id=?`, r.seasonScope, sqlNullInt(r.qualityProfileID), id)
	return err
}

type TVRepairPreview struct {
	RequestID           int64            `json:"request_id"`
	Title               string           `json:"title"`
	Status              string           `json:"status"`
	InstanceID          string           `json:"instance_id"`
	InstanceName        string           `json:"instance_name"`
	RecordedTarget      *TVRequestTarget `json:"recorded_target,omitempty"`
	RecordedTVDBID      int              `json:"recorded_tvdb_id,omitempty"`
	RecordedTargetKnown bool             `json:"recorded_target_known"`
	IntendedTarget      *TVRequestTarget `json:"intended_target,omitempty"`
	Revision            string           `json:"revision"`
	RepairRequestID     int64            `json:"repair_request_id,omitempty"`
	RequiresApproval    bool             `json:"requires_approval"`
	CanRepair           bool             `json:"can_repair"`
	Message             string           `json:"message"`
}

func (s *Service) tvRepairPreview(adminID, id int64) (*TVRepairPreview, error) {
	if !s.userIsAdmin(adminID) {
		return nil, ErrTVMatchAdmin
	}
	r, status, err := s.loadRequest(id)
	if err != nil {
		return nil, err
	}
	if r.mediaType != "tv" {
		return nil, errors.New("choose a TV request")
	}
	p := &TVRepairPreview{RequestID: id, Title: r.title, Status: status, InstanceID: r.instanceID, RecordedTVDBID: r.tvdbID, Message: "The previous target and files will be preserved. This creates a linked corrective request."}
	_ = s.db.QueryRow(`SELECT name FROM service_instances WHERE id=?`, r.instanceID).Scan(&p.InstanceName)
	_ = s.db.QueryRow(`SELECT request_id FROM request_tv_targets WHERE repair_of=?`, id).Scan(&p.RepairRequestID)
	if target, _, _, e := s.loadTVTarget(id); e == nil {
		p.RecordedTarget, p.RecordedTargetKnown = target, true
	} else if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	} else {
		p.Message = "Legacy history cannot prove the original series or season scope. Any recorded TVDB ID is unverified. The previous target and files will be preserved."
	}
	if status == StatusDenied {
		p.Message = "Denied requests are not revived by a TV correction."
		return p, nil
	}
	if r.instanceID == "" {
		p.Message = "The original library was not recorded. Choose a library and submit a new request from the title page."
		return p, nil
	}
	eff, err := s.effectiveSettings(r.userID, s.userIsAdmin(r.userID))
	if err != nil {
		return nil, err
	}
	p.RequiresApproval = eff.RequiresApproval || (status == StatusPending && r.parkReason == "")
	if err = s.checkContentPolicy(r.userID, s.userIsAdmin(r.userID), "tv", r.tmdbID); err != nil {
		p.Message = err.Error()
		return p, nil
	}
	target, err := s.prepareTVTarget(r)
	if err != nil {
		p.Message = err.Error()
		return p, nil
	}
	p.IntendedTarget, p.CanRepair = target, true
	fingerprint, _ := json.Marshal([]any{id, r.userID, r.tmdbID, r.instanceID, status, r.parkReason, r.seasonScope, r.qualityProfileID, target})
	p.Revision = fmt.Sprintf("%x", sha256.Sum256(fingerprint))[:24]
	return p, nil
}

func (s *Service) TVRepairPreviews(adminID int64, tmdbID int) ([]TVRepairPreview, error) {
	if !s.userIsAdmin(adminID) {
		return nil, ErrTVMatchAdmin
	}
	rows, err := s.db.Query(`SELECT r.id FROM request_log r WHERE r.media_type='tv' AND r.tmdb_id=? AND r.status!='denied' AND NOT EXISTS(SELECT 1 FROM request_tv_targets t WHERE t.request_id=r.id AND t.repair_of IS NOT NULL) ORDER BY r.id DESC LIMIT 200`, tmdbID)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []TVRepairPreview{}
	for _, id := range ids {
		p, e := s.tvRepairPreview(adminID, id)
		if e != nil {
			return nil, e
		}
		if p.RecordedTargetKnown && p.IntendedTarget != nil {
			if p.RecordedTarget.Match.Revision == p.IntendedTarget.Match.Revision ||
				(p.Status != StatusPending && sameTVDeliveryScope(p.RecordedTarget, p.IntendedTarget)) {
				continue
			}
		}
		out = append(out, *p)
	}
	return out, nil
}

// A completed delivery does not need repeating merely because an admin has
// reviewed the same target. Queued work still needs its revision reviewed.
func sameTVDeliveryScope(a, b *TVRequestTarget) bool {
	if a.Match.TVDBID != b.Match.TVDBID || a.Pilot != b.Pilot {
		return false
	}
	left, right := slices.Clone(a.TargetSeasons), slices.Clone(b.TargetSeasons)
	sort.Ints(left)
	sort.Ints(right)
	return slices.Equal(left, right)
}

func (s *Service) RepairTVMatch(adminID, id int64, revision string) (*CreateResponse, error) {
	if !s.userIsAdmin(adminID) {
		return nil, ErrTVMatchAdmin
	}
	s.tvMatchMu.Lock()
	defer s.tvMatchMu.Unlock()
	p, err := s.tvRepairPreview(adminID, id)
	if err != nil {
		return nil, err
	}
	if revision == "" || revision != p.Revision {
		return nil, ErrTVMatchStale
	}
	if p.RepairRequestID > 0 {
		return s.tvDeliveryResponse(adminID, p.RepairRequestID)
	}
	if !p.CanRepair {
		return nil, fmt.Errorf("cannot repair this request: %s", p.Message)
	}
	r, _, err := s.loadRequest(id)
	if err != nil {
		return nil, err
	}
	r.tvdbID = p.IntendedTarget.Match.TVDBID
	// History keeps the original title, including any legacy display text.
	repair, inserted, err := s.saveTVDelivery(r, p.IntendedTarget, p.RequiresApproval, id, adminID)
	if err != nil {
		return nil, err
	}
	if inserted {
		s.notifyCreated(repair, p.RequiresApproval)
	}
	s.wakeDispatch()
	return s.tvDeliveryResponse(adminID, repair)
}
