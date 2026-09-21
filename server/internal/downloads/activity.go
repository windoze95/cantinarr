package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/serversettings"
	"golang.org/x/sync/singleflight"
)

const activityTTL = 15 * time.Second

type cachedSource struct {
	key      string
	snapshot sourceSnapshot
}

// ActivityService caches only source truth. No filtered user result survives a
// request; grants, scope, requests and content policy are evaluated afterwards.
type ActivityService struct {
	db         *sql.DB
	store      *instance.Store
	registry   *instance.Registry
	policy     *contentpolicy.Service
	settings   *serversettings.Service
	authorize  auth.PermissionAuthorizer
	mu         sync.Mutex
	generation uint64
	cache      map[string]cachedSource
	flight     singleflight.Group
}

func (h *Handler) ConfigureActivity(db *sql.DB, policy *contentpolicy.Service, settings *serversettings.Service, authorize auth.PermissionAuthorizer) {
	h.activity = &ActivityService{db: db, store: h.store, registry: h.registry, policy: policy, settings: settings, authorize: authorize, cache: map[string]cachedSource{}}
}

func (s *ActivityService) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.cache = map[string]cachedSource{}
}

func sourceKey(inst instance.Instance) string {
	b, _ := json.Marshal(inst)
	return opaqueID(string(b))
}

func (s *ActivityService) snapshot(ctx context.Context, inst instance.Instance) sourceSnapshot {
	key := sourceKey(inst)
	s.mu.Lock()
	generation := s.generation
	cached, ok := s.cache[inst.ID]
	s.mu.Unlock()
	if ok && cached.key == key && time.Since(cached.snapshot.at) < activityTTL {
		return cached.snapshot
	}
	ch := s.flight.DoChan(fmt.Sprintf("%d:%s:%s", generation, inst.ID, key), func() (any, error) {
		s.mu.Lock()
		current, exists := s.cache[inst.ID]
		valid := exists && s.generation == generation && current.key == key && time.Since(current.snapshot.at) < activityTTL
		s.mu.Unlock()
		if valid {
			return current.snapshot, nil
		}
		// Coalesced work is independent of the first caller disconnecting.
		readCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		out := s.fetchSource(readCtx, inst)
		s.mu.Lock()
		if s.generation == generation {
			s.cache[inst.ID] = cachedSource{key, out}
		}
		s.mu.Unlock()
		return out, nil
	})
	select {
	case <-ctx.Done():
		return sourceSnapshot{at: time.Now().UTC(), err: ctx.Err()}
	case result := <-ch:
		return result.Val.(sourceSnapshot)
	}
}

type activityAccess struct {
	admin       bool
	scope       string
	userScope   string
	policy      *contentpolicy.Policy
	instances   []instance.Instance
	visible     map[string]bool
	requests    []savedRequest
	fingerprint string
}

func (s *ActivityService) access(ctx context.Context, claims *auth.Claims, scope string) (activityAccess, error) {
	out := activityAccess{scope: scope, visible: map[string]bool{}}
	if claims == nil || !auth.HasPermission(claims.Role, auth.PermissionDownloadsActivity) {
		return out, errors.New("unauthorized")
	}
	var role string
	if err := s.db.QueryRowContext(ctx, "SELECT role FROM users WHERE id=?", claims.UserID).Scan(&role); err != nil || role != claims.Role {
		return out, errors.New("session changed")
	}
	if s.authorize != nil {
		if err := s.authorize(ctx, claims.UserID, claims.DeviceID, auth.PermissionDownloadsActivity); err != nil {
			return out, err
		}
	}
	out.admin = role == auth.RoleAdmin
	settings, err := s.settings.Read()
	if err != nil {
		return out, err
	}
	out.userScope = settings.DownloadsUserScope
	if out.userScope != "all" && out.userScope != "mine" {
		return out, errors.New("visibility unavailable")
	}
	if out.admin {
		out.scope = "all"
	} else if out.userScope == "mine" {
		out.scope = "mine"
	}
	out.policy, err = s.policy.PolicyFor(claims.UserID, role)
	if err != nil {
		return out, err
	}
	out.instances, err = s.store.ListAll()
	if err != nil {
		return out, err
	}
	for service := range mediaForService {
		ids, err := s.store.VisibleInstanceIDs(claims.UserID, service)
		if err != nil {
			return out, err
		}
		for _, id := range ids {
			out.visible[id] = true
		}
	}
	if out.scope == "mine" {
		out.requests, err = s.savedRequests(ctx, claims.UserID)
		if err != nil {
			return out, err
		}
	}
	// No sensitive fields leave this digest. Repointed instances, deleted users,
	// changed requests and policy/grant restrictions invalidate an in-flight read.
	b, _ := json.Marshal([]any{role, out.scope, out.userScope, out.policy, out.visible, out.instances, out.requests})
	out.fingerprint = opaqueID(string(b))
	return out, nil
}

