package arr

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

var errArtistTagRefresh = &transporterr.Upstream{
	Message:   "Lidarr is refreshing this artist. Requester tagging will retry after the refresh.",
	Transient: true,
}

// artistTagRefreshes returns the relevant completed command IDs. Comparing
// these before and after the write also catches a refresh that both starts and
// finishes during the attempt. Empty artist scopes mean a library-wide refresh.
func (c *RequesterTags) artistTagRefreshes(ctx context.Context, artistID int) ([]int, error) {
	var commands []struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		Body   struct {
			Name      string `json:"name"`
			ArtistID  int    `json:"artistId"`
			ArtistIDs []int  `json:"artistIds"`
		} `json:"body"`
	}
	if err := c.do(ctx, http.MethodGet, c.apiPath+"/command", nil, &commands, nil); err != nil {
		return nil, err
	}
	if commands == nil {
		return nil, &transporterr.Upstream{Message: "Lidarr refresh status could not be verified. Requester tagging will retry.", Transient: true}
	}
	var completed []int
	for _, command := range commands {
		name := command.Name
		if name == "" {
			name = command.Body.Name
		}
		if !strings.EqualFold(name, "RefreshArtist") && !strings.EqualFold(name, "BulkRefreshArtist") {
			continue
		}
		if command.Body.ArtistID != artistID && !slices.Contains(command.Body.ArtistIDs, artistID) && (command.Body.ArtistID > 0 || len(command.Body.ArtistIDs) > 0) {
			continue
		}
		switch strings.ToLower(command.Status) {
		case "completed", "failed", "aborted", "cancelled":
			completed = append(completed, command.ID)
		default:
			return nil, errArtistTagRefresh
		}
	}
	slices.Sort(completed)
	return completed, nil
}
