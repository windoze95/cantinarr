package request

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/discordnotify"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// DiscordAuthorize is a per-recipient boundary after shared provider reads.
// Channel publication is an explicit administrator-controlled disclosure, as
// with submission alerts; a mention additionally requires this current grant.
func (s *Service) DiscordAuthorize(ctx context.Context, userID int64, subject discordnotify.Subject) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var role string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id=?`, userID).Scan(&role); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	admin := role == auth.RoleAdmin
	if subject.MediaType == "movie" || subject.MediaType == "tv" {
		if err := s.checkContentPolicy(userID, admin, subject.MediaType, subject.TmdbID); err != nil {
			if errors.Is(err, ErrTitleNotAvailable) {
				return false, nil
			}
			return false, err
		}
	}
	if subject.InstanceID == "" || s.registry == nil {
		return false, nil
	}
	service := map[string]string{"movie": "radarr", "tv": "sonarr", "book": "chaptarr", "music": "lidarr"}[subject.MediaType]
	if service == "" {
		return false, nil
	}
	var actual string
	if err := s.db.QueryRow(`SELECT service_type FROM service_instances WHERE id=?`, subject.InstanceID).Scan(&actual); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if actual != service {
		return false, nil
	}
	if admin {
		return true, nil
	}
	return s.registry.UserCanAccessInstance(userID, subject.InstanceID, service)
}

// DiscordAvailability reads the exact saved request. The cross-library Seerr
// ledger and the aggregate user history are intentionally unsuitable here.
func (s *Service) DiscordAvailability(ctx context.Context, id int64) (discordnotify.Availability, error) {
	out := discordnotify.Availability{Units: []discordnotify.Unit{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	r, status, err := s.loadRequest(id)
	if err != nil {
		return out, err
	}
	out.Subject = discordnotify.Subject{RequestID: id, Title: r.title, MediaType: r.mediaType, TmdbID: r.tmdbID, ForeignID: r.foreignID, InstanceID: r.instanceID}
	if r.instanceID == "" || s.registry == nil {
		return out, fmt.Errorf("request has no verifiable library")
	}
	if err = s.db.QueryRow(`SELECT name FROM service_instances WHERE id=?`, r.instanceID).Scan(&out.Subject.Library); err != nil {
		return out, err
	}
	if status == StatusDenied {
		return out, nil
	}
	accepted := map[string]bool{}
	states, err := s.deliveryStates(id)
	if err != nil {
		return out, err
	}
	for _, d := range states {
		accepted[d.Format] = d.State == "complete"
	}
	if len(states) == 0 && status != StatusPending {
		accepted[""] = true
	}
	switch r.mediaType {
	case "movie":
		if !accepted[""] {
			return out, nil
		}
		digest, known := s.movieAvailabilityDigestForInstance(r.instanceID)
		if !known {
			return out, fmt.Errorf("movie library could not be read")
		}
		if digest[r.tmdbID].HasFile {
			out.Units = append(out.Units, discordnotify.Unit{Key: "movie", Label: "Ready to watch"})
		}
	case "tv":
		var repairOf int64
		if err = s.db.QueryRow(`SELECT COALESCE(repair_of,0) FROM request_tv_targets WHERE request_id=?`, id).Scan(&repairOf); err != nil && err != sql.ErrNoRows {
			return out, err
		}
		if !accepted[""] {
			if repairOf > 0 {
				// A repair re-files existing work. Its baseline starts at
				// delivery, so an empty pre-delivery read must not become one.
				return out, fmt.Errorf("%w: the repair has not been delivered", discordnotify.ErrUnverifiable)
			}
			return out, nil
		}
		out.Baseline = repairOf > 0
		return s.discordTVAvailability(ctx, id, r, out)
	case "book":
		client, err := s.registry.GetChaptarrClient(r.instanceID)
		if err != nil {
			return out, err
		}
		projection, err := s.liveBookProjectionCached(client, r.instanceID)
		if err != nil {
			return out, err
		}
		var live map[string]string
		resolved := false
		for _, format := range expandBookFormat(r.bookFormat) {
			if !accepted[""] && !accepted[format] {
				continue
			}
			state := StatusUnavailable
			var recordID int
			err = s.db.QueryRow(`SELECT COALESCE(NULLIF(d.book_record_id,0),r.book_record_id,0) FROM request_log r LEFT JOIN request_dispatch d ON d.request_id=r.id AND d.format=? WHERE r.id=?`, format, id).Scan(&recordID)
			if err != nil {
				return out, err
			}
			if record, ok := projection.recordByID(recordID); ok && record.Format == format {
				state = record.Status
				if state == StatusAvailable {
					out.Subject.ForeignID = record.ForeignID
				}
			} else {
				if !resolved {
					live, _, err = projection.resolveFormatsWithLookup(client, r.foreignID, nil)
					if err != nil {
						return out, err
					}
					resolved = true
				}
				state = live[format]
			}
			if state == StatusAvailable {
				label := "eBook ready"
				if format == BookFormatAudiobook {
					label = "Audiobook ready"
				}
				out.Units = append(out.Units, discordnotify.Unit{Key: format, Label: label, Format: format})
			}
		}
	case "music":
		if !accepted[""] {
			return out, nil
		}
		client, err := s.registry.GetLidarrClient(r.instanceID)
		if err != nil {
			return out, err
		}
		projection, err := s.liveMusicProjectionCached(client, r.instanceID)
		if err != nil {
			return out, err
		}
		var recordID int
		if err = s.db.QueryRow(`SELECT COALESCE(book_record_id,0) FROM request_log WHERE id=?`, id).Scan(&recordID); err != nil {
			return out, err
		}
		complete := false
		if record, ok := projection.recordByID(recordID); ok {
			complete = record.Complete
			out.Subject.ForeignID = record.ForeignID
		} else {
			for _, record := range projection.Records {
				if record.ForeignID == r.foreignID && record.Complete {
					complete = true
				}
			}
		}
		if complete {
			out.Units = append(out.Units, discordnotify.Unit{Key: "album", Label: "Album ready"})
		}

	}
	return out, nil
}

func (s *Service) discordTVAvailability(ctx context.Context, id int64, r *resolvedRequest, out discordnotify.Availability) (discordnotify.Availability, error) {
	s.tvMatchMu.Lock()
	defer s.tvMatchMu.Unlock()
	client, err := s.registry.GetSonarrClient(r.instanceID)
	if err != nil {
		return out, err
	}
	target, _, _, err := s.loadTVTarget(id)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	// Legacy explicit season selections can still be proved. A coarse old
	// selection without a snapshot is not permission to guess its seasons, and
	// neither is a selection naming Specials. Neither can ever gain a target.
	legacy := err == sql.ErrNoRows
	if legacy {
		if len(r.seasonNumbers) == 0 {
			return out, fmt.Errorf("%w: the saved TV selection predates season tracking", discordnotify.ErrUnverifiable)
		}
		for _, season := range r.seasonNumbers {
			if season <= 0 {
				return out, fmt.Errorf("%w: the saved TV selection includes Specials", discordnotify.ErrUnverifiable)
			}
		}
	}
	m, err := s.resolveTVMatch(client, r.tmdbID)
	if err != nil {
		return out, discordTVMatchError(err)
	}
	if legacy {
		target = &TVRequestTarget{Match: *m, SourceSeasons: r.seasonNumbers}
		for _, season := range r.seasonNumbers {
			if m.SeasonMap[season] <= 0 {
				return out, fmt.Errorf("%w: the saved TV selection names a season the title no longer lists", discordnotify.ErrUnverifiable)
			}
			target.TargetSeasons = append(target.TargetSeasons, m.SeasonMap[season])
		}
	}
	// The whole-match revision also hashes every season the title lists, so it
	// changes whenever TMDB announces another season. Only the series identity
	// and the selected seasons' mapping must still match what was saved.
	if m.TVDBID != target.Match.TVDBID {
		return out, ErrTVMatchStale
	}
	scopes := map[int]int{}
	selectedMapping := map[int]int{}
	for i, source := range target.SourceSeasons {
		if i >= len(target.TargetSeasons) || m.SeasonMap[source] != target.TargetSeasons[i] || target.TargetSeasons[i] <= 0 {
			return out, ErrTVMatchStale
		}
		scopes[target.TargetSeasons[i]] = source
		selectedMapping[source] = target.TargetSeasons[i]
	}
	// Cache provider data briefly and independently of recipients. The caller
	// still rechecks every recipient after this shared read.
	type snapshot struct {
		Series   *sonarr.Series   `json:"series"`
		Episodes []sonarr.Episode `json:"episodes"`
	}
	var snap snapshot
	key := "discord-tv:" + r.instanceID + ":" + strconv.Itoa(m.TVDBID)
	cached := false
	if s.libraryCache != nil {
		if data, ok := s.libraryCache.Get(key); ok {
			cached = json.Unmarshal(data, &snap) == nil
		}
	}
	if !cached {
		snap.Series, err = client.GetSeriesByTVDB(m.TVDBID)
		if err != nil {
			return out, err
		}
		if snap.Series == nil {
			return out, nil
		}
		snap.Episodes, err = client.GetAllEpisodes(snap.Series.ID)
		if err != nil {
			return out, err
		}
		if raw, e := json.Marshal(snap); e == nil && s.libraryCache != nil {
			s.libraryCache.Set(key, raw, 10*time.Second)
		}
	}
	if snap.Series == nil {
		return out, nil
	}
	if err = validateTVSeasons(selectedMapping, target.SourceSeasons, snap.Series.Seasons); err != nil {
		return out, err
	}
	imports := []sonarr.ImportedEpisode{}
	for _, ep := range snap.Episodes {
		if _, ok := scopes[ep.SeasonNumber]; ok && ep.HasFile {
			imports = append(imports, sonarr.ImportedEpisode{EpisodeID: ep.ID, SeasonNumber: ep.SeasonNumber})
		}
	}
	if len(imports) > 0 {
		corrected, _, err := s.importTVMatches(snap.Series)
		if err != nil {
			return out, err
		}
		// Sonarr reports a series' TMDB id only from v4.0.6. This series was
		// found by the request's own TVDB id, so an uncorrected series without
		// one has nothing to disambiguate, as in tvLiveStatus. A correction
		// that splits one series between titles must still prove ownership.
		if corrected || snap.Series.TmdbID != 0 {
			titles, err := s.resolveTVImports(client, snap.Series, imports)
			if err != nil {
				return out, err
			}
			matched := false
			for _, title := range titles {
				if title.TmdbID == r.tmdbID {
					matched = true
				}
			}
			if !matched {
				return out, ErrTVMatchStale
			}
		}
	}
	for _, ep := range snap.Episodes {
		source, ok := scopes[ep.SeasonNumber]
		if !ok || !ep.HasFile || ep.ID <= 0 || (target.Pilot && ep.EpisodeNumber != 1) {
			continue
		}
		out.Units = append(out.Units, discordnotify.Unit{Key: fmt.Sprintf("episode:%d:%d", snap.Series.ID, ep.ID), Label: fmt.Sprintf("S%02dE%02d", source, ep.EpisodeNumber)})
	}
	return out, ctx.Err()
}

// discordTVMatchError treats a paused TV match as an administrator's decision,
// not an outage. The request is announced again once the match is resumed.
func discordTVMatchError(err error) error {
	var failure *tvMatchError
	if errors.As(err, &failure) && failure.code == "tv_match_paused" {
		return fmt.Errorf("%w: %w", discordnotify.ErrUnverifiable, err)
	}
	return err
}

// DiscordObservationKey fingerprints what a TV availability read depends on,
// without TMDB or per-series reads: delivery acceptance, the saved target, the
// local match configuration, and the selected seasons' file statistics from
// the shared series digest (arrLibraryCacheTTL, cleared by arr webhooks). An
// unchanged key means no new file can have arrived. "" means read in full;
// movies, books, and music already read cached library digests.
func (s *Service) DiscordObservationKey(ctx context.Context, id int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r, status, err := s.loadRequest(id)
	if err != nil {
		return "", err
	}
	if r.mediaType != "tv" || r.instanceID == "" || s.registry == nil {
		return "", nil
	}
	states, err := s.deliveryStates(id)
	if err != nil {
		return "", err
	}
	delivery := []string{}
	for _, d := range states {
		delivery = append(delivery, d.Format+":"+d.State)
	}
	m, _, err := configuredTVMatch(s.db, r.tmdbID)
	if err != nil {
		return "", err
	}
	var tvdbID int
	var seasons []int
	target, _, _, err := s.loadTVTarget(id)
	switch {
	case err == nil:
		tvdbID, seasons = target.Match.TVDBID, target.TargetSeasons
	case err != sql.ErrNoRows:
		return "", err
	case m.Provenance == "default" && r.tvdbID > 0 && len(r.seasonNumbers) > 0:
		// A legacy explicit selection of an uncorrected title maps each season
		// to itself.
		tvdbID, seasons = r.tvdbID, r.seasonNumbers
	default:
		return "", nil
	}
	digest, ok := s.seriesAvailabilityDigestForInstance(r.instanceID)
	if !ok {
		return "", nil
	}
	entry, found := digest[tvdbID]
	stats := make([]seasonAvailability, 0, len(seasons))
	for _, season := range seasons {
		st, ok := entry.Seasons[season]
		if found && !ok {
			return "", nil // missing statistics are not a stable reading
		}
		stats = append(stats, st)
	}
	data, err := json.Marshal([]any{status, delivery, m.Revision, target, tvdbID, seasons, found, stats})
	if err != nil {
		return "", nil
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func (s *Service) SetDiscordAvailabilityWake(wake func()) { s.discordAvailabilityWake = wake }
func (s *Service) wakeDiscordAvailability() {
	if s.discordAvailabilityWake != nil {
		s.discordAvailabilityWake()
	}
}

// DiscordPoster reuses the bounded metadata lookup. Only TMDB CDN artwork is
// handed to Discord; private arr URLs and credentials are never exposed.
func (s *Service) DiscordPoster(ctx context.Context, subject discordnotify.Subject) string {
	if ctx.Err() != nil || (subject.MediaType != "movie" && subject.MediaType != "tv") {
		return ""
	}
	items := []PendingRequest{{MediaType: subject.MediaType, TmdbID: subject.TmdbID}}
	s.attachPosterPaths(items)
	path := items[0].PosterPath
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "?#") {
		return ""
	}
	return "https://image.tmdb.org/t/p/w500" + path
}
