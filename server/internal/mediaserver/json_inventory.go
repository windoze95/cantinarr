package mediaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// JSONInventory shares only read-side DTO/pagination code between Jellyfin
// and Emby; transport, authentication, and library IDs stay provider-owned.
type JSONInventory struct {
	Read                func(context.Context, string, any) error
	Access              func(context.Context, string) (map[string]any, error)
	Libraries           func(context.Context) ([]Library, error)
	UserPath            bool
	ExactProviderFilter bool
}

type inventoryID string

func (id *inventoryID) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*id = inventoryID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*id = inventoryID(n.String())
	return nil
}

type inventoryVideo struct {
	ID                inventoryID       `json:"Id"`
	Type              string            `json:"Type"`
	SeriesID          inventoryID       `json:"SeriesId"`
	ProviderIDs       map[string]string `json:"ProviderIds"`
	IndexNumber       *int              `json:"IndexNumber"`
	ParentIndexNumber *int              `json:"ParentIndexNumber"`
	IndexNumberEnd    *int              `json:"IndexNumberEnd"`
	IsVirtualItem     bool              `json:"IsVirtualItem"`
	IsPlaceHolder     bool              `json:"IsPlaceHolder"`
	LocationType      string            `json:"LocationType"`
	MediaSources      []struct {
		ID           inventoryID       `json:"Id"`
		MediaStreams []inventoryStream `json:"MediaStreams"`
	} `json:"MediaSources"`
	MediaStreams []inventoryStream `json:"MediaStreams"`
}
type inventoryStream struct {
	Type   string `json:"Type"`
	Width  int    `json:"Width"`
	Height int    `json:"Height"`
}

