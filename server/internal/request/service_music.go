package request

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/lidarr"
)

var (
	ErrLidarrInstanceForbidden = errors.New("lidarr instance is not available to you")
	ErrLidarrInstanceInvalid   = errors.New("invalid lidarr instance")
	// ErrMusicMetadataUnresolved means no Lidarr metadata record could be found
	// for the requested foreignAlbumId, so there is nothing to add an album
	// from. Like its book sibling it says nothing about the requester, the
	// instance config, or the album's availability, so it parks the request in
	// the approval queue instead of discarding it.
	ErrMusicMetadataUnresolved = errors.New("album not found for foreign id")
)

// musicParkedMessage explains, in requester vocabulary, why a music request
// that could not be added is sitting in the approval queue instead.
const musicParkedMessage = "This album couldn't be matched in the library, so it was saved as a request for an admin instead of being added automatically."

// getLidarrWithID resolves the user's effective Lidarr client plus the
// instance id it resolved to, for cache keying. Mirrors getChaptarrWithID:
// the user's granted instance, else (for admins) the default Lidarr instance.
func (s *Service) getLidarrWithID(userID int64) (*lidarr.Client, string) {
	if s.registry == nil {
		return nil, ""
	}
	if client, id, err := s.registry.GetUserLidarrClient(userID); err == nil && client != nil {
		return client, id
	}
	if s.userIsAdmin(userID) {
		if client, id, err := s.registry.GetDefaultLidarrClient(); err == nil && client != nil {
			return client, id
		}
	}
	return nil, ""
}

// resolveLidarr resolves an explicitly selected instance when present and
// enforces requester access before returning a client. Admins may select any
// configured Lidarr; omitted IDs resolve the user's granted instance (Lidarr
// is grant-only — no global default), with the admin default as the admin
// fallback.
func (s *Service) resolveLidarr(userID int64, instanceID string) (*lidarr.Client, string, error) {
	if s.registry == nil {
		return nil, "", nil
	}
	if instanceID != "" {
		if !s.userIsAdmin(userID) {
			allowed, err := s.registry.UserCanAccessInstance(userID, instanceID, "lidarr")
			if err != nil {
				return nil, "", fmt.Errorf("check lidarr access: %w", err)
			}
			if !allowed {
				return nil, "", ErrLidarrInstanceForbidden
			}
		}
		client, err := s.registry.GetLidarrClient(instanceID)
		if err != nil {
			return nil, "", ErrLidarrInstanceInvalid
		}
		return client, instanceID, nil
	}
	client, id, err := s.registry.GetUserLidarrClient(userID)
	if err != nil {
		return nil, "", err
	}
	if client != nil {
		return client, id, nil
	}
	if s.userIsAdmin(userID) {
		return s.registry.GetDefaultLidarrClient()
	}
	return nil, "", nil
}

