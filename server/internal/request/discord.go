package request

import (
	"context"
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
		if !accepted[""] {
			return out, nil
		}
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
	if err != nil {
		if err != sql.ErrNoRows {
			return out, err
		}
		// Legacy explicit season selections can still be proved. A coarse old
		// selection without a snapshot is not permission to guess its seasons.
		if len(r.seasonNumbers) == 0 {
			return out, fmt.Errorf("saved TV season selection cannot be verified")
		}
		m, err := s.resolveTVMatch(client, r.tmdbID)
		if err != nil {
			return out, err
		}
		target = &TVRequestTarget{Match: *m, SourceSeasons: r.seasonNumbers}
		for _, season := range r.seasonNumbers {
			target.TargetSeasons = append(target.TargetSeasons, m.SeasonMap[season])
		}
	}
	m, err := s.resolveTVMatch(client, r.tmdbID)
	if err != nil {
		return out, err
	}
	if m.TVDBID != target.Match.TVDBID || m.Revision != target.Match.Revision {
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
	for _, ep := range snap.Episodes {
		source, ok := scopes[ep.SeasonNumber]
		if !ok || !ep.HasFile || ep.ID <= 0 || (target.Pilot && ep.EpisodeNumber != 1) {
			continue
		}
		out.Units = append(out.Units, discordnotify.Unit{Key: fmt.Sprintf("episode:%d:%d", snap.Series.ID, ep.ID), Label: fmt.Sprintf("S%02dE%02d", source, ep.EpisodeNumber)})
	}
	return out, ctx.Err()
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
