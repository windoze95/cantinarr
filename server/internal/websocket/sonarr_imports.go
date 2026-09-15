package websocket

import (
	"log"
	"sort"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

func (h *Hub) SetTVImportResolver(resolver sonarr.ImportResolver) { h.tvImports = resolver }

// sonarrImportEvidence keeps episode identity until after reverse resolution.
// Upgrade proof remains count-aware per episode; only the final title grouping
// decides whether a mixed batch is news for the household.
func sonarrImportEvidence(client *sonarr.Client, since time.Time) (map[int][]sonarr.ImportedEpisode, error) {
	records, complete, err := client.GetImportHistorySince(since, catchUpHistoryPageSize)
	if err != nil {
		return nil, err
	}
	if !complete {
		return nil, errImportBacklogOverflow
	}
	imports := map[int][]sonarr.ImportedEpisode{}
	counts := map[int]int{}
	for _, rec := range records {
		if !strings.EqualFold(rec.EventType, "downloadFolderImported") {
			continue
		}
		seriesID, episodeID, season := rec.SeriesID, rec.EpisodeID, 0
		if seriesID <= 0 && rec.Series != nil {
			seriesID = rec.Series.ID
		}
		if rec.Episode != nil {
			if seriesID <= 0 {
				seriesID = rec.Episode.SeriesID
			}
			if episodeID <= 0 {
				episodeID = rec.Episode.ID
			}
			// Contradictory embedded identity is not season evidence.
			if (rec.Episode.ID == 0 || rec.Episode.ID == episodeID) &&
				(rec.Episode.SeriesID == 0 || rec.Episode.SeriesID == seriesID) {
				season = rec.Episode.SeasonNumber
			}
		}
		if seriesID <= 0 {
			continue
		}
		imports[seriesID] = append(imports[seriesID], sonarr.ImportedEpisode{EpisodeID: episodeID, SeasonNumber: season, ImportedAt: rec.Date})
		counts[episodeID]++
	}
	if len(imports) == 0 {
		return imports, nil
	}
	deletes := sonarrUpgradeDeletesSince(client, since)
	for _, episodes := range imports {
		for i := range episodes {
			id := episodes[i].EpisodeID
			episodes[i].Upgrade = id > 0 && deletes[id] >= counts[id]
		}
	}
	return imports, nil
}

// resolveSonarrAnnouncements uses import receipts for corrected parents even
// while they stay in the queue. Ordinary series retain the existing departure
// fallback. Resumptions share the six-hour window, one-page limit, enrollment
// hold and independent ten-title caps with the other arrs.
func (h *Hub) resolveSonarrAnnouncements(instanceID string, client *sonarr.Client, current map[int]float64, departed []int) (titles []sonarr.ImportedTitle, refreshed []int, hold bool) {
	since, boot := h.resumeOrigin(instanceID)
	resuming := !since.IsZero()
	if resuming && time.Since(since) > queueWitnessStaleAfter {
		delete(h.restoredWitness, instanceID)
		log.Printf("websocket: dropping stale resumed TV imports for %s", instanceID)
		return nil, departed, false
	}
	if !resuming {
		since = h.lastPollAt[instanceID]
	}
	observed := unionInts(progressKeys(current), progressKeys(h.prevSonarrQueue[instanceID]))
	seriesCache := map[int]*sonarr.Series{}
	corrected := map[int]bool{}
	load := func(id int) *sonarr.Series {
		if series, ok := seriesCache[id]; ok {
			return series
		}
		series, err := client.GetSeries(id)
		seriesCache[id] = series
		if err != nil {
			log.Printf("websocket: cannot verify imported TV series %d (%s)", id, instanceID)
			return nil
		}
		if h.tvImports != nil {
			var e error
			corrected[id], e = h.tvImports.HasTVImportCorrection(series)
			if e != nil {
				corrected[id] = true // configuration blindness cannot authorize a fallback
				log.Printf("websocket: TV import configuration (%s): %v", instanceID, e)
			}
		}
		return series
	}
	needsHistory := resuming
	for _, id := range observed {
		if load(id) != nil && corrected[id] {
			needsHistory = true
		}
	}
	imports := map[int][]sonarr.ImportedEpisode{}
	if needsHistory && !since.IsZero() {
		var err error
		imports, err = sonarrImportEvidence(client, since)
		if err == errImportBacklogOverflow {
			delete(h.restoredWitness, instanceID)
			log.Printf("websocket: skipping TV import alerts for %s: history exceeds one page", instanceID)
			return nil, departed, false
		}
		if err != nil {
			log.Printf("websocket: TV import history unavailable (%s); corrected scopes skipped: %v", instanceID, err)
			imports = map[int][]sonarr.ImportedEpisode{}
		}
	}
	// A first poll only seeds the witness. Never inspect old files or replay
	// historical imports merely because a correction shipped in an upgrade.
	ids := append([]int(nil), departed...)
	for id := range imports {
		if resuming || containsID(observed, id) {
			ids = unionInts(ids, []int{id})
		}
	}
	byTitle := map[int]sonarr.ImportedTitle{}
	for _, id := range ids {
		series := load(id)
		if series == nil {
			continue
		}
		refreshed = append(refreshed, id)
		episodes := imports[id]
		if corrected[id] {
			if floor, legacy := h.legacySonarrSince[instanceID]; legacy {
				fresh := make([]sonarr.ImportedEpisode, 0, len(episodes))
				for _, episode := range episodes {
					if episode.ImportedAt.After(floor) {
						fresh = append(fresh, episode)
					}
				}
				if len(fresh) < len(episodes) {
					log.Printf("websocket: skipping pre-upgrade corrected TV imports from legacy witness (%s)", instanceID)
				}
				episodes = fresh
			}
			// History may omit the embedded episode. Resolve only that exact
			// imported episode from the live list, never the parent's other files.
			live, err := client.GetAllEpisodes(id)
			if err != nil {
				log.Printf("websocket: skipping unverifiable TV imports for series %d (%s)", id, instanceID)
				continue
			}
			byID := map[int]sonarr.Episode{}
			for _, episode := range live {
				byID[episode.ID] = episode
			}
			verified := make([]sonarr.ImportedEpisode, 0, len(episodes))
			for _, episode := range episodes {
				actual, ok := byID[episode.EpisodeID]
				if !ok || !actual.HasFile || (actual.SeriesID > 0 && actual.SeriesID != id) ||
					(episode.SeasonNumber > 0 && episode.SeasonNumber != actual.SeasonNumber) {
					log.Printf("websocket: skipping unverified imported episode %d of series %d (%s)", episode.EpisodeID, id, instanceID)
					continue
				}
				episode.SeasonNumber = actual.SeasonNumber
				verified = append(verified, episode)
			}
			episodes = verified
		}
		resolved := sonarr.UncorrectedImportTitle(series, episodes)
		if h.tvImports != nil {
			var err error
			resolved, err = h.tvImports.ResolveTVImports(client, series, episodes)
			if err != nil {
				log.Printf("websocket: skipping unresolved TV import scope (%s): %v", instanceID, err)
			}
		}
		for _, title := range resolved {
			if previous, ok := byTitle[title.TmdbID]; ok {
				title.Upgrade = title.Upgrade && previous.Upgrade
			}
			byTitle[title.TmdbID] = title
		}
	}
	if resuming && boot && len(byTitle) > 0 && !h.contentReady() {
		return nil, refreshed, true
	}
	delete(h.restoredWitness, instanceID)
	counts := map[bool]int{}
	for _, title := range byTitle {
		counts[title.Upgrade]++
	}
	for _, title := range byTitle {
		if !resuming || counts[title.Upgrade] <= restoredAlertCap {
			titles = append(titles, title)
		}
	}
	if resuming && (counts[false] > restoredAlertCap || counts[true] > restoredAlertCap) {
		log.Printf("websocket: skipped over-cap resumed TV alerts (%s): %d new, %d upgraded", instanceID, counts[false], counts[true])
	}
	sort.Slice(titles, func(i, j int) bool { return titles[i].TmdbID < titles[j].TmdbID })
	return titles, refreshed, false
}

func containsID(ids []int, id int) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}
