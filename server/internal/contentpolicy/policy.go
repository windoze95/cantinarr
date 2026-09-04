// Package contentpolicy owns kids accounts: the per-user content policy an
// admin sets, the TMDB rating lookups that decide whether a title fits it,
// and the decision itself.
//
// Every server surface that can name a movie or show asks this package after
// its shared-cache read and before writing to the client — discover rows,
// search, details, people, Trakt feeds, the Radarr/Sonarr proxy reads, the AI
// tools, request creation, and new-content pushes. The app never filters.
//
// Decisions fail closed. A title whose rating cannot be read is hidden, and
// the response carrying the failed lookup answers as an error rather than an
// empty list, so an outage never reads as "nothing here" (AGENTS.md: absence
// versus blindness).
package contentpolicy

import (
	"sort"
	"strings"
)

// Media types a policy speaks about. Books and music carry no ratings from
// Chaptarr or Lidarr and are deliberately outside this package: those modules
// are granted per user, so an admin grants them to a child on purpose.
const (
	MediaMovie = "movie"
	MediaTV    = "tv"
)

// roleAdmin mirrors auth.RoleAdmin. The auth package imports this one for the
// content limits on a user, so the string is repeated here rather than
// imported; the store's tests pin it against a real users row.
const roleAdmin = "admin"

// Policy is a kids account's content limits. A row in user_content_policies
// makes the account a child; no row means the account is unrestricted.
//
// Approval is deliberately not here: user_request_settings.require_approval
// stays the one control (the app pre-sets it when an admin turns a kids
// account on).
type Policy struct {
	// MaxMovieRating and MaxTVRating are certifications from RatingRegion's
	// TMDB lists ("PG", "TV-PG"). Everything ordered above them is hidden.
	MaxMovieRating string `json:"max_movie_rating"`
	MaxTVRating    string `json:"max_tv_rating"`
	// RatingRegion is the ISO 3166-1 alpha-2 country whose rating scheme the
	// limits are expressed in and whose certification a title is judged by.
	RatingRegion string `json:"rating_region"`
	// BlockUnrated hides a title that has no certification in RatingRegion.
	BlockUnrated bool `json:"block_unrated"`
	// BlockedMovieGenres and BlockedTVGenres are TMDB genre ids hidden
	// regardless of rating (Horror is 27 for movies).
	BlockedMovieGenres []int `json:"blocked_movie_genres"`
	BlockedTVGenres    []int `json:"blocked_tv_genres"`
}

// Limits is the summary a user's own profile carries: enough for the account
// line in Settings, nothing an admin screen would need.
type Limits struct {
	MaxMovieRating string `json:"max_movie_rating"`
	MaxTVRating    string `json:"max_tv_rating"`
	RatingRegion   string `json:"rating_region"`
}

// Limits summarises the policy for the owning user.
func (p *Policy) Limits() *Limits {
	if p == nil {
		return nil
	}
	return &Limits{
		MaxMovieRating: p.MaxMovieRating,
		MaxTVRating:    p.MaxTVRating,
		RatingRegion:   p.RatingRegion,
	}
}

// blockedGenres returns the blocked ids for a media type.
func (p *Policy) blockedGenres(mediaType string) []int {
	switch mediaType {
	case MediaMovie:
		return p.BlockedMovieGenres
	case MediaTV:
		return p.BlockedTVGenres
	}
	return nil
}

// maxRating returns the cap for a media type.
func (p *Policy) maxRating(mediaType string) string {
	switch mediaType {
	case MediaMovie:
		return p.MaxMovieRating
	case MediaTV:
		return p.MaxTVRating
	}
	return ""
}

// Candidate is one title as a list payload describes it. Adult and GenreIDs
// let the evaluator hide a title without a rating lookup; TMDBID is what the
// lookup keys on.
type Candidate struct {
	MediaType string
	TMDBID    int
	Adult     bool
	GenreIDs  []int
}

// Rating is a title's certification in the policy's region. Known is false
// when the region has no entry for the title.
type Rating struct {
	Certification string
	Known         bool
}

// certOrders maps a normalised certification to its TMDB order for one
// region's list. Order 0 is "NR"-style unrated and never allows anything.
type certOrders map[string]int

// Evaluator is a policy compiled against the certification lists and genre
// names it needs, so the per-title decision is a pure map lookup.
type Evaluator struct {
	policy Policy
	// region and us hold each media type's certification orders for the
	// policy region and for the US (the fallback scheme arr records are
	// usually written in).
	region map[string]certOrders
	us     map[string]certOrders
	// blockedIDs and blockedNames are the hidden genres per media type, the
	// latter as the normalised names and aliases arr records use.
	blockedIDs   map[string]map[int]struct{}
	blockedNames map[string]map[string]struct{}
	// genreNames names the blocked ids for Describe.
	genreNames map[string]genreTable
}

