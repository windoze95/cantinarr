package sonarr

import (
	"fmt"
	"time"
)

// SubmitEpisodeSearch returns the command receipt for exact episode work.
// Callers must preserve an uncertain result instead of automatically replaying
// a search whose response was lost.
func (c *Client) SubmitEpisodeSearch(ids []int) (int, error) {
	if len(ids) == 0 {
		return 0, fmt.Errorf("no episodes selected")
	}
	for _, id := range ids {
		if id <= 0 {
			return 0, fmt.Errorf("invalid episode identity")
		}
	}
	var response struct {
		ID int `json:"id"`
	}
	if err := c.do("POST", "/api/v3/command", map[string]any{"name": "EpisodeSearch", "episodeIds": ids}, &response); err != nil {
		return 0, err
	}
	if response.ID <= 0 {
		return 0, fmt.Errorf("episode search receipt is missing")
	}
	return response.ID, nil
}

// FindEpisodeSearch is positive reconciliation only. Missing/expired command
// history cannot prove that a previous POST was never accepted.
func (c *Client) FindEpisodeSearch(episodeID int, since time.Time) (int, error) {
	var commands []struct {
		ID     int       `json:"id"`
		Name   string    `json:"name"`
		Queued time.Time `json:"queued"`
		Body   struct {
			EpisodeIDs []int `json:"episodeIds"`
		} `json:"body"`
	}
	if err := c.do("GET", "/api/v3/command", nil, &commands); err != nil {
		return 0, err
	}
	for _, command := range commands {
		if command.ID > 0 && command.Name == "EpisodeSearch" && !command.Queued.Before(since) && len(command.Body.EpisodeIDs) == 1 && command.Body.EpisodeIDs[0] == episodeID {
			return command.ID, nil
		}
	}
	return 0, nil
}

// EpisodeSearchPending distinguishes an active command from an old receipt.
// Completed searches must not suppress a later deliberate request after a file
// is removed. Unreadable receipts remain unknown to the caller.
func (c *Client) EpisodeSearchPending(commandID, episodeID int) (bool, error) {
	var command struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		Body   struct {
			IDs []int `json:"episodeIds"`
		} `json:"body"`
	}
	if err := c.do("GET", fmt.Sprintf("/api/v3/command/%d", commandID), nil, &command); err != nil {
		return false, err
	}
	if command.ID != commandID || command.Name != "EpisodeSearch" || len(command.Body.IDs) != 1 || command.Body.IDs[0] != episodeID {
		return false, fmt.Errorf("episode search identity changed")
	}
	switch command.Status {
	case "queued", "started", "cancelling":
		return true, nil
	case "completed", "failed", "aborted":
		return false, nil
	default:
		return false, fmt.Errorf("episode search state is unknown")
	}
}
