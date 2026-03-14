package trakt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const baseURL = "https://api.trakt.tv"

type Client struct {
	clientID   string
	httpClient *http.Client
}

func NewClient(clientID string) *Client {
	return &Client{
		clientID: clientID,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type IDResult struct {
	TVDBID int
	IMDBID string
}

type searchResult struct {
	Show *struct {
		IDs struct {
			TVDB int    `json:"tvdb"`
			IMDB string `json:"imdb"`
		} `json:"ids"`
	} `json:"show,omitempty"`
}

func (c *Client) SearchByTMDB(tmdbID int, mediaType string) (*IDResult, error) {
	url := fmt.Sprintf("%s/search/tmdb/%d?type=%s", baseURL, tmdbID, mediaType)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", c.clientID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trakt request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trakt API returned status %d", resp.StatusCode)
	}

	var results []searchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode trakt response: %w", err)
	}

	if len(results) == 0 || results[0].Show == nil {
		return nil, fmt.Errorf("no results found")
	}

	return &IDResult{
		TVDBID: results[0].Show.IDs.TVDB,
		IMDBID: results[0].Show.IDs.IMDB,
	}, nil
}
