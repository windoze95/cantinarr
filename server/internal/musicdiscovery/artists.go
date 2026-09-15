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

type mbArtist struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Disambiguation string `json:"disambiguation"`
	Type           string `json:"type"`
	Country        string `json:"country"`
}

func (a mbArtist) artist() (Artist, error) {
	if !validID(a.ID) || strings.TrimSpace(a.Name) == "" {
		return Artist{}, errors.New("invalid MusicBrainz artist")
	}
	return Artist{ForeignID: a.ID, Name: a.Name, Disambiguation: a.Disambiguation, Type: a.Type, Country: a.Country}, nil
}

func (s *Service) SearchArtists(ctx context.Context, query string, page int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, InteractiveDeadline)
	defer cancel()
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 300 || page < 1 || page > maxPage {
		return nil, errors.New("invalid artist search")
	}
	return s.cache.get(ctx, "artist-search:"+query+":"+strconv.Itoa(page), 5*time.Minute, func(ctx context.Context) ([]byte, error) {
		offset := (page - 1) * pageSize
		escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(query)
		params := url.Values{"query": {`artist:"` + escaped + `"`}, "fmt": {"json"}, "limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(offset)}}
		var raw struct {
			Count   *int       `json:"count"`
			Offset  *int       `json:"offset"`
			Artists []mbArtist `json:"artists"`
		}
		if err := s.mb.get(ctx, "/artist/?"+params.Encode(), &raw); err != nil {
			return nil, err
		}
		if raw.Count == nil || *raw.Count < 0 || raw.Offset == nil || *raw.Offset != offset || raw.Artists == nil || len(raw.Artists) > pageSize || (len(raw.Artists) == 0 && offset < *raw.Count) {
			return nil, errors.New("incomplete MusicBrainz artist search")
		}
		out := ArtistPage{Results: []Artist{}, Page: page, Source: "MusicBrainz", Scope: "MusicBrainz artist search, in relevance order"}
		for _, a := range raw.Artists {
			artist, err := a.artist()
			if err != nil {
				return nil, err
			}
			out.Results = append(out.Results, artist)
		}
		if offset+len(raw.Artists) < *raw.Count && page < maxPage {
			out.NextPage = page + 1
		}
		if len(out.Results) == 0 {
			out.EmptyMessage = "No artists matched this MusicBrainz page. This does not search your library."
		}
		return json.Marshal(out)
	})
}

func (s *Service) Artist(ctx context.Context, id string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, InteractiveDeadline)
	defer cancel()
	if !validID(id) {
		return nil, errors.New("invalid MusicBrainz artist ID")
	}
	return s.cache.get(ctx, "artist:"+id, 24*time.Hour, func(ctx context.Context) ([]byte, error) {
		var raw mbArtist
		resolved := ""
		if err := s.mb.get(ctx, "/artist/"+id+"?fmt=json", &raw, &resolved); err != nil {
			return nil, err
		}
		base, _ := url.Parse(s.mb.base)
		if resolved != strings.TrimSuffix(base.Path, "/")+"/artist/"+raw.ID {
			return nil, errors.New("MusicBrainz returned a different artist identity")
		}
		artist, err := raw.artist()
		if err != nil {
			return nil, err
		}
		return json.Marshal(artist)
	})
}

// Browse is keyed by the exact artist, including artists not present in Lidarr.
// Provider offsets refer to all returned rows, so filtering cannot skip a page.
func (s *Service) ArtistAlbums(ctx context.Context, id string, page int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, InteractiveDeadline)
	defer cancel()
	if !validID(id) || page < 1 || page > maxPage {
		return nil, errors.New("invalid artist discography page")
	}
	return s.cache.get(ctx, "discography:"+id+":"+strconv.Itoa(page), time.Hour, func(ctx context.Context) ([]byte, error) {
		offset := (page - 1) * pageSize
		params := url.Values{"artist": {id}, "type": {"album|ep|single"}, "inc": {"artist-credits"}, "fmt": {"json"}, "limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(offset)}}
		var raw struct {
			Count  *int           `json:"release-group-count"`
			Offset *int           `json:"release-group-offset"`
			Groups []releaseGroup `json:"release-groups"`
		}
		if err := s.mb.get(ctx, "/release-group?"+params.Encode(), &raw); err != nil {
			return nil, err
		}
		if raw.Count == nil || *raw.Count < 0 || raw.Offset == nil || *raw.Offset != offset || raw.Groups == nil || len(raw.Groups) > pageSize || (len(raw.Groups) == 0 && offset < *raw.Count) {
			return nil, errors.New("incomplete MusicBrainz discography")
		}
		out := Page{Page: page, Results: []Album{}, Source: "MusicBrainz", Scope: "Albums, EPs, and singles credited to MusicBrainz artist " + id}
		for _, r := range raw.Groups {
			a, err := r.album()
			if err != nil {
				return nil, err
			}
			if a.ReleaseType != "" {
				out.Results = append(out.Results, a)
			}
		}
		if offset+len(raw.Groups) < *raw.Count && page < maxPage {
			out.NextPage = page + 1
		}
		if len(out.Results) == 0 {
			out.EmptyMessage = "No releases on this page of this artist's MusicBrainz discography. This does not rule out library-only releases."
		}
		return json.Marshal(out)
	})
}
