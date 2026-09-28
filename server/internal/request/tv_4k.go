package request

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"time"

	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// tv4KCacheTTL bounds how long a show's 4K answer is reused for an unchanged
// set of episode files. An import, upgrade or deletion changes the set (Sonarr
// gives every new file a new id), so those are re-read on the next status
// read. The TTL only covers Sonarr re-analysing a file in place.
const tv4KCacheTTL = 30 * time.Minute

// markTV4K sets Is4K on a TV status, while the admin's 4K badges switch is
// on, when the selected library holds the whole show and Sonarr measured
// every counted episode file at 4K. A show with any file below 4K, any file
// Sonarr never analysed, or missing episodes gets no claim, and neither does
// a failed read. The response has already passed the library grant and
// content policy checks; this reads the same library.
func (s *Service) markTV4K(userID int64, instanceID string, resp *StatusResponse) {
	// Off means off: an app cannot buy the Sonarr read the admin did not
	// turn on.
	if s.cover4KBadges == nil || !s.cover4KBadges() {
		return
	}
	if resp == nil || resp.Status != StatusAvailable || resp.StatusKnown == nil || !*resp.StatusKnown ||
		resp.Match == nil || resp.Match.SeriesID <= 0 || len(resp.tvFileIDs) == 0 {
		return
	}
	client, resolved, err := s.resolveSonarr(userID, instanceID)
	if err != nil || client == nil {
		return
	}
	key := tv4KCacheKey(resolved, resp.Match.SeriesID, resp.tvFileIDs)
	if s.libraryCache != nil {
		if cached, ok := s.libraryCache.Get(key); ok {
			resp.Is4K = string(cached) == "1"
			return
		}
	}
	files, err := client.GetEpisodeFiles(resp.Match.SeriesID)
	if err != nil {
		return
	}
	is4K := episodeFilesMeasure4K(files, resp.tvFileIDs)
	if s.libraryCache != nil {
		value := "0"
		if is4K {
			value = "1"
		}
		s.libraryCache.Set(key, []byte(value), tv4KCacheTTL)
	}
	resp.Is4K = is4K
}

// episodeFilesMeasure4K requires a measured 4K record for every wanted file.
// Records no episode points at (Sonarr can briefly hold duplicates) are
// ignored rather than allowed to vote.
func episodeFilesMeasure4K(files []sonarr.EpisodeFile, wanted []int) bool {
	byID := make(map[int]*sonarr.FileMediaInfo, len(files))
	for i := range files {
		byID[files[i].ID] = files[i].MediaInfo
	}
	for _, id := range wanted {
		if !byID[id].Measures4K() {
			return false
		}
	}
	return len(wanted) > 0
}

// tv4KCacheKey names one library's series and the exact files counted, so a
// changed file set never reuses an answer given for the old one.
func tv4KCacheKey(instanceID string, seriesID int, fileIDs []int) string {
	sorted := append([]int(nil), fileIDs...)
	sort.Ints(sorted)
	h := fnv.New64a()
	for _, id := range sorted {
		_, _ = h.Write([]byte(strconv.Itoa(id)))
		_, _ = h.Write([]byte{','})
	}
	return fmt.Sprintf("series-4k:%s:%d:%x", instanceID, seriesID, h.Sum64())
}
