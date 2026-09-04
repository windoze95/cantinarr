// contentpolicy.go — kids accounts: the per-user content policy, the
// certification catalog (GET /api/admin/certifications), the admin policy
// routes, and the decision helpers every title chokepoint calls.
//
// Mirrors server/internal/contentpolicy. A policy row makes an account a
// child; admins never carry one (both directions are refused). Decisions
// copy Evaluator.Allows (TMDB payloads: blocked genre, then the title's
// certification in the policy region) and Evaluator.AllowsArrRecord
// (Radarr/Sonarr records: the record's US string read in the policy region's
// scheme first, the US scheme second). The demo has no shared cache and no
// rating lookups that can fail: every rating is an in-process map read
// (data_policy.go), so the filter runs post-render, per request, and the
// one fail-closed rule that carries over is that a title with no entry for
// the policy region is unrated, never "unknown, allow". Every list helper
// returns a non-nil slice.
//
// Store rule: cpPolicies is guarded by cpMu only. Nothing here touches
// stateMu, and no state accessor is called while cpMu is held or from init().
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
)

// cpPolicy is one user_content_policies row. A row existing is what makes an
// account a child. Approval is deliberately not here: the per-user
// require_approval flag stays the one control (the app pre-sets it when an
// admin turns a kids account on).
type cpPolicy struct {
	MaxMovieRating     string `json:"max_movie_rating"`
	MaxTVRating        string `json:"max_tv_rating"`
	RatingRegion       string `json:"rating_region"`
	BlockUnrated       bool   `json:"block_unrated"`
	BlockedMovieGenres []int  `json:"blocked_movie_genres"`
	BlockedTVGenres    []int  `json:"blocked_tv_genres"`
}

// clone copies the policy so a caller never shares slices with the store.
func (p *cpPolicy) clone() *cpPolicy {
	out := *p
	out.BlockedMovieGenres = append([]int{}, p.BlockedMovieGenres...)
	out.BlockedTVGenres = append([]int{}, p.BlockedTVGenres...)
	return &out
}

// blockedGenres returns the hidden ids for a media type.
func (p *cpPolicy) blockedGenres(mediaType string) []int {
	switch mediaType {
	case mediaTypeMovie:
		return p.BlockedMovieGenres
	case mediaTypeTV:
		return p.BlockedTVGenres
	}
	return nil
}

// maxRating returns the cap for a media type.
func (p *cpPolicy) maxRating(mediaType string) string {
	switch mediaType {
	case mediaTypeMovie:
		return p.MaxMovieRating
	case mediaTypeTV:
		return p.MaxTVRating
	}
	return ""
}

// ─── Store ──────────────────────────────────────────────

var (
	cpMu sync.Mutex
	// cpPolicies: user id -> policy. Seeded by data_policy.go's init().
	cpPolicies = map[int]*cpPolicy{}
)

// cpPolicyFor returns a copy of the user's policy, nil for admins and
// unrestricted users. Admins are never filtered even if a row somehow
// existed (the real PolicyFor short-circuits on role).
func cpPolicyFor(u *DemoUser) *cpPolicy {
	if u == nil || u.Role == roleAdmin {
		return nil
	}
	cpMu.Lock()
	defer cpMu.Unlock()
	p := cpPolicies[u.ID]
	if p == nil {
		return nil
	}
	return p.clone()
}

// cpIsChild reports whether a policy row exists for the user.
func cpIsChild(userID int) bool {
	cpMu.Lock()
	defer cpMu.Unlock()
	_, ok := cpPolicies[userID]
	return ok
}

// cpForgetUser drops the policy when a user is deleted (the real table
// cascades with the user).
func cpForgetUser(userID int) {
	cpMu.Lock()
	defer cpMu.Unlock()
	delete(cpPolicies, userID)
}

// cpStore upserts a validated policy.
func cpStore(userID int, p *cpPolicy) {
	cpMu.Lock()
	defer cpMu.Unlock()
	cpPolicies[userID] = p.clone()
}

