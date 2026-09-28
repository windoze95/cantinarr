package lidarr

import "github.com/windoze95/cantinarr-server/internal/arr"

func (c *Client) RequesterTags() *arr.RequesterTags {
	return arr.NewRequesterTags(c.httpClient, c.baseURL, c.apiKey, "artist", "foreignArtistId")
}
