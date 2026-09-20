package downloads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

type record map[string]any

func (m record) str(k string) string { v, _ := m[k].(string); return v }
func (m record) num(k string) int    { v, _ := m[k].(float64); return int(v) }
func (m record) obj(k string) record { v, _ := m[k].(map[string]any); return record(v) }
func (m record) list(k string) []any { v, _ := m[k].([]any); return v }
func (m record) strings(k string) []string {
	var out []string
	for _, v := range m.list(k) {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

type activityRow struct {
	queue         record
	parent        record
	children      []record
	creator       string
	identityKnown bool
	detailsKnown  bool
}

type sourceSnapshot struct {
	at             time.Time
	err            error
	rows           []activityRow
	definitions    []record
	definitionsErr error
	queue          *QueueView
}

var mediaForService = map[string]string{"radarr": "movie", "sonarr": "tv", "chaptarr": "book", "lidarr": "music"}
var parentForService = map[string]string{"radarr": "movie", "sonarr": "series", "chaptarr": "book", "lidarr": "album"}

// arrRead never forwards provider errors or raw provider bodies to requesters.
// These are admin-configured LAN endpoints, including cluster-only names.
func arrRead(ctx context.Context, inst instance.Instance, path string, out any) error {
	prefix := "/api/v3/"
	if inst.ServiceType == "chaptarr" || inst.ServiceType == "lidarr" {
		prefix = "/api/v1/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(inst.URL, "/")+prefix+path, nil)
	if err != nil {
		return errors.New("invalid library address")
	}
	req.Header.Set("X-Api-Key", inst.APIKey)
	client := &http.Client{Transport: httpx.Internal(), Timeout: 12 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("library unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("library read failed")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024+1))
	if err != nil || len(data) > 16*1024*1024 {
		return errors.New("library response incomplete")
	}
	return json.Unmarshal(data, out)
}

func (s *ActivityService) fetchSource(ctx context.Context, inst instance.Instance) sourceSnapshot {
	out := sourceSnapshot{at: time.Now().UTC()}
	if IsDownloadClientType(inst.ServiceType) {
		out.queue, out.err = Snapshot(s.registry, inst)
		return out
	}
	var page struct {
		TotalRecords *int     `json:"totalRecords"`
		Records      []record `json:"records"`
	}
	path := "queue?page=1&pageSize=1000&sortKey=id&sortDirection=ascending&includeMovie=true&includeSeries=true&includeEpisode=true&includeBook=true&includeAuthor=true&includeAlbum=true&includeArtist=true&includeUnknownMovieItems=true&includeUnknownSeriesItems=true&includeUnknownAuthorItems=true&includeUnknownArtistItems=true"
	if out.err = arrRead(ctx, inst, path, &page); out.err != nil {
		return out
	}
	if page.TotalRecords == nil || *page.TotalRecords != len(page.Records) || len(page.Records) > 1000 {
		out.err = errors.New("library queue incomplete")
		return out
	}
	seen := map[int]bool{}
	metadata := map[string]record{}
	readRecord := func(path string) (record, error) {
		if cached, ok := metadata[path]; ok {
			if cached == nil {
				return nil, errors.New("metadata unavailable")
			}
			return cached, nil
		}
		var value record
		err := arrRead(ctx, inst, path, &value)
		if err != nil {
			metadata[path] = nil
			return nil, err
		}
		metadata[path] = value
		return value, nil
	}
	for _, q := range page.Records {
		if q.num("id") <= 0 || seen[q.num("id")] {
			out.err = errors.New("library queue identity incomplete")
			return out
		}
		seen[q.num("id")] = true
		size, hasSize := q["size"].(float64)
		left, hasLeft := q["sizeleft"].(float64)
		if q.str("status") == "" || !hasSize || !hasLeft || size < 0 || left < 0 {
			out.err = errors.New("library queue progress incomplete")
			return out
		}
		if !unfinished(q.str("status"), int64(q.num("size")), int64(q.num("sizeleft"))) {
			continue
		}
		row := activityRow{queue: q}
		parentType := parentForService[inst.ServiceType]
		id := q.num(parentType + "Id")
		if id > 0 {
			p, err := readRecord(fmt.Sprintf("%s/%d", parentType, id))
			if err == nil && p.num("id") == id && p.str("title") != "" {
				row.parent = p
				row.identityKnown = true
			}
		}
		if row.identityKnown {
			switch inst.ServiceType {
			case "radarr":
				row.detailsKnown = true
			case "sonarr":
				// A series' episode catalog does not prove a pack contains those
				// episodes. Only queue-associated episode IDs are confirmed.
				if episodeID := q.num("episodeId"); episodeID > 0 {
					ep, err := readRecord(fmt.Sprintf("episode/%d", episodeID))
					if err == nil && ep.num("id") == episodeID && ep.num("seriesId") == id && ep.str("title") != "" {
						row.children = []record{ep}
						row.detailsKnown = true
					}
				}
			case "chaptarr", "lidarr":
				creatorType, name := "author", "authorName"
				if inst.ServiceType == "lidarr" {
					creatorType, name = "artist", "artistName"
				}
				creator := row.parent.obj(creatorType)
				if creator.num("id") != row.parent.num(creatorType+"Id") || creator.str(name) == "" {
					creator, _ = readRecord(fmt.Sprintf("%s/%d", creatorType, row.parent.num(creatorType+"Id")))
				}
				if creator.num("id") == row.parent.num(creatorType+"Id") {
					row.creator = creator.str(name)
				}
				row.detailsKnown = inst.ServiceType == "chaptarr"
				if inst.ServiceType == "lidarr" {
					// Do not present the album's full catalog as verified download
					// contents. Accept only explicitly associated track records.
					for _, raw := range q.list("tracks") {
						track, ok := raw.(map[string]any)
						if !ok {
							continue
						}
						t := record(track)
						if t.num("albumId") == id && t.num("id") > 0 && t.str("title") != "" {
							row.children = append(row.children, t)
						}
					}
					row.detailsKnown = len(row.children) > 0
				}
			}
		}
		out.rows = append(out.rows, row)
	}
	if len(out.rows) > 0 {
		out.definitionsErr = arrRead(ctx, inst, "downloadclient", &out.definitions)
	}
	return out
}

func unfinished(status string, size, left int64) bool {
	status = strings.ToLower(status)
	for _, finished := range []string{"complet", "seed", "upload", "import", "unpack", "extract", "repair", "verif", "postprocess", "moving"} {
		if strings.Contains(status, finished) {
			return false
		}
	}
	if strings.HasSuffix(status, "up") {
		return false
	}
	return !(size > 0 && left <= 0)
}

func jobStatus(raw string, paused bool) string {
	s := strings.ToLower(raw)
	switch {
	case paused || strings.Contains(s, "paused") || strings.Contains(s, "stopped"):
		return "paused"
	case strings.Contains(s, "stall"):
		return "stalled"
	case strings.Contains(s, "fail") || strings.Contains(s, "error") || strings.Contains(s, "missing"):
		return "failed"
	case strings.Contains(s, "queue") || strings.Contains(s, "wait"):
		return "queued"
	case strings.Contains(s, "check"):
		return "checking"
	default:
		return "downloading"
	}
}

func opaqueID(parts ...string) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}

// endpointKey is deliberately exact: DNS aliases, names and categories do
// not prove that two clients address the same download service.
func endpointKey(kind, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	path := strings.TrimRight(u.Path, "/")
	switch kind {
	case "transmission":
		path = strings.TrimSuffix(path, "/rpc")
		if path == "/transmission" {
			path = ""
		}
	case "nzbget":
		path = strings.TrimSuffix(path, "/jsonrpc")
	case "sabnzbd":
		path = strings.TrimSuffix(path, "/api")
	}
	return kind + "|" + u.Scheme + "|" + net.JoinHostPort(strings.ToLower(u.Hostname()), port) + "|" + path
}

func definitionEndpoint(d record) string {
	implementation := strings.ToLower(d.str("implementation"))
	kind := map[string]string{"sabnzbd": "sabnzbd", "qbittorrent": "qbittorrent", "nzbget": "nzbget", "transmission": "transmission", "deluge": "deluge", "rtorrent": "rutorrent"}[implementation]
	if kind == "" {
		return ""
	}
	fields := map[string]any{}
	for _, raw := range d.list("fields") {
		if f, ok := raw.(map[string]any); ok {
			fields[strings.ToLower(record(f).str("name"))] = f["value"]
		}
	}
	host, _ := fields["host"].(string)
	if host == "" {
		return ""
	}
	scheme := "http"
	if fields["usessl"] == true {
		scheme = "https"
	}
	port := ""
	switch p := fields["port"].(type) {
	case float64:
		port = strconv.Itoa(int(p))
	case string:
		port = p
	}
	if port != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	base, _ := fields["urlbase"].(string)
	if implementation == "rtorrent" {
		base, _ = fields["urlpath"].(string)
	}
	if base != "" && !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	return endpointKey(kind, scheme+"://"+host+base)
}

func boundEndpoint(q record, defs []record) string {
	var found []record
	for _, d := range defs {
		if (q.num("downloadClientId") > 0 && d.num("id") == q.num("downloadClientId")) ||
			(q.num("downloadClientId") == 0 && q.str("downloadClient") != "" && d.str("name") == q.str("downloadClient")) {
			found = append(found, d)
		}
	}
	if len(found) != 1 {
		return ""
	}
	return definitionEndpoint(found[0])
}

func artwork(p record, inst instance.Instance) string {
	origin, _ := url.Parse(inst.URL)
	for _, raw := range p.list("images") {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		remote := record(m).str("remoteUrl")
		u, err := url.Parse(remote)
		if err == nil && u.Hostname() != "" && u.User == nil && (u.Scheme == "https" || u.Scheme == "http") && (origin == nil || !strings.EqualFold(origin.Hostname(), u.Hostname())) {
			return remote
		}
	}
	return ""
}
