package discordnotify

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/contentnotify"
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

// Channel-neutral request identities are shared with native content alerts.
type Unit = contentnotify.Unit
type Subject = contentnotify.Subject
type Availability = contentnotify.Availability

// ErrUnverifiable marks a request whose saved selection can never be proven.
// The scan skips it without a warning or a receipt; queued parts are dropped.
var ErrUnverifiable = errors.New("the requested selection cannot be verified")

// ErrDeferred is temporary, such as a paused TV match or an undelivered repair.
// Observation stays quiet, but an already queued notification can retry.
var ErrDeferred = errors.New("availability checks are temporarily deferred")

type Source interface {
	// A fresh read bypasses the TV episode cache when observing availability.
	// Delivery preflights can reuse the short-lived snapshot.
	DiscordAvailability(ctx context.Context, id int64, fresh bool) (Availability, error)
	DiscordAuthorize(context.Context, int64, Subject) (bool, error)
	// DiscordObservationKey is a cheap change hint, not proof that no new
	// episode is available. Matching keys still need periodic full reads;
	// "" means always read.
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
