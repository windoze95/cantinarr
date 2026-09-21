package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

// savedRequest contains only persisted identities and scopes, never titles or
// requester identities. Dispatch identities follow provider re-keying without
// matching a display name to a different record.
type savedRequest struct {
	Media, Instance, Foreign, Canonical, Format, DeliveryFormat, Seasons, Target string
	TMDB, TVDB, Native, DeliveryNative, Series                                   int
}

func (s *ActivityService) savedRequests(ctx context.Context, userID int64) ([]savedRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.media_type,COALESCE(r.instance_id,''),r.tmdb_id,COALESCE(r.tvdb_id,0),COALESCE(r.foreign_id,''),COALESCE(r.book_record_id,0),
 CASE WHEN r.user_id=? THEN COALESCE(r.book_format,'both') ELSE w.book_format END,
 COALESCE(r.season_scope,''),COALESCE(t.snapshot,''),COALESCE(t.series_id,0),
 COALESCE(d.canonical_foreign_id,''),COALESCE(d.book_record_id,0),COALESCE(d.format,'')
 FROM request_log r LEFT JOIN book_request_waiters w ON w.request_id=r.id AND w.user_id=?
 LEFT JOIN request_tv_targets t ON t.request_id=r.id LEFT JOIN request_dispatch d ON d.request_id=r.id
 WHERE (r.user_id=? OR (r.media_type='book' AND w.user_id=?)) AND r.status NOT IN ('denied','cancelled')
 ORDER BY r.id,d.format`, userID, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []savedRequest{}
	for rows.Next() {
		var r savedRequest
		if err := rows.Scan(&r.Media, &r.Instance, &r.TMDB, &r.TVDB, &r.Foreign, &r.Native, &r.Format, &r.Seasons, &r.Target, &r.Series, &r.Canonical, &r.DeliveryNative, &r.DeliveryFormat); err != nil {
			return nil, err
		}
		if r.Media == "tv" && r.Target != "" && !json.Valid([]byte(r.Target)) {
			return nil, errors.New("saved TV request scope unavailable")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func bookFormat(p record) string {
	switch strings.ToLower(p.str("mediaType")) {
	case "ebook", "audiobook":
		return strings.ToLower(p.str("mediaType"))
	}
	return ""
}

// A pack with no confirmed episodes cannot establish whether it contains a
// selected season/pilot. That is an unreadable My requests scope, not zero.
func uncertainRequestScope(requests []savedRequest, inst instance.Instance, row activityRow) bool {
	for _, r := range requests {
		if r.Instance != inst.ID {
			continue
		}
		if r.Media == "tv" && inst.ServiceType == "sonarr" {
			var target struct {
				Match struct {
					TVDB int `json:"tvdb_id"`
				} `json:"match"`
			}
			_ = json.Unmarshal([]byte(r.Target), &target)
			if (r.TVDB > 0 && r.TVDB == row.parent.num("tvdbId")) || (target.Match.TVDB > 0 && target.Match.TVDB == row.parent.num("tvdbId")) {
				if len(row.children) == 0 || (r.Target == "" && r.Seasons != "all") {
					return true
				}
			}
		}
		if r.Media == "book" && inst.ServiceType == "chaptarr" && bookFormat(row.parent) == "" && bookIdentityMatches(r, row.parent) {
			return true
		}
	}
	return false
}

func bookIdentityMatches(r savedRequest, parent record) bool {
	if r.DeliveryNative > 0 {
		return r.DeliveryNative == parent.num("id")
	}
	if r.Native > 0 {
		return r.Native == parent.num("id")
	}
	foreign := r.Canonical
	if foreign == "" {
		foreign = r.Foreign
	}
	return foreign != "" && foreign == parent.str("foreignBookId")
}

func matchesRequests(requests []savedRequest, inst instance.Instance, row activityRow) (bool, []record) {
	media := mediaForService[inst.ServiceType]
	var selected []record
	seen := map[int]bool{}
	for _, r := range requests {
		if r.Media != media || r.Instance != inst.ID {
			continue
		}
		switch media {
		case "movie":
			if r.TMDB > 0 && r.TMDB == row.parent.num("tmdbId") {
				return true, row.children
			}
		case "book", "music":
			foreignKey := "foreignBookId"
			if media == "music" {
				foreignKey = "foreignAlbumId"
			}
			foreign := row.parent.str(foreignKey)
			identity := (r.Canonical != "" && r.Canonical == foreign) || (r.Canonical == "" && r.Foreign != "" && r.Foreign == foreign)
			if media == "book" {
				format := bookFormat(row.parent)
				if format == "" || (r.Format != "both" && r.Format != format) {
					continue
				}
				if r.DeliveryFormat != "" && r.DeliveryFormat != format {
					continue
				}
				identity = bookIdentityMatches(r, row.parent)
			}
			if identity {
				return true, row.children
			}
		case "tv":
			var target struct {
				Match struct {
					TVDB int `json:"tvdb_id"`
				} `json:"match"`
				Seasons []int `json:"target_seasons"`
				Pilot   bool  `json:"pilot"`
			}
			if r.Target != "" {
				if json.Unmarshal([]byte(r.Target), &target) != nil || target.Match.TVDB <= 0 || target.Match.TVDB != row.parent.num("tvdbId") {
					continue
				}
				if r.Series > 0 && r.Series != row.parent.num("id") {
					continue
				}
			} else {
				// Legacy rows did not freeze custom season mappings. Only the
				// unambiguous all-seasons scope can be recovered without guessing.
				if r.TVDB <= 0 || r.TVDB != row.parent.num("tvdbId") || r.Seasons != "all" {
					continue
				}
				return true, row.children
			}
			for _, child := range row.children {
				for _, n := range target.Seasons {
					if child.num("seasonNumber") != n || (target.Pilot && child.num("episodeNumber") != 1) {
						continue
					}
					if !seen[child.num("id")] {
						selected = append(selected, child)
						seen[child.num("id")] = true
					}
				}
			}
		}
	}
	return len(selected) > 0, selected
}
