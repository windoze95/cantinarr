package sonarr

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// DownloadEpisodes reads the complete grab scope of a download, then resolves
// its episode records live. Failed-download handling acts on that whole scope,
// even if some episodes have already left the queue. Never infer it from a
// release name or from a page of global history.
func (c *Client) DownloadEpisodes(downloadID string) ([]Episode, error) {
	refs, err := c.downloadEpisodeRefs(downloadID)
	if err != nil {
		return nil, err
	}
	return c.resolveDownloadEpisodes(refs)
}

func (c *Client) downloadEpisodeRefs(downloadID string) (map[int]int, error) {
	if strings.TrimSpace(downloadID) == "" {
		return nil, fmt.Errorf("cannot establish download episode scope without a download id")
	}
	var resp struct {
		TotalRecords int             `json:"totalRecords"`
		Records      []HistoryRecord `json:"records"`
	}
	path := fmt.Sprintf("/api/v3/history?page=1&pageSize=%d&sortKey=date&sortDirection=descending&eventType=1&downloadId=%s", queueMaxRecords, url.QueryEscape(downloadID))
	if err := c.do("GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("read download grab scope: %w", err)
	}
	if resp.TotalRecords < 0 || resp.TotalRecords > queueMaxRecords || len(resp.Records) != resp.TotalRecords {
		return nil, fmt.Errorf("download grab scope is incomplete")
	}
	refs := make(map[int]int)
	for _, rec := range resp.Records {
		if !strings.EqualFold(rec.DownloadID, downloadID) || rec.EventType != "grabbed" || rec.EpisodeID <= 0 || rec.SeriesID <= 0 {
			return nil, fmt.Errorf("download grab scope contains inconsistent episode identity")
		}
		if series, ok := refs[rec.EpisodeID]; ok && series != rec.SeriesID {
			return nil, fmt.Errorf("download grab scope contains conflicting series identity")
		}
		refs[rec.EpisodeID] = rec.SeriesID
	}
	return refs, nil
}

func (c *Client) resolveDownloadEpisodes(refs map[int]int) ([]Episode, error) {
	seriesEpisodes := make(map[int][]Episode)
	var result []Episode
	for episodeID, seriesID := range refs {
		episodes, ok := seriesEpisodes[seriesID]
		if !ok {
			var err error
			episodes, err = c.GetAllEpisodes(seriesID)
			if err != nil {
				return nil, err
			}
			seriesEpisodes[seriesID] = episodes
		}
		var matches []Episode
		for _, ep := range episodes {
			if ep.ID == episodeID {
				matches = append(matches, ep)
			}
		}
		if len(matches) != 1 || matches[0].SeriesID != seriesID || matches[0].SeasonNumber < 0 || matches[0].EpisodeNumber <= 0 {
			return nil, fmt.Errorf("cannot verify download episode %d in its series", episodeID)
		}
		result = append(result, matches[0])
	}
	return result, nil
}

// QueueReplacementEpisodes includes both the live queue and the download's
// complete grab history. Queue siblings protect packs whose history was pruned;
// history protects episodes in a pack that already imported and left the queue.
func (c *Client) QueueReplacementEpisodes(items []DetailedQueueItem, target DetailedQueueItem) ([]Episode, error) {
	refs := make(map[int]int)
	for _, item := range items {
		if item.ID != target.ID && (target.DownloadID == "" || !strings.EqualFold(item.DownloadID, target.DownloadID)) {
			continue
		}
		if item.SeriesID <= 0 || item.EpisodeID <= 0 ||
			(item.Series != nil && item.Series.ID != item.SeriesID) ||
			(item.Episode != nil && (item.Episode.ID != item.EpisodeID || (item.Episode.SeriesID != 0 && item.Episode.SeriesID != item.SeriesID))) {
			return nil, fmt.Errorf("cannot verify queue item %d episode identity", item.ID)
		}
		if series, ok := refs[item.EpisodeID]; ok && series != item.SeriesID {
			return nil, fmt.Errorf("queue download contains conflicting series identity")
		}
		refs[item.EpisodeID] = item.SeriesID
	}
	if target.DownloadID != "" {
		historyRefs, err := c.downloadEpisodeRefs(target.DownloadID)
		if err != nil {
			return nil, err
		}
		for episodeID, seriesID := range historyRefs {
			if series, ok := refs[episodeID]; ok && series != seriesID {
				return nil, fmt.Errorf("queue and history disagree about an episode's series")
			}
			refs[episodeID] = seriesID
		}
	}
	return c.resolveDownloadEpisodes(refs)
}

// HasUnairedEpisode is about suppressing a replacement search, not diagnosing
// bad content. It deliberately does not use the destructive pre-air detector's
// tolerance. Missing dates are unknown, not evidence that an episode is unaired.
func HasUnairedEpisode(episodes []Episode, now time.Time) bool {
	for _, ep := range episodes {
		if ep.AirDateUtc != nil && ep.AirDateUtc.After(now) {
			return true
		}
	}
	return false
}
