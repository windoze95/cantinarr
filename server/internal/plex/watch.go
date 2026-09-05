package plex

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

const (
	watchPageSize  = 100
	watchMaxItems  = 1000
	watchBodyLimit = 4 << 20
)

// These DTOs are private to lookup. In particular, neither resource
// connections nor share tokens belong in the admin's server picker.
type watchResource struct {
	ClientIdentifier string            `json:"clientIdentifier"`
	Provides         string            `json:"provides"`
	Owned            bool              `json:"owned"`
	HTTPSRequired    bool              `json:"httpsRequired"`
	Connections      []watchConnection `json:"connections"`
}

type watchConnection struct {
	URI   string `json:"uri"`
	Local bool   `json:"local"`
	Relay bool   `json:"relay"`
}

// watchToken reads the current share, never falling back to the owner's
// token for another person. No sign-in credential is retained or minted.
func (p *Provider) watchToken(ctx context.Context, identity string) (string, error) {
	identity = mediaserver.CanonicalEmail(identity)
	if identity == "" {
		return "", mediaserver.ErrItemUnverified
	}
	if identity == mediaserver.CanonicalEmail(p.owner.Email) {
		owner, err := p.client.GetUser(ctx, p.clientID, p.token)
		if err != nil {
			return "", errors.New("plex watch: could not verify the server owner")
		}
		if mediaserver.CanonicalEmail(owner.Email) != identity {
			return "", mediaserver.ErrItemUnverified
		}
		return p.token, nil
	}
	var result struct {
		XMLName xml.Name `xml:"MediaContainer"`
		Shares  []struct {
			Email      string `xml:"email,attr"`
			Accepted   string `xml:"accepted,attr"`
			AcceptedAt string `xml:"acceptedAt,attr"`
			Token      string `xml:"accessToken,attr"`
		} `xml:"SharedServer"`
	}
	if err := p.client.doXML(ctx, http.MethodGet, "/api/servers/"+url.PathEscape(p.machineID)+"/shared_servers", p.clientID, p.token, &result); err != nil {
		return "", errors.New("plex watch: could not read the current share")
	}
	for _, share := range result.Shares {
		if mediaserver.CanonicalEmail(share.Email) != identity {
			continue
		}
		accepted := share.Accepted == "1" || share.Accepted == "true" ||
			(share.Accepted == "" && share.AcceptedAt != "" && share.AcceptedAt != "0")
		if accepted && share.Token != "" {
			return share.Token, nil
		}
	}
	return "", mediaserver.ErrItemUnverified
}

type watchClient struct {
	http     *http.Client
	baseURL  string
	clientID string
	token    string
}

