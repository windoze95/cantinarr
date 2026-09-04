package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/discover"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// browseTitlesTool is the assistant's Browse page: the same discover contract
// the app's grid uses, taking plain names the server resolves to TMDB ids.
var browseTitlesTool = Tool{
	Name:        "browse_titles",
	Permission:  auth.PermissionMediaDiscover,
	Description: "Browse TMDB's catalog of movies or TV shows by filters instead of by title: genre, release or air year range, minimum rating and vote count, original language, streaming service (per region), keyword or theme, and studio, with a sort and a page. Use it whenever the user names any such filter (\"90s sci-fi comedies\", \"Korean thrillers on Netflix\", \"top-rated A24 movies\"); use get_trending only for unfiltered trending asks and search_movies/search_tv_shows only for a specific title. media_type is movie or tv, never \"all\": for a mixed ask call it once per type with the same filters. Pass plain names; the server resolves them to TMDB ids and reports any name it could not resolve, with the valid options, instead of silently dropping it. The admin's English-only discovery preference applies unless original_language is given. A result is one TMDB page (up to 20 titles) plus the total page count, so ask for the next page to continue the same browse.",
	InputSchema: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"media_type": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"movie", "tv"},
				"description": "Browse movies or TV shows. No \"all\": call once per type for a mixed answer.",
			},
			"genres": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"maxItems":    5,
				"description": "Genre names as TMDB lists them (\"Science Fiction\", \"Comedy\"; TV uses \"Sci-Fi & Fantasy\", \"Action & Adventure\"). Case-insensitive; short forms like \"sci-fi\" resolve. ALL listed genres must apply.",
			},
			"year_from": map[string]interface{}{
				"type":        "integer",
				"minimum":     1874,
				"maximum":     2100,
				"description": "Earliest year, inclusive (movies: primary release; TV: first air).",
			},
			"year_to": map[string]interface{}{
				"type":        "integer",
				"minimum":     1874,
				"maximum":     2100,
				"description": "Latest year, inclusive. Same value as year_from for one year; 1990 and 1999 for a decade.",
			},
			"min_rating": map[string]interface{}{
				"type":        "number",
				"minimum":     0,
				"maximum":     10,
				"description": "Minimum TMDB average rating, 0-10.",
			},
			"min_votes": map[string]interface{}{
				"type":        "integer",
				"minimum":     0,
				"description": "Minimum TMDB vote count. sort \"top_rated\" applies a floor of 200 when this is omitted so one-vote 10.0s do not lead.",
			},
			"sort": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"popular", "newest", "oldest", "top_rated", "title"},
				"description": "Order: popular (default), newest or oldest by release or first-air date, top_rated by average rating, title alphabetical.",
			},
			"original_language": map[string]interface{}{
				"type":        "string",
				"description": "Original language as an ISO 639-1 code (\"ko\") or English name (\"Korean\"). Naming one turns off the admin's English-only default for this call.",
			},
			"streaming_services": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"maxItems":    5,
				"description": "Streaming service names as TMDB lists them for the region (\"Netflix\", \"Disney Plus\", \"Amazon Prime Video\", \"Max\"); case-insensitive. ANY listed service may carry the title.",
			},
			"region": map[string]interface{}{
				"type":        "string",
				"description": "ISO 3166-1 country code the streaming_services filter applies to. Default \"US\".",
			},
			"keywords": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"maxItems":    3,
				"description": "Theme or plot keywords (\"time travel\", \"heist\", \"based on novel\"). Each is matched to TMDB's closest keyword; ALL must apply.",
			},
			"studios": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"maxItems":    3,
				"description": "Production company names (\"A24\", \"Marvel Studios\", \"Studio Ghibli\"). ANY listed studio may apply. TV networks are not matched here.",
			},
			"page": map[string]interface{}{
				"type":        "integer",
				"minimum":     1,
				"maximum":     500,
				"description": "Result page, 1-500 (20 titles per page). Default 1; the result reports the total page count.",
			},
		},
		"required": []string{"media_type"},
	},
}

// browsePageSize is how many of a page's titles the text and carousel carry.
const browsePageSize = 20

// browseCatalogTTL matches the discover handler's TTL for the same tables.
const browseCatalogTTL = 24 * time.Hour

// Bounds enforced in code as well as in the schema, since a model may ignore
// maxItems.
const (
	maxBrowseGenres   = 5
	maxBrowseServices = 5
	maxBrowseKeywords = 3
	maxBrowseStudios  = 3
)

