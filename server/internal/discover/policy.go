package discover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
)

// Kids accounts: every payload this handler writes passes the caller's
// content policy AFTER the shared-cache read. The cache stays server-wide
// and unfiltered (the English-only preference is baked in before caching
// because it is one preference for everyone; a child's limits are theirs
// alone), and the filter fails closed: a payload that cannot be read or a
// rating that cannot be looked up answers as an error, never as a thinner
// list that looks complete.

// payloadKind names what a route's body contains, so the filter knows
// where the titles are and what a blocked one means.
type payloadKind int

const (
	// payloadOpaque carries no titles (providers, languages, keyword and
	// company lookups) and is written verbatim for everyone.
	payloadOpaque payloadKind = iota
	// payloadTitleList is a TMDB list envelope ({results:[...]}). The item's
	// media_type wins; the shape's media type covers single-type feeds.
	payloadTitleList
	// payloadMultiSearch is /search/multi: titles plus people, whose adult
	// flag hides them and whose known_for is a title list of its own.
	payloadMultiSearch
	// payloadMovieDetail and payloadTVDetail are one title; a blocked one
	// is 404. The detail body carries the ratings, so it primes the cache.
	payloadMovieDetail
	payloadTVDetail
	// payloadPersonDetail is one person; an adult-film performer is 404.
	payloadPersonDetail
	// payloadPersonCredits is combined_credits: cast and crew title lists.
	payloadPersonCredits
	// payloadGenreList is {genres:[...]}; hidden genres are dropped so the
	// browse strip and filter sheet never offer them.
	payloadGenreList
)

// payloadShape is a route's kind plus the media type that applies when an
// item does not name its own.
type payloadShape struct {
	kind      payloadKind
	mediaType string
}

var (
	shapeOpaque        = payloadShape{kind: payloadOpaque}
	shapeMixedList     = payloadShape{kind: payloadTitleList}
	shapeMovieList     = payloadShape{kind: payloadTitleList, mediaType: contentpolicy.MediaMovie}
	shapeTVList        = payloadShape{kind: payloadTitleList, mediaType: contentpolicy.MediaTV}
	shapeMultiSearch   = payloadShape{kind: payloadMultiSearch}
	shapeMovieDetail   = payloadShape{kind: payloadMovieDetail, mediaType: contentpolicy.MediaMovie}
	shapeTVDetail      = payloadShape{kind: payloadTVDetail, mediaType: contentpolicy.MediaTV}
	shapePersonDetail  = payloadShape{kind: payloadPersonDetail}
	shapePersonCredits = payloadShape{kind: payloadPersonCredits}
	shapeMovieGenres   = payloadShape{kind: payloadGenreList, mediaType: contentpolicy.MediaMovie}
	shapeTVGenres      = payloadShape{kind: payloadGenreList, mediaType: contentpolicy.MediaTV}
)

func listShape(mediaType string) payloadShape {
	if mediaType == contentpolicy.MediaTV {
		return shapeTVList
	}
	return shapeMovieList
}

// traktShape is the Trakt counterpart: the media type the route serves
// (an item that names its own kind overrides it), or opaque for list
// metadata that carries no titles.
type traktShape struct {
	mediaType string
	opaque    bool
}

// traktTypeShape maps the routes' ?type=movies|shows to a shape.
func traktTypeShape(typ string) traktShape {
	if typ == "shows" || typ == "show" {
		return traktShape{mediaType: contentpolicy.MediaTV}
	}
	return traktShape{mediaType: contentpolicy.MediaMovie}
}

var (
	errPolicyUnwired   = errors.New("content policy service is not wired")
	errPolicyPayload   = errors.New("content limits could not be applied")
	errTitleNotAllowed = errors.New("not available")
)

// SetContentPolicy wires the kids-account service. Until it is wired a
// child's request answers 503 rather than unfiltered.
func (h *Handler) SetContentPolicy(svc *contentpolicy.Service) { h.policies = svc }