// cpLimitsJSON is the content_limits object a user's own profile carries:
// enough for the account line in Settings, nothing an admin screen needs.
func cpLimitsJSON(p *cpPolicy) map[string]any {
	return map[string]any{
		"max_movie_rating": p.MaxMovieRating,
		"max_tv_rating":    p.MaxTVRating,
		"rating_region":    p.RatingRegion,
	}
}

// cpDecorateUserJSON adds child (always) and content_limits (when a child, or
// an explicit null when explicitNull) to a rendered user object. Login and
// refresh bodies and the admin users list omit the limits for a non-child
// (omitempty on the real struct); GET /api/auth/me carries the null.
func cpDecorateUserJSON(out map[string]any, u *DemoUser, explicitNull bool) {
	p := cpPolicyFor(u)
	out["child"] = p != nil
	switch {
	case p != nil:
		out["content_limits"] = cpLimitsJSON(p)
	case explicitNull:
		out["content_limits"] = nil
	}
}

// cpDescribe renders the policy the way the real Evaluator.Describe does:
// "movies up to PG and shows up to TV-PG (US ratings); unrated titles
// hidden; hidden genres: Horror (movies)".
func cpDescribe(p *cpPolicy) string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("movies up to ")
	sb.WriteString(p.MaxMovieRating)
	sb.WriteString(" and shows up to ")
	sb.WriteString(p.MaxTVRating)
	sb.WriteString(" (")
	sb.WriteString(p.RatingRegion)
	sb.WriteString(" ratings)")
	if p.BlockUnrated {
		sb.WriteString("; unrated titles hidden")
	}
	var hidden []string
	for _, mediaType := range []string{mediaTypeMovie, mediaTypeTV} {
		ids := append([]int(nil), p.blockedGenres(mediaType)...)
		sort.Ints(ids)
		label := "movies"
		if mediaType == mediaTypeTV {
			label = "shows"
		}
		for _, id := range ids {
			name, ok := cpGenreName(mediaType, id)
			if !ok {
				continue
			}
			hidden = append(hidden, name+" ("+label+")")
		}
	}
	if len(hidden) > 0 {
		sb.WriteString("; hidden genres: ")
		sb.WriteString(strings.Join(hidden, ", "))
	}
	return sb.String()
}

// cpGenreName names a genre id from the discover domain's real TMDB tables.
func cpGenreName(mediaType string, id int) (string, bool) {
	table := discMovieGenres
	if mediaType == mediaTypeTV {
		table = discTVGenres
	}
	for _, g := range table {
		if g.ID == id {
			return g.Name, true
		}
	}
	return "", false
}

// ─── Certification catalog ──────────────────────────────

// cpCertOption is one entry of a region's rating scheme, as TMDB lists it.
// Order ranks the entries within the region; 0 is the unrated placeholder
// ("NR") and is never offered as a cap. Field order is the wire order of the
// real CertificationOption.
type cpCertOption struct {
	Certification string `json:"certification"`
	Meaning       string `json:"meaning,omitempty"`
	Order         int    `json:"order"`
	// Default marks the entry the admin UI starts a new kids account on.
	Default bool `json:"default,omitempty"`
}

// cpCertificationsResponse is GET /api/admin/certifications. The app offers
// only regions present in BOTH maps and drops entries with order <= 0.
type cpCertificationsResponse struct {
	Movie  map[string][]cpCertOption `json:"movie"`
	TV     map[string][]cpCertOption `json:"tv"`
	Source string                    `json:"source"`
}