// nameList accepts a JSON array of strings or a single, optionally
// comma-separated string: smaller models send "Comedy, Sci-Fi" as often as
// ["Comedy","Sci-Fi"].
type nameList []string

func (l *nameList) UnmarshalJSON(b []byte) error {
	var list []string
	if err := json.Unmarshal(b, &list); err == nil {
		*l = trimNames(list)
		return nil
	}
	var single string
	if err := json.Unmarshal(b, &single); err != nil {
		return err
	}
	*l = trimNames(strings.Split(single, ","))
	return nil
}

func trimNames(in []string) []string {
	out := make([]string, 0, len(in))
	for _, name := range in {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

type browseTitlesInput struct {
	MediaType         string   `json:"media_type"`
	Genres            nameList `json:"genres"`
	YearFrom          int      `json:"year_from"`
	YearTo            int      `json:"year_to"`
	MinRating         float64  `json:"min_rating"`
	MinVotes          int      `json:"min_votes"`
	Sort              string   `json:"sort"`
	OriginalLanguage  string   `json:"original_language"`
	StreamingServices nameList `json:"streaming_services"`
	Region            string   `json:"region"`
	Keywords          nameList `json:"keywords"`
	Studios           nameList `json:"studios"`
	Page              int      `json:"page"`
}

// browseCatalog is a small in-process cache for the tables plain names are
// resolved against. Hand-rolled rather than internal/cache: that one runs a
// sweeper goroutine the tool server would have to close, and tests build
// tool servers by the dozen.
type browseCatalog struct {
	mu      sync.Mutex
	entries map[string]catalogEntry
}

type catalogEntry struct {
	value   any
	expires time.Time
}

func newBrowseCatalog() *browseCatalog {
	return &browseCatalog{entries: map[string]catalogEntry{}}
}

// get returns the cached value for key, loading and caching it on a miss.
func (c *browseCatalog) get(key string, load func() (any, error)) (any, error) {
	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.value, nil
	}
	value, err := load()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries[key] = catalogEntry{value: value, expires: time.Now().Add(browseCatalogTTL)}
	c.mu.Unlock()
	return value, nil
}

func catalogValue[T any](c *browseCatalog, key string, load func() (T, error)) (T, error) {
	value, err := c.get(key, func() (any, error) { return load() })
	if err != nil {
		var zero T
		return zero, err
	}
	return value.(T), nil
}

// browseSortBy maps the tool's sort names onto the TMDB sort the discover
// builder validates for the media type.
func browseSortBy(sortName, mediaType string) (string, bool) {
	dateField := "primary_release_date"
	titleField := "title"
	if mediaType == "tv" {
		dateField = "first_air_date"
		titleField = "name"
	}
	switch sortName {
	case "", "popular":
		return "popularity.desc", true
	case "newest":
		return dateField + ".desc", true
	case "oldest":
		return dateField + ".asc", true
	case "top_rated":
		return "vote_average.desc", true
	case "title":
		return titleField + ".asc", true
	}
	return "", false
}

// browseName is a resolvable catalog entry.
type browseName struct {
	id       int
	name     string
	priority int
}

// normalizeBrowseName folds case, punctuation, and the spellings that trip
// name matching ("Disney+" against "Disney Plus", "Sci-Fi & Fantasy").
func normalizeBrowseName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "+", " plus ")
	s = strings.ReplaceAll(s, "&", " and ")
	var sb strings.Builder
	lastSpace := true
	for _, r := range s {
		alnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if alnum {
			sb.WriteRune(r)
			lastSpace = false
		} else if !lastSpace {
			sb.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(sb.String())
}

// matchBrowseName finds the entry a plain name means: an exact match, else
// the one entry it abbreviates ("sci fi" for Science Fiction, "prime video"
// for Amazon Prime Video) or contains. Several such matches are ambiguous
// unless allowClosest, in which case the lowest priority (TMDB's own display
// order) wins.
func matchBrowseName(name string, entries []browseName, allowClosest bool) (match browseName, candidates []browseName, ok bool) {
	want := normalizeBrowseName(name)
	if want == "" {
		return browseName{}, nil, false
	}
	for _, entry := range entries {
		if normalizeBrowseName(entry.name) == want {
			return entry, nil, true
		}
	}
	wantTokens := strings.Fields(want)
	for _, entry := range entries {
		have := normalizeBrowseName(entry.name)
		if abbreviates(wantTokens, strings.Fields(have)) || strings.Contains(want, have) {
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 1 || (len(candidates) > 1 && allowClosest) {
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].priority < candidates[j].priority })
		return candidates[0], nil, true
	}
	return browseName{}, candidates, false
}

