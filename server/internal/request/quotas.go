package request

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/requestquota"
)

// One SQL snapshot captures the authority used during normalization. Recheck
// under the write lock so role, grants, content policy and option changes
// during provider reads cannot turn stale authority into accepted work.
func requestAuthority(q requestquota.Queryer, userID int64) (string, error) {
	var raw string
	err := q.QueryRow(`SELECT json_array(u.role,
 COALESCE((SELECT value FROM settings WHERE key='request_settings'),''),
 (SELECT json_array(require_approval,allow_season_choice,season_scope_override,allow_quality_choice,quality_profile_radarr,quality_profile_sonarr) FROM user_request_settings WHERE user_id=u.id),
 (SELECT json_array(max_movie_rating,max_tv_rating,rating_region,block_unrated,blocked_movie_genres,blocked_tv_genres) FROM user_content_policies WHERE user_id=u.id),
 (SELECT json_group_array(instance_id) FROM (SELECT instance_id FROM user_instance_grants WHERE user_id=u.id ORDER BY instance_id)),
 (SELECT json_group_array(json_array(service_type,instance_id)) FROM (SELECT service_type,instance_id FROM user_default_instances WHERE user_id=u.id ORDER BY service_type)),
 (SELECT json_group_array(json_array(id,service_type,is_default,sort_order,name)) FROM (SELECT id,service_type,is_default,sort_order,name FROM service_instances ORDER BY id))) FROM users u WHERE u.id=?`, userID).Scan(&raw)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(raw))), nil
}

func (s *Service) beginRequest(userID int64, authority string) (*sql.Tx, error) {
	tx, err := requestquota.Begin(s.db)
	if err != nil {
		return nil, err
	}
	current, err := requestAuthority(tx, userID)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	if authority != "" && current != authority {
		tx.Rollback()
		return nil, errors.New("request permissions or settings changed; refresh and try again")
	}
	return tx, nil
}

func (s *Service) PreviewRequest(userID int64, req *CreateRequest) (*requestquota.Preview, error) {
	copy := *req
	copy.previewOnly = true
	out, err := s.CreateMediaRequest(userID, &copy)
	if err != nil {
		return nil, err
	}
	return out.QuotaPreview, nil
}

func (s *Service) quotaChanged(userID int64) {
	if s.notifier != nil {
		payload := map[string]interface{}{"user_id": userID}
		s.notifier.NotifyUser(userID, "request_quota_changed", payload)
		s.notifier.NotifyAdmins("request_quota_changed", payload)
	}
}

