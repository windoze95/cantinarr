package push

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/windoze95/cantinarr-server/internal/contentnotify"
)

func (n *Notifier) SetRequestMatcher(matcher contentnotify.RequestMatcher) {
	if n != nil {
		n.requestMatcher = matcher
	}
}

// NotifyNewEpisodes is the evidence-bearing counterpart of NotifyNewEpisode.
// Title-only events still serve the broad audience, but never request matching.
func (n *Notifier) NotifyNewEpisodes(title string, tmdbID int, instanceID string, episodes []int) {
	n.notifyNewContent(CategoryNewEpisode, "tv", "sonarr", "New episodes available", "New on "+title, title, tmdbID, instanceID, episodes...)
}

func (n *Notifier) contentRecipients(ctx context.Context, event contentnotify.Import, category, service string) ([]int64, map[int64]bool) {
	audience, err := n.prefs.usersOptedIntoInstance(category, service, event.InstanceID)
	if err != nil {
		n.logger.Error("push: read content audience", "err", err)
		return nil, nil
	}
	audience = n.allowedDelivery(audience, category)
	only := map[int64]bool{}
	var requested, broad []int64
	event.RecipientEpisodeIDs = map[int64][]int{}
	for _, user := range audience {
		prefs, err := n.prefs.Get(user)
		if err != nil {
			continue
		}
		if prefs.RequesterOnly {
			if event.MediaType == "tv" {
				ids := n.unmutedEpisodes(user, event, category)
				if len(ids) == 0 {
					continue
				}
				event.RecipientEpisodeIDs[user] = ids
			} else if n.importMuted(user, event, category, 0) {
				continue
			}
			requested = append(requested, user)
			only[user] = true
		} else {
			broad = append(broad, user)
		}
	}
	if len(requested) > 0 && n.requestMatcher != nil {
		matches, err := n.requestMatcher.RequestedContentRecipients(ctx, event, requested)
		if err != nil {
			n.logger.Warn("push: requested content could not be verified", "err", err)
		}
		for _, user := range matches {
			if only[user] {
				broad = append(broad, user)
			}
		}
	}
	if n.recipientPolicy != nil && (event.MediaType == "movie" || event.MediaType == "tv") {
		broad, err = n.recipientPolicy.AllowedRecipients(ctx, broad, event.MediaType, event.TmdbID)
		if err != nil {
			n.logger.Warn("push: content policy could not verify every recipient", "err", err)
		}
	}
	// Provider reads may outlive access changes. Re-read the exact library
	// audience after matching; saved requests never confer a current grant.
	live, err := n.prefs.usersOptedIntoInstance(category, service, event.InstanceID)
	if err != nil {
		return nil, nil
	}
	allowed := map[int64]bool{}
	for _, user := range live {
		allowed[user] = true
	}
	kept := []int64{}
	seen := map[int64]bool{}
	for _, user := range broad {
		if allowed[user] && !seen[user] {
			prefs, err := n.prefs.Get(user)
			if err != nil || (prefs.RequesterOnly && !only[user]) {
				continue
			}
			kept = append(kept, user)
			seen[user] = true
		}
	}

	return n.allowedDelivery(kept, category), only
}

func (n *Notifier) deliverContent(client *Client, event contentnotify.Import, category, service, identity, mediaTitle, title, body string, data map[string]any, options SendOptions) {
	broadcast := n.claimContentAlert(category, event.MediaType, contentClaimID(event.InstanceID, identity), mediaTitle)
	initial, err := n.prefs.contentLibraryUsers(category, service, event.InstanceID, true)
	if err != nil {
		return
	}
	eligible := n.allowedDelivery(initial, category)
	for _, user := range initial {
		if !slices.Contains(eligible, user) {
			n.recordMutedImport(user, event, category)
		}
	}
	initial = eligible
	if len(initial) == 0 {
		return
	}
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				n.logger.Error("push: content preparation panicked", "err", fmt.Sprint(recovered))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		n.prepareContent(ctx, client, event, category, service, identity, mediaTitle, title, body, data, options, initial, broadcast)
	}()
}

func (n *Notifier) prepareContent(ctx context.Context, client *Client, event contentnotify.Import, category, service, identity, mediaTitle, title, body string, data map[string]any, options SendOptions, initial []int64, broadcast bool) {
	// Keep the existing broad claim and burst budget. Request-only audiences
	// claim separately, after exact matching: a different requested season's
	// first arrival must not consume this user's later eligible alert.
	requestEvent := n.withoutUpgradedEpisodes(event, category, identity, mediaTitle)
	audience, only := n.contentRecipients(ctx, requestEvent, category, service)
	if ctx.Err() != nil {
		return
	}
	recipients := []int64{}
	for _, user := range audience {
		if !slices.Contains(initial, user) {
			continue
		}
		if !only[user] && !broadcast {
			continue
		}
		if only[user] && n.upgradeSuppressed(category, event.MediaType, contentClaimID(event.InstanceID, identity), mediaTitle) {
			continue
		}
		scope := "requester:" + strconv.FormatInt(user, 10)
		// Record broad recipients too: turning the preference on must not make
		// the other witness of the same import page them again.
		if n.claimContentAlertScoped(category, event.MediaType, scope+":"+contentClaimID(event.InstanceID, identity), mediaTitle, scope) {
			recipients = append(recipients, user)
		}
	}
	if len(recipients) == 0 {
		return
	}
	n.sendWithOptions(client, recipients, title, body, data, options, func(ctx context.Context, pending []int64) []int64 {
		latest := n.withoutUpgradedEpisodes(event, category, identity, mediaTitle)
		current, _ := n.contentRecipients(ctx, latest, category, service)
		after := n.withoutUpgradedEpisodes(event, category, identity, mediaTitle)
		if !slices.Equal(latest.EpisodeIDs, after.EpisodeIDs) {
			// Upgrade evidence can arrive during a provider read. Re-match once with
			// the narrower positive evidence; continued churn fails closed.
			current, _ = n.contentRecipients(ctx, after, category, service)
			final := n.withoutUpgradedEpisodes(event, category, identity, mediaTitle)
			if !slices.Equal(after.EpisodeIDs, final.EpisodeIDs) {
				return nil
			}
		}
		if n.upgradeSuppressed(category, event.MediaType, contentClaimID(event.InstanceID, identity), mediaTitle) || (len(event.EpisodeIDs) > 0 && len(n.withoutUpgradedEpisodes(event, category, identity, mediaTitle).EpisodeIDs) == 0) {
			return nil
		}
		allowed := map[int64]bool{}
		for _, user := range current {
			allowed[user] = true
		}
		out := []int64{}
		for _, user := range pending {
			if allowed[user] {
				out = append(out, user)
			}
		}
		return out
	})
}