// abbreviates reports whether every token of want is, in order, a prefix of
// some token of have: "sci fi" abbreviates "science fiction", "prime video"
// abbreviates "amazon prime video", "comedy" abbreviates "comedy".
func abbreviates(want, have []string) bool {
	if len(want) == 0 {
		return false
	}
	i := 0
	for _, token := range have {
		if i < len(want) && strings.HasPrefix(token, want[i]) {
			i++
		}
	}
	return i == len(want)
}

func joinBrowseNames(entries []browseName) string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.name)
	}
	return strings.Join(names, ", ")
}

// browseResolution accumulates what the executor applied, what it matched
// names to, and what it could not resolve, so the text can say all three.
type browseResolution struct {
	applied    []string
	resolved   []string
	unresolved []string
}

func (r *browseResolution) apply(format string, args ...any) {
	r.applied = append(r.applied, fmt.Sprintf(format, args...))
}

func (r *browseResolution) note(format string, args ...any) {
	r.resolved = append(r.resolved, fmt.Sprintf(format, args...))
}

func (r *browseResolution) fail(format string, args ...any) {
	r.unresolved = append(r.unresolved, fmt.Sprintf(format, args...))
}

func (s *ToolServer) browseTitles(ctx context.Context, input json.RawMessage, policy *contentpolicy.Policy) (*ToolResult, error) {
	if s.creds == nil {
		return &ToolResult{Text: "TMDB is not configured on the server."}, nil
	}
	client := s.creds.TMDB()
	if client == nil {
		return &ToolResult{Text: "TMDB is not configured on the server."}, nil
	}
	var in browseTitlesInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("parse input: %w", err)
		}
	}

	mediaType := strings.ToLower(strings.TrimSpace(in.MediaType))
	switch mediaType {
	case "movie", "tv":
	case "all":
		return &ToolResult{Text: `browse_titles takes media_type "movie" or "tv" (no "all"); for a mixed answer call it once per type with the same filters.`}, nil
	default:
		return &ToolResult{Text: `browse_titles needs media_type "movie" or "tv".`}, nil
	}
	mediaLabel := "movies"
	if mediaType == "tv" {
		mediaLabel = "TV shows"
	}
	sortBy, ok := browseSortBy(in.Sort, mediaType)
	if !ok {
		return &ToolResult{Text: fmt.Sprintf("browse_titles does not know sort %q; use popular, newest, oldest, top_rated, or title.", in.Sort)}, nil
	}

	res := &browseResolution{}
	params := url.Values{}
	catalog := s.browseCatalog
	if catalog == nil {
		catalog = newBrowseCatalog()
	}

	// Genres: a closed list, so an unknown name comes back with the options.
	if len(in.Genres) > 0 {
		genres, err := catalogValue(catalog, "genres:"+mediaType, func() ([]tmdb.Genre, error) { return client.GenreList(mediaType) })
		if err != nil {
			return nil, err
		}
		entries := make([]browseName, 0, len(genres))
		for _, g := range genres {
			entries = append(entries, browseName{id: g.ID, name: g.Name})
		}
		var ids, names []string
		for _, name := range capNames(in.Genres, maxBrowseGenres) {
			match, candidates, ok := matchBrowseName(name, entries, false)
			switch {
			case ok:
				ids = append(ids, strconv.Itoa(match.id))
				names = append(names, match.name)
			case len(candidates) > 1:
				res.fail("genre %q is ambiguous (%s)", name, joinBrowseNames(candidates))
			default:
				res.fail("genre %q is not a %s genre (known: %s)", name, mediaLabel, joinBrowseNames(entries))
			}
		}
		if len(ids) == 0 {
			return &ToolResult{Text: unresolvedBrowseText(res)}, nil
		}
		params.Set("with_genres", strings.Join(ids, ","))
		res.apply("genres %s", strings.Join(names, " + "))
	}

	// Years become the media type's inclusive date bounds.
	from, to := in.YearFrom, in.YearTo
	if from > 0 && to > 0 && from > to {
		from, to = to, from
	}
	dateKey := "primary_release_date"
	if mediaType == "tv" {
		dateKey = "first_air_date"
	}
	if from > 0 {
		params.Set(dateKey+".gte", fmt.Sprintf("%04d-01-01", from))
	}
	if to > 0 {
		params.Set(dateKey+".lte", fmt.Sprintf("%04d-12-31", to))
	} else if in.Sort == "newest" {
		// "Newest" without an upper bound leads with titles announced for
		// years ahead and never rated; cap it at today.
		params.Set(dateKey+".lte", time.Now().Format("2006-01-02"))
	}
	switch {
	case from > 0 && to > 0 && from == to:
		res.apply("year %d", from)
	case from > 0 && to > 0:
		res.apply("years %d to %d", from, to)
	case from > 0:
		res.apply("from %d", from)
	case to > 0:
		res.apply("up to %d", to)
	}

	if in.MinRating > 0 {
		rating := in.MinRating
		if rating > 10 {
			rating = 10
		}
		params.Set("vote_average.gte", strconv.FormatFloat(rating, 'f', -1, 64))
		res.apply("rating %s+", strconv.FormatFloat(rating, 'f', -1, 64))
	}
	if in.MinVotes > 0 {
		params.Set("vote_count.gte", strconv.Itoa(in.MinVotes))
		res.apply("votes %d+", in.MinVotes)
	}
	params.Set("sort_by", sortBy)

	// Language: a code the table knows, else an English or native name.
	if lang := strings.TrimSpace(in.OriginalLanguage); lang != "" {
		languages, err := catalogValue(catalog, "languages", client.Languages)
		if err != nil {
			return nil, err
		}
		code, label, ok := resolveBrowseLanguage(lang, languages)
		if !ok {
			res.fail("language %q is not one TMDB lists (use an ISO 639-1 code such as \"ko\" or an English name such as \"Korean\")", lang)
			return &ToolResult{Text: unresolvedBrowseText(res)}, nil
		}
		params.Set("with_original_language", code)
		res.apply("in %s", label)
	}

	// Streaming services, per region.
	region := strings.ToUpper(strings.TrimSpace(in.Region))
	if len(region) != 2 {
		region = "US"
	}
	if len(in.StreamingServices) > 0 {
		providers, err := catalogValue(catalog, "providers:"+mediaType+":"+region, func() ([]tmdb.WatchProvider, error) {
			return client.WatchProviders(mediaType, region)
		})
		if err != nil {
			return nil, err
		}
		entries := make([]browseName, 0, len(providers))
		for _, p := range providers {
			entries = append(entries, browseName{id: p.ID, name: p.Name, priority: p.DisplayPriority})
		}
		var ids, names []string
		for _, name := range capNames(in.StreamingServices, maxBrowseServices) {
			match, _, ok := matchBrowseName(name, entries, true)
			if !ok {
				res.fail("streaming service %q is not one TMDB lists for %s (known: %s)", name, region, joinBrowseNames(topBrowseNames(entries, 15)))
				continue
			}
			ids = append(ids, strconv.Itoa(match.id))
			names = append(names, match.name)
		}
		if len(ids) == 0 {
			return &ToolResult{Text: unresolvedBrowseText(res)}, nil
		}
		params.Set("with_watch_providers", strings.Join(ids, "|"))
		params.Set("watch_region", region)
		res.apply("on %s (%s)", strings.Join(names, " or "), region)
	}

	// Keywords and studios: open vocabularies, resolved by search, first hit.
	if len(in.Keywords) > 0 {
		var ids, names []string
		for _, name := range capNames(in.Keywords, maxBrowseKeywords) {
			hits, err := client.SearchKeyword(name)
			if err != nil {
				return nil, err
			}
			if len(hits) == 0 {
				res.fail("keyword %q matched no TMDB keyword", name)
				continue
			}
			ids = append(ids, strconv.Itoa(hits[0].ID))
			names = append(names, hits[0].Name)
			if !strings.EqualFold(hits[0].Name, name) {
				res.note("keyword %q matched as %q", name, hits[0].Name)
			}
		}
		if len(ids) == 0 {
			return &ToolResult{Text: unresolvedBrowseText(res)}, nil
		}
		params.Set("with_keywords", strings.Join(ids, ","))
		res.apply("about %s", strings.Join(names, " + "))
	}
	if len(in.Studios) > 0 {
		var ids, names []string
		for _, name := range capNames(in.Studios, maxBrowseStudios) {
			hits, err := client.SearchCompany(name)
			if err != nil {
				return nil, err
			}
			if len(hits) == 0 {
				res.fail("studio %q matched no TMDB company", name)
				continue
			}
			ids = append(ids, strconv.Itoa(hits[0].ID))
			names = append(names, hits[0].Name)
			if !strings.EqualFold(hits[0].Name, name) {
				res.note("studio %q matched as %q", name, hits[0].Name)
			}
		}
		if len(ids) == 0 {
			return &ToolResult{Text: unresolvedBrowseText(res)}, nil
		}
		params.Set("with_companies", strings.Join(ids, "|"))
		res.apply("from %s", strings.Join(names, " or "))
	}

	page := discover.ClampPage(in.Page)
	englishOnly := discover.EnglishOnly(s.discoveryPrefs)
	query, explicitLanguage, err := discover.BuildBrowseQuery(mediaType, params, page, englishOnly, policy)
	if err != nil {
		return nil, err
	}
	englishApplied := englishOnly && !explicitLanguage
	if englishApplied {
		res.apply("English-language originals only (admin preference; name original_language to include others)")
	}
	if in.Sort == "top_rated" && in.MinVotes <= 0 {
		res.apply("votes %s+ (top_rated floor)", query.Get("vote_count.gte"))
	}
	sortLabel := in.Sort
	if sortLabel == "" {
		sortLabel = "popular"
	}
	res.apply("sort %s", strings.ReplaceAll(sortLabel, "_", " "))

	result, err := client.Discover(mediaType, query)
	if err != nil {
		return nil, err
	}
	kept, hidden, err := s.filterResults(ctx, policy, result.Results, mediaType)
	if err != nil {
		return nil, err
	}
	result.Results = kept
	if policy != nil {
		res.apply("this account's content limits")
	}

	applied := strings.Join(res.applied, "; ")
	var sb strings.Builder
	if len(res.unresolved) > 0 {
		fmt.Fprintf(&sb, "Could not resolve %s. Applied the rest.\n", strings.Join(res.unresolved, "; "))
	}
	if len(res.resolved) > 0 {
		fmt.Fprintf(&sb, "Resolved: %s.\n", strings.Join(res.resolved, "; "))
	}
	if len(result.Results) == 0 {
		sb.WriteString(noBrowseResultsText(mediaLabel, applied, englishApplied, page, result.TotalPages))
		sb.WriteString(hiddenNote(hidden))
		return &ToolResult{Text: sb.String()}, nil
	}
	shown := len(result.Results)
	if shown > browsePageSize {
		shown = browsePageSize
	}
	fmt.Fprintf(&sb, "Browsing %s: %s.", mediaLabel, applied)
	if in.Page > discover.MaxTMDBPage {
		fmt.Fprintf(&sb, " Page %d clamped to %d.", in.Page, discover.MaxTMDBPage)
	}
	fmt.Fprintf(&sb, " Page %d of %d (%d titles), %d shown", page, result.TotalPages, result.TotalResults, shown)
	if page < result.TotalPages {
		fmt.Fprintf(&sb, "; ask for page %d to continue", page+1)
	}
	sb.WriteString(".\n")
	sb.WriteString(formatSearchResults(result.Results, browsePageSize))
	sb.WriteString(hiddenNote(hidden))
	return &ToolResult{
		Text:           sb.String(),
		StructuredData: toMediaResultItems(result.Results, browsePageSize),
	}, nil
}