// cpCatalog holds the schemes keyed media type, then region. US is the real
// server's builtinUS verbatim (TMDB's own list and meanings) with the
// suggested defaults (PG, TV-PG) marked; GB is the entry set the real
// server's own tests use, with BBFC meanings added. Both are static: the
// demo has no TMDB outage to fall back from, so source is always "tmdb".
var cpCatalog = map[string]map[string][]cpCertOption{
	mediaTypeMovie: {
		"US": {
			{Certification: "NR", Order: 0, Meaning: "No rating information."},
			{Certification: "G", Order: 1, Meaning: "All ages admitted. There is no content that would be objectionable to most parents."},
			{Certification: "PG", Order: 2, Meaning: "Some material may not be suitable for children under 10.", Default: true},
			{Certification: "PG-13", Order: 3, Meaning: "Some material may be inappropriate for children under 13."},
			{Certification: "R", Order: 4, Meaning: "Under 17 requires accompanying parent or adult guardian 21 or older."},
			{Certification: "NC-17", Order: 5, Meaning: "No one 17 and under admitted."},
		},
		"GB": {
			{Certification: "U", Order: 1, Meaning: "Suitable for all."},
			{Certification: "PG", Order: 2, Meaning: "Parental guidance. Some scenes may be unsuitable for young children."},
			{Certification: "12A", Order: 3, Meaning: "Cinema release suitable for 12 years and over; under 12s with an adult."},
			{Certification: "12", Order: 4, Meaning: "Suitable for 12 years and over."},
			{Certification: "15", Order: 5, Meaning: "Suitable only for 15 years and over."},
			{Certification: "18", Order: 6, Meaning: "Suitable only for adults."},
		},
	},
	mediaTypeTV: {
		"US": {
			{Certification: "NR", Order: 0, Meaning: "No rating information."},
			{Certification: "TV-Y", Order: 1, Meaning: "Designed to be appropriate for all children."},
			{Certification: "TV-Y7", Order: 2, Meaning: "Designed for children age 7 and above."},
			{Certification: "TV-G", Order: 3, Meaning: "Most parents would find this program suitable for all ages."},
			{Certification: "TV-PG", Order: 4, Meaning: "Parental guidance suggested.", Default: true},
			{Certification: "TV-14", Order: 5, Meaning: "Parents strongly cautioned. May be unsuitable for children under 14."},
			{Certification: "TV-MA", Order: 6, Meaning: "Mature audiences only."},
		},
		"GB": {
			{Certification: "U", Order: 1, Meaning: "Suitable for all."},
			{Certification: "PG", Order: 2, Meaning: "Parental guidance. Some scenes may be unsuitable for young children."},
			{Certification: "12", Order: 3, Meaning: "Suitable for 12 years and over."},
			{Certification: "15", Order: 4, Meaning: "Suitable only for 15 years and over."},
			{Certification: "18", Order: 5, Meaning: "Suitable only for adults."},
		},
	},
}

// cpNormalizeCert makes certification strings comparable across sources:
// "pg-13", "PG-13 " and "PG-13" are one rating.
func cpNormalizeCert(cert string) string {
	return strings.ToUpper(strings.TrimSpace(cert))
}

// cpOrders compiles one region's scheme into the lookup the decision uses;
// ok is false when the region has no scheme for that media type.
func cpOrders(mediaType, region string) (map[string]int, bool) {
	options, ok := cpCatalog[mediaType][strings.ToUpper(region)]
	if !ok {
		return nil, false
	}
	out := make(map[string]int, len(options))
	for _, o := range options {
		out[cpNormalizeCert(o.Certification)] = o.Order
	}
	return out, true
}

// cpCanonical returns the scheme's own spelling of a certification with a
// positive order, so a policy stores "PG-13" however the admin typed it.
func cpCanonical(mediaType, region, cert string) (string, bool) {
	want := cpNormalizeCert(cert)
	for _, o := range cpCatalog[mediaType][strings.ToUpper(region)] {
		if cpNormalizeCert(o.Certification) == want && o.Order > 0 {
			return o.Certification, true
		}
	}
	return "", false
}