// addToLidarr adds an album to the resolved Lidarr instance. Music has no TMDB
// id, so the request carries a canonical MusicBrainz release-group id in
// foreign_id. Unlike books there is no format axis: one album is one record,
// and the add itself is synchronous (Lidarr finds-or-adds the artist inline),
// so there is no parked import state to watch afterwards.
func (s *Service) addToLidarr(r *resolvedRequest) (string, string, error) {
	r.foreignID = strings.TrimSpace(r.foreignID)
	r.title = strings.TrimSpace(r.title)
	if r.foreignID == "" {
		return "", "", fmt.Errorf("foreign_id is required for music requests")
	}
	actorID := r.actorID
	if actorID == 0 {
		actorID = r.userID
	}
	client, instanceID, err := s.resolveLidarr(actorID, r.instanceID)
	r.instanceID = instanceID
	if err != nil {
		return "", "", err
	}
	if client == nil {
		return "", "", fmt.Errorf("lidarr is not configured for you")
	}

	// Preflight the live library before lookup/add. The request boundary is
	// the idempotency boundary: a complete album is already available, a
	// monitored record is already requested, and an unmonitored record is
	// monitored/searched in place rather than duplicated.
	albums, libraryErr := client.GetAllAlbums()
	if libraryErr != nil {
		return "", "", fmt.Errorf("check existing album state: %w", libraryErr)
	}
	title, existing := albumsForForeignID(albums, r.foreignID)
	title = strings.TrimSpace(title)

	// One memoized id fetch serves both the alias probe below and the add-time
	// lookup's first term — the identical query must not run twice per request.
	var idFetchResults []lidarr.Album
	var idFetchErr error
	idFetched := false
	idFetch := func() ([]lidarr.Album, error) {
		if !idFetched {
			idFetched = true
			idFetchResults, idFetchErr = client.LookupAlbum(musicIDTerm(r.foreignID))
		}
		return idFetchResults, idFetchErr
	}

	// The library may already track this album under a different id:
	// MusicBrainz merges release-groups, and the provider answers the merged
	// alias with its canonical sibling. When the provider itself declares the
	// requested id an alias of a record the library already has, the request
	// completes that record — a requester tapping the duplicate listing means
	// "I want this album", not "track it twice".
	if len(existing) == 0 {
		if r.title == "" {
			return "", "", fmt.Errorf("title is required to add a new album")
		}
		if canonicalID, ok := lookupCanonicalAlbumAlias(idFetch, r.foreignID); ok {
			aliasTitle, aliasRecords := albumsForForeignID(albums, canonicalID)
			if len(aliasRecords) > 0 {
				existing = aliasRecords
				title = strings.TrimSpace(aliasTitle)
			}
		}
	}
	if title == "" {
		title = r.title
	}

	if len(existing) > 0 {
		queued := s.queuedAlbumIDs(client)
		available := false
		monitored := false
		anyFile := false
		ids := make([]int, 0, len(existing))
		best := existing[0]
		for _, rec := range existing {
			ids = append(ids, rec.ID)
			available = available || albumComplete(rec)
			anyFile = anyFile || rec.Statistics.TrackFileCount > 0
			monitored = monitored || rec.Monitored
			// The record that proves the state is the one worth remembering: a
			// complete album outranks a bare monitored record outranks the rest.
			if albumComplete(rec) && !albumComplete(best) {
				best = rec
			} else if rec.Monitored && !albumComplete(best) && !best.Monitored {
				best = rec
			}
		}
		status := ""
		switch {
		case available:
			status = StatusAvailable
		case monitored:
			status = musicIncompleteStatus(anyFile, queued, ids)
		default:
			if err := client.SetAlbumMonitored(ids, true); err != nil {
				return "", "", fmt.Errorf("monitor album: %w", err)
			}
			// Monitoring is the durable request contract; the immediate search
			// is a best-effort accelerator. A failed command must not make the
			// now-monitored record requestable again.
			_ = client.TriggerAlbumSearch(ids)
			status = musicIncompleteStatus(anyFile, queued, ids)
		}
		r.noteBookRecord("", best.ID, best.ForeignAlbumID)
		s.InvalidateMusicDigests(r.instanceID)
		return status, title, nil
	}

	match, lookupErr := lookupAlbumForAdd(func(term string) ([]lidarr.Album, error) {
		if term == musicIDTerm(r.foreignID) {
			return idFetch()
		}
		return client.LookupAlbum(term)
	}, r.foreignID, r.title, r.searchTerm)
	if match == nil {
		if lookupErr != nil {
			return "", "", fmt.Errorf("album lookup failed: %w", lookupErr)
		}
		// No search term produced this foreignAlbumId, so there is no metadata
		// record to build an add payload from and no record to monitor.
		// Nothing was mutated here; the caller parks the request rather than
		// dropping it.
		return "", "", fmt.Errorf("%w %s", ErrMusicMetadataUnresolved, r.foreignID)
	}
	if match.Artist == nil || strings.TrimSpace(match.Artist.ForeignArtistID) == "" {
		return "", "", fmt.Errorf("album lookup result carries no artist identity")
	}

	config, err := s.selectMusicAddConfig(client)
	if err != nil {
		return "", "", err
	}

	title = strings.TrimSpace(match.Title)
	if title == "" {
		title = r.title
	}

	addReq := lidarr.AddAlbumRequest{
		ForeignAlbumID: match.ForeignAlbumID,
		Title:          title,
		Monitored:      true,
		AnyReleaseOk:   true,
	}
	addReq.AddOptions.SearchForNewAlbum = true
	addReq.Artist = lidarr.AddArtistRequest{
		ArtistName:        match.Artist.ArtistName,
		ForeignArtistID:   match.Artist.ForeignArtistID,
		QualityProfileID:  config.qualityProfileID,
		MetadataProfileID: config.metadataProfileID,
		RootFolderPath:    config.rootFolderPath,
		Monitored:         true,
		// A request for one album must never silently subscribe the whole
		// discography: albums released later stay unmonitored, and the add's
		// hydrated siblings arrive unmonitored on their own (verified live).
		MonitorNewItems: "none",
	}
	// No addOptions.monitor is sent on purpose (verified live against Lidarr):
	// "none" does not mean "monitor no OTHER albums" — it unmonitors the
	// artist itself, and an unmonitored artist's albums never count as
	// wanted, so nothing would ever search for the requested album again.
	// With the option omitted, the artist stays monitored, exactly the
	// album-add body's own monitored flag is honored, and only the requested
	// album becomes wanted.
	addReq.Artist.AddOptions.SearchForMissingAlbums = false

	created, err := client.AddAlbum(addReq)
	if err != nil {
		return "", "", err
	}
	if created != nil {
		r.noteBookRecord("", created.ID, created.ForeignAlbumID)
	}
	s.InvalidateMusicDigests(r.instanceID)
	return StatusRequested, title, nil
}

