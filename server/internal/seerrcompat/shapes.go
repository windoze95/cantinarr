// Package seerrcompat serves Cantinarr's request ledger in the shape of the
// Seerr v1 API (the merged Overseerr/Jellyseerr project), under /api/v1, so
// tools written against Seerr read Cantinarr without changes: Maintainerr's
// request-aware cleanup, Dashbrr's request panel, Homepage's request counts.
//
// The surface is what those integrators actually call, verified against
// their source, not the whole Seerr API: status, about, the paginated request
// list and its counts, one request, approve/decline, movie/TV/season detail
// with the title's media info, request and media deletion, the user list,
// and the availability-sync job trigger. Requests are the request_log rows
// for movies and TV; availability is read live from the arrs through the
// ledger, never stored; books and music have no Seerr shape and stay out.
//
// Authentication is Seerr's: an X-Api-Key header carrying the key an
// administrator issued under Settings. Every call acts as that
// administrator, so this is an admin surface and kids-account filtering does
// not apply to it; no user session can reach it.
package seerrcompat

import (
	"time"

	"github.com/windoze95/cantinarr-server/internal/request"
)

// Seerr's enums, verbatim (server/constants/media.ts, server/constants/user.ts,
// server/lib/permissions.ts).
const (
	requestPending   = 1
	requestApproved  = 2
	requestDeclined  = 3
	requestFailed    = 4
	requestCompleted = 5

	mediaUnknown            = 1
	mediaPending            = 2
	mediaProcessing         = 3
	mediaPartiallyAvailable = 4
	mediaAvailable          = 5

	userTypePlex     = 1
	userTypeLocal    = 2
	userTypeJellyfin = 3
	userTypeEmby     = 4

	permissionAdmin   = 2
	permissionRequest = 32
)

