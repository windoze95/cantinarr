// Package tracearr provides a client for Tracearr's read-only public API and
// adapts it to the watchhistory.Provider contract. Tracearr watches Plex,
// Jellyfin and Emby servers from one place; Cantinarr reads what is playing
// and what was played, never artwork (Tracearr serves it from its own
// origin, which clients must not be pointed at).
package tracearr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

const (
	healthPath  = "/api/v1/public/health"
	streamsPath = "/api/v2/public/streams"
	historyPath = "/api/v2/public/history"

	// MaxPageSize is the largest page Tracearr's v2 API serves.
	MaxPageSize = 100
)

var (
	// ErrKeyRejected is the 401/403 family: the key is missing, wrong, or
	// not the owner's.
	ErrKeyRejected = errors.New("tracearr rejected the API key")
	// ErrRateLimited is a 429; RetryAfter on the error says how long to wait.
	ErrRateLimited = errors.New("tracearr rate limit reached")
)

// Client talks to one Tracearr instance with a public API key
// (Authorization: Bearer trr_pub_...).
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a client. Redirects are never followed: a redirect would
// replay the bearer token to whatever host the upstream named.
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Transport:     httpx.Internal(),
			Timeout:       30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// statusError is a non-2xx answer. The response body is never kept: an
// upstream may echo request details, and 401/403/429 need no explanation
// beyond their status.
type statusError struct {
	op         string
	status     int
	retryAfter time.Duration
}

func (e *statusError) Error() string {
	switch e.status {
	case http.StatusUnauthorized:
		return ErrKeyRejected.Error()
	case http.StatusForbidden:
		return "tracearr refused the API key: it must belong to the owner account"
	case http.StatusTooManyRequests:
		if e.retryAfter > 0 {
			return fmt.Sprintf("%s; retry after %s", ErrRateLimited.Error(), e.retryAfter.Round(time.Second))
		}
		return ErrRateLimited.Error() + "; retry later"
	default:
		return fmt.Sprintf("tracearr %s: server returned status %d", e.op, e.status)
	}
}

// Is lets callers classify with errors.Is(err, ErrKeyRejected) and
// errors.Is(err, ErrRateLimited).
func (e *statusError) Is(target error) bool {
	switch target {
	case ErrKeyRejected:
		return e.status == http.StatusUnauthorized || e.status == http.StatusForbidden
	case ErrRateLimited:
		return e.status == http.StatusTooManyRequests
	}
	return false
}

// RetryAfter is the wait a 429 asked for, zero when it named none.
func (e *statusError) RetryAfter() time.Duration { return e.retryAfter }

// parseRetryAfter reads Retry-After as delay seconds or an HTTP date.
func parseRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}

// get performs an authenticated GET and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, query url.Values, op string, out interface{}) error {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("tracearr %s: build request: %w", op, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// The token travels in a header, never the URL, so a transport
		// error cannot carry it; the host it can carry is summarised away.
		return fmt.Errorf("tracearr %s: %s", op, transporterr.Summarize(err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("tracearr %s: read response: %w", op, err)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location := redactSecret(resp.Header.Get("Location"), c.token)
		return fmt.Errorf("tracearr %s: server returned redirect status %d to %q (redirects are not followed; use the service's final URL)", op, resp.StatusCode, location)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{op: op, status: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("tracearr %s: decode response: %w", op, err)
	}
	return nil
}

// flexInt is an int64 that tolerates how Tracearr's Postgres driver hands
// over bigint columns: as JSON strings ("1609"), sometimes null. Junk decodes
// as zero rather than failing the whole page.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	*f = flexInt(flexNumber(b))
	return nil
}

// flexFloat is the float64 twin of flexInt.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	*f = flexFloat(flexNumber(b))
	return nil
}

func flexNumber(b []byte) float64 {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return 0
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return n
}

// HealthServer is one monitored media server as /health reports it.
type HealthServer struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Online        bool   `json:"online"`
	ActiveStreams int    `json:"activeStreams"`
}

// Health is GET /api/v1/public/health: the instance's version and the servers
// it monitors. It is the connection test, since it answers only to a valid
// owner key.
type Health struct {
	Status    string         `json:"status"`
	Version   string         `json:"version"`
	Timestamp string         `json:"timestamp"`
	Servers   []HealthServer `json:"servers"`
}

