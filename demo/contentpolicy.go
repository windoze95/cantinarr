// contentpolicy.go — kids accounts: the per-user content policy, the
// certification catalog (GET /api/admin/certifications), the admin policy
// routes, and the decision helpers every title chokepoint calls.
//
// SKELETON: every helper below currently allows everything. The kids-accounts
// domain replaces this file wholesale; the signatures are the contract the
// other domains compile against.
package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// cpPolicy is one user_content_policies row. A row existing is what makes an
// account a child.
type cpPolicy struct {
	MaxMovieRating     string `json:"max_movie_rating"`
	MaxTVRating        string `json:"max_tv_rating"`
	RatingRegion       string `json:"rating_region"`
	BlockUnrated       bool   `json:"block_unrated"`
	BlockedMovieGenres []int  `json:"blocked_movie_genres"`
	BlockedTVGenres    []int  `json:"blocked_tv_genres"`
}

// cpMovieCerts / cpShowCerts: TMDB id -> region -> certification string. A
// missing region key means "no entry in that region" (unrated there). The
// discover domain reads these to emit release_dates[].certification and
// content_ratings on detail payloads.
var (
	cpMovieCerts = map[int]map[string]string{}
	cpShowCerts  = map[int]map[string]string{}
)

// registerContentPolicy mounts the four admin routes.
func registerContentPolicy(r chi.Router) {
	admin := r.With(requireAdmin)
	admin.Get("/admin/certifications", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"movie": map[string]any{}, "tv": map[string]any{}, "source": "builtin",
		})
	})
	admin.Get("/admin/users/{userID}/content-policy", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "not a kids account")
	})
	admin.Put("/admin/users/{userID}/content-policy", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotImplemented, "content policies are not available yet")
	})
	admin.Delete("/admin/users/{userID}/content-policy", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "cleared"})
	})
}

// cpPolicyFor returns the user's policy, nil for admins and unrestricted users.
func cpPolicyFor(u *DemoUser) *cpPolicy { return nil }

// cpIsChild reports whether a policy row exists for the user.
func cpIsChild(userID int) bool { return false }

// cpForgetUser drops the policy when a user is deleted.
func cpForgetUser(userID int) {}

// cpDescribe renders the policy the way the real Evaluator.Describe does.
func cpDescribe(p *cpPolicy) string { return "" }

// cpDecorateUserJSON adds child (always) and content_limits (when a child, or
// an explicit null when explicitNull) to a rendered user object.
func cpDecorateUserJSON(out map[string]any, u *DemoUser, explicitNull bool) {
	out["child"] = false
	if explicitNull {
		out["content_limits"] = nil
	}
}

// TMDB-shaped decisions (discover, Trakt, requests).
func policyAllowsMovie(u *DemoUser, m *DemoMovie) bool { return true }
func policyAllowsShow(u *DemoUser, s *DemoShow) bool   { return true }

// Arr-shaped decisions (Radarr/Sonarr records seen through the proxy).
func policyAllowsMovieRecord(u *DemoUser, m *DemoMovie) bool { return true }
func policyAllowsShowRecord(u *DemoUser, s *DemoShow) bool   { return true }

// cpAllowsTmdb resolves a catalog title by media type and id; ids the
// catalog does not hold are allowed (nothing to judge).
func cpAllowsTmdb(u *DemoUser, mediaType string, tmdbID int) bool { return true }

// List filters: return the input with blocked titles removed, never nil.
func cpKeepMovies(u *DemoUser, movies []*DemoMovie) []*DemoMovie { return movies }
func cpKeepShows(u *DemoUser, shows []*DemoShow) []*DemoShow     { return shows }

// cpKeepItems filters already-rendered TMDB items carrying "id" and
// "media_type" (search, trending, person credits).
func cpKeepItems(u *DemoUser, items []map[string]any) []map[string]any { return items }

// cpKeepGenres drops the policy's blocked genres from a genre list.
func cpKeepGenres(u *DemoUser, mediaType string, genres []DemoGenre) []DemoGenre { return genres }
