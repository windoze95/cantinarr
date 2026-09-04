package tracearr

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/watchhistory"
)

const (
	// statsCacheTTL bounds how often a stats window is recomputed: each
	// recomputation is up to statsMaxPages requests against the key's
	// per-minute budget.
	statsCacheTTL = 5 * time.Minute
	// statsMaxPages caps a stats walk at 2,000 plays; beyond that the answer
	// is reported as a floor.
	statsMaxPages = 20
	// historyMaxPages caps a history read at 500 rows.
	historyMaxPages = 5
	// topN is how many rows each stats bucket keeps, Tautulli's default.
	topN = 10
)

// Provider adapts a Tracearr client to the watch-history contract. Tracearr
// has no ranked-stats endpoint, so Stats is derived from paged history and
// cached per window.
type Provider struct {
	client *Client
	now    func() time.Time

	// mu is held for the whole of a stats walk so concurrent callers share
	// one fetch instead of each spending the rate budget.
	mu    sync.Mutex
	stats map[int]cachedStats
}

type cachedStats struct {
	at    time.Time
	stats watchhistory.Stats
}

// NewProvider builds a provider over a fresh client.
func NewProvider(baseURL, token string) *Provider {
	return newProvider(NewClient(baseURL, token), time.Now)
}

func newProvider(client *Client, now func() time.Time) *Provider {
	return &Provider{client: client, now: now, stats: make(map[int]cachedStats)}
}

// ServerInfo is the connection test: /health answers only to a valid key
// and names every server Tracearr monitors.
func (p *Provider) ServerInfo(ctx context.Context) (watchhistory.ServerInfo, error) {
	health, err := p.client.Health(ctx)
	if err != nil {
		return watchhistory.ServerInfo{}, err
	}
	info := watchhistory.ServerInfo{
		Name:    "Tracearr",
		Version: health.Version,
		Servers: make([]watchhistory.MonitoredServer, 0, len(health.Servers)),
	}
	for _, s := range health.Servers {
		info.Servers = append(info.Servers, watchhistory.MonitoredServer{
			ID:            s.ID,
			Name:          s.Name,
			Type:          s.Type,
			Online:        s.Online,
			ActiveStreams: s.ActiveStreams,
		})
	}
	return info, nil
}

// Activity is what is playing across every monitored server. Bandwidth is
// the sum of per-stream bitrates; Tracearr's own total is a formatted label.
func (p *Provider) Activity(ctx context.Context) (watchhistory.Activity, error) {
	streams, err := p.client.Streams(ctx)
	if err != nil {
		return watchhistory.Activity{}, err
	}
	out := watchhistory.Activity{
		StreamCount: len(streams.Data),
		Streams:     make([]watchhistory.Stream, 0, len(streams.Data)),
	}
	for _, s := range streams.Data {
		out.TotalBandwidthKbps += int(s.Bitrate)
		out.Streams = append(out.Streams, watchhistory.Stream{
			User:            s.Username,
			Title:           s.MediaTitle,
			FullTitle:       fullTitle(s.MediaType, s.MediaTitle, s.ShowTitle, int(s.SeasonNumber), int(s.EpisodeNumber), s.ArtistName),
			Player:          firstNonEmpty(s.Player, s.Device),
			Product:         s.Product,
			State:           s.State,
			ProgressPercent: percent(int64(s.ProgressMS), int64(s.DurationMS)),
			Quality:         quality(s.Resolution, s.StreamVideoCodecDisplay, s.SourceVideoCodecDisplay),
			StreamType:      streamType(s.IsTranscode, s.VideoDecision, s.AudioDecision),
			BandwidthKbps:   int(s.Bitrate),
			MediaType:       s.MediaType,
			Server:          s.ServerName,
			ServerType:      s.ServerType,
		})
	}
	return out, nil
}