// Health fetches the health document.
func (c *Client) Health(ctx context.Context) (*Health, error) {
	var out Health
	if err := c.get(ctx, healthPath, nil, "health", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ActiveStream is one playing session from GET /api/v2/public/streams.
// Numbers are flexInt because Tracearr serves bigint columns as strings;
// nullable strings decode as empty.
type ActiveStream struct {
	ID                      string  `json:"id"`
	ServerID                string  `json:"server_id"`
	ServerName              string  `json:"server_name"`
	ServerType              string  `json:"server_type"`
	Username                string  `json:"username"`
	MediaType               string  `json:"media_type"`
	MediaTitle              string  `json:"media_title"`
	ShowTitle               string  `json:"show_title"`
	SeasonNumber            flexInt `json:"season_number"`
	EpisodeNumber           flexInt `json:"episode_number"`
	Year                    flexInt `json:"year"`
	ArtistName              string  `json:"artist_name"`
	AlbumName               string  `json:"album_name"`
	DurationMS              flexInt `json:"duration_ms"`
	State                   string  `json:"state"`
	ProgressMS              flexInt `json:"progress_ms"`
	StartedAt               string  `json:"started_at"`
	IsTranscode             bool    `json:"is_transcode"`
	VideoDecision           string  `json:"video_decision"`
	AudioDecision           string  `json:"audio_decision"`
	Bitrate                 flexInt `json:"bitrate"`
	Resolution              string  `json:"resolution"`
	SourceVideoCodecDisplay string  `json:"source_video_codec_display"`
	StreamVideoCodecDisplay string  `json:"stream_video_codec_display"`
	Device                  string  `json:"device"`
	Player                  string  `json:"player"`
	Product                 string  `json:"product"`
	Platform                string  `json:"platform"`
}

// StreamsResponse is the streams envelope; the summary block is skipped
// because its totals are formatted labels, not numbers.
type StreamsResponse struct {
	Data []ActiveStream `json:"data"`
}

// Streams lists what is playing right now on every monitored server.
func (c *Client) Streams(ctx context.Context) (*StreamsResponse, error) {
	var out StreamsResponse
	if err := c.get(ctx, streamsPath, nil, "streams", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HistoryUser is the viewer of a history record.
type HistoryUser struct {
	ID           string `json:"id"`
	ServerUserID string `json:"server_user_id"`
	Username     string `json:"username"`
}

// HistoryRecord is one play chain from GET /api/v2/public/history: every
// segment of one sitting rolled into a row, newest first. Tracearr only
// lists chains with at least one segment of two minutes of watch time.
type HistoryRecord struct {
	ID                      string      `json:"id"`
	ServerID                string      `json:"server_id"`
	ServerName              string      `json:"server_name"`
	ServerType              string      `json:"server_type"`
	State                   string      `json:"state"`
	MediaType               string      `json:"media_type"`
	MediaTitle              string      `json:"media_title"`
	ShowTitle               string      `json:"show_title"`
	SeasonNumber            flexInt     `json:"season_number"`
	EpisodeNumber           flexInt     `json:"episode_number"`
	Year                    flexInt     `json:"year"`
	ArtistName              string      `json:"artist_name"`
	AlbumName               string      `json:"album_name"`
	DurationMS              flexInt     `json:"duration_ms"`
	ProgressMS              flexInt     `json:"progress_ms"`
	TotalDurationMS         flexInt     `json:"total_duration_ms"`
	PercentComplete         flexFloat   `json:"percent_complete"`
	StartedAt               string      `json:"started_at"`
	StoppedAt               string      `json:"stopped_at"`
	Watched                 bool        `json:"watched"`
	SegmentCount            flexInt     `json:"segment_count"`
	Device                  string      `json:"device"`
	Player                  string      `json:"player"`
	Product                 string      `json:"product"`
	Platform                string      `json:"platform"`
	IsTranscode             bool        `json:"is_transcode"`
	VideoDecision           string      `json:"video_decision"`
	AudioDecision           string      `json:"audio_decision"`
	Bitrate                 flexInt     `json:"bitrate"`
	Resolution              string      `json:"resolution"`
	SourceVideoCodecDisplay string      `json:"source_video_codec_display"`
	StreamVideoCodecDisplay string      `json:"stream_video_codec_display"`
	MediaID                 string      `json:"media_id"`
	ShowMediaID             string      `json:"show_media_id"`
	User                    HistoryUser `json:"user"`
}

// PageMeta is the cursor block on list responses.
type PageMeta struct {
	NextCursor string `json:"nextCursor"`
	PageSize   int    `json:"pageSize"`
}

// HistoryPage is one page of history.
type HistoryPage struct {
	Data []HistoryRecord `json:"data"`
	Meta PageMeta        `json:"meta"`
}

// HistoryQuery selects a page of history. PageSize is clamped to
// 1..MaxPageSize; zero times mean no bound.
type HistoryQuery struct {
	Cursor   string
	PageSize int
	Since    time.Time
	Until    time.Time
}

func (q HistoryQuery) values() url.Values {
	v := url.Values{}
	size := q.PageSize
	if size <= 0 || size > MaxPageSize {
		size = MaxPageSize
	}
	v.Set("pageSize", strconv.Itoa(size))
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	if !q.Since.IsZero() {
		v.Set("since", q.Since.UTC().Format(time.RFC3339))
	}
	if !q.Until.IsZero() {
		v.Set("until", q.Until.UTC().Format(time.RFC3339))
	}
	return v
}

// HistoryPage fetches one page.
func (c *Client) HistoryPage(ctx context.Context, q HistoryQuery) (*HistoryPage, error) {
	var out HistoryPage
	if err := c.get(ctx, historyPath, q.values(), "history", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WalkHistory pages through history newest-first, calling visit for every
// record until the cursor runs out, visit returns false, or maxPages pages
// have been read. truncated reports that a page cap left unread records
// behind: the caller's answer is then a floor, not a total.
func (c *Client) WalkHistory(ctx context.Context, q HistoryQuery, maxPages int, visit func(HistoryRecord) bool) (truncated bool, err error) {
	for page := 0; ; page++ {
		if page >= maxPages {
			return true, nil
		}
		result, err := c.HistoryPage(ctx, q)
		if err != nil {
			return false, err
		}
		for _, record := range result.Data {
			if !visit(record) {
				return false, nil
			}
		}
		if result.Meta.NextCursor == "" || len(result.Data) == 0 {
			return false, nil
		}
		q.Cursor = result.Meta.NextCursor
	}
}

// redactSecret removes a secret value from a string before it can escape
// into logs or HTTP responses.
func redactSecret(msg, secret string) string {
	if secret == "" {
		return msg
	}
	return strings.ReplaceAll(msg, secret, "[redacted]")
}
