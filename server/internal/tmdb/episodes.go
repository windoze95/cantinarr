package tmdb

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// TVEpisode identifies a catalog episode independently of a media server's
// mutable item IDs and numbering order. AirDate is the catalog's UTC date.
type TVEpisode struct {
	ID            int         `json:"id"`
	SeasonNumber  int         `json:"season_number"`
	EpisodeNumber int         `json:"episode_number"`
	AirDate       string      `json:"air_date"`
	Name          string      `json:"name"`
	ExternalIDs   ExternalIDs `json:"external_ids"`
}

// FindTVEpisode resolves a stable TVDB episode ID without guessing display
// numbering. Callers still validate the returned show and catalog episode.
func (c *Client) FindTVEpisode(tvdbID int) (int, int, *int, error) {
	raw, err := c.DoGetRaw(fmt.Sprintf("/find/%d", tvdbID), url.Values{"external_source": {"tvdb_id"}})
	if err != nil {
		return 0, 0, nil, err
	}
	var out struct {
		Episodes []struct {
			ID           int  `json:"id"`
			ShowID       int  `json:"show_id"`
			SeasonNumber *int `json:"season_number"`
		} `json:"tv_episode_results"`
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return 0, 0, nil, err
	}
	if len(out.Episodes) != 1 || out.Episodes[0].ID <= 0 || out.Episodes[0].ShowID <= 0 {
		return 0, 0, nil, fmt.Errorf("TV episode identity is ambiguous")
	}
	return out.Episodes[0].ShowID, out.Episodes[0].ID, out.Episodes[0].SeasonNumber, nil
}

type TVSeasonDetails struct {
	ID           int         `json:"id"`
	SeasonNumber int         `json:"season_number"`
	Episodes     []TVEpisode `json:"episodes"`
}

func (c *Client) GetTVSeason(tmdbID, season int) (*TVSeasonDetails, error) {
	raw, err := c.DoGetRaw(fmt.Sprintf("/tv/%d/season/%d", tmdbID, season), nil)
	if err != nil {
		return nil, err
	}
	var out TVSeasonDetails
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out.SeasonNumber != season {
		return nil, fmt.Errorf("TV season identity changed")
	}
	seen := map[int]bool{}
	for _, ep := range out.Episodes {
		if ep.ID <= 0 || ep.SeasonNumber != season || ep.EpisodeNumber <= 0 || seen[ep.EpisodeNumber] {
			return nil, fmt.Errorf("TV episode identity is ambiguous")
		}
		seen[ep.EpisodeNumber] = true
	}
	return &out, nil
}

func (c *Client) GetTVEpisodeExternalIDs(tmdbID, season, episode int) (*ExternalIDs, error) {
	raw, err := c.DoGetRaw(fmt.Sprintf("/tv/%d/season/%d/episode/%d/external_ids", tmdbID, season, episode), nil)
	if err != nil {
		return nil, err
	}
	var out ExternalIDs
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
