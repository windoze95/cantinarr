package musicdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type releaseGroup struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Type           string `json:"primary-type"`
	Date           string `json:"first-release-date"`
	Disambiguation string `json:"disambiguation"`
	Credits        []struct {
		Name   string `json:"name"`
		Join   string `json:"joinphrase"`
		Artist struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artist"`
	} `json:"artist-credit"`
}

func (r releaseGroup) album() (Album, error) {
	if !validID(r.ID) || strings.TrimSpace(r.Title) == "" {
		return Album{}, errors.New("invalid MusicBrainz release group")
	}
	var artist strings.Builder
	artists := []Artist{}
	for _, credit := range r.Credits {
		name := credit.Name
		if name == "" {
			name = credit.Artist.Name
		}
		if validID(credit.Artist.ID) {
			artists = append(artists, Artist{ForeignID: credit.Artist.ID, Name: name})
		}
		artist.WriteString(name)
		artist.WriteString(credit.Join)
	}
	return Album{ForeignID: r.ID, Title: r.Title, Artist: artist.String(), Artists: artists,
		ReleaseDate: r.Date, ReleaseType: releaseType(r.Type),
		Disambiguation: r.Disambiguation, Artwork: artworkPath(r.ID)}, nil
}

type groupSearch struct {
	Count  *int           `json:"count"`
	Offset *int           `json:"offset"`
	Groups []releaseGroup `json:"release-groups"`
}