// History is the most recent plays, newest first, up to limit rows and
// historyMaxPages pages.
func (p *Provider) History(ctx context.Context, limit int) (watchhistory.History, error) {
	pageSize := limit
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	// The limit stops the walk; the page cap only guards against an upstream
	// that serves smaller pages than asked for.
	items := make([]watchhistory.HistoryEntry, 0, limit)
	truncated, err := p.client.WalkHistory(ctx, HistoryQuery{PageSize: pageSize}, historyMaxPages, func(r HistoryRecord) bool {
		items = append(items, historyEntry(r))
		return len(items) < limit
	})
	if err != nil {
		return watchhistory.History{}, err
	}
	note := fmt.Sprintf("The %d most recent plays across every server Tracearr monitors; anything older is outside this window.", len(items))
	if truncated {
		note += fmt.Sprintf(" Capped at %d rows.", len(items))
	}
	return watchhistory.History{
		Items:    items,
		Coverage: watchhistory.Coverage{Plays: len(items), Truncated: truncated, Note: note},
	}, nil
}

// Stats ranks the last days of history. The walk is bounded and cached; the
// coverage says exactly what was read.
func (p *Provider) Stats(ctx context.Context, days int) (watchhistory.Stats, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now().UTC()
	if cached, ok := p.stats[days]; ok && now.Sub(cached.at) < statsCacheTTL {
		return cached.stats, nil
	}

	since := now.AddDate(0, 0, -days)
	agg := newAggregator()
	truncated, err := p.client.WalkHistory(ctx, HistoryQuery{PageSize: MaxPageSize, Since: since}, statsMaxPages, func(r HistoryRecord) bool {
		agg.add(r)
		return true
	})
	if err != nil {
		return watchhistory.Stats{}, err
	}
	stats := agg.result()
	stats.Coverage = watchhistory.Coverage{
		Plays:     agg.plays,
		Since:     since,
		Until:     now,
		Truncated: truncated,
		Note:      statsNote(agg.plays, since, truncated),
	}
	p.stats[days] = cachedStats{at: now, stats: stats}
	return stats, nil
}

func statsNote(plays int, since time.Time, truncated bool) string {
	day := since.Format("2 Jan 2006")
	switch {
	case plays == 0:
		return fmt.Sprintf("No plays recorded by Tracearr since %s on any server it monitors; nothing older than that was searched.", day)
	case truncated:
		return fmt.Sprintf("Based on the %d most recent plays Tracearr recorded since %s (the first %d pages); older plays inside the window were not read, so counts are a floor.", plays, day, statsMaxPages)
	default:
		return fmt.Sprintf("Based on %d plays Tracearr recorded since %s across every server it monitors.", plays, day)
	}
}

// --- mapping ---

// streamType renders Tracearr's per-track decisions in Tautulli's vocabulary,
// which the app matches by substring.
func streamType(isTranscode bool, video, audio string) string {
	if isTranscode || video == "transcode" || audio == "transcode" {
		return "transcode"
	}
	if video == "copy" || audio == "copy" {
		return "copy"
	}
	return "direct play"
}

// quality is "1080p HEVC": the resolution plus the codec being delivered,
// falling back to the source codec when nothing is being re-encoded.
func quality(resolution, streamCodec, sourceCodec string) string {
	return strings.TrimSpace(resolution + " " + firstNonEmpty(streamCodec, sourceCodec))
}

// fullTitle composes the one-line title the app shows: show, season and
// episode for TV, artist for music, the title alone otherwise.
func fullTitle(mediaType, title, show string, season, episode int, artist string) string {
	switch mediaType {
	case "episode":
		parts := make([]string, 0, 3)
		if show != "" {
			parts = append(parts, show)
		}
		if season > 0 || episode > 0 {
			parts = append(parts, fmt.Sprintf("S%02dE%02d", season, episode))
		}
		if title != "" {
			parts = append(parts, title)
		}
		return strings.Join(parts, " - ")
	case "track":
		if artist != "" && title != "" {
			return artist + " - " + title
		}
		return firstNonEmpty(title, artist)
	default:
		return title
	}
}