// queuedAlbumIDs reads the album ids currently downloading, best-effort: an
// unreadable queue degrades to "not downloading", exactly like the projection.
func (s *Service) queuedAlbumIDs(client *lidarr.Client) map[int]bool {
	queued := make(map[int]bool)
	if queue, err := client.GetQueueDetailed(); err == nil {
		for _, item := range queue {
			if item.AlbumID != 0 && musicQueueItemDownloading(item) {
				queued[item.AlbumID] = true
			}
		}
	}
	return queued
}

// musicIncompleteStatus is the status of a monitored album that is not
// complete: downloading when its download is active, partial when some tracks
// already landed, requested otherwise.
func musicIncompleteStatus(anyFile bool, queued map[int]bool, ids []int) string {
	for _, id := range ids {
		if queued[id] {
			return StatusDownloading
		}
	}
	if anyFile {
		return StatusPartial
	}
	return StatusRequested
}

// albumComplete reports whether an album's monitored tracks are all on disk. A
// record with files but no track count makes no completeness claim, so any
// file counts as complete rather than stranding it in partial forever.
func albumComplete(album lidarr.Album) bool {
	stats := album.Statistics
	if stats.TrackFileCount <= 0 {
		return false
	}
	if stats.TrackCount <= 0 {
		return true
	}
	return stats.TrackFileCount >= stats.TrackCount
}

// albumsForForeignID collects the library records filed under one
// foreignAlbumId (normally exactly one — music has no format split — but
// duplicates are aggregated rather than trusted not to exist).
func albumsForForeignID(albums []lidarr.Album, foreignID string) (string, []lidarr.Album) {
	var matched []lidarr.Album
	title := ""
	for _, album := range albums {
		if foreignID == "" || album.ForeignAlbumID != foreignID {
			continue
		}
		if title == "" {
			title = album.Title
		}
		matched = append(matched, album)
	}
	return title, matched
}

// musicIDTerm is the exact-fetch lookup term for a MusicBrainz release-group
// id: Lidarr answers "lidarr:<mbid>" with that record (SkyHookProxy's
// IsMbidQuery), the same deterministic resolution Radarr gives movies via
// "term=tmdb:{id}".
func musicIDTerm(foreignID string) string {
	return "lidarr:" + foreignID
}