// cpNormalizeGenreIDs drops non-positive ids and duplicates and sorts the
// rest, so two writes of the same set compare equal. Never nil, so it
// encodes as [] rather than null.
func cpNormalizeGenreIDs(ids []int) []int {
	seen := map[int]struct{}{}
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// cpValidate checks a submitted policy against the catalog and normalises
// it in place (upper-cased region, the scheme's own spelling of each cap,
// sorted genre ids), in the real Validate's order. The message is the
// exact 400 body the app renders in its snackbar.
func cpValidate(p *cpPolicy) (string, bool) {
	region := strings.ToUpper(strings.TrimSpace(p.RatingRegion))
	if region == "" {
		region = "US"
	}
	if len(region) != 2 || strings.IndexFunc(region, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
		return "unknown ratings region", false
	}
	if _, ok := cpCatalog[mediaTypeMovie][region]; !ok {
		return "unknown ratings region", false
	}
	if _, ok := cpCatalog[mediaTypeTV][region]; !ok {
		return "unknown ratings region", false
	}
	movieCap, ok := cpCanonical(mediaTypeMovie, region, p.MaxMovieRating)
	if !ok {
		return fmt.Sprintf("movie rating %q is not part of the %s scheme", strings.TrimSpace(p.MaxMovieRating), region), false
	}
	tvCap, ok := cpCanonical(mediaTypeTV, region, p.MaxTVRating)
	if !ok {
		return fmt.Sprintf("TV rating %q is not part of the %s scheme", strings.TrimSpace(p.MaxTVRating), region), false
	}
	p.RatingRegion = region
	p.MaxMovieRating = movieCap
	p.MaxTVRating = tvCap
	p.BlockedMovieGenres = cpNormalizeGenreIDs(p.BlockedMovieGenres)
	p.BlockedTVGenres = cpNormalizeGenreIDs(p.BlockedTVGenres)
	return "", true
}

// ─── Routes ─────────────────────────────────────────────

// registerContentPolicy mounts the four admin routes (users:manage in the
// real server; the demo's only admin holds it). Paths are registered
// individually so they sit beside users_admin.go's /admin/users/{userID}/…
// siblings without a mount collision.
func registerContentPolicy(r chi.Router) {
	admin := r.With(requireAdmin)
	admin.Get("/admin/certifications", cpCertificationsHandler)
	admin.Get("/admin/users/{userID}/content-policy", cpGetPolicyHandler)
	admin.Put("/admin/users/{userID}/content-policy", cpPutPolicyHandler)
	admin.Delete("/admin/users/{userID}/content-policy", cpDeletePolicyHandler)
}

// cpUserID parses {userID}; ok is false unless it is a positive integer.
func cpUserID(r *http.Request) (int, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return int(id), true
}

// cpCertificationsHandler — GET /api/admin/certifications. Also the app's
// probe for kids-account support: it never 404s.
func cpCertificationsHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, cpCertificationsResponse{
		Movie:  cpCatalog[mediaTypeMovie],
		TV:     cpCatalog[mediaTypeTV],
		Source: "tmdb",
	})
}