// connectForWatch uses only the configured, owned machine's advertised
// direct connections. /identity needs no token; prove the machine before
// sending a credential. plex.tv is external; PMS is a media server and
// always dialed directly, including when it advertises a public address.
func (p *Provider) connectForWatch(ctx context.Context, token string) (*watchClient, error) {
	var resources []watchResource
	if err := p.client.doJSON(ctx, http.MethodGet, "/api/v2/resources?includeHttps=1", p.clientID, p.token, nil, &resources); err != nil {
		return nil, errors.New("plex watch: could not discover the server")
	}
	var connections []watchConnection
	for _, resource := range resources {
		if !resource.Owned || resource.ClientIdentifier != p.machineID || !containsServer(resource.Provides) {
			continue
		}
		for _, conn := range resource.Connections {
			u, err := url.Parse(conn.URI)
			if err != nil || conn.Relay || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
				(u.Scheme != "https" && (u.Scheme != "http" || resource.HTTPSRequired)) {
				continue
			}
			connections = append(connections, conn)
		}
	}
	sort.SliceStable(connections, func(i, j int) bool {
		score := func(c watchConnection) int {
			n := 0
			if !strings.HasPrefix(c.URI, "https:") {
				n += 2
			}
			if !c.Local {
				n++
			}
			return n
		}
		return score(connections[i]) < score(connections[j])
	})
	client := &watchClient{clientID: p.clientID, http: &http.Client{
		Transport: httpx.Internal(), Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
	for i, conn := range connections {
		if i == 8 || ctx.Err() != nil {
			break
		}
		client.baseURL = strings.TrimRight(conn.URI, "/")
		var identity struct {
			XMLName   xml.Name `xml:"MediaContainer"`
			MachineID string   `xml:"machineIdentifier,attr"`
		}
		probe, cancel := context.WithTimeout(ctx, time.Second)
		err := client.get(probe, "/identity", nil, &identity)
		cancel()
		if err == nil && identity.MachineID == p.machineID {
			client.token = token
			return client, nil
		}
	}
	return nil, errors.New("plex watch: no verified direct server connection answered")
}

func containsServer(provides string) bool {
	for _, capability := range strings.Split(provides, ",") {
		if strings.TrimSpace(capability) == "server" {
			return true
		}
	}
	return false
}

// get never returns an upstream URL, error body, or token, even to logs.
func (c *watchClient) get(ctx context.Context, path string, params url.Values, out any) error {
	endpoint := c.baseURL + path
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("plex watch: invalid server address")
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-Plex-Product", "Cantinarr")
	req.Header.Set("X-Plex-Client-Identifier", c.clientID)
	if c.token != "" {
		req.Header.Set("X-Plex-Token", c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("plex watch: server did not answer")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("plex watch: server returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, watchBodyLimit+1))
	if err != nil || len(data) > watchBodyLimit || xml.Unmarshal(data, out) != nil {
		return errors.New("plex watch: invalid server response")
	}
	return nil
}

type watchMetadata struct {
	RatingKey string `xml:"ratingKey,attr"`
	Type      string `xml:"type,attr"`
	GUID      string `xml:"guid,attr"`
	GUIDs     []struct {
		ID string `xml:"id,attr"`
	} `xml:"Guid"`
}

func (m watchMetadata) matches(q mediaserver.ItemQuery) bool {
	ids := map[string]string{}
	parse := func(raw string) {
		u, err := url.Parse(raw)
		if err != nil {
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
	for _, guid := range m.GUIDs {
		parse(guid.ID)
	}
	if ids["tmdb"] != "" && q.TMDBID > 0 {
		return ids["tmdb"] == strconv.FormatInt(q.TMDBID, 10)
	}
	return q.MediaType == "tv" && q.TVDBID > 0 && ids["tvdb"] == strconv.FormatInt(q.TVDBID, 10)
}

// FindItem searches the caller's visible sections, not the owner's library.
// The year/title-narrowed search is not an absence proof: no exact match,
// ambiguous editions, or incomplete pagination mean unverified, never missing.
// Plex Web's path is generated here; the service uses the fixed app.plex.tv
// origin rather than any of the private addresses that discovery returned.
func (p *Provider) FindItem(ctx context.Context, identity string, q mediaserver.ItemQuery) (mediaserver.Item, error) {
	itemType, typeID := "movie", "1"
	if q.MediaType == "tv" {
		itemType, typeID = "show", "2"
	}
	if (q.MediaType != "movie" && q.MediaType != "tv") || p.machineID == "" ||
		(q.TMDBID <= 0 && (q.MediaType != "tv" || q.TVDBID <= 0)) ||
		((q.Year <= 0 || q.Year > 9999) && strings.TrimSpace(q.Title) == "") {
		return mediaserver.Item{}, mediaserver.ErrItemUnverified
	}
	token, err := p.watchToken(ctx, identity)
	if err != nil {
		return mediaserver.Item{}, err
	}
	client, err := p.connectForWatch(ctx, token)
	if err != nil {
		return mediaserver.Item{}, err
	}
	var sections struct {
		XMLName     xml.Name `xml:"MediaContainer"`
		Directories []struct {
			Key  string `xml:"key,attr"`
			Type string `xml:"type,attr"`
		} `xml:"Directory"`
	}
	if err := client.get(ctx, "/library/sections", nil, &sections); err != nil {
		return mediaserver.Item{}, err
	}
	var match string
	seen := 0
	for _, section := range sections.Directories {
		if section.Type != itemType {
			continue
		}
		if _, err := strconv.ParseUint(section.Key, 10, 64); err != nil {
			return mediaserver.Item{}, mediaserver.ErrItemUnverified
		}
		params := url.Values{"type": {typeID}, "includeGuids": {"1"}, "X-Plex-Container-Size": {strconv.Itoa(watchPageSize)}}
		if q.Year > 0 && q.Year <= 9999 {
			params.Set("year", fmt.Sprintf("%d,%d,%d", q.Year-1, q.Year, q.Year+1))
		} else {
			params.Set("title", strings.TrimSpace(q.Title))
		}
		for offset := 0; ; {
			params.Set("X-Plex-Container-Start", strconv.Itoa(offset))
			var page struct {
				XMLName     xml.Name        `xml:"MediaContainer"`
				Size        int             `xml:"size,attr"`
				Offset      int             `xml:"offset,attr"`
				Total       *int            `xml:"totalSize,attr"`
				Videos      []watchMetadata `xml:"Video"`
				Directories []watchMetadata `xml:"Directory"`
			}
			if err := client.get(ctx, "/library/sections/"+section.Key+"/all", params, &page); err != nil {
				return mediaserver.Item{}, err
			}
			items := append(page.Videos, page.Directories...)
			seen += len(items)
			if page.Size != len(items) || page.Offset != offset || seen > watchMaxItems ||
				(page.Total != nil && *page.Total < offset+len(items)) {
				return mediaserver.Item{}, mediaserver.ErrItemUnverified
			}
			for _, item := range items {
				if item.Type != itemType || !item.matches(q) {
					continue
				}
				if _, err := strconv.ParseUint(item.RatingKey, 10, 64); err != nil || item.RatingKey == "0" || match != "" {
					return mediaserver.Item{}, mediaserver.ErrItemUnverified
				}
				match = item.RatingKey
			}
			offset += len(items)
			if page.Total != nil && offset == *page.Total {
				break
			}
			if page.Total == nil && len(items) < watchPageSize {
				break
			}
			if len(items) == 0 || seen >= watchMaxItems {
				return mediaserver.Item{}, mediaserver.ErrItemUnverified
			}
		}
	}
	if match == "" {
		return mediaserver.Item{}, mediaserver.ErrItemUnverified
	}
	return mediaserver.Item{ID: match, WebPath: "/desktop/#!/server/" + url.PathEscape(p.machineID) + "/details?key=" + url.QueryEscape("/library/metadata/"+match)}, nil
}