// lookupCanonicalAlbumAlias resolves the provider's alias→canonical link for a
// foreignAlbumId, given the id-term fetch. A response of exactly one record
// filed under a DIFFERENT id is the provider itself declaring the two ids one
// release-group (MusicBrainz merges). Anything else — a miss, an error, a
// fuzzy multi-hit — declares nothing, and the caller must treat the ids as
// distinct.
func lookupCanonicalAlbumAlias(idFetch func() ([]lidarr.Album, error), foreignID string) (string, bool) {
	results, err := idFetch()
	if err != nil || len(results) != 1 {
		return "", false
	}
	canonical := strings.TrimSpace(results[0].ForeignAlbumID)
	if canonical == "" || canonical == strings.TrimSpace(foreignID) {
		return "", false
	}
	return canonical, true
}

// lookupAlbumForAdd re-finds the metadata record a music request points at, so
// a brand-new album can be added. The exact-id term comes first (a
// deterministic fetch); the requester's own search text and the title forms
// are the fallbacks, under the same rule as books: only an exact
// foreignAlbumId match is ever accepted, so widening the search can find the
// right record but can never select a different one.
func lookupAlbumForAdd(lookup func(term string) ([]lidarr.Album, error), foreignID, title, searchTerm string) (*lidarr.Album, error) {
	foreignID = strings.TrimSpace(foreignID)
	var firstErr error
	for _, term := range musicLookupTerms(foreignID, title, searchTerm) {
		results, err := lookup(term)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for i := range results {
			if strings.TrimSpace(results[i].ForeignAlbumID) == foreignID {
				return &results[i], nil
			}
		}
	}
	return nil, firstErr
}

// musicLookupTerms is the ordered search-term list lookupAlbumForAdd tries:
// the exact-id fetch, the requester's own proven search text, then the title
// and its headline (see mainBookTitle — the munging is media-agnostic).
func musicLookupTerms(foreignID, title, searchTerm string) []string {
	terms := make([]string, 0, 4)
	add := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" {
			return
		}
		for _, existing := range terms {
			if strings.EqualFold(existing, term) {
				return
			}
		}
		terms = append(terms, term)
	}
	if foreignID != "" {
		add(musicIDTerm(foreignID))
	}
	add(searchTerm)
	add(title)
	add(mainBookTitle(title))
	return terms
}

// musicAddConfig is the artist configuration a Lidarr album add requires.
type musicAddConfig struct {
	qualityProfileID  int
	metadataProfileID int
	rootFolderPath    string
}

// selectMusicAddConfig resolves the profiles and root folder for a new
// artist. Lidarr root folders carry per-folder defaults, so the chosen root's
// defaults win; missing or dangling defaults fall back to the first profile,
// skipping Lidarr's hidden "None" metadata profile (an import-list exclusion
// marker, not a real profile — selecting it would hydrate no albums).
func (s *Service) selectMusicAddConfig(client *lidarr.Client) (musicAddConfig, error) {
	folders, err := client.GetRootFolders()
	if err != nil || len(folders) == 0 {
		return musicAddConfig{}, fmt.Errorf("no root folders available")
	}
	var root *lidarr.RootFolder
	for i := range folders {
		if folders[i].Accessible && strings.TrimSpace(folders[i].Path) != "" {
			root = &folders[i]
			break
		}
	}
	if root == nil {
		return musicAddConfig{}, fmt.Errorf("no accessible root folder available")
	}

	qps, err := client.GetQualityProfiles()
	if err != nil || len(qps) == 0 {
		return musicAddConfig{}, fmt.Errorf("no quality profiles available")
	}
	mps, err := client.GetMetadataProfiles()
	if err != nil || len(mps) == 0 {
		return musicAddConfig{}, fmt.Errorf("no metadata profiles available")
	}

	config := musicAddConfig{rootFolderPath: root.Path}
	for _, p := range qps {
		if p.ID == root.DefaultQualityProfileID {
			config.qualityProfileID = p.ID
			break
		}
	}
	if config.qualityProfileID == 0 {
		config.qualityProfileID = qps[0].ID
	}
	for _, p := range mps {
		if p.ID == root.DefaultMetadataProfileID && !isNoneMetadataProfile(p) {
			config.metadataProfileID = p.ID
			break
		}
	}
	if config.metadataProfileID == 0 {
		for _, p := range mps {
			if !isNoneMetadataProfile(p) {
				config.metadataProfileID = p.ID
				break
			}
		}
	}
	if config.metadataProfileID == 0 {
		return musicAddConfig{}, fmt.Errorf("no usable metadata profile available")
	}
	return config, nil
}