// viewerPolicy resolves the caller's policy: nil for an unrestricted
// caller. The per-request user carries the child flag, so a non-child
// costs no read at all.
func (h *Handler) viewerPolicy(r *http.Request) (*contentpolicy.Policy, error) {
	ctx := r.Context()
	if user := auth.GetUserFromContext(ctx); user != nil {
		if !user.Child || user.Role == auth.RoleAdmin {
			return nil, nil
		}
		if h.policies == nil {
			return nil, errPolicyUnwired
		}
		return h.policies.Store.Get(user.ID)
	}
	claims := auth.GetClaims(ctx)
	if claims == nil || claims.Role == auth.RoleAdmin || h.policies == nil {
		return nil, nil
	}
	return h.policies.PolicyFor(claims.UserID, claims.Role)
}

// writeForCaller applies the caller's policy to a body the cache or the
// upstream just produced and writes the result.
func (h *Handler) writeForCaller(w http.ResponseWriter, r *http.Request, shape payloadShape, body []byte) {
	policy, err := h.viewerPolicy(r)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	if policy == nil || shape.kind == payloadOpaque {
		writeJSON(w, body)
		return
	}
	ev, err := h.policies.EvaluatorFor(r.Context(), policy)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	out, err := h.applyPolicy(r.Context(), ev, shape, body)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, out)
}

// writeTraktForCaller is writeForCaller for a Trakt body.
func (h *Handler) writeTraktForCaller(w http.ResponseWriter, r *http.Request, shape traktShape, body []byte) {
	policy, err := h.viewerPolicy(r)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	if policy == nil || shape.opaque {
		writeJSON(w, body)
		return
	}
	ev, err := h.policies.EvaluatorFor(r.Context(), policy)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	out, err := h.filterTraktArray(r.Context(), ev, shape.mediaType, body)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, out)
}

// writePolicyError maps a filter failure to a status the app can act on: a
// blocked title is 404, a payload or lookup that could not be read is 502
// (the upstream is what failed), and a service that cannot decide at all
// is 503.
func writePolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTitleNotAllowed):
		http.Error(w, `{"error":"not available"}`, http.StatusNotFound)
	case errors.Is(err, errPolicyPayload):
		http.Error(w, `{"error":"content limits could not be applied"}`, http.StatusBadGateway)
	default:
		log.Printf("discover: content policy unavailable: %v", err)
		http.Error(w, `{"error":"temporarily unavailable, retry shortly"}`, http.StatusServiceUnavailable)
	}
}

// itemProbe is the part of a list entry the decision needs.
type itemProbe struct {
	ID        int               `json:"id"`
	MediaType string            `json:"media_type"`
	Adult     bool              `json:"adult"`
	GenreIDs  []int             `json:"genre_ids"`
	KnownFor  []json.RawMessage `json:"known_for"`
}