func (s *Service) search(ctx context.Context, query string, offset, limit int) (groupSearch, error) {
	var result groupSearch
	params := url.Values{"query": {query}, "fmt": {"json"}, "limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	err := s.mb.get(ctx, "/release-group/?"+params.Encode(), &result)
	if err != nil {
		return result, err
	}
	if result.Count == nil || *result.Count < 0 || result.Offset == nil || *result.Offset != offset ||
		result.Groups == nil || len(result.Groups) > limit ||
		(len(result.Groups) == 0 && offset < *result.Count) {
		return result, errors.New("incomplete MusicBrainz search response")
	}
	return result, nil
}

// One OR query enriches a whole chart page. Missing IDs fail the page, rather
// than turning a provider failure into an apparently complete shorter chart.
func (s *Service) enrich(ctx context.Context, ids []string) (map[string]Album, error) {
	result := make(map[string]Album, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	query := "rgid:(" + strings.Join(ids, " OR ") + ")"
	raw, err := s.cache.get(ctx, "batch:"+query, 24*time.Hour, func(ctx context.Context) ([]byte, error) {
		search, err := s.search(ctx, query, 0, len(ids))
		if err != nil {
			return nil, err
		}
		byID := make(map[string]Album, len(ids))
		for _, rg := range search.Groups {
			album, err := rg.album()
			if err != nil {
				return nil, err
			}
			byID[album.ForeignID] = album
		}
		for _, id := range ids {
			if _, ok := byID[id]; !ok {
				return nil, errors.New("MusicBrainz could not resolve every chart entry; please retry")
			}
		}
		return json.Marshal(byID)
	})
	if err == nil {
		err = json.Unmarshal(raw, &result)
	}
	return result, err
}

func (s *Service) Album(ctx context.Context, id string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, InteractiveDeadline)
	defer cancel()
	if !validID(id) {
		return nil, errors.New("invalid MusicBrainz release-group ID")
	}
	return s.cache.get(ctx, "album:"+id, 24*time.Hour, func(ctx context.Context) ([]byte, error) {
		var rg releaseGroup
		path := "/release-group/" + id
		base, _ := url.Parse(s.mb.base)
		prefix := strings.TrimSuffix(base.Path, "/")
		resolvedPath := prefix + path
		if err := s.mb.get(ctx, path+"?inc=artists&fmt=json", &rg, &resolvedPath); err != nil {
			return nil, err
		}
		if resolvedPath != prefix+"/release-group/"+rg.ID {
			return nil, errors.New("MusicBrainz returned a different album identity")
		}
		album, err := rg.album()
		if err != nil {
			return nil, err
		}
		if album.ReleaseType == "" {
			return nil, errors.New("this release is not an album, EP, or single")
		}
		return json.Marshal(album)
	})
}

func (s *Service) genre(ctx context.Context, genre Genre, page int) (Page, error) {
	result := Page{Page: page, Results: []Album{}, Source: "MusicBrainz", Scope: genre.Name + " albums and EPs, in MusicBrainz matching order"}
	offset := (page - 1) * pageSize
	search, err := s.search(ctx, "tag:\""+genre.Tag+"\" AND (primarytype:album OR primarytype:ep)", offset, pageSize)
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, rg := range search.Groups {
		album, err := rg.album()
		if err != nil {
			return result, err
		}
		if albumType(album.ReleaseType) != "" && !seen[album.ForeignID] {
			result.Results = append(result.Results, album)
			seen[album.ForeignID] = true
		}
	}
	if offset+len(search.Groups) < *search.Count && page < maxPage {
		result.NextPage = page + 1
	}
	return result, nil
}

const InteractiveDeadline = 10 * time.Second

func (s *Service) Search(ctx context.Context, query string, page int, includeSingles ...bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, InteractiveDeadline)
	defer cancel()
	singles := len(includeSingles) > 0 && includeSingles[0]
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 300 || page < 1 || page > maxPage {
		return nil, errors.New("invalid music search")
	}
	return s.cache.get(ctx, "search:"+query+":"+strconv.Itoa(page)+":"+strconv.FormatBool(singles), 5*time.Minute, func(ctx context.Context) ([]byte, error) {
		// Quote user text so it cannot remove the album/EP constraint.
		escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(query)
		types := "primarytype:album OR primarytype:ep"
		scope := "MusicBrainz album and EP search"
		if singles {
			types += " OR primarytype:single"
			scope = "MusicBrainz album, EP, and single search"
		}
		found, err := s.search(ctx, `(releasegroup:"`+escaped+`" OR artist:"`+escaped+`") AND (`+types+`)`, (page-1)*pageSize, pageSize)
		if err != nil {
			return nil, err
		}
		out := Page{Page: page, Results: []Album{}, Source: "MusicBrainz", Scope: scope}
		for _, group := range found.Groups {
			album, err := group.album()
			if err != nil {
				return nil, err
			}
			if albumType(album.ReleaseType) != "" || (singles && album.ReleaseType == "Single") {
				out.Results = append(out.Results, album)
			}
		}
		if (page-1)*pageSize+len(found.Groups) < *found.Count && page < maxPage {
			out.NextPage = page + 1
		}
		if len(out.Results) == 0 {
			out.EmptyMessage = "No releases matched this page of " + scope + ". This does not search your library."
		}
		return json.Marshal(out)
	})
}

// ResolveAlbum accepts the provider's canonical release-group identity from an
// exact lookup. A release reference is explicitly converted to its group.
func (s *Service) ResolveAlbum(ctx context.Context, id string, isRelease bool) (Album, error) {
	if !validID(id) {
		return Album{}, errors.New("invalid MusicBrainz identity")
	}
	key := "resolved-group:" + id
	if isRelease {
		key = "resolved-release:" + id
	}
	body, err := s.cache.get(ctx, key, 24*time.Hour, func(ctx context.Context) ([]byte, error) {
		groupID := id
		if isRelease {
			var release struct {
				Group releaseGroup `json:"release-group"`
			}
			if err := s.mb.get(ctx, "/release/"+id+"?inc=release-groups&fmt=json", &release); err != nil {
				return nil, err
			}
			groupID = release.Group.ID
			if !validID(groupID) {
				return nil, errors.New("the release does not identify an album")
			}
		}
		return s.Album(ctx, groupID)
	})
	var album Album
	if err == nil {
		err = json.Unmarshal(body, &album)
	}
	return album, err
}