func isNoneMetadataProfile(p lidarr.MetadataProfile) bool {
	return strings.EqualFold(strings.TrimSpace(p.Name), "none")
}

// GetUserMusicStatusForInstance reports the current user's request state for
// an album, keyed by the MusicBrainz release-group id (music has no tmdb_id).
// Stored request rows are overlaid with live library truth, and a record the
// library re-keyed to a different foreignAlbumId is followed through its
// persisted record id.
func (s *Service) GetUserMusicStatusForInstance(userID int64, foreignID, requestedInstanceID string) (*StatusResponse, error) {
	foreignID = strings.TrimSpace(foreignID)
	if foreignID == "" {
		return nil, fmt.Errorf("foreign_id is required")
	}
	client, instanceID, err := s.resolveLidarr(userID, requestedInstanceID)
	if err != nil {
		return nil, err
	}
	query := "SELECT status, COALESCE(book_record_id, 0) FROM request_log WHERE (user_id = ? OR status = 'pending') AND foreign_id = ? AND media_type = 'music'"
	args := []interface{}{userID, foreignID}
	if instanceID != "" {
		if requestedInstanceID != "" {
			// An explicit selection must never absorb history from another
			// instance.
			query += " AND instance_id = ?"
		} else {
			query += " AND (instance_id = ? OR instance_id IS NULL)"
		}
		args = append(args, instanceID)
	} else if requestedInstanceID != "" {
		query += " AND instance_id = ?"
		args = append(args, requestedInstanceID)
	}
	query += " ORDER BY requested_at DESC, id DESC LIMIT 1"

	logged := ""
	recordID := 0
	if err := s.db.QueryRow(query, args...).Scan(&logged, &recordID); err != nil && logged == "" {
		// No rows is the common case for an unrequested album; other errors
		// degrade to "nothing logged" exactly like a missing row.
		logged = ""
	}

	status := logged
	canonicalForeignID := ""
	if client != nil {
		projection, lerr := s.liveMusicProjectionCached(client, instanceID)
		if lerr != nil {
			return nil, lerr
		}
		live, exists := projection.Statuses[foreignID]
		if !exists {
			// The projection is keyed by each record's CURRENT foreignAlbumId,
			// which a metadata refresh can re-key away from the id this
			// request was logged under. The persisted record id is the
			// identity that survives.
			if rec, ok := projection.recordByID(recordID); ok {
				live, exists = rec.Status, true
				if rec.ForeignID != "" && rec.ForeignID != foreignID {
					canonicalForeignID = rec.ForeignID
				}
			}
		}
		switch {
		case exists && live != StatusUnavailable:
			status = live
		case logged == StatusPending || logged == StatusDenied:
			// Approval workflow outcomes remain meaningful while there is no
			// live record for the album.
			status = logged
		default:
			status = StatusUnavailable
		}
	}

	if status == "" {
		return &StatusResponse{Status: StatusUnavailable}, nil
	}
	return &StatusResponse{Status: status, CanonicalForeignID: canonicalForeignID}, nil
}

const musicLiveProjectionTTL = 15 * time.Second

// musicLiveProjection is the per-instance live status projection: one status
// per current foreignAlbumId, plus every record by its numeric Lidarr id so
// status reads can follow a re-keyed record.
type musicLiveProjection struct {
	Statuses map[string]string       `json:"statuses"`
	Records  map[int]musicLiveRecord `json:"records,omitempty"`
}

type musicLiveRecord struct {
	Status    string `json:"status"`
	ForeignID string `json:"foreignId,omitempty"`
}

