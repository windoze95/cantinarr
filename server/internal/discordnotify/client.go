// Package discordnotify delivers opt-in administrator request alerts to Discord.
package discordnotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/windoze95/cantinarr-server/internal/httpx"
)

var webhookPath = regexp.MustCompile(`^/api/(?:v[0-9]+/)?webhooks/[0-9]+/[A-Za-z0-9_-]+$`)

func normalizeWebhook(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || (u.Host != "discord.com" && u.Host != "discordapp.com") ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || !webhookPath.MatchString(u.Path) {
		return "", errors.New("enter a Discord HTTPS webhook URL for a text channel")
	}
	// Old copied URLs use discordapp.com. Send directly to the current host;
	// redirects are never followed with a credential-bearing path.
	u.Host = "discord.com"
	return u.String(), nil
}

type requestAlert struct {
	Title            string `json:"title"`
	MediaType        string `json:"media_type"`
	Username         string `json:"username"`
	RequiresApproval bool   `json:"requires_approval"`
	BookFormat       string `json:"book_format,omitempty"`
}

type Delivery struct {
	RequestID     int64  `json:"request_id,omitempty"`
	Status        string `json:"status"`
	Detail        string `json:"detail"`
	Attempts      int    `json:"attempts"`
	UpdatedAt     int64  `json:"updated_at"`
	NextAttemptAt int64  `json:"next_attempt_at,omitempty"`
}

type sendResult struct {
	Status     string
	Detail     string
	RetryAfter time.Duration
}

func newClient() *http.Client {
	return &http.Client{Transport: httpx.External(), Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// plainText bounds Discord fields and escapes formatting. allowed_mentions
// independently forbids pings, including names containing @everyone or <@id>.
func plainText(value string, limit int) string {
	var out strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			r = ' '
		}
		if strings.ContainsRune(`\*_~`+"`"+`>|[]()`, r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	runes := []rune(out.String())
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return string(runes)
}

func message(alert requestAlert, external string) map[string]any {
	kind := map[string]string{"movie": "Movie", "tv": "TV show", "book": "Book", "music": "Music"}[alert.MediaType]
	if alert.MediaType == "book" {
		switch alert.BookFormat {
		case "ebook":
			kind = "Book · eBook"
		case "audiobook":
			kind = "Book · Audiobook"
		case "both":
			kind = "Book · eBook and Audiobook"
		}
	}
	approval := "Automatically approved"
	if alert.RequiresApproval {
		approval = "Awaiting approval"
	}
	embed := map[string]any{"title": "New media request", "description": plainText(alert.Title, 1000),
		"fields": []map[string]any{
			{"name": "Type", "value": kind, "inline": true},
			{"name": "Requester", "value": plainText(alert.Username, 256), "inline": true},
			{"name": "Approval", "value": approval},
		}}
	if u, err := url.Parse(external); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" {
		u.Path = strings.TrimRight(u.Path, "/") + "/"
		if alert.RequiresApproval {
			u.Path += "approvals"
		}
		embed["url"] = u.String()
	}
	return map[string]any{"embeds": []any{embed}, "allowed_mentions": map[string]any{"parse": []string{}}, "tts": false}
}

func send(ctx context.Context, client *http.Client, webhook string, payload map[string]any) sendResult {
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook+"?wait=true", bytes.NewReader(data))
	if err != nil {
		return sendResult{Status: "failed", Detail: "The webhook could not be used."}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		// A timeout or disconnect cannot prove Discord did not save the post.
		// Never expose err: net/http errors include the token in the URL path.
		return sendResult{Status: "unconfirmed", Detail: "Delivery unconfirmed. Discord may have received the message; it will not be sent again automatically."}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		var body struct {
			RetryAfter float64 `json:"retry_after"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&body)
		seconds, _ := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64)
		seconds = math.Max(seconds, body.RetryAfter)
		if math.IsNaN(seconds) || seconds <= 0 {
			seconds = 60
		}
		if math.IsInf(seconds, 0) || seconds > 86400 {
			seconds = 86400
		}
		return sendResult{Status: "pending", Detail: "Discord asked us to wait before sending again.", RetryAfter: time.Duration(math.Ceil(seconds)) * time.Second}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var body struct {
			ID string `json:"id"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&body) == nil && body.ID != "" {
			return sendResult{Status: "sent", Detail: "Delivered to Discord."}
		}
		return sendResult{Status: "unconfirmed", Detail: "Discord returned no delivery confirmation; the message will not be sent again automatically."}
	}
	if resp.StatusCode >= 500 {
		return sendResult{Status: "unconfirmed", Detail: "Discord encountered an error. Delivery is unconfirmed; the message will not be sent again automatically."}
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 {
		return sendResult{Status: "failed", Detail: "Discord refused the webhook. Check that it still exists and can post to the channel."}
	}
	return sendResult{Status: "failed", Detail: "Discord refused the message. Check the webhook and use a text channel."}
}