// unresolvedBrowseText is the answer when a named filter matched nothing:
// querying without it would answer a different question.
func unresolvedBrowseText(res *browseResolution) string {
	return "Could not resolve " + strings.Join(res.unresolved, "; ") +
		". Nothing was searched; retry with a name from the options, or drop that filter."
}

func capNames(names []string, limit int) []string {
	if len(names) > limit {
		return names[:limit]
	}
	return names
}

func topBrowseNames(entries []browseName, limit int) []browseName {
	sorted := append([]browseName(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].priority < sorted[j].priority })
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}

// resolveBrowseLanguage accepts a code TMDB lists, else an English or native
// name, and returns the code plus the English label to echo.
func resolveBrowseLanguage(input string, languages []tmdb.Language) (code, label string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(input))
	for _, lang := range languages {
		if strings.ToLower(lang.Code) == lower {
			return lang.Code, languageLabel(lang), true
		}
	}
	want := normalizeBrowseName(input)
	for _, lang := range languages {
		if normalizeBrowseName(lang.EnglishName) == want || normalizeBrowseName(lang.Name) == want {
			return lang.Code, languageLabel(lang), true
		}
	}
	return "", "", false
}

func languageLabel(lang tmdb.Language) string {
	if lang.EnglishName != "" {
		return lang.EnglishName
	}
	if lang.Name != "" {
		return lang.Name
	}
	return lang.Code
}