func newEvaluator(p Policy, region, us map[string]certOrders, genres map[string]genreTable) *Evaluator {
	ev := &Evaluator{
		policy:       p,
		region:       region,
		us:           us,
		blockedIDs:   map[string]map[int]struct{}{},
		blockedNames: map[string]map[string]struct{}{},
		genreNames:   genres,
	}
	for _, mediaType := range []string{MediaMovie, MediaTV} {
		ids := map[int]struct{}{}
		names := map[string]struct{}{}
		for _, id := range p.blockedGenres(mediaType) {
			ids[id] = struct{}{}
			if name, ok := genres[mediaType][id]; ok {
				for _, alias := range genreAliases(name) {
					names[alias] = struct{}{}
				}
			}
		}
		ev.blockedIDs[mediaType] = ids
		ev.blockedNames[mediaType] = names
	}
	return ev
}

// Policy returns the policy the evaluator was compiled from.
func (e *Evaluator) Policy() Policy { return e.policy }

// BlockedGenre reports whether any of the ids is hidden for the media type.
// It needs no rating lookup, so list filters ask it first.
func (e *Evaluator) BlockedGenre(mediaType string, genreIDs []int) bool {
	blocked := e.blockedIDs[mediaType]
	for _, id := range genreIDs {
		if _, ok := blocked[id]; ok {
			return true
		}
	}
	return false
}

// Allows decides one title from what a TMDB payload says about it: adult
// titles and hidden genres are out before any rating is consulted; an
// unrated title follows BlockUnrated; a rated one must sit at or below the
// cap. A media type the policy does not know is never allowed.
func (e *Evaluator) Allows(mediaType string, r Rating, adult bool, genreIDs []int) bool {
	if adult || !knownMediaType(mediaType) {
		return false
	}
	if e.BlockedGenre(mediaType, genreIDs) {
		return false
	}
	if !r.Known {
		return !e.policy.BlockUnrated
	}
	return e.allowsIn(e.region[mediaType], mediaType, r.Certification)
}

// AllowsArrRecord decides a Radarr/Sonarr record from the arr's own
// certification string and genre names. The certification is read in the
// policy region's scheme first and the US scheme second (Radarr and Sonarr
// write US certifications unless told otherwise); a string neither scheme
// knows is unrated. Genre names are matched by normalised name and alias, so
// Sonarr's "Sci-Fi & Fantasy" still hits a hidden "Science Fiction".
func (e *Evaluator) AllowsArrRecord(mediaType, certification string, genreNames []string) bool {
	if !knownMediaType(mediaType) {
		return false
	}
	blocked := e.blockedNames[mediaType]
	for _, name := range genreNames {
		for _, alias := range genreAliases(name) {
			if _, ok := blocked[alias]; ok {
				return false
			}
		}
	}
	cert := normalizeCert(certification)
	if cert == "" {
		return !e.policy.BlockUnrated
	}
	if _, ok := e.region[mediaType][cert]; ok {
		return e.allowsIn(e.region[mediaType], mediaType, cert)
	}
	if _, ok := e.us[mediaType][cert]; ok {
		return e.allowsIn(e.us[mediaType], mediaType, cert)
	}
	return !e.policy.BlockUnrated
}

// allowsIn compares a certification with the cap inside one scheme. Both
// must resolve there: a cap the scheme does not know allows nothing rated,
// so a bad write can never lift a limit.
func (e *Evaluator) allowsIn(orders certOrders, mediaType, certification string) bool {
	order, ok := orders[normalizeCert(certification)]
	if !ok || order <= 0 {
		return !e.policy.BlockUnrated
	}
	limit, ok := orders[normalizeCert(e.policy.maxRating(mediaType))]
	if !ok || limit <= 0 {
		return false
	}
	return order <= limit
}

// Describe renders the limits for the assistant's instructions and logs:
// "movies up to PG and shows up to TV-PG (US ratings); unrated titles
// hidden; hidden genres: Horror (movies), War & Politics (shows)".
func (e *Evaluator) Describe() string {
	var sb strings.Builder
	sb.WriteString("movies up to ")
	sb.WriteString(e.policy.MaxMovieRating)
	sb.WriteString(" and shows up to ")
	sb.WriteString(e.policy.MaxTVRating)
	sb.WriteString(" (")
	sb.WriteString(e.policy.RatingRegion)
	sb.WriteString(" ratings)")
	if e.policy.BlockUnrated {
		sb.WriteString("; unrated titles hidden")
	}
	var hidden []string
	for _, mediaType := range []string{MediaMovie, MediaTV} {
		ids := append([]int(nil), e.policy.blockedGenres(mediaType)...)
		sort.Ints(ids)
		label := "movies"
		if mediaType == MediaTV {
			label = "shows"
		}
		for _, id := range ids {
			name, ok := e.genreNames[mediaType][id]
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

func knownMediaType(mediaType string) bool {
	return mediaType == MediaMovie || mediaType == MediaTV
}

// normalizeCert makes certification strings comparable across sources:
// "pg-13", "PG-13 " and "PG-13" are one rating.
func normalizeCert(cert string) string {
	return strings.ToUpper(strings.TrimSpace(cert))
}
