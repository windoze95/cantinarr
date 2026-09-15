package request

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/musicdiscovery"
)

type UnifiedMusicAlbum struct {
	musicdiscovery.Album
	ForeignAlbumID string `json:"foreign_album_id"`
	InstanceID     string `json:"instance_id,omitempty"`
	SearchTerm     string `json:"search_term"`
	RecordID       int    `json:"record_id,omitempty"`
	Status         string `json:"status,omitempty"`
	StatusKnown    bool   `json:"status_known"`
}
type UnifiedMusicPage struct {
	Results  []UnifiedMusicAlbum `json:"results"`
	Page     int                 `json:"page"`
	NextPage int                 `json:"next_page,omitempty"`
	Warnings []string            `json:"warnings"`
	Scope    string              `json:"scope"`
}

// UnifiedMusicSearch joins live records and saved intent by ID without
// rearranging catalog relevance. Every dependency shares the interactive
// deadline; partial provider failures remain explicit beside usable results.
func (s *Service) UnifiedMusicSearch(ctx context.Context, userID int64, query, instanceID string, page int) (*UnifiedMusicPage, error) {
	ctx, cancel := context.WithTimeout(ctx, musicdiscovery.InteractiveDeadline)
	defer cancel()
	if err := s.authorizeCatalogMetadata(userID, "music", instanceID); err != nil {
		return nil, err
	}
	client, resolved, err := s.resolveLidarr(userID, instanceID)
	if err != nil {
		return nil, err
	}
	if client != nil {
		instanceID = resolved
		client = client.WithContext(ctx)
	}
	var catalog musicdiscovery.Page
	var digest *MusicLibraryDigest
	var catalogErr, libraryErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		body, err := s.MusicCatalog.Search(ctx, query, page, true)
		catalogErr = err
		if err == nil {
			catalogErr = json.Unmarshal(body, &catalog)
		}
	}()
	if client != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			albums, err := client.GetAllAlbums()
			libraryErr = err
			if err == nil {
				d := reduceMusicLibrary(albums)
				digest = &d
			}
		}()
	}
	saved := []SavedMusicRequest{}
	var savedErr error
	if client != nil {
		saved, savedErr = s.SavedMusicRequests(userID, instanceID)
	}
	wg.Wait()
	if err := s.authorizeCatalogMetadata(userID, "music", instanceID); err != nil {
		return nil, err
	}
	out := &UnifiedMusicPage{Results: []UnifiedMusicAlbum{}, Page: page, NextPage: catalog.NextPage, Warnings: []string{}, Scope: "MusicBrainz albums, EPs, and singles in relevance order, supplemented by matching records in the selected library; identity matches use release-group IDs only"}
	if catalogErr != nil {
		out.Warnings = append(out.Warnings, "The album catalog could not be searched.")
	}
	if libraryErr != nil {
		out.Warnings = append(out.Warnings, "Library availability could not be checked.")
	}
	if savedErr != nil {
		out.Warnings = append(out.Warnings, "Saved requests could not be checked.")
	}
	used := map[int]bool{}
	for _, album := range catalog.Results {
		row := UnifiedMusicAlbum{Album: album, ForeignAlbumID: album.ForeignID, InstanceID: instanceID, SearchTerm: query, StatusKnown: digest != nil}
		identities := verifiedMusicIDs(album.ForeignID, saved)
		if digest != nil {
			row.Status = StatusUnavailable
			for i, record := range digest.Titles {
				if identities[record.ForeignAlbumID] && !used[i] {
					used[i] = true
					row.RecordID = record.RecordID
					row.Status = record.Status
					break
				}
			}
		}
		out.Results = append(out.Results, row)
	}
	if digest != nil {
		for i, record := range digest.Titles {
			if used[i] || record.ForeignAlbumID == "" || !musicQueryMatches(query, record.Artist+" "+record.Title) {
				continue
			}
			album := musicdiscovery.Album{ForeignID: record.ForeignAlbumID, Title: record.Title, Artist: record.Artist, ReleaseType: record.ReleaseType, Artwork: "/api/discover/music/artwork/" + record.ForeignAlbumID}
			if record.Year > 0 {
				album.ReleaseDate = time.Date(record.Year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006")
			}
			if record.ForeignArtistID != "" {
				album.Artists = []musicdiscovery.Artist{{ForeignID: record.ForeignArtistID, Name: record.Artist}}
			}
			out.Results = append(out.Results, UnifiedMusicAlbum{Album: album, ForeignAlbumID: album.ForeignID, InstanceID: instanceID, SearchTerm: query, RecordID: record.RecordID, Status: record.Status, StatusKnown: true})
		}
	}
	for i := range out.Results {
		row := &out.Results[i]
		if row.Status == StatusAvailable || row.Status == StatusDownloading {
			continue
		}
		identities := verifiedMusicIDs(row.ForeignID, saved)
		for _, r := range saved {
			match := identities[r.ForeignID] || identities[r.CanonicalForeignID] || (r.CatalogRef != nil && r.CatalogRef.Provider == "musicbrainz" && identities[r.CatalogRef.ID])
			if !match {
				continue
			}
			for _, d := range r.Delivery {
				if d.State == "approval" {
					row.Status = StatusPending
				} else if d.State != "complete" && d.State != "cancelled" {
					row.Status = StatusRequested
				}
			}
			if len(r.Delivery) == 0 && r.Status == StatusPending {
				row.Status = StatusPending
			}
			break
		}
	}
	return out, nil
}
func musicQueryMatches(query, title string) bool {
	title = strings.ToLower(title)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(title, word) {
			return false
		}
	}
	return strings.TrimSpace(query) != ""
}

// Aliases require a canonical identity persisted by the delivery worker.
func verifiedMusicIDs(id string, saved []SavedMusicRequest) map[string]bool {
	ids := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for _, row := range saved {
			if row.CanonicalForeignID == "" {
				continue
			}
			aliases := []string{row.CanonicalForeignID, row.ForeignID}
			if row.CatalogRef != nil && row.CatalogRef.Provider == "musicbrainz" {
				aliases = append(aliases, row.CatalogRef.ID)
			}
			intersects := false
			for _, alias := range aliases {
				intersects = intersects || ids[alias]
			}
			if intersects {
				for _, alias := range aliases {
					if alias != "" && !ids[alias] {
						ids[alias] = true
						changed = true
					}
				}
			}
		}
	}
	return ids
}
