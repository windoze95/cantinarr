package tautulli

import (
	"context"
	"fmt"
	"time"

	"github.com/windoze95/cantinarr-server/internal/watchhistory"
)

// Provider adapts a Tautulli client to the watch-history contract. Tautulli
// watches exactly one Plex server, so every stream and play is Plex and no
// server name is reported.
type Provider struct {
	client *Client
}

// NewProvider builds a provider over a fresh client.
func NewProvider(baseURL, apiKey string) *Provider {
	return &Provider{client: NewClient(baseURL, apiKey)}
}

// ServerInfo is the connection test: get_server_info answers only with a
// valid API key.
func (p *Provider) ServerInfo(_ context.Context) (watchhistory.ServerInfo, error) {
	info, err := p.client.GetServerInfo()
	if err != nil {
		return watchhistory.ServerInfo{}, err
	}
	return watchhistory.ServerInfo{Name: info.PMSName, Version: info.PMSVersion}, nil
}

// Activity maps get_activity: quality_profile becomes quality,
// transcode_decision becomes stream_type, bandwidth becomes bandwidth_kbps.
func (p *Provider) Activity(_ context.Context) (watchhistory.Activity, error) {
	activity, err := p.client.GetActivity()
	if err != nil {
		return watchhistory.Activity{}, err
	}
	out := watchhistory.Activity{
		StreamCount:        int(activity.StreamCount),
		TotalBandwidthKbps: int(activity.TotalBandwidth),
		Streams:            make([]watchhistory.Stream, 0, len(activity.Sessions)),
	}
	for _, s := range activity.Sessions {
		out.Streams = append(out.Streams, watchhistory.Stream{
			User:            s.User,
			Title:           s.Title,
			FullTitle:       s.FullTitle,
			Player:          s.Player,
			Product:         s.Product,
			State:           s.State,
			ProgressPercent: int(s.ProgressPercent),
			Quality:         s.QualityProfile,
			StreamType:      s.TranscodeDecision,
			BandwidthKbps:   int(s.Bandwidth),
			MediaType:       s.MediaType,
			ServerType:      "plex",
		})
	}
	return out, nil
}

// History maps get_history; dates arrive as unix seconds and leave as UTC
// instants, zero when Tautulli sent none.
func (p *Provider) History(_ context.Context, limit int) (watchhistory.History, error) {
	rows, err := p.client.GetHistory(limit)
	if err != nil {
		return watchhistory.History{}, err
	}
	out := watchhistory.History{
		Items: make([]watchhistory.HistoryEntry, 0, len(rows)),
		Coverage: watchhistory.Coverage{
			Plays: len(rows),
			Note:  fmt.Sprintf("The %d most recent plays Tautulli recorded; anything older is outside this window.", len(rows)),
		},
	}
	for _, row := range rows {
		var date time.Time
		if row.Date > 0 {
			date = time.Unix(int64(row.Date), 0).UTC()
		}
		out.Items = append(out.Items, watchhistory.HistoryEntry{
			User:            row.User,
			FullTitle:       row.FullTitle,
			Date:            date,
			DurationSeconds: int(row.Duration),
			PercentComplete: int(row.PercentComplete),
			Player:          row.Player,
			Platform:        row.Platform,
			ServerType:      "plex",
		})
	}
	return out, nil
}

// Stats maps get_home_stats: top_movies and top_users keep their names,
// top_tv becomes the shows bucket, and every other block is dropped. A
// user's friendly name wins over the account name when Tautulli has one.
func (p *Provider) Stats(_ context.Context, days int) (watchhistory.Stats, error) {
	blocks, err := p.client.GetHomeStats(days)
	if err != nil {
		return watchhistory.Stats{}, err
	}
	now := time.Now().UTC()
	out := watchhistory.Stats{
		TopMovies: []watchhistory.TitleCount{},
		TopShows:  []watchhistory.TitleCount{},
		TopUsers:  []watchhistory.UserCount{},
		Coverage: watchhistory.Coverage{
			Since: now.AddDate(0, 0, -days),
			Until: now,
			Note:  fmt.Sprintf("Ranked by Tautulli over the last %d days.", days),
		},
	}
	for _, block := range blocks {
		switch block.StatID {
		case "top_movies":
			for _, row := range block.Rows {
				out.TopMovies = append(out.TopMovies, watchhistory.TitleCount{Title: row.Title, Plays: int(row.TotalPlays)})
			}
		case "top_tv":
			for _, row := range block.Rows {
				out.TopShows = append(out.TopShows, watchhistory.TitleCount{Title: row.Title, Plays: int(row.TotalPlays)})
			}
		case "top_users":
			for _, row := range block.Rows {
				user := row.FriendlyName
				if user == "" {
					user = row.User
				}
				out.TopUsers = append(out.TopUsers, watchhistory.UserCount{User: user, Plays: int(row.TotalPlays)})
			}
		}
	}
	return out, nil
}
