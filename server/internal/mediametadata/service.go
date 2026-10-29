// Package mediametadata provides optional presentation text. It never changes
// provider identities, availability, permissions, or persisted request records.
// Callers must apply their existing authorization and content policy to every
// response, including cache hits and source-language fallbacks.
package mediametadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"

	"github.com/windoze95/cantinarr-server/internal/cache"
	"github.com/windoze95/cantinarr-server/internal/credentials"
)

// SupportedLanguages is the legacy capability for older apps that only know
// these choices. New apps use metadata_language_tags and their own ARB locales.
var SupportedLanguages = []string{"en-US", "fr-FR"}

// NormalizeLanguage preserves omission for old clients and limits cache keys
// to registered two-letter languages and optional country codes. Arbitrary
// BCP-47 extensions, scripts, private-use tags, and unknown codes use English.
func NormalizeLanguage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) != 2 && (len(value) != 5 || value[2] != '-') {
		return "en-US"
	}
	for i, char := range []byte(value) {
		if i == 2 {
			continue
		}
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') {
			return "en-US"
		}
	}
	// Only collapse deprecated identifiers. CLDR's broader legacy mapping
	// would turn valid ISO-639-1 tl into the three-letter fil, which TMDB does
	// not use for this parameter.
	tag, err := language.Deprecated.Parse(value)
	if err != nil {
		return "en-US"
	}
	base, _, region := tag.Raw()
	code := base.String()
	if len(code) != 2 || base.IsPrivateUse() {
		return "en-US"
	}
	if len(value) == 5 || region.String() != "ZZ" {
		if !region.IsCountry() || region.IsPrivateUse() || len(region.String()) != 2 {
			return "en-US"
		}
		return code + "-" + region.String()
	}
	switch code {
	case "en":
		return "en-US"
	case "fr":
		return "fr-FR"
	default:
		return code
	}
}
func Language(r *http.Request) string {
	return NormalizeLanguage(r.URL.Query().Get("metadata_language"))
}

type Presentation struct {
	TMDBID       int    `json:"-"`
	MediaType    string `json:"-"`
	EpisodeTitle string `json:"episode_title,omitempty"`
	Title        string `json:"title,omitempty"`
	SourceTitle  string `json:"source_title,omitempty"`
	Overview     string `json:"overview,omitempty"`
	PosterPath   string `json:"poster_path,omitempty"`
	BackdropPath string `json:"backdrop_path,omitempty"`
}

type pendingRead struct {
	done chan struct{}
	data []byte
	err  error
}
type Service struct {
	creds   *credentials.Registry
	cache   *cache.Cache
	slots   chan struct{}
	mu      sync.Mutex
	pending map[string]*pendingRead
}

func New(creds *credentials.Registry, c *cache.Cache) *Service {
	return &Service{creds: creds, cache: c, slots: make(chan struct{}, 4), pending: map[string]*pendingRead{}}
}

// Raw localizes a provider response while preserving its selected identities
// and ranking. English supplies only missing presentation fields and source
// labels; its result list never adds, removes, or reorders localized entries.
// Feed TTLs belong to the discovery handler, so Raw itself does not cache feeds.
func (s *Service) Raw(ctx context.Context, path string, params url.Values, language string) ([]byte, error) {
	language = NormalizeLanguage(language)
	if language == "" {
		language = "en-US"
	}
	if language == "en-US" {
		return s.fetch(ctx, path, params, language)
	}
	// Fetch the fallback alongside the requested language: a slow translation
	// must not consume a second full provider timeout before English can show.
	type rawResult struct {
		data []byte
		err  error
	}
	english := make(chan rawResult, 1)
	go func() { data, err := s.fetch(ctx, path, params, "en-US"); english <- rawResult{data, err} }()
	localized, err := s.fetch(ctx, path, params, language)
	fallbackResult := <-english
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	source, sourceErr := fallbackResult.data, fallbackResult.err
	if err != nil {
		return source, sourceErr
	}
	var preferred, fallback any
	if json.Unmarshal(localized, &preferred) != nil {
		if sourceErr == nil {
			return source, nil
		}
		return nil, errors.New("invalid localized metadata")
	}
	if sourceErr == nil && json.Unmarshal(source, &fallback) == nil {
		mergePresentation(preferred, fallback)
	}
	fallbackOriginalNames(preferred)
	return json.Marshal(preferred)
}