func (p *musicLiveProjection) recordByID(id int) (musicLiveRecord, bool) {
	if id <= 0 {
		return musicLiveRecord{}, false
	}
	rec, ok := p.Records[id]
	return rec, ok
}

func (s *Service) liveMusicProjectionCached(client *lidarr.Client, instanceID string) (*musicLiveProjection, error) {
	cacheKey := "music-live:" + instanceID
	if projection, ok := s.cachedMusicProjection(cacheKey); ok {
		return projection, nil
	}
	projectionLock := s.projectionLock(instanceID)
	projectionLock.Lock()
	defer projectionLock.Unlock()
	if projection, ok := s.cachedMusicProjection(cacheKey); ok {
		return projection, nil
	}
	projection, err := buildMusicLiveProjection(client)
	if err != nil {
		return nil, err
	}
	s.cacheMusicProjection(cacheKey, projection)
	return projection, nil
}

// freshLiveMusicStatus bypasses the cached projection for the one read whose
// answer gates a mutation (the approval-required create), then re-primes the
// cache with what it fetched.
func (s *Service) freshLiveMusicStatus(client *lidarr.Client, instanceID, foreignID string) (string, bool, error) {
	projectionLock := s.projectionLock(instanceID)
	projectionLock.Lock()
	defer projectionLock.Unlock()
	projection, err := buildMusicLiveProjection(client)
	if err != nil {
		return "", false, err
	}
	s.cacheMusicProjection("music-live:"+instanceID, projection)
	status, ok := projection.Statuses[foreignID]
	return status, ok, nil
}

func buildMusicLiveProjection(client *lidarr.Client) (*musicLiveProjection, error) {
	albums, err := client.GetAllAlbums()
	if err != nil {
		return nil, fmt.Errorf("check live album state: %w", err)
	}
	queued := make(map[int]bool)
	if queue, err := client.GetQueueDetailed(); err == nil {
		for _, item := range queue {
			if item.AlbumID != 0 && musicQueueItemDownloading(item) {
				queued[item.AlbumID] = true
			}
		}
	}
	projection := &musicLiveProjection{
		Statuses: make(map[string]string, len(albums)),
		Records:  make(map[int]musicLiveRecord, len(albums)),
	}
	for _, album := range albums {
		if album.ID <= 0 {
			continue
		}
		status := StatusUnavailable
		switch {
		case albumComplete(album):
			status = StatusAvailable
		case queued[album.ID]:
			status = StatusDownloading
		case album.Statistics.TrackFileCount > 0:
			status = StatusPartial
		case album.Monitored:
			status = StatusRequested
		}
		projection.Records[album.ID] = musicLiveRecord{Status: status, ForeignID: album.ForeignAlbumID}
		if album.ForeignAlbumID == "" {
			continue
		}
		// Duplicate records under one foreignAlbumId aggregate the same way
		// the add preflight does: the strongest claim wins.
		if existing, ok := projection.Statuses[album.ForeignAlbumID]; ok {
			projection.Statuses[album.ForeignAlbumID] = strongerMusicStatus(existing, status)
			continue
		}
		projection.Statuses[album.ForeignAlbumID] = status
	}
	return projection, nil
}

