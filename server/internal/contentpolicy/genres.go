package contentpolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// genreTable maps TMDB genre ids to their names for one media type.
type genreTable map[int]string

// builtinGenres is TMDB's genre list as shipped, the fallback when the live
// list cannot be read. Names only decide arr-record matches and Describe;
// ids are what the policy stores.
var builtinGenres = map[string]genreTable{
	MediaMovie: {
		28: "Action", 12: "Adventure", 16: "Animation", 35: "Comedy", 80: "Crime",
		99: "Documentary", 18: "Drama", 10751: "Family", 14: "Fantasy", 36: "History",
		27: "Horror", 10402: "Music", 9648: "Mystery", 10749: "Romance", 878: "Science Fiction",
		10770: "TV Movie", 53: "Thriller", 10752: "War", 37: "Western",
	},
	MediaTV: {
		10759: "Action & Adventure", 16: "Animation", 35: "Comedy", 80: "Crime", 99: "Documentary",
		18: "Drama", 10751: "Family", 10762: "Kids", 9648: "Mystery", 10763: "News",
		10764: "Reality", 10765: "Sci-Fi & Fantasy", 10766: "Soap", 10767: "Talk",
		10768: "War & Politics", 37: "Western",
	},
}

const genresTTL = 24 * time.Hour

// genres returns the live genre table for a media type, cached for a day,
// falling back to the built-in table. It never fails: a name is only ever a
// convenience on top of the id the policy stores.
func (s *Service) genres(ctx context.Context, mediaType string) genreTable {
	key := "certgenres:" + mediaType
	if data, ok := s.cache.Get(key); ok {
		if table, err := decodeGenreTable(data); err == nil {
			return table
		}
	}
	getter := s.getter()
	if getter == nil {
		return builtinGenres[mediaType]
	}
	data, err := s.get(ctx, getter, "/genre/"+mediaType+"/list")
	if err != nil {
		return builtinGenres[mediaType]
	}
	table, err := decodeGenreTable(data)
	if err != nil || len(table) == 0 {
		return builtinGenres[mediaType]
	}
	s.cache.Set(key, data, genresTTL)
	return table
}

func decodeGenreTable(data []byte) (genreTable, error) {
	var out struct {
		Genres []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"genres"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode genre list: %w", err)
	}
	table := genreTable{}
	for _, g := range out.Genres {
		if g.ID > 0 && strings.TrimSpace(g.Name) != "" {
			table[g.ID] = g.Name
		}
	}
	return table, nil
}

// genreSynonyms maps a normalised genre token to the other spellings the
// arrs and TheTVDB use for it, so a hidden TMDB genre still matches a Sonarr
// record whose metadata came from elsewhere.
var genreSynonyms = map[string][]string{
	"science fiction": {"sci-fi", "scifi", "sci fi"},
	"sci-fi":          {"science fiction", "scifi", "sci fi"},
	"kids":            {"children", "children's", "childrens"},
	"children":        {"kids", "children's", "childrens"},
	"talk":            {"talk show"},
	"talk show":       {"talk"},
	"tv movie":        {"television movie"},
}

// genreAliases turns a genre name into the normalised tokens it can be
// matched by: lower-cased, split on the separators compound names use
// ("Sci-Fi & Fantasy" is both "sci-fi" and "fantasy"), plus known synonyms.
func genreAliases(name string) []string {
	var out []string
	seen := map[string]struct{}{}
	add := func(token string) {
		token = strings.TrimSpace(strings.ToLower(token))
		if token == "" {
			return
		}
		if _, dup := seen[token]; dup {
			return
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	whole := strings.TrimSpace(strings.ToLower(name))
	add(whole)
	for _, part := range strings.FieldsFunc(whole, func(r rune) bool { return r == '&' || r == '/' || r == ',' }) {
		add(part)
		for _, syn := range genreSynonyms[strings.TrimSpace(part)] {
			add(syn)
		}
	}
	for _, syn := range genreSynonyms[whole] {
		add(syn)
	}
	return out
}
