package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/contentnotify"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// RequestedContentRecipients intersects positive import evidence with accepted
// saved work. The caller supplies an already opted-in library audience; this
// method rechecks current access after provider reads and never grants access.
func (s *Service) RequestedContentRecipients(ctx context.Context, event contentnotify.Import, candidates []int64) ([]int64, error) {
	if len(candidates) == 0 || event.InstanceID == "" || (event.MediaType == "tv" && len(event.EpisodeIDs) == 0) {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.user_id,COALESCE(r.book_format,'') FROM request_log r
 WHERE r.instance_id=? AND r.media_type=? AND r.status NOT IN ('pending','denied')
 AND (? NOT IN ('movie','tv') OR r.tmdb_id=?)
 AND (r.user_id IN (SELECT CAST(value AS INTEGER) FROM json_each(?)) OR EXISTS
 (SELECT 1 FROM book_request_waiters w WHERE w.request_id=r.id AND w.user_id IN (SELECT CAST(value AS INTEGER) FROM json_each(?))))`,
		event.InstanceID, event.MediaType, event.MediaType, event.TmdbID, jsonIDs(candidates), jsonIDs(candidates))
	if err != nil {
		return nil, err
	}
	type saved struct {
		id, owner int64
		format    string
	}
	var requests []saved
	for rows.Next() {
		var r saved
		if err = rows.Scan(&r.id, &r.owner, &r.format); err != nil {
			break
		}
		requests = append(requests, r)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}
	matched := map[int64][]int64{}
	var failures []error
	read := &contentAvailabilityRead{freshLibrary: true, freshEpisodes: true}
	for _, r := range requests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		live, err := s.requestContentAvailability(ctx, r.id, read)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !importMatchesAvailability(event, live) {
			continue
		}
		// Request decisions/ownership can change while provider I/O is in flight.
		current, status, err := s.loadRequest(r.id)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if status == StatusDenied || status == StatusPending || current.instanceID != event.InstanceID || current.mediaType != event.MediaType || ((event.MediaType == "movie" || event.MediaType == "tv") && current.tmdbID != event.TmdbID) {
			continue
		}
		states, err := s.deliveryStates(r.id)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		accepted := len(states) == 0
		for _, d := range states {
			if d.State == "complete" && (event.MediaType != "book" || d.Format == "" || d.Format == event.Format) {
				accepted = true
			}
		}
		if !accepted {
			continue
		}
		owners := map[int64]string{current.userID: current.bookFormat}
		if event.MediaType == "book" {
			rows, err := s.db.QueryContext(ctx, `SELECT user_id,book_format FROM book_request_waiters WHERE request_id=?`, r.id)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var user int64
				var format string
				if err = rows.Scan(&user, &format); err != nil {
					break
				}
				owners[user] = format
			}
			readErr := rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			if readErr != nil {
				return nil, readErr
			}
		}
		for user, format := range owners {
			scoped := event
			if ids, ok := event.RecipientEpisodeIDs[user]; ok {
				scoped.EpisodeIDs = ids
			}
			if !importMatchesAvailability(scoped, live) {
				continue
			}
			if !slices.Contains(candidates, user) || (event.MediaType == "book" && format != "" && format != "both" && format != event.Format) {
				continue
			}
			allowed, err := s.authorizeContentRecipient(ctx, user, live.Subject)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if allowed {
				matched[user] = append(matched[user], r.id)
			}
		}
	}
	out := []int64{}
	for _, user := range candidates {
		for _, id := range matched[user] {
			allowed, err := s.currentRequestRecipient(ctx, id, user, event)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if allowed {
				out = append(out, user)
				break
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, errors.Join(failures...)
}

func jsonIDs(ids []int64) string { raw, _ := json.Marshal(ids); return string(raw) }

func importMatchesAvailability(event contentnotify.Import, live contentnotify.Availability) bool {
	if live.Subject.InstanceID != event.InstanceID || live.Subject.MediaType != event.MediaType {
		return false
	}
	switch event.MediaType {
	case "movie":
		return event.TmdbID > 0 && live.Subject.TmdbID == event.TmdbID && len(live.Units) > 0
	case "tv":
		if event.TmdbID <= 0 || live.Subject.TmdbID != event.TmdbID {
			return false
		}
		for _, unit := range live.Units {
			parts := strings.Split(unit.Key, ":")
			if len(parts) != 3 || parts[0] != "episode" {
				continue
			}
			id, err := strconv.Atoi(parts[2])
			if err == nil && id > 0 && slices.Contains(event.EpisodeIDs, id) {
				return true
			}
		}
	case "book", "music":
		if event.ForeignID == "" {
			return false
		}
		for _, unit := range live.Units {
			foreignID := unit.ForeignID
			if foreignID == "" {
				foreignID = live.Subject.ForeignID
			}
			if foreignID == event.ForeignID && (event.MediaType == "music" || (event.Format != "" && unit.Format == event.Format)) {
				return true
			}
		}
	}
	return false
}

// A missing-only follow names exact canonical episodes. Excluded coverage,
// queued future work and uncertain searches do not become requests for every
// file in the season. Stopping following retains work already submitted.
func (s *Service) requestedTVEpisodeIDs(id int64, target *TVRequestTarget, seriesID int, episodes []sonarr.Episode) (map[int]bool, error) {
	if target.EpisodeIntent == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT identity,episode_id FROM request_tv_follow_episodes WHERE request_id=? AND state IN ('submitted','available')`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	selected := map[int]bool{}
	for rows.Next() {
		var raw string
		var savedID int
		var canonical CanonicalEpisode
		if err = rows.Scan(&raw, &savedID); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &canonical); err != nil {
			return nil, err
		}
		native, err := resolveCanonicalEpisode(canonical, seriesID, episodes)
		if err != nil {
			return nil, err
		}
		if native == nil {
			continue
		}
		if savedID > 0 && savedID != native.ID {
			return nil, fmt.Errorf("requested episode identity changed: %w", ErrTVMatchStale)
		}
		selected[native.ID] = true
	}
	return selected, rows.Err()
}

// Final database-only pass after every provider/content-policy read. A later
// request in the batch cannot preserve an earlier revoked subscription.
func (s *Service) currentRequestRecipient(ctx context.Context, id, user int64, event contentnotify.Import) (bool, error) {
	var allowed int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_log r WHERE r.id=? AND r.instance_id=? AND r.media_type=? AND r.status NOT IN ('pending','denied')
 AND (? NOT IN ('movie','tv') OR r.tmdb_id=?)
 AND ((r.user_id=? AND (?!='book' OR (COALESCE(r.book_format,'') IN ('','both',?) AND NOT EXISTS(SELECT 1 FROM book_request_waiters owner_waiter WHERE owner_waiter.request_id=r.id AND owner_waiter.user_id=r.user_id)))) OR (?='book' AND EXISTS(SELECT 1 FROM book_request_waiters w WHERE w.request_id=r.id AND w.user_id=? AND w.book_format IN ('','both',?))))
 AND (NOT EXISTS(SELECT 1 FROM request_dispatch d WHERE d.request_id=r.id) OR EXISTS(SELECT 1 FROM request_dispatch d WHERE d.request_id=r.id AND d.state='complete' AND (?!='book' OR d.format IN ('',?))))`, id, event.InstanceID, event.MediaType, event.MediaType, event.TmdbID, user, event.MediaType, event.Format, event.MediaType, user, event.Format, event.MediaType, event.Format).Scan(&allowed)
	return allowed > 0, err
}