// strongerMusicStatus picks the stronger of two live claims about one
// foreignAlbumId, in the same order the projection assigns them.
func strongerMusicStatus(a, b string) string {
	rank := map[string]int{
		StatusAvailable:   4,
		StatusDownloading: 3,
		StatusPartial:     2,
		StatusRequested:   1,
		StatusUnavailable: 0,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func (s *Service) cachedMusicProjection(cacheKey string) (*musicLiveProjection, bool) {
	if s.libraryCache == nil || cacheKey == "music-live:" {
		return nil, false
	}
	data, ok := s.libraryCache.Get(cacheKey)
	if !ok {
		return nil, false
	}
	var projection musicLiveProjection
	if json.Unmarshal(data, &projection) != nil || projection.Statuses == nil {
		return nil, false
	}
	return &projection, true
}

func (s *Service) cacheMusicProjection(cacheKey string, projection *musicLiveProjection) {
	if s.libraryCache == nil || cacheKey == "music-live:" {
		return
	}
	if data, err := json.Marshal(projection); err == nil {
		s.libraryCache.Set(cacheKey, data, musicLiveProjectionTTL)
	}
}

// musicQueueItemDownloading mirrors bookQueueItemDownloading for the Lidarr
// queue shape: an item counts as downloading only when nothing about it reads
// as stuck.
func musicQueueItemDownloading(item lidarr.QueueItem) bool {
	trackedStatus := strings.ToLower(strings.TrimSpace(item.TrackedDownloadStatus))
	trackedState := strings.ToLower(strings.TrimSpace(item.TrackedDownloadState))
	status := strings.ToLower(strings.TrimSpace(item.Status))
	problemState := trackedStatus + " " + trackedState + " " + status
	for _, token := range []string{"paused", "unavailable", "problem", "warning", "error", "failed", "blocked", "stalled"} {
		if strings.Contains(problemState, token) {
			return false
		}
	}
	return true
}

// InvalidateMusicDigests drops the cached music availability, recency, and
// artist digests for one Lidarr instance so the next read refetches. Called by
// the arr webhook receiver when a Lidarr library changes out-of-band, and by
// the add path after Cantinarr itself mutates the library.
func (s *Service) InvalidateMusicDigests(instanceID string) {
	if s.libraryCache == nil || instanceID == "" {
		return
	}
	s.libraryCache.Delete("music-library:" + instanceID)
	s.libraryCache.Delete("music-live:" + instanceID)
	s.libraryCache.Delete("music-recent:" + instanceID)
	s.libraryCache.Delete("music-artists:" + instanceID)
}

// lidarrClientReachableImage picks one image from a Lidarr record that a
// client is allowed to dereference, under the same rule as book covers:
// clients must never dereference an arr-origin URL, so only the relative
// MediaCover path — rewritten onto the /api/v1/mediacover route the instance
// proxy allowlists — or the metadata CDN copy is passed through.
//
// Lidarr's root-level cover URLs (/MediaCover/{artistId}/poster.jpg and
// /MediaCover/Albums/{albumId}/cover.jpg) are NOT the same shape as its
// API-prefixed routes (/api/v1/mediacover/artist/{id}/... and
// .../album/{id}/...), so the relative path is rebuilt rather than passed
// verbatim.
func lidarrClientReachableImage(images []lidarr.Image, preferred string) string {
	pick := func(match func(lidarr.Image) bool) (lidarr.Image, bool) {
		for _, img := range images {
			if img.URL != "" && match(img) {
				return img, true
			}
		}
		return lidarr.Image{}, false
	}
	img, ok := pick(func(i lidarr.Image) bool { return i.CoverType == preferred })
	if !ok {
		img, ok = pick(func(lidarr.Image) bool { return true })
	}
	if !ok {
		return ""
	}
	if rewritten := rewriteLidarrCoverPath(img.URL); rewritten != "" {
		return rewritten
	}
	if strings.HasPrefix(img.RemoteURL, "http") {
		return img.RemoteURL
	}
	return ""
}

// rewriteLidarrCoverPath maps a root-level Lidarr MediaCover URL onto the
// API-prefixed mediacover route the proxy allowlists, preserving any
// cache-buster query. Anything that is not one of the two known root shapes
// yields "" so the caller falls back to the CDN copy or a placeholder.
func rewriteLidarrCoverPath(raw string) string {
	if !strings.HasPrefix(raw, "/") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	rewritten := ""
	switch {
	case len(segments) == 4 && strings.EqualFold(segments[0], "MediaCover") && strings.EqualFold(segments[1], "Albums"):
		rewritten = "/mediacover/album/" + segments[2] + "/" + segments[3]
	case len(segments) == 3 && strings.EqualFold(segments[0], "MediaCover"):
		rewritten = "/mediacover/artist/" + segments[1] + "/" + segments[2]
	default:
		return ""
	}
	if parsed.RawQuery != "" {
		rewritten += "?" + parsed.RawQuery
	}
	return rewritten
}

func clientReachableAlbumCover(album lidarr.Album) string {
	return lidarrClientReachableImage(album.Images, "cover")
}

func clientReachableArtistImage(artist lidarr.Artist) string {
	return lidarrClientReachableImage(artist.Images, "poster")
}

// MusicSearchResult is one album row from a user-scoped Lidarr metadata
// lookup, shaped for the AI tools (search_music / display_media).
type MusicSearchResult struct {
	Title          string `json:"title"`
	ArtistName     string `json:"artist_name,omitempty"`
	Year           int    `json:"year,omitempty"`
	ForeignAlbumID string `json:"foreign_album_id"`
	Overview       string `json:"overview,omitempty"`
	RemoteCover    string `json:"remote_cover,omitempty"`
}

// ErrNoLidarrAccess reports that the user has no usable Lidarr instance (none
// configured, or no per-user music grant).
var ErrNoLidarrAccess = errors.New("music is not available for this account")

// musicSearchCacheTTL keeps just-searched results addressable by foreign id
// for the immediate follow-up (display_media verification) without
// re-querying Lidarr once per displayed item.
const musicSearchCacheTTL = 60 * time.Second

// SearchAlbumsForUser looks a query up on the user's effective Lidarr
// instance (their per-user grant, or the admin default for admins) — the same
// resolution every music request uses, so the AI assistant sees exactly the
// catalog the Music tab would.
func (s *Service) SearchAlbumsForUser(userID int64, query string) ([]MusicSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	client, instanceID, err := s.resolveLidarr(userID, "")
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, ErrNoLidarrAccess
	}
	results, err := client.LookupAlbum(query)
	if err != nil {
		return nil, fmt.Errorf("album lookup: %w", err)
	}
	out := make([]MusicSearchResult, 0, len(results))
	for _, r := range results {
		if strings.TrimSpace(r.ForeignAlbumID) == "" {
			continue // not addressable by any request flow
		}
		cover := externalCoverURL(r.RemoteCover)
		for _, img := range r.Images {
			if remote := externalCoverURL(img.RemoteURL); remote != "" {
				cover = remote // images[].remoteUrl is the repo-preferred source
				break
			}
		}
		artist := ""
		if r.Artist != nil {
			artist = r.Artist.ArtistName
		}
		year := 0
		if r.ReleaseDate != nil {
			year = r.ReleaseDate.Year()
		}
		result := MusicSearchResult{
			Title:          r.Title,
			ArtistName:     artist,
			Year:           year,
			ForeignAlbumID: r.ForeignAlbumID,
			Overview:       r.Overview,
			RemoteCover:    cover,
		}
		out = append(out, result)
		if s.libraryCache != nil {
			if data, err := json.Marshal(result); err == nil {
				s.libraryCache.Set(musicSearchCacheKey(instanceID, r.ForeignAlbumID), data, musicSearchCacheTTL)
			}
		}
	}
	return out, nil
}

func musicSearchCacheKey(instanceID, foreignID string) string {
	return "musicsearch|" + instanceID + "|" + foreignID
}

// CachedAlbumByForeignID returns an album the user's own recent search
// surfaced, keyed by foreign id on their effective instance. It performs no
// network I/O: a miss means the id was not in a recent search and the caller
// must re-verify with a live lookup (or reject).
func (s *Service) CachedAlbumByForeignID(userID int64, foreignID string) (*MusicSearchResult, bool) {
	if s.libraryCache == nil {
		return nil, false
	}
	_, instanceID, err := s.resolveLidarr(userID, "")
	if err != nil || instanceID == "" {
		return nil, false
	}
	data, ok := s.libraryCache.Get(musicSearchCacheKey(instanceID, strings.TrimSpace(foreignID)))
	if !ok {
		return nil, false
	}
	var result MusicSearchResult
	if json.Unmarshal(data, &result) != nil {
		return nil, false
	}
	return &result, true
}
