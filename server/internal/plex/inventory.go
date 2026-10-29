package plex

import (
	"context"
	"encoding/xml"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type inventoryMetadata struct {
	RatingKey   string `xml:"ratingKey,attr"`
	Type        string `xml:"type,attr"`
	Section     string `xml:"librarySectionID,attr"`
	Grandparent string `xml:"grandparentRatingKey,attr"`
	Index       *int   `xml:"index,attr"`
	ParentIndex *int   `xml:"parentIndex,attr"`
	GUID        string `xml:"guid,attr"`
	GUIDs       []struct {
		ID string `xml:"id,attr"`
	} `xml:"Guid"`
	Media []struct {
		ID     string `xml:"id,attr"`
		Width  int    `xml:"width,attr"`
		Height int    `xml:"height,attr"`
		Parts  []struct {
			ID         string `xml:"id,attr"`
			Key        string `xml:"key,attr"`
			Exists     string `xml:"exists,attr"`
			Accessible string `xml:"accessible,attr"`
		} `xml:"Part"`
	} `xml:"Media"`
}

func (m inventoryMetadata) ids() (int64, int64) {
	ids := map[string]string{}
	parse := func(raw string) {
		u, e := url.Parse(raw)
		if e != nil || u.Path != "" {
			return
		}
		switch u.Scheme {
		case "tmdb", "com.plexapp.agents.themoviedb":
			ids["tmdb"] = u.Host
		case "tvdb", "com.plexapp.agents.thetvdb":
			ids["tvdb"] = u.Host
		}
	}
	parse(m.GUID)
	for _, g := range m.GUIDs {
		parse(g.ID)
	}
	return mediaserver.VideoProviderIDs(ids)
}
func (m inventoryMetadata) copies() []mediaserver.InventoryCopy {
	copies := []mediaserver.InventoryCopy{}
	for _, media := range m.Media {
		for _, part := range media.Parts {
			playable := part.Key != "" && part.Exists != "0" && part.Accessible != "0" && !strings.EqualFold(part.Exists, "false") && !strings.EqualFold(part.Accessible, "false")
			copies = append(copies, mediaserver.InventoryCopy{SourceID: media.ID, PartID: part.ID, Width: media.Width, Height: media.Height, Playable: playable, Is4K: playable && (media.Width >= 3840 || media.Height >= 2160)})
		}
	}
	return copies
}
func (m inventoryMetadata) playable() (bool, bool) {
	found, uhd := false, false
	for _, copy := range m.copies() {
		found = found || copy.Playable
		uhd = uhd || copy.Is4K
	}
	return found, uhd
}
func inventoryLibraries(ctx context.Context, c *watchClient) ([]mediaserver.Library, error) {
	var data struct {
		XMLName  xml.Name `xml:"MediaContainer"`
		Sections []struct {
			Key  string `xml:"key,attr"`
			Name string `xml:"title,attr"`
			Type string `xml:"type,attr"`
		} `xml:"Directory"`
	}
	if e := c.get(ctx, "/library/sections", nil, &data); e != nil {
		return nil, e
	}
	out := []mediaserver.Library{}
	for _, s := range data.Sections {
		if !mediaserver.VideoLibrary(s.Type) {
			continue
		}
		if !inventoryNumericID(s.Key) {
			return nil, mediaserver.ErrItemUnverified
		}
		out = append(out, mediaserver.Library{ID: s.Key, Name: s.Name, CollectionType: s.Type})
	}
	return out, nil
}
func inventoryNumericID(id string) bool {
	n, e := strconv.ParseUint(id, 10, 64)
	return e == nil && n > 0
}
func (p *Provider) InventoryLibraries(ctx context.Context) ([]mediaserver.Library, error) {
	c, e := p.connectForWatch(ctx, p.token)
	if e != nil {
		return nil, e
	}
	return inventoryLibraries(ctx, c)
}
func inventoryPages(ctx context.Context, c *watchClient, path string, params url.Values) ([]inventoryMetadata, error) {
	params.Set("X-Plex-Container-Size", "100")
	params.Set("includeGuids", "1")
	all := []inventoryMetadata{}
	seen := map[string]bool{}
	total := -1
	for offset := 0; ; {
		params.Set("X-Plex-Container-Start", strconv.Itoa(offset))
		var page struct {
			XMLName     xml.Name            `xml:"MediaContainer"`
			Size        int                 `xml:"size,attr"`
			Offset      int                 `xml:"offset,attr"`
			Total       *int                `xml:"totalSize,attr"`
			Videos      []inventoryMetadata `xml:"Video"`
			Directories []inventoryMetadata `xml:"Directory"`
		}
		if e := c.get(ctx, path, params, &page); e != nil {
			return nil, e
		}
		rows := append(page.Videos, page.Directories...)
		if page.Size != len(rows) || page.Offset != offset || len(rows) > 100 || offset+len(rows) > 200000 {
			return nil, mediaserver.ErrItemUnverified
		}
		if page.Total != nil {
			if *page.Total < offset+len(rows) || *page.Total > 200000 || (total >= 0 && total != *page.Total) {
				return nil, mediaserver.ErrItemUnverified
			}
			total = *page.Total
		}
		for _, r := range rows {
			if !inventoryNumericID(r.RatingKey) || seen[r.RatingKey] {
				return nil, mediaserver.ErrItemUnverified
			}
			seen[r.RatingKey] = true
			all = append(all, r)
		}
		offset += len(rows)
		if total >= 0 && offset == total || total < 0 && len(rows) < 100 {
			return all, nil
		}
		if len(rows) == 0 {
			return nil, mediaserver.ErrItemUnverified
		}
	}
}
func inventoryOne(ctx context.Context, c *watchClient, id string) (inventoryMetadata, error) {
	if !inventoryNumericID(id) {
		return inventoryMetadata{}, mediaserver.ErrItemUnverified
	}
	var page struct {
		XMLName     xml.Name            `xml:"MediaContainer"`
		Videos      []inventoryMetadata `xml:"Video"`
		Directories []inventoryMetadata `xml:"Directory"`
	}
	if e := c.get(ctx, "/library/metadata/"+id, url.Values{"includeGuids": {"1"}}, &page); e != nil {
		return inventoryMetadata{}, e
	}
	rows := append(page.Videos, page.Directories...)
	if len(rows) != 1 || rows[0].RatingKey != id {
		return inventoryMetadata{}, mediaserver.ErrItemUnverified
	}
	return rows[0], nil
}
func (p *Provider) Inventory(ctx context.Context, libs []string, user string, q *mediaserver.ItemQuery) ([]mediaserver.InventoryItem, error) {
	return p.readInventory(ctx, libs, user, q, nil, time.Time{}, false)
}
func (p *Provider) VerifyInventory(ctx context.Context, libs []string, user string, q mediaserver.ItemQuery, candidates []mediaserver.InventoryItem) ([]mediaserver.InventoryItem, error) {
	return p.readInventory(ctx, libs, user, &q, candidates, time.Time{}, false)
}
func (p *Provider) InventorySince(ctx context.Context, libs []string, since time.Time) ([]mediaserver.InventoryItem, error) {
	return p.readInventory(ctx, libs, "", nil, nil, since, false)
}
func (p *Provider) readInventory(ctx context.Context, libs []string, user string, q *mediaserver.ItemQuery, candidates []mediaserver.InventoryItem, since time.Time, catalogOnly bool) ([]mediaserver.InventoryItem, error) {
	access := watchGrant{owner: true, token: p.token}
	var e error
	if user != "" {
		access, e = p.watchAccess(ctx, user)
		if e != nil {
			return nil, e
		}
	}
	c, e := p.connectForWatch(ctx, access.token)
	if e != nil {
		return nil, e
	}
	available, e := inventoryLibraries(ctx, c)
	if e != nil {
		return nil, e
	}
	valid := map[string]string{}
	for _, l := range available {
		valid[l.ID] = l.CollectionType
	}
	out := []mediaserver.InventoryItem{}
	for _, lib := range libs {
		if !inventoryNumericID(lib) {
			return nil, mediaserver.ErrItemUnverified
		}
		if !access.owner && !access.sections[lib] {
			continue
		}
		kind, ok := valid[lib]
		if !ok {
			return nil, mediaserver.ErrItemUnverified
		}
		rows := []inventoryMetadata{}
		ids := []string{}
		for _, v := range candidates {
			if v.LibraryID == lib {
				ids = append(ids, v.ItemID)
			}
		}
		if q != nil && len(candidates) > 0 && len(ids) == 0 {
			continue
		}
		if q != nil && len(ids) > 0 {
			for _, id := range ids {
				m, e := inventoryOne(ctx, c, id)
				if e != nil {
					return nil, e
				}
				if m.Section != lib {
					return nil, mediaserver.ErrItemUnverified
				}
				rows = append(rows, m)
			}
		} else {
			types := []string{"1", "2"}
			if kind == "movie" {
				types = []string{"1"}
			} else if kind == "show" {
				types = []string{"2"}
			}
			for _, typ := range types {
				params := url.Values{"type": {typ}}
				if q != nil {
					if q.MediaType == "movie" && typ != "1" || q.MediaType == "tv" && typ != "2" {
						continue
					}
					if q.Year > 0 {
						params.Set("year", strconv.Itoa(q.Year-1)+","+strconv.Itoa(q.Year)+","+strconv.Itoa(q.Year+1))
					} else if strings.TrimSpace(q.Title) != "" {
						params.Set("title", q.Title)
					} else {
						return nil, mediaserver.ErrItemUnverified
					}
				}
				if !since.IsZero() {
					if typ == "2" {
						params.Set("type", "4")
					}
					params.Set("addedAt>>", strconv.FormatInt(since.Unix(), 10))
					params.Set("sort", "addedAt:desc")
				}
				page, e := inventoryPages(ctx, c, "/library/sections/"+lib+"/all", params)
				if e != nil {
					return nil, e
				}
				rows = append(rows, page...)
			}
		}
		seen := map[string]bool{}
		for _, raw := range rows {
			m := raw
			if m.Type == "episode" {
				if seen[m.Grandparent] {
					continue
				}
				m, e = inventoryOne(ctx, c, m.Grandparent)
				if e != nil {
					return nil, e
				}
			}
			if m.Type != "movie" && m.Type != "show" {
				continue
			}
			if seen[m.RatingKey] {
				continue
			}
			seen[m.RatingKey] = true
			if m.Section != "" && m.Section != lib {
				return nil, mediaserver.ErrItemUnverified
			}
			tmdb, tvdb := m.ids()
			item := mediaserver.InventoryItem{LibraryID: lib, ItemID: m.RatingKey, MediaType: "movie", TMDBID: tmdb, TVDBID: tvdb}
			if m.Type == "show" {
				item.MediaType = "tv"
			}
			if q != nil && !mediaserver.InventoryMatches(item, *q) {
				continue
			}
			if m.Type == "movie" {
				item.Movie, item.Is4K = m.playable()
				item.Copies = m.copies()
			} else if !catalogOnly {
				eps, e := inventoryPages(ctx, c, "/library/metadata/"+m.RatingKey+"/allLeaves", url.Values{})
				if e != nil {
					return nil, e
				}
				for _, ep := range eps {
					playable, uhd := ep.playable()
					if !playable || ep.Type != "episode" || ep.Index == nil || ep.ParentIndex == nil || *ep.Index < 1 || *ep.ParentIndex < 0 {
						continue
					}
					if ep.Grandparent != "" && ep.Grandparent != m.RatingKey {
						return nil, mediaserver.ErrItemUnverified
					}
					etmdb, etvdb := ep.ids()
					item.Episodes = append(item.Episodes, mediaserver.InventoryEpisode{SeasonNumber: *ep.ParentIndex, EpisodeNumber: *ep.Index, TVDBID: etvdb, TMDBID: etmdb, SeriesTMDBID: tmdb, SeriesTVDBID: tvdb, Numbering: "unknown", LibraryID: lib, ItemID: ep.RatingKey, Is4K: uhd, Copies: ep.copies()})
					item.Is4K = item.Is4K || uhd
				}
			}
			out = append(out, item)
		}
	}
	if user != "" {
		current, e := p.watchAccess(ctx, user)
		if e != nil {
			return nil, e
		}
		if !reflect.DeepEqual(access, current) {
			return nil, mediaserver.ErrItemUnverified
		}
	}
	if q != nil && len(out) == 0 {
		return nil, mediaserver.ErrItemUnverified
	}
	return out, nil
}

func (p *Provider) InventoryCatalog(ctx context.Context, libs []string, user string) ([]mediaserver.InventoryItem, error) {
	return p.readInventory(ctx, libs, user, nil, nil, time.Time{}, true)
}

var (
	_ mediaserver.InventoryProvider    = (*Provider)(nil)
	_ mediaserver.InventoryVerifier    = (*Provider)(nil)
	_ mediaserver.InventoryCatalog     = (*Provider)(nil)
	_ mediaserver.IncrementalInventory = (*Provider)(nil)
)