// applyPolicy filters one TMDB body by shape. Every surviving entry is the
// bytes TMDB sent; only the arrays around them are rebuilt.
func (h *Handler) applyPolicy(ctx context.Context, ev *contentpolicy.Evaluator, shape payloadShape, body []byte) ([]byte, error) {
	switch shape.kind {
	case payloadOpaque:
		return body, nil
	case payloadTitleList, payloadMultiSearch:
		return h.filterEnvelope(ctx, ev, shape, body)
	case payloadMovieDetail, payloadTVDetail:
		return h.checkDetail(ctx, ev, shape.mediaType, body)
	case payloadPersonDetail:
		var probe struct {
			Adult bool `json:"adult"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			return nil, fmt.Errorf("%w: person: %v", errPolicyPayload, err)
		}
		if probe.Adult {
			return nil, errTitleNotAllowed
		}
		return body, nil
	case payloadPersonCredits:
		return h.filterCredits(ctx, ev, body)
	case payloadGenreList:
		return filterGenreList(ev, shape.mediaType, body)
	}
	return body, nil
}

// filterEnvelope rebuilds {results:[...]} with the allowed entries. A
// headline page (it carries a source) reports the served count; a plain
// TMDB page keeps total_results describing the upstream feed, as the
// English filter does, so paging still walks every upstream page.
func (h *Handler) filterEnvelope(ctx context.Context, ev *contentpolicy.Evaluator, shape payloadShape, body []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: envelope: %v", errPolicyPayload, err)
	}
	raw, ok := envelope["results"]
	if !ok {
		return nil, fmt.Errorf("%w: no results", errPolicyPayload)
	}
	var results []json.RawMessage
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, fmt.Errorf("%w: results: %v", errPolicyPayload, err)
	}
	kept, err := h.filterItems(ctx, ev, shape, results)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
	}
	envelope["results"] = encoded
	if _, headline := envelope["source"]; headline {
		envelope["total_results"] = json.RawMessage(fmt.Sprintf("%d", len(kept)))
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
	}
	return out, nil
}

// filterItems decides a list of entries. Titles are rated through the
// service; people (multi search) are kept unless flagged adult, with their
// known_for titles filtered in turn. An entry that names no media type and
// arrives on a mixed feed cannot be judged and is dropped.
func (h *Handler) filterItems(ctx context.Context, ev *contentpolicy.Evaluator, shape payloadShape, items []json.RawMessage) ([]json.RawMessage, error) {
	kept := make([]json.RawMessage, 0, len(items))
	cands := make([]contentpolicy.Candidate, 0, len(items))
	candIndex := make([]int, 0, len(items)) // index into kept for each candidate
	for _, item := range items {
		var probe itemProbe
		if err := json.Unmarshal(item, &probe); err != nil {
			return nil, fmt.Errorf("%w: entry: %v", errPolicyPayload, err)
		}
		if probe.MediaType == "person" {
			if shape.kind != payloadMultiSearch || probe.Adult {
				continue
			}
			rewritten, err := h.filterKnownFor(ctx, ev, item, probe.KnownFor)
			if err != nil {
				return nil, err
			}
			kept = append(kept, rewritten)
			continue
		}
		mediaType := probe.MediaType
		if mediaType == "" {
			mediaType = shape.mediaType
		}
		cands = append(cands, contentpolicy.Candidate{MediaType: mediaType, TMDBID: probe.ID, Adult: probe.Adult, GenreIDs: probe.GenreIDs})
		candIndex = append(candIndex, len(kept))
		kept = append(kept, item)
	}
	if len(cands) == 0 {
		return kept, nil
	}
	keep, err := h.policies.FilterWith(ctx, ev, cands)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
	}
	drop := map[int]struct{}{}
	for i, ok := range keep {
		if !ok {
			drop[candIndex[i]] = struct{}{}
		}
	}
	if len(drop) == 0 {
		return kept, nil
	}
	out := make([]json.RawMessage, 0, len(kept)-len(drop))
	for i, item := range kept {
		if _, dropped := drop[i]; !dropped {
			out = append(out, item)
		}
	}
	return out, nil
}

// filterKnownFor rewrites a person entry's known_for to the allowed titles.
func (h *Handler) filterKnownFor(ctx context.Context, ev *contentpolicy.Evaluator, item json.RawMessage, knownFor []json.RawMessage) (json.RawMessage, error) {
	if len(knownFor) == 0 {
		return item, nil
	}
	kept, err := h.filterItems(ctx, ev, shapeMixedList, knownFor)
	if err != nil {
		return nil, err
	}
	if len(kept) == len(knownFor) {
		return item, nil
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(item, &entry); err != nil {
		return nil, fmt.Errorf("%w: person entry: %v", errPolicyPayload, err)
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
	}
	entry["known_for"] = encoded
	out, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
	}
	return out, nil
}

// checkDetail decides one title from its own detail body, which carries
// the ratings (release_dates for a movie, content_ratings for a show) and
// primes the lookup cache with them.
func (h *Handler) checkDetail(ctx context.Context, ev *contentpolicy.Evaluator, mediaType string, body []byte) ([]byte, error) {
	var probe struct {
		ID     int  `json:"id"`
		Adult  bool `json:"adult"`
		Genres []struct {
			ID int `json:"id"`
		} `json:"genres"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("%w: detail: %v", errPolicyPayload, err)
	}
	if probe.ID <= 0 {
		return nil, fmt.Errorf("%w: detail without an id", errPolicyPayload)
	}
	h.policies.Prime(mediaType, probe.ID, body)
	genreIDs := make([]int, 0, len(probe.Genres))
	for _, g := range probe.Genres {
		genreIDs = append(genreIDs, g.ID)
	}
	keep, err := h.policies.FilterWith(ctx, ev, []contentpolicy.Candidate{{MediaType: mediaType, TMDBID: probe.ID, Adult: probe.Adult, GenreIDs: genreIDs}})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
	}
	if !keep[0] {
		return nil, errTitleNotAllowed
	}
	return body, nil
}