func (s *Service) fetch(ctx context.Context, path string, params url.Values, language string) ([]byte, error) {
	return s.coalesced(ctx, "raw:"+path+"?"+params.Encode()+":"+language, 0, func() ([]byte, error) {
		return s.fetchOnce(ctx, path, params, language)
	})
}

func (s *Service) fetchOnce(ctx context.Context, path string, params url.Values, language string) ([]byte, error) {
	if s == nil || s.creds == nil || s.creds.TMDB() == nil {
		return nil, errors.New("TMDB is not configured")
	}
	client := s.creds.TMDB()
	if client == nil {
		return nil, errors.New("TMDB is not configured")
	}
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.slots }()
	return client.DoGetRawContext(ctx, path, params, language)
}

// cached coalesces identical cold reads without retaining a failed lookup.
// Credential-client identity prevents a credential replacement using old data.
func (s *Service) cached(ctx context.Context, key string, read func() ([]byte, error)) ([]byte, error) {
	return s.coalesced(ctx, key, time.Hour, read)
}

func (s *Service) coalesced(ctx context.Context, key string, ttl time.Duration, read func() ([]byte, error)) ([]byte, error) {
	if s == nil || s.creds == nil || s.creds.TMDB() == nil {
		return nil, errors.New("TMDB is not configured")
	}
	key = fmt.Sprintf("metadata:%p:%s", s.creds.TMDB(), key)
	if ttl > 0 && s.cache != nil {
		if data, ok := s.cache.Get(key); ok {
			return data, nil
		}
	}
	s.mu.Lock()
	if pending := s.pending[key]; pending != nil {
		s.mu.Unlock()
		select {
		case <-pending.done:
			return pending.data, pending.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	pending := &pendingRead{done: make(chan struct{})}
	s.pending[key] = pending
	s.mu.Unlock()
	data, err := read()
	s.mu.Lock()
	pending.data, pending.err = data, err
	if err == nil && ttl > 0 && s.cache != nil {
		s.cache.Set(key, data, ttl)
	}
	delete(s.pending, key)
	close(pending.done)
	s.mu.Unlock()
	return data, err
}

func (s *Service) Presentation(ctx context.Context, mediaType string, id int, language string) (*Presentation, error) {
	if (mediaType != "movie" && mediaType != "tv") || id <= 0 {
		return nil, errors.New("invalid media identity")
	}
	language = NormalizeLanguage(language)
	data, err := s.cached(ctx, fmt.Sprintf("%s:%d:%s", mediaType, id, language), func() ([]byte, error) {
		return s.Raw(ctx, fmt.Sprintf("/%s/%d", mediaType, id), nil, language)
	})
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil || number(value["id"]) != id {
		return nil, errors.New("metadata identity changed")
	}
	out := presentation(value)
	out.TMDBID, out.MediaType = id, mediaType
	return out, nil
}

// Episode needs an exact external episode ID. Season/episode numbering only
// selects a candidate; it cannot prove identity across provider numbering.
func (s *Service) Episode(ctx context.Context, showID, season, episode, expectedTVDB int, language string) (*Presentation, error) {
	if showID <= 0 || season < 0 || episode <= 0 || expectedTVDB <= 0 {
		return nil, errors.New("unverified episode identity")
	}
	path := fmt.Sprintf("/tv/%d/season/%d/episode/%d", showID, season, episode)
	language = NormalizeLanguage(language)
	data, err := s.cached(ctx, fmt.Sprintf("episode:%s:%d:%s", path, expectedTVDB, language), func() ([]byte, error) {
		raw, err := s.fetch(ctx, path+"/external_ids", nil, "en-US")
		if err != nil {
			return nil, err
		}
		var ids struct {
			ID   int `json:"id"`
			TVDB int `json:"tvdb_id"`
		}
		if json.Unmarshal(raw, &ids) != nil || ids.ID <= 0 || ids.TVDB != expectedTVDB {
			return nil, errors.New("episode identity changed")
		}
		raw, err = s.Raw(ctx, path, nil, language)
		if err != nil {
			return nil, err
		}
		var value map[string]any
		if json.Unmarshal(raw, &value) != nil || number(value["id"]) != ids.ID || number(value["season_number"]) != season || number(value["episode_number"]) != episode {
			return nil, errors.New("episode identity changed")
		}
		return raw, nil
	})
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err = json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	out := presentation(value)
	out.TMDBID, out.MediaType = showID, "tv"
	return out, nil
}

func presentation(value map[string]any) *Presentation {
	title := textValue(value["title"])
	if title == "" {
		title = textValue(value["name"])
	}
	source := title
	if raw, exists := value["source_title"]; exists {
		source = textValue(raw)
	} else if raw, exists := value["source_name"]; exists {
		source = textValue(raw)
	}
	return &Presentation{Title: title, SourceTitle: source, Overview: textValue(value["overview"]), PosterPath: textValue(value["poster_path"]), BackdropPath: textValue(value["backdrop_path"])}
}
func textValue(value any) string { text, _ := value.(string); return text }
func number(value any) int       { n, _ := value.(float64); return int(n) }

var presentationFields = map[string]bool{"title": true, "name": true, "overview": true, "tagline": true, "biography": true, "poster_path": true, "backdrop_path": true, "still_path": true, "description": true}

func mergePresentation(preferred, fallback any) {
	switch p := preferred.(type) {
	case map[string]any:
		f, ok := fallback.(map[string]any)
		if !ok {
			return
		}
		if p["id"] != nil && f["id"] != nil && p["id"] != f["id"] {
			return
		}
		for _, key := range []string{"title", "name"} {
			if source := textValue(f[key]); source != "" {
				p["source_"+key] = source
			}
		}
		for key := range presentationFields {
			if strings.TrimSpace(textValue(p[key])) == "" && strings.TrimSpace(textValue(f[key])) != "" {
				p[key] = f[key]
			}
		}
		// TMDB language filters appended trailers. An empty localized trailer
		// set may use English videos without adding titles to any catalog list.
		if sourceVideos, ok := f["videos"].(map[string]any); ok {
			fallbackVideos, _ := sourceVideos["results"].([]any)
			videos, _ := p["videos"].(map[string]any)
			currentVideos, _ := videos["results"].([]any)
			if len(currentVideos) == 0 && len(fallbackVideos) > 0 {
				if videos == nil {
					videos = map[string]any{}
					p["videos"] = videos
				}
				videos["results"] = fallbackVideos
			}
		}
		for key, value := range p {
			if presentationFields[key] && strings.TrimSpace(textValue(value)) == "" && strings.TrimSpace(textValue(f[key])) != "" {
				p[key] = f[key]
				continue
			}
			mergePresentation(value, f[key])
		}
	case []any:
		f, ok := fallback.([]any)
		if !ok {
			return
		}
		byID := map[string]any{}
		for _, value := range f {
			if key := identityKey(value); key != "" {
				byID[key] = value
			}
		}
		for _, value := range p {
			if key := identityKey(value); key != "" {
				mergePresentation(value, byID[key])
			}
		}
	}
}
func identityKey(value any) string {
	m, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	if id := number(m["id"]); id > 0 {
		return textValue(m["media_type"]) + ":" + strconv.Itoa(id)
	}
	return ""
}

func fallbackOriginalNames(value any) {
	switch value := value.(type) {
	case map[string]any:
		for _, field := range []string{"title", "name"} {
			original := textValue(value["original_"+field])
			_, hasField := value[field]
			if !hasField && original == "" {
				continue
			}
			if original != "" && strings.TrimSpace(textValue(value[field])) == "" {
				value[field] = original
			}
			if textValue(value["source_"+field]) == "" {
				// Empty is intentional: callers must not match a library using
				// translated display text when canonical text is unavailable.
				value["source_"+field] = original
			}
		}
		for _, child := range value {
			fallbackOriginalNames(child)
		}
	case []any:
		for _, child := range value {
			fallbackOriginalNames(child)
		}
	}
}