// percent is progress over duration, clamped to 0..100; zero when the
// duration is unknown.
func percent(progressMS, durationMS int64) int {
	if durationMS <= 0 || progressMS <= 0 {
		return 0
	}
	pct := progressMS * 100 / durationMS
	if pct > 100 {
		return 100
	}
	return int(pct)
}

func roundPercent(pct float64) int {
	if math.IsNaN(pct) || pct <= 0 {
		return 0
	}
	if pct >= 100 {
		return 100
	}
	return int(math.Round(pct))
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func historyEntry(r HistoryRecord) watchhistory.HistoryEntry {
	return watchhistory.HistoryEntry{
		User:            r.User.Username,
		FullTitle:       fullTitle(r.MediaType, r.MediaTitle, r.ShowTitle, int(r.SeasonNumber), int(r.EpisodeNumber), r.ArtistName),
		Date:            parseTime(r.StartedAt),
		DurationSeconds: int(int64(r.DurationMS) / 1000),
		PercentComplete: roundPercent(float64(r.PercentComplete)),
		Player:          firstNonEmpty(r.Player, r.Device),
		Platform:        r.Platform,
		MediaType:       r.MediaType,
		Server:          r.ServerName,
		ServerType:      r.ServerType,
	}
}

// --- aggregation ---

type counter struct {
	label string
	plays int
}

// aggregator counts plays per movie, show and viewer. Every history row is
// one play, whatever its watched flag: that is what Tautulli's total_plays
// counts, and filtering here would silently undercount beside it. Rows are
// keyed by Tracearr's ids, so two titles that merely share a name stay two
// rows.
type aggregator struct {
	plays  int
	movies map[string]*counter
	shows  map[string]*counter
	users  map[string]*counter
}

func newAggregator() *aggregator {
	return &aggregator{
		movies: make(map[string]*counter),
		shows:  make(map[string]*counter),
		users:  make(map[string]*counter),
	}
}

func (a *aggregator) add(r HistoryRecord) {
	a.plays++
	switch r.MediaType {
	case "movie":
		bump(a.movies, firstNonEmpty(r.MediaID, r.MediaTitle), r.MediaTitle)
	case "episode":
		bump(a.shows, firstNonEmpty(r.ShowMediaID, r.ShowTitle, r.MediaTitle), firstNonEmpty(r.ShowTitle, r.MediaTitle))
	}
	if user := firstNonEmpty(r.User.ID, r.User.Username); user != "" {
		bump(a.users, user, firstNonEmpty(r.User.Username, r.User.ID))
	}
}

func bump(m map[string]*counter, key, label string) {
	if key == "" {
		return
	}
	c, ok := m[key]
	if !ok {
		c = &counter{label: label}
		m[key] = c
	}
	c.plays++
}

func ranked(m map[string]*counter) []counter {
	out := make([]counter, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].plays != out[j].plays {
			return out[i].plays > out[j].plays
		}
		return out[i].label < out[j].label
	})
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}

func (a *aggregator) result() watchhistory.Stats {
	stats := watchhistory.Stats{
		TopMovies: []watchhistory.TitleCount{},
		TopShows:  []watchhistory.TitleCount{},
		TopUsers:  []watchhistory.UserCount{},
	}
	for _, c := range ranked(a.movies) {
		stats.TopMovies = append(stats.TopMovies, watchhistory.TitleCount{Title: c.label, Plays: c.plays})
	}
	for _, c := range ranked(a.shows) {
		stats.TopShows = append(stats.TopShows, watchhistory.TitleCount{Title: c.label, Plays: c.plays})
	}
	for _, c := range ranked(a.users) {
		stats.TopUsers = append(stats.TopUsers, watchhistory.UserCount{User: c.label, Plays: c.plays})
	}
	return stats
}