// filterCredits rebuilds combined_credits' cast and crew arrays.
func (h *Handler) filterCredits(ctx context.Context, ev *contentpolicy.Evaluator, body []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: credits: %v", errPolicyPayload, err)
	}
	for _, field := range []string{"cast", "crew"} {
		raw, ok := envelope[field]
		if !ok {
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", errPolicyPayload, field, err)
		}
		kept, err := h.filterItems(ctx, ev, shapeMixedList, items)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(kept)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
		}
		envelope[field] = encoded
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
	}
	return out, nil
}

// filterGenreList drops the hidden genres from {genres:[...]}.
func filterGenreList(ev *contentpolicy.Evaluator, mediaType string, body []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: genres: %v", errPolicyPayload, err)
	}
	raw, ok := envelope["genres"]
	if !ok {
		return body, nil
	}
	var genres []json.RawMessage
	if err := json.Unmarshal(raw, &genres); err != nil {
		return nil, fmt.Errorf("%w: genres: %v", errPolicyPayload, err)
	}
	kept := make([]json.RawMessage, 0, len(genres))
	for _, g := range genres {
		var probe struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(g, &probe); err != nil {
			return nil, fmt.Errorf("%w: genre: %v", errPolicyPayload, err)
		}
		if ev.BlockedGenre(mediaType, []int{probe.ID}) {
			continue
		}
		kept = append(kept, g)
	}
	if len(kept) == len(genres) {
		return body, nil
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
	}
	envelope["genres"] = encoded
	out, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
	}
	return out, nil
}

// traktProbe is the part of a Trakt feed element the decision needs. The
// feeds differ in shape: trending and anticipated wrap the media object
// (`movie`/`show`), popular and recommendations are the bare object, the
// calendar pairs a show with an episode, and list items name a `type`.
type traktProbe struct {
	Type  string      `json:"type"`
	Movie *traktIDs   `json:"movie"`
	Show  *traktIDs   `json:"show"`
	IDs   *traktIDSet `json:"ids"`
}

type traktIDs struct {
	IDs traktIDSet `json:"ids"`
}

type traktIDSet struct {
	TMDB int `json:"tmdb"`
}

// filterTraktArray keeps the elements whose title the policy allows. An
// element with no TMDB id, or of a kind that is not a movie or a show,
// cannot be judged and is dropped; Trakt's own certification field is never
// read as permission.
func (h *Handler) filterTraktArray(ctx context.Context, ev *contentpolicy.Evaluator, routeMediaType string, body []byte) ([]byte, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("%w: trakt: %v", errPolicyPayload, err)
	}
	cands := make([]contentpolicy.Candidate, 0, len(items))
	candItems := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		var probe traktProbe
		if err := json.Unmarshal(item, &probe); err != nil {
			return nil, fmt.Errorf("%w: trakt entry: %v", errPolicyPayload, err)
		}
		var cand contentpolicy.Candidate
		switch {
		case probe.Movie != nil:
			cand = contentpolicy.Candidate{MediaType: contentpolicy.MediaMovie, TMDBID: probe.Movie.IDs.TMDB}
		case probe.Show != nil:
			cand = contentpolicy.Candidate{MediaType: contentpolicy.MediaTV, TMDBID: probe.Show.IDs.TMDB}
		case probe.IDs != nil && probe.Type == "":
			cand = contentpolicy.Candidate{MediaType: routeMediaType, TMDBID: probe.IDs.TMDB}
		default:
			continue
		}
		if cand.TMDBID <= 0 {
			continue
		}
		cands = append(cands, cand)
		candItems = append(candItems, item)
	}
	kept := make([]json.RawMessage, 0, len(candItems))
	if len(cands) > 0 {
		keep, err := h.policies.FilterWith(ctx, ev, cands)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
		}
		for i, ok := range keep {
			if ok {
				kept = append(kept, candItems[i])
			}
		}
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPolicyPayload, err)
	}
	return out, nil
}
