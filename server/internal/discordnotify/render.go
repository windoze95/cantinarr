package discordnotify

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

func webhookDestination(c configuration) string {
	u, _ := url.Parse(c.Webhook)
	if c.ThreadID != "" {
		q := u.Query()
		q.Set("thread_id", c.ThreadID)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func (s *Service) requestRecipients(ctx context.Context, a requestAlert, units []Unit) ([]int64, error) {
	ids := map[int64]string{a.UserID: a.BookFormat}
	if a.MediaType == "book" {
		rows, err := s.db.Query(`SELECT user_id,book_format FROM book_request_waiters WHERE request_id=?`, a.Subject.RequestID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var format string
			if err = rows.Scan(&id, &format); err != nil {
				rows.Close()
				return nil, err
			}
			ids[id] = format
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	out := []int64{}
	for id, format := range ids {
		if id <= 0 {
			continue
		}
		if a.MediaType == "book" && len(units) > 0 {
			match := false
			for _, unit := range units {
				if format == "" || format == "both" || format == unit.Format {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		if s.source != nil {
			allowed, err := s.source.DiscordAuthorize(ctx, id, a.Subject)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
		}
		out = append(out, id)
	}
	return out, nil
}

func (s *Service) prepareMessage(ctx context.Context, a requestAlert, c configuration, external string) (map[string]any, error) {
	kind := eventKind(a)
	recipients := []int64{}
	if kind == RequestAvailable {
		if s.source == nil {
			return nil, nil
		}
		units := map[string]Unit{}
		for _, part := range a.Parts {
			current, err := loadRequestAlert(s.db, part.RequestID)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return nil, err
			}
			live, err := s.source.DiscordAvailability(ctx, part.RequestID)
			if err != nil {
				return nil, err
			}
			available := map[string]Unit{}
			for _, u := range live.Units {
				available[u.Key] = u
			}
			verified := []Unit{}
			for _, u := range part.Units {
				if latest, ok := available[u.Key]; ok {
					verified = append(verified, latest)
				}
			}
			if len(verified) == 0 {
				continue
			}
			current.Subject = live.Subject
			ids, err := s.requestRecipients(ctx, current, verified)
			if err != nil {
				return nil, err
			}
			if len(ids) == 0 {
				continue
			}
			a.Subject, a.Title, a.MediaType = live.Subject, live.Subject.Title, live.Subject.MediaType
			recipients = append(recipients, ids...)
			for _, u := range verified {
				units[u.Key] = u
			}
		}
		if len(units) == 0 {
			return nil, nil
		}
		for _, u := range units {
			a.Units = append(a.Units, u)
		}
		sort.Slice(a.Units, func(i, j int) bool { return a.Units[i].Label < a.Units[j].Label })
	} else if a.Subject.RequestID > 0 {
		current, err := loadRequestAlert(s.db, a.Subject.RequestID)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		// Retain the recorded event; refresh identity and audience only.
		current.Kind, current.ActorID = kind, a.ActorID
		current.MentionIDs, current.SuppressRole = a.MentionIDs, a.SuppressRole
		a = current
		if kind == RequestAutoApproved {
			a.ActorID = a.UserID
		}
		if !adminEvent(kind) {
			recipients, err = s.requestRecipients(ctx, a, nil)
			if err != nil {
				return nil, err
			}
			if len(recipients) == 0 {
				return nil, nil
			}
		}
	} else if a.IssueID > 0 {
		var reporter int64
		var source string
		if err := s.db.QueryRow(`SELECT COALESCE(reporter_id,0),source FROM issues WHERE id=?`, a.IssueID).Scan(&reporter, &source); err != nil {
			if err == sql.ErrNoRows {
				return nil, nil
			}
			return nil, err
		}
		if source != "user" {
			return nil, nil
		}
		if kind != IssueCreated && reporter > 0 {
			allowed := true
			var err error
			if s.source != nil {
				allowed, err = s.source.DiscordAuthorize(ctx, reporter, a.Subject)
			}
			if err != nil {
				return nil, err
			}
			if allowed {
				recipients = append(recipients, reporter)
			}
		}
	}
	if adminEvent(kind) || strings.HasPrefix(kind, "issue_") {
		rows, err := s.db.Query(`SELECT id FROM users WHERE role='admin'`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			recipients = append(recipients, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	ids := []string{}
	roles := []string{}
	if c.EnableMentions {
		var err error
		ids, err = s.userMentions(recipients, kind, a.ActorID)
		if err != nil {
			return nil, err
		}
		if a.MentionIDs != nil {
			allowed := map[string]bool{}
			for _, id := range a.MentionIDs {
				allowed[id] = true
			}
			filtered := []string{}
			for _, id := range ids {
				if allowed[id] {
					filtered = append(filtered, id)
				}
			}
			ids = filtered
		}
		if !a.SuppressRole && c.RoleID != "" && c.RoleEvents[kind] {
			roles = append(roles, c.RoleID)
		}
	}
	if c.EmbedPoster {
		if source, ok := s.source.(interface {
			DiscordPoster(context.Context, Subject) string
		}); ok {
			a.Subject.PosterURL = source.DiscordPoster(ctx, a.Subject)
		}
	}
	payload := renderEvent(a, external, c)
	mentions := []string{}
	for _, id := range ids {
		mentions = append(mentions, "<@"+id+">")
	}
	for _, id := range roles {
		mentions = append(mentions, "<@&"+id+">")
	}
	payload["content"] = strings.Join(mentions, " ")
	payload["allowed_mentions"] = map[string]any{"parse": []string{}, "users": ids, "roles": roles}
	return payload, nil
}

func renderEvent(a requestAlert, external string, c configuration) map[string]any {
	kind := eventKind(a)
	color := 0x9b59b6
	switch kind {
	case RequestPending:
		color = 0xf39c12
	case RequestAvailable, IssueResolved:
		color = 0x2ecc71
	case RequestDenied, RequestFailed, IssueCreated, IssueReopened:
		color = 0xe74c3c
	}
	fields := []map[string]any{}
	add := func(name, value string) {
		if value != "" {
			fields = append(fields, map[string]any{"name": name, "value": plainText(value, 1000), "inline": true})
		}
	}
	add("Type", map[string]string{"movie": "Movie", "tv": "TV show", "book": "Book", "music": "Music"}[a.MediaType])
	add("Requester", a.Username)
	add("Library", a.Subject.Library)
	if len(a.Units) > 0 {
		labels := []string{}
		for _, unit := range a.Units {
			labels = append(labels, unit.Label)
		}
		add("Available", strings.Join(labels, ", "))
	} else if a.BookFormat != "" {
		add("Format", map[string]string{"ebook": "eBook", "audiobook": "Audiobook", "both": "eBook and audiobook"}[a.BookFormat])
	}
	if a.Subject.RequestID > 0 && len(a.Parts) <= 1 {
		add("Request", fmt.Sprintf("#%d", a.Subject.RequestID))
	}
	if a.IssueID > 0 {
		add("Report", fmt.Sprintf("#%d", a.IssueID))
	}
	embed := map[string]any{"title": eventTitles[kind], "description": plainText(a.Title, 1000), "color": color, "fields": fields}
	if link := eventLink(a, external); link != "" {
		embed["url"] = link
	}
	if c.EmbedPoster && publicImageURL(a.Subject.PosterURL) {
		embed["thumbnail"] = map[string]string{"url": a.Subject.PosterURL}
	}
	payload := map[string]any{"embeds": []any{embed}, "tts": false, "allowed_mentions": map[string]any{"parse": []string{}, "users": []string{}, "roles": []string{}}}
	return withAppearance(c, payload)
}

func withAppearance(c configuration, payload map[string]any) map[string]any {
	if c.Username != "" {
		payload["username"] = c.Username
	}
	if c.AvatarURL != "" {
		payload["avatar_url"] = c.AvatarURL
	}
	return payload
}

func eventLink(a requestAlert, external string) string {
	u, err := url.Parse(external)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	path := ""
	switch {
	case a.IssueID > 0:
		path = fmt.Sprintf("/issues/%d", a.IssueID)
	case eventKind(a) == RequestPending || eventKind(a) == RequestFailed:
		path = "/approvals"
	case (a.MediaType == "movie" || a.MediaType == "tv") && a.Subject.TmdbID > 0:
		path = "/detail/" + a.MediaType + "/" + strconv.Itoa(a.Subject.TmdbID)
	case a.MediaType == "book" && a.Subject.ForeignID != "":
		path = "/detail/book/" + url.PathEscape(a.Subject.ForeignID)
	case a.MediaType == "music" && a.Subject.ForeignID != "":
		path = "/detail/album/" + url.PathEscape(a.Subject.ForeignID)
	}
	if path == "" {
		return ""
	}
	u.RawPath = strings.TrimRight(u.EscapedPath(), "/") + path
	u.Path, _ = url.PathUnescape(u.RawPath)
	if a.Subject.InstanceID != "" && a.IssueID == 0 && path != "/approvals" {
		q := u.Query()
		q.Set("instance_id", a.Subject.InstanceID)
		u.RawQuery = q.Encode()
	}
	if a.MediaType == "book" && a.IssueID == 0 && path != "/approvals" {
		q := u.Query()
		q.Set("source", "chaptarr")
		u.RawQuery = q.Encode()
	}
	return u.String()
}
