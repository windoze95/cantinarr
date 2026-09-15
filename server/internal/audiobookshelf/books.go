package audiobookshelf

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

var asinPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)

func normalizedASIN(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if !asinPattern.MatchString(value) {
		return ""
	}
	return value
}

type bookIdentifiers struct {
	asins, isbns map[string]bool
	terms        []string
}

func identifiers(q mediaserver.BookQuery) bookIdentifiers {
	out := bookIdentifiers{asins: map[string]bool{}, isbns: map[string]bool{}}
	terms := map[string]bool{}
	for _, raw := range q.ASINs {
		if value := normalizedASIN(raw); value != "" {
			out.asins[value], terms[value] = true, true
		}
	}
	for _, raw := range q.ISBNs {
		value := chaptarr.NormalizeISBN(raw)
		if value == "" {
			continue
		}
		out.isbns[value], terms[value] = true, true
		// A 978 ISBN has an ISBN-10 spelling. Search both; compare canonical
		// ISBN-13 values so punctuation and checksum-equivalent forms agree.
		if strings.HasPrefix(value, "978") {
			base, sum := value[3:12], 0
			for i, digit := range base {
				sum += (10 - i) * int(digit-'0')
			}
			check := (11 - sum%11) % 11
			last := strconv.Itoa(check)
			if check == 10 {
				last = "X"
			}
			terms[base+last] = true
		}
	}
	for term := range terms {
		out.terms = append(out.terms, term)
	}
	sort.Strings(out.terms)
	return out
}

type bookItem struct {
	ID        string `json:"id"`
	LibraryID string `json:"libraryId"`
	MediaType string `json:"mediaType"`
	Missing   *bool  `json:"isMissing"`
	Invalid   *bool  `json:"isInvalid"`
	Media     struct {
		Tags   []string `json:"tags"`
		Tracks []struct {
			Duration float64 `json:"duration"`
		} `json:"tracks"`
		Metadata struct {
			Title     string   `json:"title"`
			ASIN      string   `json:"asin"`
			ISBN      string   `json:"isbn"`
			Explicit  *bool    `json:"explicit"`
			Narrators []string `json:"narrators"`
		} `json:"metadata"`
	} `json:"media"`
}

func (ids bookIdentifiers) matches(item bookItem) bool {
	return ids.asins[normalizedASIN(item.Media.Metadata.ASIN)] || ids.isbns[chaptarr.NormalizeISBN(item.Media.Metadata.ISBN)]
}

func (u user) canReadLibrary(id string) bool {
	return u.Active != nil && *u.Active && u.Permissions.AccessAllLibraries != nil &&
		(*u.Permissions.AccessAllLibraries || slices.Contains(u.Libraries, id))
}

// Match User.checkCanAccessLibraryItem in Audiobookshelf 2.36.0, using the
// linked person's policy, never the administrator API key's visibility.
func (u user) canReadItem(item bookItem) bool {
	p := u.Permissions
	if !u.canReadLibrary(item.LibraryID) || p.AccessExplicitContent == nil || p.AccessAllTags == nil {
		return false
	}
	if !*p.AccessExplicitContent && (item.Media.Metadata.Explicit == nil || *item.Media.Metadata.Explicit) {
		return false
	}
	if *p.AccessAllTags {
		return true
	}
	if p.SelectedTagsDenied == nil || u.Tags == nil || item.Media.Tags == nil {
		return false
	}
	for _, tag := range item.Media.Tags {
		if slices.Contains(u.Tags, tag) {
			return !*p.SelectedTagsDenied
		}
	}
	return *p.SelectedTagsDenied
}

func (item bookItem) playable() bool {
	if !validID.MatchString(item.ID) || item.MediaType != "book" || item.Missing == nil || *item.Missing || item.Invalid == nil || *item.Invalid {
		return false
	}
	for _, track := range item.Media.Tracks {
		if track.Duration > 0 {
			return true
		}
	}
	return false
}

func (c *Client) FindBooks(ctx context.Context, remoteID string, q mediaserver.BookQuery) ([]mediaserver.BookItem, error) {
	ids := identifiers(q)
	if !q.Available || len(ids.terms) == 0 {
		return nil, mediaserver.ErrItemUnverified
	}
	u, err := c.getUser(ctx, remoteID)
	if err != nil {
		return nil, err
	}
	if u.remote().IsDisabled {
		return nil, mediaserver.ErrItemUnverified
	}
	libraries, err := c.Libraries(ctx)
	if err != nil {
		return nil, err
	}
	found := map[string]mediaserver.BookItem{}
	candidates := 0
	// These searches are capped, not paginated by Audiobookshelf. Hitting a
	// cap cannot establish that all copies were seen, even if one matched.
	for _, library := range libraries {
		if library.CollectionType != "book" || !u.canReadLibrary(library.ID) {
			continue
		}
		for _, term := range ids.terms {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var response struct {
				Books []struct {
					Item bookItem `json:"libraryItem"`
				} `json:"book"`
			}
			path := "/api/libraries/" + url.PathEscape(library.ID) + "/search?" + url.Values{"q": {term}, "limit": {"100"}}.Encode()
			if err := c.do(ctx, "GET", path, nil, &response); err != nil {
				return nil, err
			}
			candidates += len(response.Books)
			if response.Books == nil || len(response.Books) >= 100 || candidates > 1000 {
				return nil, mediaserver.ErrItemUnverified
			}
			for _, match := range response.Books {
				item := match.Item
				if item.LibraryID != library.ID || !item.playable() || !ids.matches(item) || !u.canReadItem(item) {
					continue
				}
				if _, seen := found[item.ID]; seen {
					continue
				} // Same record returned for another identifier.
				var live bookItem
				if err := c.do(ctx, "GET", "/api/items/"+url.PathEscape(item.ID)+"?expanded=1", nil, &live); err != nil {
					return nil, err
				}
				if live.ID != item.ID || live.LibraryID != library.ID || !live.playable() || !ids.matches(live) || !u.canReadItem(live) {
					return nil, mediaserver.ErrItemUnverified
				}
				found[item.ID] = mediaserver.BookItem{ID: live.ID, Title: live.Media.Metadata.Title, LibraryName: library.Name, Narrators: append([]string{}, live.Media.Metadata.Narrators...)}
			}
		}
	}
	// Permissions can change while an administrator-key search is running.
	liveUser, err := c.getUser(ctx, remoteID)
	if err != nil {
		return nil, err
	}
	if !sameAccess(u, liveUser) || len(found) == 0 {
		return nil, mediaserver.ErrItemUnverified
	}
	out := make([]mediaserver.BookItem, 0, len(found))
	for _, item := range found {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LibraryName != out[j].LibraryName {
			return out[i].LibraryName < out[j].LibraryName
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