func (s *Service) quotaRequestChanged(id int64) {
	rows, err := s.db.Query(`SELECT DISTINCT user_id FROM request_quota_items WHERE request_id=?`, id)
	if err != nil {
		return
	}
	ids := []int64{}
	for rows.Next() {
		var uid int64
		if rows.Scan(&uid) != nil {
			rows.Close()
			return
		}
		ids = append(ids, uid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	for _, uid := range ids {
		s.quotaChanged(uid)
	}
}

// Called immediately before a library write, never before a read. Lease and
// request checks plus the permanent marker share one durable transaction.
func (s *Service) startDelivery(ctx context.Context, id int64, format, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := requestquota.Begin(s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.Quotas.Now().UTC()
	leaseNow := time.Now().Unix()
	res, err := tx.Exec(`UPDATE request_dispatch SET delivery_started_at=CASE WHEN delivery_started_at=0 THEN ? ELSE delivery_started_at END WHERE request_id=? AND format=? AND lease_token=? AND state='processing' AND lease_until>? AND EXISTS(SELECT 1 FROM request_log r JOIN request_dispatch_locks l ON l.instance_id=r.instance_id WHERE r.id=? AND r.status='pending' AND r.park_reason='delivery' AND l.lease_token=? AND l.lease_until>?)`, now.UnixNano(), id, format, token, leaseNow, id, token, leaseNow)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("delivery lease no longer held")
	}
	_, err = tx.Exec(`UPDATE request_quota_items SET delivery_started_at=CASE WHEN delivery_started_at=0 THEN ? ELSE delivery_started_at END WHERE request_id=? AND book_format=? AND released_at IS NULL`, now.UnixNano(), id, format)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) refundNoOp(tx *sql.Tx, id int64, format string) error {
	var started int64
	if err := tx.QueryRow(`SELECT delivery_started_at FROM request_dispatch WHERE request_id=? AND format=?`, id, format).Scan(&started); err != nil {
		return err
	}
	if started != 0 {
		return nil
	}
	return s.Quotas.Release(tx, id, 0, format, "", "no_op")
}

func movieUnit(r *resolvedRequest, id int64, free bool) requestquota.Unit {
	return requestquota.Unit{Key: requestquota.Key{MediaType: "movie"}, RequestID: id, InstanceID: r.instanceID, UnitKey: fmt.Sprint(r.tmdbID), Free: free}
}

func (s *Service) tvUnits(r *resolvedRequest, target *TVRequestTarget, id int64) []requestquota.Unit {
	out := []requestquota.Unit{}
	work := "season"
	if target.Pilot {
		work = "pilot"
	}
	for _, n := range target.SourceSeasons {
		out = append(out, requestquota.Unit{Key: requestquota.Key{MediaType: "tv"}, RequestID: id, InstanceID: r.instanceID, UnitKey: fmt.Sprintf("%d:%d", r.tmdbID, n), Work: work, Free: target.freeSeasons[n]})
	}
	return out
}

// Catalog identities are normalized once by the same intake used by preview
// and submission. Accepted native bindings are the only alias evidence.
func catalogQuotaKey(mediaType, foreignID, provider, sourceID string) string {
	if mediaType == "music" && provider == "musicbrainz" {
		return sourceID
	}
	if provider != "" {
		b, _ := json.Marshal([]string{provider, sourceID})
		return string(b)
	}
	return foreignID
}

// Try a bounded, read-only preflight. An unavailable/slow library reserves the
// selected units and the worker can later refund a proven no-op. This keeps
// durable catalog intake responsive while letting known available work cost 0.
func (s *Service) prepareCatalogQuota(userID int64, req *CreateRequest, instanceID, foreignID, provider, sourceID string) {
	req.quotaKey = catalogQuotaKey(req.MediaType, foreignID, provider, sourceID)
	req.quotaFree = map[string]string{}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if req.MediaType == "book" {
		client, _, err := s.resolveChaptarr(userID, instanceID)
		if err != nil || client == nil {
			return
		}
		client = client.WithContext(ctx)
		books, err := client.GetAllBooks()
		if err != nil || len(books) == 0 {
			return
		}
		canonical := foreignID
		_, records, unresolved := recordsForForeignID(books, canonical)
		if unresolved {
			return
		}
		if len(records) == 0 {
			index := chaptarr.IndexBookIdentities(books)
			bound, err := index.Resolve(foreignID, nil)
			if err != nil {
				return
			}
			if bound != "" {
				canonical = bound
			} else {
				selected, e := lookupBookForAdd(client.LookupBook, foreignID, req.Title, req.SearchTerm)
				if e != nil || selected == nil {
					return
				}
				bound, err = index.Resolve(foreignID, selected.IdentityKeys())
				if err != nil || bound == "" {
					return
				}
				canonical = bound
			}
			_, records, unresolved = recordsForForeignID(books, canonical)
			if unresolved {
				return
			}
		}
		req.quotaKey = canonical
		for _, f := range expandBookFormat(normalizeBookFormat(req.BookFormat)) {
			for _, book := range records[f] {
				if book.Statistics.BookFileCount > 0 {
					req.quotaFree[f] = StatusAvailable
					break
				}
				if book.Monitored {
					req.quotaFree[f] = StatusRequested
				}
			}
		}
	} else {
		client, _, err := s.resolveLidarr(userID, instanceID)
		if err != nil || client == nil {
			return
		}
		key := foreignID
		if provider == "musicbrainz" {
			key = sourceID
		}
		if key == "" {
			return
		}
		albums, err := client.WithContext(ctx).GetAllAlbums()
		if err != nil {
			return
		}
		for _, a := range albums {
			if a.ForeignAlbumID == key {
				if albumComplete(a) {
					req.quotaFree[""] = StatusAvailable
					break
				}
				if a.Monitored {
					req.quotaFree[""] = StatusRequested
				}
			}
		}
	}
}
