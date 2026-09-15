package mediaaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/windoze95/cantinarr-server/internal/appletv"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// AuthorizeAppleTVTitle is called on both sides of the TV connection wait.
// The Apple TV handler rejects kids accounts and revalidates the live session
// and TV grant. Derive every lookup hint from TMDB so a forged TVDB id cannot
// prove access to a different series before launching the submitted TMDB id.
func (h *Handler) AuthorizeAppleTVTitle(ctx context.Context, userID int64, mediaType string, tmdbID int64) error {
	if (mediaType != "movie" && mediaType != "tv") || tmdbID <= 0 {
		return appletv.ErrTitleUnavailable
	}
	if h.watchMetadata == nil {
		return appletv.ErrLookupUnavailable
	}
	metadata := h.watchMetadata()
	if metadata == nil {
		return appletv.ErrLookupUnavailable
	}
	raw, err := metadata.DoGetRaw(fmt.Sprintf("/%s/%d", mediaType, tmdbID), url.Values{"append_to_response": {"external_ids"}})
	if err != nil {
		return appletv.ErrLookupUnavailable
	}
	var detail struct {
		ID           int64  `json:"id"`
		Title        string `json:"title"`
		Name         string `json:"name"`
		ReleaseDate  string `json:"release_date"`
		FirstAirDate string `json:"first_air_date"`
		ExternalIDs  struct {
			TVDBID int64 `json:"tvdb_id"`
		} `json:"external_ids"`
	}
	if json.Unmarshal(raw, &detail) != nil || detail.ID != tmdbID {
		return appletv.ErrLookupUnavailable
	}
	query := mediaserver.ItemQuery{MediaType: mediaType, TMDBID: tmdbID, Title: detail.Title}
	date := detail.ReleaseDate
	if mediaType == "tv" {
		query.Title = detail.Name
		query.TVDBID = detail.ExternalIDs.TVDBID
		date = detail.FirstAirDate
	}
	if len(date) >= 4 {
		query.Year, _ = strconv.Atoi(date[:4])
	}
	links, err := h.svc.WatchLinks(ctx, userID, query)
	if err != nil {
		return appletv.ErrLookupUnavailable
	}
	uncertain := false
	for _, link := range links {
		if link.State == WatchFound {
			return nil
		}
		if link.State == WatchUnreachable || link.State == WatchUnverified {
			uncertain = true
		}
	}
	if uncertain {
		return appletv.ErrLookupUnavailable
	}
	return appletv.ErrTitleUnavailable
}