// seerrUser is Seerr's User as its API serializes it. Nullable strings are
// pointers so an absent identity reads as JSON null, the way Seerr answers
// for a local user with no Plex or Jellyfin account.
type seerrUser struct {
	ID               int64     `json:"id"`
	Email            string    `json:"email"`
	Username         *string   `json:"username"`
	PlexUsername     *string   `json:"plexUsername"`
	JellyfinUsername *string   `json:"jellyfinUsername"`
	UserType         int       `json:"userType"`
	PlexID           *int64    `json:"plexId"`
	JellyfinUserID   *string   `json:"jellyfinUserId"`
	Permissions      int       `json:"permissions"`
	Avatar           string    `json:"avatar"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	RequestCount     int       `json:"requestCount"`
	DisplayName      string    `json:"displayName"`
}

// seerrMedia is Seerr's Media, the per-title record a request points at.
type seerrMedia struct {
	ID           int64         `json:"id"`
	MediaType    string        `json:"mediaType"`
	TmdbID       int           `json:"tmdbId"`
	TvdbID       *int          `json:"tvdbId"`
	Status       int           `json:"status"`
	Status4k     int           `json:"status4k"`
	CreatedAt    time.Time     `json:"createdAt"`
	UpdatedAt    time.Time     `json:"updatedAt"`
	MediaAddedAt *time.Time    `json:"mediaAddedAt"`
	Seasons      []seerrSeason `json:"seasons,omitempty"`
	// Requests is present only on the detail endpoints (Seerr leaves it off
	// the list, where it would be circular); a pointer so an empty list is
	// still rendered there.
	Requests *[]seerrRequest `json:"requests,omitempty"`
}

// seerrSeason is Seerr's per-season availability on a Media record.
type seerrSeason struct {
	ID           int64 `json:"id"`
	SeasonNumber int   `json:"seasonNumber"`
	Status       int   `json:"status"`
	Status4k     int   `json:"status4k"`
}

// seerrSeasonRequest is one season of a TV request.
type seerrSeasonRequest struct {
	ID           int64     `json:"id"`
	SeasonNumber int       `json:"seasonNumber"`
	Status       int       `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// seerrRequest is Seerr's MediaRequest.
type seerrRequest struct {
	ID          int64                `json:"id"`
	Status      int                  `json:"status"`
	CreatedAt   time.Time            `json:"createdAt"`
	UpdatedAt   time.Time            `json:"updatedAt"`
	Type        string               `json:"type"`
	Is4k        bool                 `json:"is4k"`
	ServerID    int                  `json:"serverId"`
	ProfileID   *int                 `json:"profileId"`
	RootFolder  string               `json:"rootFolder"`
	Media       seerrMedia           `json:"media"`
	Seasons     []seerrSeasonRequest `json:"seasons"`
	RequestedBy seerrUser            `json:"requestedBy"`
	ModifiedBy  *seerrUser           `json:"modifiedBy"`
	SeasonCount int                  `json:"seasonCount"`
}

// pageInfo is Seerr's pagination envelope.
type pageInfo struct {
	Pages    int `json:"pages"`
	PageSize int `json:"pageSize"`
	Results  int `json:"results"`
	Page     int `json:"page"`
}

// mediaID is the Seerr media id for a title. Seerr assigns its own integer
// per Media row; Cantinarr has no such row, so the id is derived from the
// identity it does have, and decodes back without a lookup. Integrators
// delete by this id after reading it from mediaInfo, so it must differ from
// the TMDB id (Maintainerr guards against exactly that confusion).
func mediaID(mediaType string, tmdbID int) int64 {
	id := int64(tmdbID) * 2
	if mediaType == "tv" {
		id++
	}
	return id
}

// decodeMediaID reverses mediaID.
func decodeMediaID(id int64) (mediaType string, tmdbID int) {
	if id%2 == 1 {
		return "tv", int(id / 2)
	}
	return "movie", int(id / 2)
}

// mediaStatus maps a title's live state onto Seerr's media status. A title
// no library holds is UNKNOWN, as it is in Seerr before anything is added.
func mediaStatus(state request.LedgerTitleState) int {
	switch state.Status {
	case request.StatusAvailable:
		return mediaAvailable
	case request.StatusPartial:
		return mediaPartiallyAvailable
	case request.StatusRequested:
		return mediaProcessing
	}
	if state.InLibrary {
		return mediaProcessing
	}
	return mediaUnknown
}

// seasonStatus maps one season's live state onto Seerr's media status.
func seasonStatus(state request.LedgerSeasonState) int {
	switch state.Status {
	case request.StatusAvailable:
		return mediaAvailable
	case request.StatusPartial:
		return mediaPartiallyAvailable
	case request.StatusRequested:
		return mediaProcessing
	}
	return mediaProcessing
}

// requestStatus maps a row plus its title's live state onto Seerr's request
// status. Seerr completes a request when its media arrives, so an approved
// row whose title (or, for TV, whose every requested season) is available
// reads COMPLETED; one whose automatic add failed and never landed reads
// FAILED; the rest read as their decision.
func requestStatus(row request.LedgerRow, state request.LedgerTitleState) int {
	switch row.Stored {
	case request.StatusPending:
		return requestPending
	case request.StatusDenied:
		return requestDeclined
	}
	if row.AddFailed && !state.InLibrary {
		return requestFailed
	}
	if requestFulfilled(row, state) {
		return requestCompleted
	}
	return requestApproved
}

// requestFulfilled reports whether everything the row asked for is on disk.
func requestFulfilled(row request.LedgerRow, state request.LedgerTitleState) bool {
	if row.MediaType != "tv" || len(row.Seasons) == 0 {
		return state.Status == request.StatusAvailable
	}
	for _, n := range row.Seasons {
		if state.Seasons[n].Status != request.StatusAvailable {
			return false
		}
	}
	return true
}

// seasonRequestStatus is requestStatus for one season of a TV request.
func seasonRequestStatus(row request.LedgerRow, state request.LedgerTitleState, season int) int {
	switch row.Stored {
	case request.StatusPending:
		return requestPending
	case request.StatusDenied:
		return requestDeclined
	}
	if row.AddFailed && !state.InLibrary {
		return requestFailed
	}
	if state.Seasons[season].Status == request.StatusAvailable {
		return requestCompleted
	}
	return requestApproved
}