// cpGetPolicyHandler — GET …/content-policy. 404 "not a kids account" for
// any user without a row, unknown ids included (the real Store.Get does not
// consult the users table).
func cpGetPolicyHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := cpUserID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	cpMu.Lock()
	p := cpPolicies[id]
	if p != nil {
		p = p.clone()
	}
	cpMu.Unlock()
	if p == nil {
		writeErr(w, http.StatusNotFound, "not a kids account")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// cpPutPolicyHandler — PUT …/content-policy. Creates or replaces the row,
// turning the account into a kids account. Validation runs before the user
// lookup, like the real Validate before Store.Set; an admin is refused so
// no account is ever both.
func cpPutPolicyHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := cpUserID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	var p cpPolicy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if msg, valid := cpValidate(&p); !valid {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	u := userByID(id)
	if u == nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if u.Role == roleAdmin {
		writeErr(w, http.StatusConflict, "admin accounts cannot be kids accounts")
		return
	}
	cpStore(id, &p)
	writeJSON(w, http.StatusOK, p.clone())
}

// cpDeletePolicyHandler — DELETE …/content-policy. Turns the kids account
// off; clearing an account that has none (or does not exist) is still 200.
func cpDeletePolicyHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := cpUserID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	cpForgetUser(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

// ─── Decisions ──────────────────────────────────────────

// cpBlockedGenre reports whether any of the ids is hidden for the media type.
func cpBlockedGenre(p *cpPolicy, mediaType string, genreIDs []int) bool {
	blocked := p.blockedGenres(mediaType)
	for _, id := range genreIDs {
		for _, b := range blocked {
			if id == b {
				return true
			}
		}
	}
	return false
}

// cpAllowsIn compares a certification with the cap inside one scheme
// (Evaluator.allowsIn). A certification the scheme does not know, or ranked
// 0, is judged like an unrated title; a cap the scheme does not know allows
// nothing rated, so a bad write can never lift a limit.
func cpAllowsIn(p *cpPolicy, orders map[string]int, mediaType, cert string) bool {
	order, ok := orders[cpNormalizeCert(cert)]
	if !ok || order <= 0 {
		return !p.BlockUnrated
	}
	limit, ok := orders[cpNormalizeCert(p.maxRating(mediaType))]
	if !ok || limit <= 0 {
		return false
	}
	return order <= limit
}

// cpDecide mirrors Evaluator.Allows for a TMDB payload: hidden genres are
// out before any rating is consulted; a title with no entry for the policy
// region (known false) follows block_unrated; a rated one must sit at or
// below the cap in the region's scheme. The demo never renders adult titles.
func cpDecide(p *cpPolicy, mediaType string, genreIDs []int, cert string, known bool) bool {
	if mediaType != mediaTypeMovie && mediaType != mediaTypeTV {
		return false
	}
	if cpBlockedGenre(p, mediaType, genreIDs) {
		return false
	}
	if !known {
		return !p.BlockUnrated
	}
	orders, _ := cpOrders(mediaType, p.RatingRegion)
	return cpAllowsIn(p, orders, mediaType, cert)
}

// cpDecideRecord mirrors Evaluator.AllowsArrRecord for a Radarr/Sonarr
// record: the record's certification string is read in the policy region's
// scheme first and the US scheme second (the arrs write US certifications
// unless told otherwise); a string neither scheme knows is unrated. Genres
// are matched by id here because the fakes render their names from the same
// catalog the ids come from.
func cpDecideRecord(p *cpPolicy, mediaType string, genreIDs []int, cert string) bool {
	if mediaType != mediaTypeMovie && mediaType != mediaTypeTV {
		return false
	}
	if cpBlockedGenre(p, mediaType, genreIDs) {
		return false
	}
	c := cpNormalizeCert(cert)
	if c == "" {
		return !p.BlockUnrated
	}
	if region, ok := cpOrders(mediaType, p.RatingRegion); ok {
		if _, known := region[c]; known {
			return cpAllowsIn(p, region, mediaType, c)
		}
	}
	if us, ok := cpOrders(mediaType, "US"); ok {
		if _, known := us[c]; known {
			return cpAllowsIn(p, us, mediaType, c)
		}
	}
	return !p.BlockUnrated
}

// TMDB-shaped decisions (discover, Trakt, requests, AI carousels). The real
// server reads release_dates / content_ratings for the policy region, so a
// title with no entry for that region is unrated there even when it has a
// US one.
func policyAllowsMovie(u *DemoUser, m *DemoMovie) bool {
	p := cpPolicyFor(u)
	if p == nil {
		return true
	}
	if m == nil {
		return false
	}
	cert, known := cpMovieCerts[m.TmdbID][p.RatingRegion]
	return cpDecide(p, mediaTypeMovie, m.GenreIDs(), cert, known)
}

func policyAllowsShow(u *DemoUser, s *DemoShow) bool {
	p := cpPolicyFor(u)
	if p == nil {
		return true
	}
	if s == nil {
		return false
	}
	cert, known := cpShowCerts[s.TmdbID][p.RatingRegion]
	return cpDecide(p, mediaTypeTV, discShowGenreIDs(s), cert, known)
}

// Arr-shaped decisions (Radarr/Sonarr records seen through the proxy). A
// US-configured arr writes the US string into the record's certification,
// so the title's US entry is the record string; the region-then-US fallback
// is faithful to production, where a GB policy can see a Radarr row for a
// title whose US string ranks under the cap while the discover rows hide it.
func policyAllowsMovieRecord(u *DemoUser, m *DemoMovie) bool {
	p := cpPolicyFor(u)
	if p == nil {
		return true
	}
	if m == nil {
		return false
	}
	return cpDecideRecord(p, mediaTypeMovie, m.GenreIDs(), cpMovieCerts[m.TmdbID]["US"])
}

func policyAllowsShowRecord(u *DemoUser, s *DemoShow) bool {
	p := cpPolicyFor(u)
	if p == nil {
		return true
	}
	if s == nil {
		return false
	}
	return cpDecideRecord(p, mediaTypeTV, discShowGenreIDs(s), cpShowCerts[s.TmdbID]["US"])
}

// cpAllowsTmdb resolves a catalog title by media type and id. Ids the
// catalog does not hold are allowed (there is no title to judge, and no
// title to hide: the demo cannot render one), and media types that carry no
// ratings (books, music) always pass.
func cpAllowsTmdb(u *DemoUser, mediaType string, tmdbID int) bool {
	switch mediaType {
	case mediaTypeMovie:
		if m, ok := findMovie(tmdbID); ok {
			return policyAllowsMovie(u, m)
		}
	case mediaTypeTV:
		if s, ok := findShow(tmdbID); ok {
			return policyAllowsShow(u, s)
		}
	}
	return true
}

// ─── List filters (never nil) ───────────────────────────

// cpKeepMovies returns the input with blocked titles removed.
func cpKeepMovies(u *DemoUser, movies []*DemoMovie) []*DemoMovie {
	out := make([]*DemoMovie, 0, len(movies))
	if cpPolicyFor(u) == nil {
		return append(out, movies...)
	}
	for _, m := range movies {
		if policyAllowsMovie(u, m) {
			out = append(out, m)
		}
	}
	return out
}

// cpKeepShows returns the input with blocked shows removed.
func cpKeepShows(u *DemoUser, shows []*DemoShow) []*DemoShow {
	out := make([]*DemoShow, 0, len(shows))
	if cpPolicyFor(u) == nil {
		return append(out, shows...)
	}
	for _, s := range shows {
		if policyAllowsShow(u, s) {
			out = append(out, s)
		}
	}
	return out
}

// cpItemID reads a rendered item's id whatever numeric type built it.
func cpItemID(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

// cpKeepItems filters already-rendered TMDB items carrying "id" and
// "media_type" (search, trending, person credits, AI carousels). Movie and
// TV items are judged through the catalog; anything else (people, books,
// items naming no media type) passes through untouched.
func cpKeepItems(u *DemoUser, items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	if cpPolicyFor(u) == nil {
		return append(out, items...)
	}
	for _, item := range items {
		mediaType, _ := item["media_type"].(string)
		if mediaType == mediaTypeMovie || mediaType == mediaTypeTV {
			if id, ok := cpItemID(item["id"]); ok && !cpAllowsTmdb(u, mediaType, id) {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

// cpKeepGenres drops the policy's blocked genres from a genre list, so a
// kids account's browse strip and filter sheet never offer them.
func cpKeepGenres(u *DemoUser, mediaType string, genres []DemoGenre) []DemoGenre {
	out := make([]DemoGenre, 0, len(genres))
	p := cpPolicyFor(u)
	if p == nil {
		return append(out, genres...)
	}
	for _, g := range genres {
		if cpBlockedGenre(p, mediaType, []int{g.ID}) {
			continue
		}
		out = append(out, g)
	}
	return out
}
