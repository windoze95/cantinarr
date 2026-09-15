package musicdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *Service) popular(ctx context.Context, period string, page int) (Page, error) {
	result := Page{Page: page, Results: []Album{}, Source: "ListenBrainz",
		Scope: "Popular albums and EPs " + strings.ReplaceAll(period, "_", " ")}
	offset := (page - 1) * pageSize
	var chart struct {
		Payload struct {
			Groups []struct {
				ID string `json:"release_group_mbid"`
			} `json:"release_groups"`
			Total  *int `json:"total_release_group_count"`
			Offset *int `json:"offset"`
		} `json:"payload"`
	}
	params := url.Values{"range": {period}, "count": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(offset)}}
	if err := s.lb.get(ctx, "/stats/sitewide/release-groups?"+params.Encode(), &chart); err != nil {
		return result, err
	}
	p := chart.Payload
	if p.Groups == nil || p.Total == nil || *p.Total < 0 || p.Offset == nil || *p.Offset != offset ||
		len(p.Groups) > pageSize || (len(p.Groups) == 0 && offset < *p.Total) {
		return result, errors.New("incomplete ListenBrainz chart response")
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, group := range p.Groups {
		// ListenBrainz can include unidentified listens; they cannot address
		// an album. A nonempty malformed identity is a provider error.
		if group.ID == "" {
			continue
		}
		if !validID(group.ID) {
			return result, errors.New("invalid ListenBrainz release group")
		}
		if !seen[group.ID] {
			ids = append(ids, group.ID)
			seen[group.ID] = true
		}
	}
	albums, err := s.enrich(ctx, ids)
	if err != nil {
		return result, err
	}
	for _, id := range ids {
		if album := albums[id]; albumType(album.ReleaseType) != "" {
			result.Results = append(result.Results, album)
		}
	}
	// Continuation follows the upstream position, including duplicate IDs and
	// singles filtered out above. A short displayed page is not exhaustion.
	if offset+len(p.Groups) < *p.Total && page < maxPage {
		result.NextPage = page + 1
	}
	return result, nil
}

type freshRelease struct {
	ID     string `json:"release_group_mbid"`
	Title  string `json:"release_name"`
	Artist string `json:"artist_credit_name"`
	Date   string `json:"release_date"`
	Type   string `json:"release_group_primary_type"`
}

func (s *Service) fresh(ctx context.Context, page int, today time.Time) (Page, error) {
	result := Page{Page: page, Results: []Album{}, Source: "ListenBrainz", Scope: "Albums and EPs released in the past 30 days"}
	raw, err := s.cache.get(ctx, "fresh-window:"+today.Format(time.DateOnly), time.Hour, func(ctx context.Context) ([]byte, error) {
		var response struct {
			Payload struct {
				Releases []freshRelease `json:"releases"`
			} `json:"payload"`
		}
		params := url.Values{"release_date": {today.Format(time.DateOnly)}, "days": {"30"}, "past": {"true"}, "future": {"false"}, "sort": {"release_date"}}
		if err := s.lb.get(ctx, "/explore/fresh-releases/?"+params.Encode(), &response); err != nil {
			return nil, err
		}
		if response.Payload.Releases == nil {
			return nil, errors.New("incomplete ListenBrainz fresh releases response")
		}
		albums, err := normalizeFresh(response.Payload.Releases, today)
		if err != nil {
			return nil, err
		}
		return json.Marshal(albums)
	})
	if err != nil {
		return result, err
	}
	var albums []Album
	if err := json.Unmarshal(raw, &albums); err != nil {
		return result, err
	}
	start := (page - 1) * pageSize
	if start < len(albums) {
		result.Results = albums[start:min(start+pageSize, len(albums))]
	}
	if start+pageSize < len(albums) && page < maxPage {
		result.NextPage = page + 1
	}
	return result, nil
}

func normalizeFresh(releases []freshRelease, today time.Time) ([]Album, error) {
	albums := []Album{}
	// Today plus the preceding 29 calendar days is exactly 30 days.
	from := today.AddDate(0, 0, -29)
	for _, release := range releases {
		kind := albumType(release.Type)
		if kind == "" || release.ID == "" {
			continue
		}
		if !validID(release.ID) || strings.TrimSpace(release.Title) == "" {
			return nil, errors.New("invalid ListenBrainz fresh release")
		}
		date, err := time.Parse(time.DateOnly, release.Date)
		// Imprecise dates cannot establish membership in this date window.
		if err != nil || date.Before(from) || date.After(today) {
			continue
		}
		albums = append(albums, Album{ForeignID: release.ID, Title: release.Title, Artist: release.Artist,
			ReleaseDate: release.Date, ReleaseType: kind, Artwork: artworkPath(release.ID)})
	}
	sort.SliceStable(albums, func(i, j int) bool { return albums[i].ReleaseDate > albums[j].ReleaseDate })
	seen := map[string]bool{}
	unique := []Album{}
	for _, album := range albums {
		if !seen[album.ForeignID] {
			unique = append(unique, album)
			seen[album.ForeignID] = true
		}
	}
	return unique, nil
}