func VideoProviderIDs(ids map[string]string) (tmdb, tvdb int64) {
	for k, v := range ids {
		switch strings.ToLower(k) {
		case "tmdb", "themoviedb":
			tmdb, _ = strconv.ParseInt(v, 10, 64)
		case "tvdb":
			tvdb, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	return
}
func (v inventoryVideo) copies() []InventoryCopy {
	copies := []InventoryCopy{}
	available := !v.IsVirtualItem && !v.IsPlaceHolder && !strings.EqualFold(v.LocationType, "Virtual")
	add := func(id string, streams []inventoryStream) {
		copy := InventoryCopy{SourceID: id}
		for _, stream := range streams {
			if strings.EqualFold(stream.Type, "Video") {
				copy.Playable = available
				if stream.Width > copy.Width {
					copy.Width = stream.Width
				}
				if stream.Height > copy.Height {
					copy.Height = stream.Height
				}
			}
		}
		copy.Is4K = copy.Playable && (copy.Width >= 3840 || copy.Height >= 2160)
		copies = append(copies, copy)
	}
	for _, source := range v.MediaSources {
		streams := source.MediaStreams
		if len(streams) == 0 && len(v.MediaSources) == 1 {
			streams = v.MediaStreams
		}
		add(string(source.ID), streams)
	}
	if len(v.MediaSources) == 0 && len(v.MediaStreams) > 0 {
		add(string(v.ID), v.MediaStreams)
	}
	return copies
}
func (v inventoryVideo) playable() (bool, bool) {
	playable, uhd := false, false
	for _, copy := range v.copies() {
		playable = playable || copy.Playable
		uhd = uhd || copy.Is4K
	}
	return playable, uhd
}
func (j JSONInventory) pages(ctx context.Context, user string, p url.Values) ([]inventoryVideo, error) {
	path := "/Items"
	if user != "" {
		if j.UserPath {
			path = "/Users/" + url.PathEscape(user) + "/Items"
		} else {
			p.Set("UserId", user)
		}
	}
	p.Set("Fields", "ProviderIds,MediaSources,MediaStreams,ParentId")
	p.Set("EnableImages", "false")
	p.Set("EnableUserData", "false")
	p.Set("EnableTotalRecordCount", "true")
	p.Set("Limit", "200")
	p.Set("SortBy", "SortName")
	p.Set("SortOrder", "Ascending")
	var all []inventoryVideo
	seen := map[string]bool{}
	total := -1
	for start := 0; ; {
		p.Set("StartIndex", strconv.Itoa(start))
		var page struct {
			Items []inventoryVideo `json:"Items"`
			Total *int             `json:"TotalRecordCount"`
		}
		if err := j.Read(ctx, path+"?"+p.Encode(), &page); err != nil {
			return nil, err
		}
		if page.Total == nil || *page.Total < 0 || *page.Total > 200000 || (total >= 0 && total != *page.Total) || len(page.Items) > 200 || start+len(page.Items) > *page.Total {
			return nil, ErrItemUnverified
		}
		total = *page.Total
		for _, item := range page.Items {
			if item.ID == "" || seen[string(item.ID)] {
				return nil, ErrItemUnverified
			}
			seen[string(item.ID)] = true
			all = append(all, item)
		}
		start += len(page.Items)
		if start == total {
			return all, nil
		}
		if len(page.Items) == 0 {
			return nil, ErrItemUnverified
		}
	}
}

// Run uses exact native IDs from the scan when available. A narrowed
// title/year query without a result cannot assert library-wide absence.
func (j JSONInventory) Run(ctx context.Context, libs []string, user string, q *ItemQuery, candidates []InventoryItem, since time.Time, catalogOnly bool) ([]InventoryItem, error) {
	var before map[string]any
	var err error
	if user != "" {
		before, err = j.Access(ctx, user)
		if err != nil {
			return nil, err
		}
	}
	available, err := j.Libraries(ctx)
	if err != nil {
		return nil, err
	}
	valid := map[string]bool{}
	for _, l := range available {
		if VideoLibrary(l.CollectionType) {
			valid[l.ID] = true
		}
	}
	out := []InventoryItem{}
	for _, lib := range libs {
		if !valid[lib] {
			return nil, ErrItemUnverified
		}
		p := url.Values{"ParentId": {lib}, "Recursive": {"true"}, "IncludeItemTypes": {"Movie,Series"}}
		ids := []string{}
		for _, c := range candidates {
			if c.LibraryID == lib {
				ids = append(ids, c.ItemID)
			}
		}
		if q != nil && len(candidates) > 0 && len(ids) == 0 {
			continue
		}
		if q != nil {
			if len(ids) > 0 {
				p.Set("Ids", strings.Join(ids, ","))
			} else if j.ExactProviderFilter {
				parts := []string{}
				if q.TMDBID > 0 {
					parts = append(parts, fmt.Sprintf("tmdb.%d", q.TMDBID))
				}
				if q.MediaType == "tv" && q.TVDBID > 0 {
					parts = append(parts, fmt.Sprintf("tvdb.%d", q.TVDBID))
				}
				p.Set("AnyProviderIdEquals", strings.Join(parts, ","))
			} else if q.Year > 0 {
				p.Set("Years", fmt.Sprintf("%d,%d,%d", q.Year-1, q.Year, q.Year+1))
			} else if strings.TrimSpace(q.Title) != "" {
				p.Set("SearchTerm", q.Title)
			} else {
				return nil, ErrItemUnverified
			}
		}
		if !since.IsZero() {
			p.Set("MinDateLastSaved", since.UTC().Format(time.RFC3339))
			p.Set("IncludeItemTypes", "Movie,Series,Episode")
		}
		rows, err := j.pages(ctx, user, p)
		if err != nil {
			return nil, err
		}
		// Episode updates refresh the complete parent, including newly imported
		// episodes. This replaces that show's inventory atomically.
		parents := map[string]bool{}
		for _, row := range rows {
			if row.Type == "Series" {
				parents[string(row.ID)] = true
			}
		}
		for _, row := range append([]inventoryVideo{}, rows...) {
			if row.Type == "Episode" && row.SeriesID != "" && !parents[string(row.SeriesID)] {
				parents[string(row.SeriesID)] = true
				extra, e := j.pages(ctx, user, url.Values{"ParentId": {lib}, "Recursive": {"true"}, "IncludeItemTypes": {"Series"}, "Ids": {string(row.SeriesID)}})
				if e != nil {
					return nil, e
				}
				rows = append(rows, extra...)
			}
		}
		for _, row := range rows {
			if row.Type != "Movie" && row.Type != "Series" {
				continue
			}
			tmdb, tvdb := VideoProviderIDs(row.ProviderIDs)
			item := InventoryItem{LibraryID: lib, ItemID: string(row.ID), MediaType: "movie", TMDBID: tmdb, TVDBID: tvdb}
			if row.Type == "Series" {
				item.MediaType = "tv"
			}
			if q != nil && !InventoryMatches(item, *q) {
				continue
			}
			if row.Type == "Movie" {
				item.Movie, item.Is4K = row.playable()
				item.Copies = row.copies()
			} else if !catalogOnly {
				eps, e := j.pages(ctx, user, url.Values{"ParentId": {string(row.ID)}, "Recursive": {"true"}, "IncludeItemTypes": {"Episode"}})
				if e != nil {
					return nil, e
				}
				for _, ep := range eps {
					playable, uhd := ep.playable()
					if !playable || ep.IndexNumber == nil || ep.ParentIndexNumber == nil || *ep.IndexNumber < 1 || *ep.ParentIndexNumber < 0 {
						continue
					}
					if ep.SeriesID != "" && ep.SeriesID != row.ID {
						return nil, ErrItemUnverified
					}
					etmdb, etvdb := VideoProviderIDs(ep.ProviderIDs)
					end := *ep.IndexNumber
					if ep.IndexNumberEnd != nil {
						end = *ep.IndexNumberEnd
					}
					if end < *ep.IndexNumber || end-*ep.IndexNumber > 100 {
						return nil, ErrItemUnverified
					}
					for n := *ep.IndexNumber; n <= end; n++ {
						e := InventoryEpisode{SeasonNumber: *ep.ParentIndexNumber, EpisodeNumber: n, TMDBID: etmdb, TVDBID: etvdb, SeriesTMDBID: tmdb, SeriesTVDBID: tvdb, Numbering: "unknown", LibraryID: lib, ItemID: string(ep.ID), Is4K: uhd, Copies: ep.copies()}
						if n != *ep.IndexNumber {
							e.TMDBID = 0
							e.TVDBID = 0
						}
						item.Episodes = append(item.Episodes, e)
					}
					item.Is4K = item.Is4K || uhd
				}
			}
			out = append(out, item)
		}
	}
	if user != "" {
		after, e := j.Access(ctx, user)
		if e != nil {
			return nil, e
		}
		if !reflect.DeepEqual(before, after) {
			return nil, ErrItemUnverified
		}
	}
	if q != nil && len(out) == 0 && !j.ExactProviderFilter {
		return nil, ErrItemUnverified
	}
	return out, nil
}
