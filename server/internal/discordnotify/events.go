package discordnotify

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	RequestPending      = "request_pending"
	RequestAutoApproved = "request_auto_approved"
	RequestApproved     = "request_approved"
	RequestDenied       = "request_denied"
	RequestAvailable    = "request_available"
	RequestFailed       = "request_failed"
	IssueCreated        = "issue_created"
	IssueComment        = "issue_comment"
	IssueResolved       = "issue_resolved"
	IssueReopened       = "issue_reopened"
)

// EventKinds is deliberately independent of phone push preferences.
var EventKinds = []string{RequestPending, RequestAutoApproved, RequestApproved, RequestDenied, RequestAvailable, RequestFailed, IssueCreated, IssueComment, IssueResolved, IssueReopened}

var eventTitles = map[string]string{
	RequestPending: "Request awaiting approval", RequestAutoApproved: "Request automatically approved",
	RequestApproved: "Request approved", RequestDenied: "Request denied", RequestAvailable: "Request available",
	RequestFailed: "Request needs attention", IssueCreated: "Problem reported", IssueComment: "New reply to a report",
	IssueResolved: "Problem resolved", IssueReopened: "Problem report reopened",
}

var snowflake = regexp.MustCompile(`^[1-9][0-9]{16,19}$`)

func validDiscordID(id string) bool {
	if !snowflake.MatchString(id) {
		return false
	}
	_, err := strconv.ParseUint(id, 10, 64)
	return err == nil
}

// Unit identifies available content, not the current file: replacing a file
// with an upgrade must never create a second availability notification.
type Unit struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Format string `json:"format,omitempty"`
}

type Subject struct {
	RequestID  int64  `json:"request_id,omitempty"`
	IssueID    int64  `json:"issue_id,omitempty"`
	Title      string `json:"title"`
	MediaType  string `json:"media_type"`
	TmdbID     int    `json:"tmdb_id,omitempty"`
	ForeignID  string `json:"foreign_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	Library    string `json:"library,omitempty"`
	PosterURL  string `json:"poster_url,omitempty"`
}

// Availability is read from the owning request service, under its canonical
// identity and scope rules. Persisted Discord receipts are never library truth.
// Baseline makes the first observation silent: a repair re-files existing work.
type Availability struct {
	Subject  Subject
	Units    []Unit
	Baseline bool
}

// ErrUnverifiable marks a request whose saved selection can never be proven,
// or whose TV match an admin paused. It is not a library outage: the scan
// skips it without a warning or a receipt.
var ErrUnverifiable = errors.New("the requested selection cannot be verified")

type Source interface {
	DiscordAvailability(context.Context, int64) (Availability, error)
	DiscordAuthorize(context.Context, int64, Subject) (bool, error)
	// DiscordObservationKey is a cheap fingerprint of the provider state a full
	// availability read depends on. An unchanged key means nothing new can be
	// available; "" means always read.
	DiscordObservationKey(context.Context, int64) (string, error)
}

type availabilityPart struct {
	RequestID int64  `json:"request_id"`
	Units     []Unit `json:"units"`
}

// Options are public configuration; the credential remains write-only.
type Options struct {
	Events         map[string]bool `json:"events"`
	EnableMentions bool            `json:"enable_mentions"`
	RoleID         string          `json:"role_id"`
	RoleEvents     map[string]bool `json:"role_events"`
	ThreadID       string          `json:"thread_id"`
	Username       string          `json:"username"`
	AvatarURL      string          `json:"avatar_url"`
	EmbedPoster    bool            `json:"embed_poster"`
}

// Update uses pointers so older clients preserve settings they cannot display.
type Update struct {
	Enabled             bool             `json:"enabled"`
	Webhook             string           `json:"webhook_url"`
	IncludeAutoApproved *bool            `json:"include_auto_approved"`
	Events              *map[string]bool `json:"events"`
	EnableMentions      *bool            `json:"enable_mentions"`
	RoleID              *string          `json:"role_id"`
	RoleEvents          *map[string]bool `json:"role_events"`
	ThreadID            *string          `json:"thread_id"`
	Username            *string          `json:"username"`
	AvatarURL           *string          `json:"avatar_url"`
	EmbedPoster         *bool            `json:"embed_poster"`
}

func validEvents(events map[string]bool) bool {
	for key := range events {
		if _, ok := eventTitles[key]; !ok {
			return false
		}
	}
	return true
}

func (c *configuration) defaults() {
	if c.Events == nil {
		c.Events = map[string]bool{RequestPending: true, RequestAutoApproved: c.IncludeAutoApproved}
	}
	if c.RoleEvents == nil {
		c.RoleEvents = map[string]bool{}
	}
	c.IncludeAutoApproved = c.Events[RequestAutoApproved]
}

func (c *configuration) apply(u Update) error {
	if u.Events != nil {
		c.Events = *u.Events
	} else if u.IncludeAutoApproved != nil {
		c.Events[RequestAutoApproved] = *u.IncludeAutoApproved
	}
	if u.RoleEvents != nil {
		c.RoleEvents = *u.RoleEvents
	}
	if !validEvents(c.Events) || !validEvents(c.RoleEvents) {
		return fmt.Errorf("unknown notification event")
	}
	if u.EnableMentions != nil {
		c.EnableMentions = *u.EnableMentions
	}
	if u.EmbedPoster != nil {
		c.EmbedPoster = *u.EmbedPoster
	}
	for _, field := range []struct {
		src *string
		dst *string
	}{{u.RoleID, &c.RoleID}, {u.ThreadID, &c.ThreadID}, {u.Username, &c.Username}, {u.AvatarURL, &c.AvatarURL}} {
		if field.src != nil {
			*field.dst = strings.TrimSpace(*field.src)
		}
	}
	if (c.RoleID != "" && !validDiscordID(c.RoleID)) || (c.ThreadID != "" && !validDiscordID(c.ThreadID)) {
		return fmt.Errorf("invalid Discord ID")
	}
	if len([]rune(c.Username)) > 80 {
		return fmt.Errorf("display name is too long")
	}
	// Discord rejects every message whose webhook name contains these words.
	if name := strings.ToLower(c.Username); strings.Contains(name, "discord") || strings.Contains(name, "clyde") {
		return fmt.Errorf("display names cannot contain Discord or Clyde")
	}
	if c.AvatarURL != "" && !publicImageURL(c.AvatarURL) {
		return fmt.Errorf("use a public HTTPS avatar URL")
	}
	c.IncludeAutoApproved = c.Events[RequestAutoApproved]
	return nil
}

func publicImageURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func eventKind(a requestAlert) string {
	if a.Kind != "" {
		return a.Kind
	}
	if a.RequiresApproval {
		return RequestPending
	}
	return RequestAutoApproved
}
