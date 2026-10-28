package discover

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/mediametadata"
)

func metadataCacheKey(key, language string) string {
	if language == "" {
		return key
	}
	return key + ":metadata:" + language
}

type metadataGetter struct {
	ctx      context.Context
	service  *mediametadata.Service
	language string
}

func (g metadataGetter) DoGetRaw(path string, params url.Values) ([]byte, error) {
	return g.service.Raw(g.ctx, path, params, g.language)
}

// Each row retains its source identity, rank, dates, and availability data.
// Only presentation fields are overlaid; policies still run on the final body.
func (h *Handler) localizeFeatured(ctx context.Context, rows []json.RawMessage, kind, language string) []json.RawMessage {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := append([]json.RawMessage(nil), rows...)
	var wg sync.WaitGroup
	for i, raw := range rows {
		wg.Add(1)
		go func(i int, raw json.RawMessage) {
			defer wg.Done()
			var value map[string]any
			if json.Unmarshal(raw, &value) != nil {
				return
			}
			p, err := h.metadata.Presentation(ctx, kind, metadataNumber(value["id"]), language)
			if err != nil {
				return
			}
			field := "title"
			if kind == "tv" {
				field = "name"
			}
			overlayMetadata(value, p, field)
			if data, err := json.Marshal(value); err == nil {
				out[i] = data
			}
		}(i, raw)
	}
	wg.Wait()
	return out
}

func overlayMetadata(value map[string]any, p *mediametadata.Presentation, titleField string) {
	source, _ := value[titleField].(string)
	if source == "" {
		source = p.SourceTitle
	}
	value["source_"+titleField] = source
	if p.Title != "" {
		value[titleField] = p.Title
	}
	if p.Overview != "" {
		value["overview"] = p.Overview
	}
	if p.PosterPath != "" {
		value["poster_path"] = p.PosterPath
	}
	if p.BackdropPath != "" {
		value["backdrop_path"] = p.BackdropPath
	}
}

func (h *Handler) localizeTrakt(ctx context.Context, raw []byte, language, kind string) []byte {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var rows []map[string]any
	if json.Unmarshal(raw, &rows) != nil {
		return raw
	}
	var wg sync.WaitGroup
	for _, row := range rows {
		wg.Add(1)
		go func(row map[string]any) {
			defer wg.Done()
			media := row
			mediaKind := kind
			if nested, ok := row["movie"].(map[string]any); ok {
				media = nested
				mediaKind = "movie"
			}
			if nested, ok := row["show"].(map[string]any); ok {
				media = nested
				mediaKind = "tv"
			}
			if mediaKind == "" {
				if typ, _ := row["type"].(string); typ == "movie" {
					mediaKind = "movie"
				} else if typ == "show" {
					mediaKind = "tv"
				}
			}
			ids, _ := media["ids"].(map[string]any)
			id := metadataNumber(ids["tmdb"])
			if id <= 0 || (mediaKind != "movie" && mediaKind != "tv") {
				return
			}
			p, err := h.metadata.Presentation(ctx, mediaKind, id, language)
			if err == nil {
				source, _ := media["title"].(string)
				if source == "" {
					source = p.SourceTitle
				}
				media["source_title"] = source
				if p.Title != "" {
					media["title"] = p.Title
				}
				if p.Overview != "" {
					media["overview"] = p.Overview
				}
				images, _ := media["images"].(map[string]any)
				if images == nil {
					images = map[string]any{}
				}
				if p.PosterPath != "" {
					images["poster"] = []string{tmdbPresentationImage(p.PosterPath, "w500")}
				}
				if p.BackdropPath != "" {
					images["fanart"] = []string{tmdbPresentationImage(p.BackdropPath, "w1280")}
				}
				media["images"] = images
			}
			// Calendar episodes are translated only when the external episode ID
			// confirms that this is the same episode across the two catalogs.
			if mediaKind == "tv" {
				if ep, ok := row["episode"].(map[string]any); ok {
					epIDs, _ := ep["ids"].(map[string]any)
					p, err := h.metadata.Episode(ctx, id, metadataNumber(ep["season"]), metadataNumber(ep["number"]), metadataNumber(epIDs["tvdb"]), language)
					if err == nil {
						source, _ := ep["title"].(string)
						if source == "" {
							source = p.SourceTitle
						}
						ep["source_title"] = source
						if p.Title != "" {
							ep["title"] = p.Title
						}
						if p.Overview != "" {
							ep["overview"] = p.Overview
						}
					}
				}
			}
		}(row)
	}
	wg.Wait()
	localized, err := json.Marshal(rows)
	if err != nil {
		return raw
	}
	return localized
}
func metadataNumber(value any) int { n, _ := value.(float64); return int(n) }
func tmdbPresentationImage(path, size string) string {
	if strings.HasPrefix(path, "/") {
		return "https://image.tmdb.org/t/p/" + size + path
	}
	return path
}