func (h *Handler) GetActivity(w http.ResponseWriter, r *http.Request) { h.serveActivity(w, r, false) }
func (h *Handler) GetSummary(w http.ResponseWriter, r *http.Request)  { h.serveActivity(w, r, true) }

func (h *Handler) serveActivity(w http.ResponseWriter, r *http.Request, summary bool) {
	w.Header().Set("Cache-Control", "no-store")
	claims := auth.GetClaims(r.Context())
	if claims == nil || !auth.HasPermission(claims.Role, auth.PermissionDownloadsActivity) {
		writeError(w, 403, "download activity is not permitted")
		return
	}
	if h.activity == nil {
		writeError(w, 503, "download activity unavailable")
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && scope != "mine" {
		writeError(w, 400, "scope must be all or mine")
		return
	}
	access, err := h.activity.access(r.Context(), claims, scope)
	if err != nil {
		writeError(w, 503, "download visibility unavailable; refresh and retry")
		return
	}
	view, err := h.activity.project(r.Context(), access)
	if err != nil {
		writeError(w, 503, "download visibility unavailable; refresh and retry")
		return
	}
	latest, err := h.activity.access(r.Context(), claims, scope)
	if err != nil || latest.fingerprint != access.fingerprint {
		writeError(w, 503, "download access changed; refresh and retry")
		return
	}
	if summary {
		view.Groups = nil
		view.Jobs = nil
	}
	writeJSON(w, view)
}

func (s *ActivityService) project(ctx context.Context, access activityAccess) (*Activity, error) {
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	out := &Activity{Scope: access.scope, UserScope: access.userScope, Complete: true, FetchedAt: time.Now().UTC(), Sources: []ActivitySource{}, Groups: []ContentGroup{}, Jobs: []ActivityJob{}}
	evaluator, err := s.policy.EvaluatorFor(ctx, access.policy)
	if err != nil {
		return nil, err
	}
	var sources []instance.Instance
	for _, inst := range access.instances {
		if IsDownloadClientType(inst.ServiceType) || (mediaForService[inst.ServiceType] != "" && (access.admin || access.visible[inst.ID])) {
			sources = append(sources, inst)
		}
	}
	snapshots := make([]sourceSnapshot, len(sources))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, inst := range sources {
		wg.Add(1)
		go func(i int, inst instance.Instance) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			snapshots[i] = s.snapshot(ctx, inst)
		}(i, inst)
	}
	wg.Wait()
	jobs := map[string]ActivityJob{}
	clientJobs := map[string]ActivityJob{}
	finishedJobs := map[string]bool{}
	// A client names its own jobs (nzo_id, torrent hash, NZBGet tracking alias)
	// and the arr stores that same name in its queue row, so the ID is the join
	// key, scoped by client kind so NZBGet's "42" never meets another "42".
	// Addresses are only a tie-breaker: the arr and Cantinarr routinely reach
	// one client through different names (Docker DNS, a VPN namespace's
	// localhost, a URL base), and demanding that they agree lists every job
	// twice while the count goes unavailable.
	kindJobs := map[string]map[string][]string{}
	jobEndpoint := map[string]string{}
	readEndpoints := map[string]bool{}
	used := map[string]bool{}
	groups := map[string]*ContentGroup{}
	for i, inst := range sources {
		ss := snapshots[i]
		isClient := IsDownloadClientType(inst.ServiceType)
		if !isClient || access.admin {
			available := ss.err == nil && time.Since(ss.at) <= activityTTL
			status := ActivitySource{InstanceID: inst.ID, Name: inst.Name, Available: available}
			if !available {
				status.Message = "Could not read a complete current snapshot"
				out.Complete = false
			}
			out.Sources = append(out.Sources, status)
			if ss.at.Before(out.FetchedAt) {
				out.FetchedAt = ss.at
			}
		}
		if !isClient || ss.err != nil || ss.queue == nil || time.Since(ss.at) > activityTTL {
			continue
		}
		ep := endpointKey(inst.ServiceType, inst.URL)
		if ep != "" {
			// The same client connected twice reports every job twice.
			if readEndpoints[ep] {
				continue
			}
			readEndpoints[ep] = true
		}
		if kindJobs[inst.ServiceType] == nil {
			kindJobs[inst.ServiceType] = map[string][]string{}
		}
		for _, item := range ss.queue.Items {
			finished := !unfinished(item.Status, item.SizeBytes, item.SizeLeftBytes)
			if item.ID == "" {
				if access.admin && !finished {
					out.Complete = false
				}
				continue
			}
			id := opaqueID(inst.ID, item.ID)
			job := newActivityJob(id, item.Status, item.SizeBytes, item.SizeLeftBytes, ss.queue.Paused)
			job.SpeedBPS = item.SpeedBPS
			job.Name = item.Name
			job.Control = &JobControl{inst.ID, item.ID, inst.ServiceType, inst.Name}
			for _, alias := range []string{item.ID, item.CorrelationID} {
				if alias != "" {
					key := normalizeDownloadID(inst.ServiceType, alias)
					kindJobs[inst.ServiceType][key] = appendUnique(kindJobs[inst.ServiceType][key], id)
				}
			}
			jobEndpoint[id] = ep
			if finished {
				finishedJobs[id] = true
			} else {
				clientJobs[id] = job
			}
		}
	}
	for i, inst := range sources {
		if IsDownloadClientType(inst.ServiceType) {
			continue
		}
		ss := snapshots[i]
		if ss.err != nil {
			continue
		}
		for _, row := range ss.rows {
			q := row.queue
			kind, endpoint, resolved := boundClient(q, ss.definitions)
			downloadID := q.str("downloadId")
			var mapped []string
			if downloadID != "" {
				if resolved {
					mapped = kindJobs[kind][normalizeDownloadID(kind, downloadID)]
				} else {
					// An unreadable definition hides the kind, not the job: an ID
					// held by exactly one connected client still names it.
					for k, table := range kindJobs {
						mapped = append(mapped, table[normalizeDownloadID(k, downloadID)]...)
					}
				}
				if len(mapped) > 1 && endpoint != "" {
					var exact []string
					for _, id := range mapped {
						if jobEndpoint[id] == endpoint {
							exact = append(exact, id)
						}
					}
					if len(exact) == 1 {
						mapped = exact
					}
				}
			}
			var id string
			switch {
			case len(mapped) == 1:
				id = mapped[0]
				// A client can have finished while the arr is still waiting to
				// import. Its verified finished state overrides arr queue lag.
				if finishedJobs[id] {
					continue
				}
			case downloadID != "":
				// Sibling rows of one pack, and libraries sharing one grab, share
				// this job even when no connected client reports it.
				id = opaqueID("arr", endpoint, normalizeDownloadID(kind, downloadID))
			default:
				id = opaqueID("arr", inst.ID, fmt.Sprint(q.num("id")))
			}
			if len(mapped) > 1 && access.admin {
				// Several connected clients hold this ID and the arr's address
				// picks none of them, so the job is listed here and under
				// Unmatched downloads: the admin's total cannot be exact.
				out.Complete = false
			}
			arrJob := newActivityJob(id, q.str("status"), int64(q.num("size")), int64(q.num("sizeleft")), false)
			if access.admin {
				arrJob.Name = q.str("title")
			}
			if !row.identityKnown {
				// A row without a readable title cannot be shown to a requester
				// or checked against a content policy. The admin still sees it,
				// or its client job under Unmatched downloads.
				if access.admin && len(mapped) != 1 {
					jobs[id] = arrJob
					gid := opaqueID(inst.ID, "unidentified")
					g := groups[gid]
					if g == nil {
						g = &ContentGroup{ID: gid, InstanceID: inst.ID, InstanceName: inst.Name, MediaType: "unknown", Title: "Unidentified content", Children: []ContentChild{}}
						groups[gid] = g
					}
					g.JobIDs = appendUnique(g.JobIDs, id)
				}
				continue
			}
			media := mediaForService[inst.ServiceType]
			if evaluator != nil && (media == "movie" || media == "tv") {
				if !evaluator.AllowsArrRecord(media, row.parent.str("certification"), row.parent.strings("genres")) {
					continue
				}
			}
			children := row.children
			if access.scope == "mine" {
				if uncertainRequestScope(access.requests, inst, row) {
					out.Complete = false
				}
				var matches bool
				matches, children = matchesRequests(access.requests, inst, row)
				if !matches {
					continue
				}
			}
			job := arrJob
			if len(mapped) == 1 {
				job = clientJobs[id]
				used[id] = true
			}
			if !access.admin {
				job.Name = ""
				job.Control = nil
			}
			jobs[id] = job
			groupID := opaqueID(inst.ID, media, fmt.Sprint(row.parent.num("id")))
			g := groups[groupID]
			if g == nil {
				g = &ContentGroup{ID: groupID, InstanceID: inst.ID, InstanceName: inst.Name, MediaType: media, Title: row.parent.str("title"), Year: row.parent.num("year"), Creator: row.creator, Artwork: artwork(row.parent, inst), DetailsKnown: true, Children: []ContentChild{}}
				if media == "book" {
					g.Format = bookFormat(row.parent)
				}
				groups[groupID] = g
			}
			g.DetailsKnown = g.DetailsKnown && row.detailsKnown
			g.JobIDs = appendUnique(g.JobIDs, id)
			for _, child := range children {
				childID := fmt.Sprint(child.num("id"))
				idx := -1
				for i := range g.Children {
					if g.Children[i].ID == childID {
						idx = i
						break
					}
				}
				if idx < 0 {
					c := ContentChild{ID: childID, Title: child.str("title"), Disc: child.num("mediumNumber"), Track: child.str("trackNumber")}
					if media == "tv" {
						n := child.num("seasonNumber")
						c.Season = &n
						c.Episode = child.num("episodeNumber")
					}
					g.Children = append(g.Children, c)
					idx = len(g.Children) - 1
				}
				g.Children[idx].JobIDs = appendUnique(g.Children[idx].JobIDs, id)
			}
		}
	}
	if access.admin {
		g := &ContentGroup{ID: "unmatched", MediaType: "unmatched", Title: "Unmatched downloads", Children: []ContentChild{}}
		for id, job := range clientJobs {
			if !used[id] {
				jobs[id] = job
				g.JobIDs = append(g.JobIDs, id)
			}
		}
		if len(g.JobIDs) > 0 {
			groups[g.ID] = g
		}
	}
	for _, g := range groups {
		sort.Strings(g.JobIDs)
		sort.Slice(g.Children, func(i, j int) bool {
			a, b := g.Children[i], g.Children[j]
			if a.Season != nil && b.Season != nil {
				if *a.Season != *b.Season {
					return *a.Season < *b.Season
				}
				return a.Episode < b.Episode
			}
			if a.Disc != b.Disc {
				return a.Disc < b.Disc
			}
			return naturalTrack(a.Track) < naturalTrack(b.Track)
		})
		var size, left int64
		for _, id := range g.JobIDs {
			size += jobs[id].SizeBytes
			left += jobs[id].SizeLeftBytes
		}
		if size > 0 {
			g.Progress = float64(size-left) / float64(size) * 100
		}
		out.Groups = append(out.Groups, *g)
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		a, b := out.Groups[i], out.Groups[j]
		if a.MediaType == "unmatched" {
			return false
		}
		if b.MediaType == "unmatched" {
			return true
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.ID < b.ID
	})
	for _, job := range jobs {
		out.Jobs = append(out.Jobs, job)
	}
	sort.Slice(out.Jobs, func(i, j int) bool { return out.Jobs[i].ID < out.Jobs[j].ID })
	out.Stale = time.Since(out.FetchedAt) > activityTTL
	if out.Stale {
		out.Complete = false
	}
	if out.Complete {
		n := len(out.Jobs)
		out.Count = &n
	}
	s.mu.Lock()
	superseded := generation != s.generation
	s.mu.Unlock()
	if superseded {
		return nil, errors.New("download snapshot changed during read")
	}
	return out, nil
}

// Torrent clients report info hashes in whichever case they like while the
// arrs store them upper-case; usenet IDs are opaque and case-sensitive.
func normalizeDownloadID(kind, id string) string {
	switch kind {
	case "qbittorrent", "transmission", "deluge", "rutorrent":
		return strings.ToUpper(id)
	}
	return id
}

func newActivityJob(id, status string, size, left int64, paused bool) ActivityJob {
	if size < 0 {
		size = 0
	}
	if left < 0 {
		left = 0
	}
	if left > size {
		left = size
	}
	j := ActivityJob{ID: id, Status: jobStatus(status, paused), SizeBytes: size, SizeLeftBytes: left}
	if size > 0 {
		j.Progress = float64(size-left) / float64(size) * 100
	}
	return j
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}
func naturalTrack(s string) int { var n int; fmt.Sscanf(s, "%d", &n); return n }
