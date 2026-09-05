package mediaaccess

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// SetWatchContentPolicy wires title authorization at the watch endpoint.
// The source is resolved per read so rotated TMDB credentials take effect.
func (h *Handler) SetWatchContentPolicy(policies *contentpolicy.Service, source contentpolicy.RawGetterSource) {
	h.watchPolicies, h.watchMetadata = policies, source
}

// authorizeWatch checks authoritative metadata, not the client's title/year
// or TVDB id: mixing an allowed TMDB id with a blocked show's TVDB id must
// never mint a link to the latter. Adults retain the existing query contract.
func (h *Handler) authorizeWatch(w http.ResponseWriter, r *http.Request, q mediaserver.ItemQuery) (mediaserver.ItemQuery, bool) {
	fail := func(status int) (mediaserver.ItemQuery, bool) {
		message := "not available"
		if status == http.StatusServiceUnavailable {
			message = "content limits are temporarily unavailable, retry shortly"
		}
		writeJSON(w, status, map[string]string{"error": message})
		return q, false
	}
	if h.watchPolicies == nil {
		if user := auth.GetUserFromContext(r.Context()); user != nil && user.Child {
			return fail(http.StatusServiceUnavailable)
		}
		return q, true
	}
	claims := auth.GetClaims(r.Context())
	policy, err := h.watchPolicies.PolicyFor(claims.UserID, claims.Role)
	if err != nil {
		return fail(http.StatusServiceUnavailable)
	}
	if policy == nil {
		return q, true
	}
	if q.TMDBID <= 0 {
		return fail(http.StatusNotFound)
	}
	if h.watchMetadata == nil {
		return fail(http.StatusServiceUnavailable)
	}
	getter := h.watchMetadata()
	if getter == nil {
		return fail(http.StatusServiceUnavailable)
	}
	raw, err := getter.DoGetRaw(fmt.Sprintf("/%s/%d", q.MediaType, q.TMDBID), url.Values{"append_to_response": {"external_ids"}})
	if err != nil {
		var status interface{ HTTPStatus() int }
		if errors.As(err, &status) && status.HTTPStatus() == http.StatusNotFound {
			return fail(http.StatusNotFound)
		}
		return fail(http.StatusServiceUnavailable)
	}
	var detail struct {
		ID     int64 `json:"id"`
		Adult  *bool `json:"adult"`
		Genres *[]struct {
			ID int `json:"id"`
		} `json:"genres"`
		Title        string `json:"title"`
		Name         string `json:"name"`
		ReleaseDate  string `json:"release_date"`
		FirstAirDate string `json:"first_air_date"`
		ExternalIDs  struct {
			TVDBID int64 `json:"tvdb_id"`
		} `json:"external_ids"`
	}
	if json.Unmarshal(raw, &detail) != nil || detail.ID != q.TMDBID || detail.Adult == nil || detail.Genres == nil {
		return fail(http.StatusServiceUnavailable)
	}
	candidate := contentpolicy.Candidate{MediaType: q.MediaType, TMDBID: int(q.TMDBID), Adult: *detail.Adult}
	for _, genre := range *detail.Genres {
		candidate.GenreIDs = append(candidate.GenreIDs, genre.ID)
	}
	allowed, err := h.watchPolicies.Allows(r.Context(), policy, candidate)
	if err != nil {
		return fail(http.StatusServiceUnavailable)
	}
	if !allowed {
		return fail(http.StatusNotFound)
	}
	q.TVDBID, q.Year, q.Title = 0, 0, detail.Title
	date := detail.ReleaseDate
	if q.MediaType == "tv" {
		q.TVDBID, q.Title, date = detail.ExternalIDs.TVDBID, detail.Name, detail.FirstAirDate
	}
	if len(date) >= 4 {
		q.Year, _ = strconv.Atoi(date[:4])
	}
	return q, true
}
