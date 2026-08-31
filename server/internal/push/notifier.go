package push

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Notifier turns request and content events into push notifications via the
// gateway. It implements request.Notifier. Sends are fire-and-forget so a slow
// or failing gateway never blocks an approval/denial. Every notification is
// filtered by the recipient's per-category preferences before dispatch.
//
// The Notifier holds the push Manager rather than a static client, so it picks
// up a gateway that enrolled after boot and no-ops cleanly while push is
// unconfigured or the gateway is unreachable (Manager.Client() == nil). After
// each send it inspects the gateway's per-device results and drops any local
// token the gateway pruned (token rejected by APNs), keeping push_tokens from
// accumulating dead rows.
type Notifier struct {
	mgr    *Manager
	db     *sql.DB
	prefs  *PrefsStore
	logger *slog.Logger

	healthMu         sync.Mutex
	consecutiveFails int
	healthSink       DeliveryHealthSink
}

// deliveryFailureThreshold is how many sends must fail back to back before the
// gateway is called out. One failure is a blip — a timeout, a restarting
// gateway — and the notification it lost is already gone whatever we do. Two
// in a row is an outage, which is the thing worth an admin's attention.
const deliveryFailureThreshold = 2

// DeliveryHealthSink turns push-delivery transitions into one deduplicated
// admin issue. The remediation service implements it.
//
// Push is the one dependency that cannot report its own failure through the
// usual channel: an alert nobody receives is indistinguishable from an alert
// nobody needed to send. An issue is the right surface precisely because it
// works when push does not — it is visible in the app's Issues list without
// any notification being delivered. The push it does raise is a bonus that
// arrives once the gateway recovers, telling an admin that alerts were lost.
type DeliveryHealthSink interface {
	RecordPushDeliveryHealth(healthy bool, detail string) error
}

// SetDeliveryHealthSink wires the admin issue surface after both services
// exist. Without it the Notifier still counts failures and logs them.
func (n *Notifier) SetDeliveryHealthSink(sink DeliveryHealthSink) {
	if n == nil {
		return
	}
	n.healthMu.Lock()
	defer n.healthMu.Unlock()
	n.healthSink = sink
}

// NewNotifier builds a push notifier over the push Manager. A nil manager (or
// one whose client has not enrolled) makes every method a no-op, so callers can
// wire it unconditionally and let the manager gate on configuration.
func NewNotifier(db *sql.DB, mgr *Manager, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &Notifier{mgr: mgr, db: db, prefs: NewPrefsStore(db), logger: logger}
}

// client returns the currently-enrolled gateway client, or nil when push is
// unconfigured or the gateway has not yet come up. nil-receiver safe.
func (n *Notifier) client() *Client {
	if n == nil || n.mgr == nil {
		return nil
	}
	return n.mgr.Client()
}

// ContentReady reports whether new-content alerts can currently be delivered.
// The WebSocket poller calls it to hold back the single queue-departure diff it
// resumes after a restart, so completions that survived the restart are not
// dropped into a gateway that has not enrolled yet. Steady-state alerts are not
// gated on it.
func (n *Notifier) ContentReady() bool { return n.client() != nil }

// NotifyUser pushes a per-user event to its recipient, gated on their opt-in
// for the matching category. Two events produce a notification today:
// "request_decision" (approval/denial outcome, off by default) and
// "plex_invite_sent" ("check your email", fixed template, on by default).
// Any other event type is a no-op here (WS-only).
func (n *Notifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	client := n.client()
	if client == nil {
		return
	}
	switch eventType {
	case CategoryRequestDecision:
		if !n.prefs.optedIn(userID, CategoryRequestDecision) {
			return
		}
		title, body := decisionMessage(data)
		if title == "" {
			return
		}
		n.send(client, []int64{userID}, title, body, passthrough(CategoryRequestDecision, data))
	case CategoryPlexInviteSent:
		if !n.prefs.optedIn(userID, CategoryPlexInviteSent) {
			return
		}
		n.send(client, []int64{userID}, "Plex invite sent",
			"Your Plex invite is on its way — check your email",
			map[string]any{"type": CategoryPlexInviteSent})
	case EventIssueQuestion, EventIssueFixConfirm, EventIssueClosed:
		// Reporter-loop beats about the user's OWN report. One shared
		// preference gates all three; the bodies are FIXED server-authored
		// copy (M5) — the title, question, and resolution text live in the
		// thread the tap opens, never in the notification.
		if !n.prefs.optedIn(userID, CategoryIssueReportUpdate) {
			return
		}
		title, body := issueReportMessage(eventType)
		n.send(client, []int64{userID}, title, body, passthrough(eventType, data))
	}
}

// Reporter-loop event types. All three ride the issue_report_update
// preference; the client deep-links each to the report's own thread.
const (
	EventIssueQuestion   = "issue_question"
	EventIssueFixConfirm = "issue_fix_confirm"
	EventIssueClosed     = "issue_closed"
)

func issueReportMessage(eventType string) (title, body string) {
	switch eventType {
	case EventIssueQuestion:
		return "Question about your report", "The assistant needs one answer from you to keep working on it"
	case EventIssueFixConfirm:
		return "A fix was applied", "Open your report and tell us whether it's right now"
	default:
		return "Your report was closed", "Open it to see how it ended"
	}
}

// NotifyAdmins pushes an admin-scoped event to every admin opted into the
// matching category. Two events produce a notification today: "request_pending"
// (a new media request) and "issue_created" (a new AI-remediation issue, on by
// default). Any other event type is a no-op here (WS-only).
//
// Untrusted-text invariant (M5): the alert body is a FIXED server-authored
// template, never an interpolated title/reason; the media title travels only as
// a structured passthrough field (and as the request alert's body, which is
// arr/user-sourced and unchanged from existing behavior).
func (n *Notifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	client := n.client()
	if client == nil {
		return
	}
	switch eventType {
	case CategoryRequestPending:
		n.notifyRequestPending(client, data)
	case CategoryIssueCreated:
		n.notifyIssueCreated(client, data)
	case CategoryAgentActionPending:
		n.notifyAgentActionPending(client, data)
	case CategoryAgentAutoApprovalPaused:
		n.notifyAgentAutoApprovalPaused(client, data)
	case CategoryProfileChangePending:
		n.notifyProfileChangePending(client, data)
	case EventAutoDispatchDisabled:
		// Autonomy standing down is exactly the "agent pipeline needs your
		// decision" class, so it shares that preference column — and unlike
		// the in-app snackbar, this one reaches an admin who was away.
		users, err := n.prefs.usersOptedInto(CategoryAgentActionPending)
		if err != nil || len(users) == 0 {
			return
		}
		n.send(client, users, "Automatic problem detection switched off",
			"Repeated unfinished investigations tripped the safety breaker — open Settings to review and re-enable",
			passthrough(EventAutoDispatchDisabled, data))
	case CategoryPlexAccessRequest:
		n.notifyPlexAccessRequested(client, data)
	case CategoryAgentDigest:
		// The one push that reports success. Counts are server-computed
		// integers; no free text ever reaches the lock screen (M5).
		//
		// "Resolved" is OUTCOME vocabulary — every problem that ended well,
		// including the ones that cleared on their own — with attribution glued
		// to the number so automation claims only its own work (the card renders
		// the same two-ledger split; see remediation.AgentDigest).
		users, err := n.prefs.usersOptedInto(CategoryAgentDigest)
		if err != nil || len(users) == 0 {
			return
		}
		resolved := intField(data, "issues_resolved") + intField(data, "self_cleared")
		byAgent := intField(data, "resolved_by_agent")
		byRules := intField(data, "rule_approved")
		byAdmin := intField(data, "resolved_by_admin")
		onOwn := resolved - byAgent - byRules - byAdmin
		var lanes []string
		if byAgent > 0 {
			lanes = append(lanes, fmt.Sprintf("%d by the agent", byAgent))
		}
		if byRules > 0 {
			lanes = append(lanes, fmt.Sprintf("%d by your rules", byRules))
		}
		if byAdmin > 0 {
			lanes = append(lanes, fmt.Sprintf("%d by you", byAdmin))
		}
		if onOwn > 0 {
			switch {
			case len(lanes) > 0:
				lanes = append(lanes, fmt.Sprintf("%d on their own", onOwn))
			case onOwn == 1:
				lanes = append(lanes, "on its own")
			default:
				lanes = append(lanes, "all on their own")
			}
		}
		body := fmt.Sprintf("Last 7 days: %d resolved", resolved)
		if len(lanes) > 0 {
			body += " — " + strings.Join(lanes, " · ")
		}
		// Open work is state right now, not something the week did, so it gets
		// its own clause instead of riding the window's separator list.
		if needs := intField(data, "needs_admin_open") + intField(data, "pending_proposals"); needs > 0 {
			verb := "need"
			if needs == 1 {
				verb = "needs"
			}
			body += fmt.Sprintf(". Right now: %d %s you", needs, verb)
		}
		n.send(client, users, "Your media assistant, this week", body, passthrough(CategoryAgentDigest, data))
	}
}

func intField(data map[string]interface{}, key string) int {
	switch v := data[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// EventAutoDispatchDisabled is the circuit-breaker stand-down. It rides the
// agent_action_pending preference; the durable record is the breaker's own
// system issue.
const EventAutoDispatchDisabled = "remediation_autodispatch_disabled"

// notifyRequestPending pushes a new pending request to opted-in admins, badging
// the home-screen icon with the live queue depth.
func (n *Notifier) notifyRequestPending(client *Client, data map[string]interface{}) {
	title := str(data["title"])
	if title == "" {
		title = "a title"
	}
	recipients, err := n.prefs.usersOptedInto(CategoryRequestPending)
	if err != nil {
		n.logger.Error("push: resolve request_pending recipients", "err", err)
		return
	}
	if len(recipients) == 0 {
		return
	}
	// Set the home-screen icon badge to the live queue depth so admins see the
	// count even while the app is closed (the gateway maps this to aps.badge).
	var opts SendOptions
	if count, ok := intval(data["pending_count"]); ok {
		opts.Badge = &count
	}
	n.sendWithOptions(client, recipients, "New request", title, passthrough(CategoryRequestPending, data), opts)
}

// notifyIssueCreated pushes a new AI-remediation issue to opted-in admins. The
// body is a fixed template (the untrusted issue title is NOT placed on the
// lock screen); issue_id rides along for tap deep-linking. Issue state does not
// write the global app-icon badge: recovery can silently remove issue attention,
// while the app icon has one independent owner (pending request approvals).
func (n *Notifier) notifyIssueCreated(client *Client, data map[string]interface{}) {
	recipients, err := n.prefs.usersOptedInto(CategoryIssueCreated)
	if err != nil {
		n.logger.Error("push: resolve issue_created recipients", "err", err)
		return
	}
	if len(recipients) == 0 {
		return
	}
	out := map[string]any{"type": CategoryIssueCreated}
	if v, ok := data["issue_id"]; ok {
		out["issue_id"] = v
	}
	source := str(data["source"])
	count, ok := intval(data["count"])
	if !ok || count < 1 {
		count = 1
	}
	title, body := issueAlertText(source, count)
	var opts SendOptions
	if count > 1 {
		// A coalesced wave is a state summary, not one incident's event:
		// collapsing by source replaces the previous summary instead of stacking
		// another identical line, which is the whole point of coalescing.
		opts.CollapseID = fmt.Sprintf("%s:%s", CategoryIssueCreated, source)
	}
	n.sendWithOptions(client, recipients, title, body, out, opts)
}

// issueAlertText picks the fixed lock-screen template for an issue alert. Every
// string is server-authored and the count is an integer the server computed, so
// no untrusted title or reason reaches the lock screen at any count.
func issueAlertText(source string, count int) (title, body string) {
	switch source {
	case "auto":
		if count > 1 {
			return "Problems need attention",
				fmt.Sprintf("Cantinarr found %d media problems that did not recover automatically", count)
		}
		return "Problem needs attention",
			"Cantinarr found a media problem that did not recover automatically"
	case "system":
		// A system issue is a condition on the SERVER rather than on a piece of
		// media, and there are several distinct ones: the shared AI model, push
		// delivery, a stalled book-metadata import. This copy was written when
		// shared-AI health was the only one and named it outright, which has been
		// wrong on the lock screen for every other kind ever since — and there is
		// nothing safe to say more specifically, because the only fields that
		// would distinguish them are the issue's own untrusted title and detail.
		// So it stays deliberately generic, and counted like the auto branch: two
		// server conditions clearing the hold-down together really do coalesce.
		if count > 1 {
			return "Cantinarr needs attention",
				fmt.Sprintf("%d server conditions need an administrator", count)
		}
		return "Cantinarr needs attention",
			"Something on the server needs an administrator"
	default:
		if count > 1 {
			return "New problems reported",
				fmt.Sprintf("%d problems were reported with media", count)
		}
		return "New problem reported",
			"Someone reported a problem with their media"
	}
}

// notifyAgentActionPending pushes "the AI proposed a fix, approve it" to opted-in
// admins. The body is a FIXED template — the agent's rationale and any release
// name are UNTRUSTED and never placed on the lock screen; issue_id rides along
// for tap deep-linking. A coalesced wave carries a server-computed count
// instead: the plural template plus a collapse id so a later summary replaces
// the previous one, and no issue_id (the app's approval queue is the
// destination either way). Like issue-created pushes, this queue does not
// overwrite the global app-icon badge owned by pending request approvals.
func (n *Notifier) notifyAgentActionPending(client *Client, data map[string]interface{}) {
	recipients, err := n.prefs.usersOptedInto(CategoryAgentActionPending)
	if err != nil {
		n.logger.Error("push: resolve agent_action_pending recipients", "err", err)
		return
	}
	if len(recipients) == 0 {
		return
	}
	out := map[string]any{"type": CategoryAgentActionPending}
	if v, ok := data["issue_id"]; ok {
		out["issue_id"] = v
	}
	count, ok := intval(data["count"])
	if !ok || count < 1 {
		count = 1
	}
	title := "A fix needs your approval"
	body := "The assistant proposed a fix for a problem and needs you to approve it"
	var opts SendOptions
	if count > 1 {
		title = "Fixes need your approval"
		body = fmt.Sprintf("The assistant proposed fixes for %d problems and needs you to approve them", count)
		opts.CollapseID = CategoryAgentActionPending
	}
	n.sendWithOptions(client, recipients, title, body, out, opts)
}

// notifyAgentAutoApprovalPaused pushes "a standing auto-approval rule disarmed
// itself" to opted-in admins (it shares the agent_action_pending preference).
// This alert exists because auto-approval removes the admin from the loop: when
// automation stands down after a failure, someone must hear about it even with
// the app closed. The body is a FIXED template — the rule label embeds a doctor
// problem string, so like every other agent surface nothing computed reaches
// the lock screen; issue_id rides along for tap deep-linking to the evidence.
// Collapsing per rule id means a flapping rule replaces its previous alert
// instead of stacking.
func (n *Notifier) notifyAgentAutoApprovalPaused(client *Client, data map[string]interface{}) {
	recipients, err := n.prefs.usersOptedInto(CategoryAgentAutoApprovalPaused)
	if err != nil {
		n.logger.Error("push: resolve agent_autoapproval_paused recipients", "err", err)
		return
	}
	if len(recipients) == 0 {
		return
	}
	out := map[string]any{"type": CategoryAgentAutoApprovalPaused}
	if v, ok := data["issue_id"]; ok {
		out["issue_id"] = v
	}
	title := "Auto-approval paused"
	body := "An automatic fix did not complete successfully, so its auto-approval rule was paused"
	var opts SendOptions
	if ruleID, ok := intval(data["rule_id"]); ok {
		opts.CollapseID = fmt.Sprintf("%s:%d", CategoryAgentAutoApprovalPaused, ruleID)
	}
	n.sendWithOptions(client, recipients, title, body, out, opts)
}

// notifyProfileChangePending pushes "an external agent parked a
// quality-profile change" to opted-in admins (it shares the
// agent_action_pending preference — same "the agent pipeline needs your
// decision" class). The body is a FIXED template: the profile and instance
// names are arr-derived text, so like every other admin surface they ride
// only as structured passthrough fields for the app, never on the lock
// screen. Collapsing per target (service/instance/profile) means a
// superseding proposal replaces the stale alert instead of stacking under
// it; proposal_id rides along for tap deep-linking.
func (n *Notifier) notifyProfileChangePending(client *Client, data map[string]interface{}) {
	recipients, err := n.prefs.usersOptedInto(CategoryProfileChangePending)
	if err != nil {
		n.logger.Error("push: resolve profile_change_pending recipients", "err", err)
		return
	}
	if len(recipients) == 0 {
		return
	}
	out := map[string]any{"type": CategoryProfileChangePending}
	if v, ok := data["proposal_id"]; ok {
		out["proposal_id"] = v
	}
	title := "A settings change needs your approval"
	body := "An external assistant proposed a quality-profile change and needs you to approve it"
	var opts SendOptions
	if profileID, ok := intval(data["profile_id"]); ok {
		opts.CollapseID = fmt.Sprintf("%s:%s:%s:%d", CategoryProfileChangePending, str(data["service"]), str(data["instance_id"]), profileID)
	}
	n.sendWithOptions(client, recipients, title, body, out, opts)
}

// notifyPlexAccessRequested pushes "a user shared their Plex email" to
// opted-in admins. The body is one of three FIXED templates picked by the
// invite_state enum ("" needs a manual invite, "sent" auto-invite went out,
// "failed" auto-invite needs a retry; "claimed", an address another user's
// row holds, reads as waiting on the admin) — the username and email are
// user-controlled and never placed on the lock screen; user_id rides along
// for tap deep-linking to the Users screen. A collapse id per user keeps a
// user editing their email from stacking alerts.
func (n *Notifier) notifyPlexAccessRequested(client *Client, data map[string]interface{}) {
	recipients, err := n.prefs.usersOptedInto(CategoryPlexAccessRequest)
	if err != nil {
		n.logger.Error("push: resolve plex_access_request recipients", "err", err)
		return
	}
	if len(recipients) == 0 {
		return
	}
	body := "Someone shared their Plex email and is waiting for an invite"
	switch str(data["invite_state"]) {
	case "sent":
		body = "Someone shared their Plex email — their invite was sent automatically"
	case "failed":
		body = "Someone shared their Plex email — the automatic invite failed, send it from the Users screen"
	}
	out := map[string]any{"type": CategoryPlexAccessRequest}
	if v, ok := data["user_id"]; ok {
		out["user_id"] = v
	}
	var opts SendOptions
	if id, ok := intval(data["user_id"]); ok {
		opts.CollapseID = fmt.Sprintf("%s:%d", CategoryPlexAccessRequest, id)
	}
	n.sendWithOptions(client, recipients, "Plex access request", body, out, opts)
}

// NotifyNewMovie pushes a "movie became available" alert to the opted-in
// users who can see the importing library (usersOptedIntoNewVideo). A
// collapse id keeps repeat availability pings for the same movie from
// stacking on-device.
func (n *Notifier) NotifyNewMovie(title string, tmdbID int, instanceID string) {
	n.notifyNewContent(CategoryNewMovie, "movie", "radarr", "New movie available", title+" is ready to watch", title, tmdbID, instanceID)
}

// NotifyNewEpisode pushes a "new episodes available" alert to the opted-in
// users who can see the importing library. The copy is plural with no
// count on purpose: the alert sends on the first import of a wave and
// claimContentAlert absorbs the rest of a season pack behind it, so at send
// time the server cannot know whether one episode or ten are landing — an
// honest count would mean holding the push, and a singular title undersells a
// pack. The body stays count-free so the pair reads naturally either way.
func (n *Notifier) NotifyNewEpisode(seriesTitle string, tmdbID int, instanceID string) {
	n.notifyNewContent(CategoryNewEpisode, "tv", "sonarr", "New episodes available", "New on "+seriesTitle, seriesTitle, tmdbID, instanceID)
}

// NotifyNewBook pushes a "book became available" alert for a completed
// Chaptarr import. The audience is users opted into the new_book category (on
// by default) whose book library is the instance the import landed on — a
// chaptarr assignment is the books access model, and it scopes admins too;
// only an admin with no books assignment hears every instance. format is the
// Chaptarr book record's media type ("ebook"/"audiobook"): a title's two
// formats are separate records sharing a foreignBookId, and each import
// alerts on its own.
func (n *Notifier) NotifyNewBook(title, foreignID, instanceID, format string) {
	client := n.client()
	if client == nil || title == "" {
		return
	}
	// The claim id carries the library: the audience is per-instance, so the
	// same foreignBookId importing on two Chaptarr instances inside the
	// window must alert both instances' readers.
	if !n.claimContentAlert(CategoryNewBook, "book", contentClaimID(instanceID, foreignID+"|"+format), title) {
		return
	}
	recipients, err := n.prefs.usersOptedIntoNewBook(instanceID)
	if err != nil {
		n.logger.Error("push: resolve new-content recipients", "err", err, "category", CategoryNewBook)
		return
	}
	if len(recipients) == 0 {
		return
	}
	// Books have no TMDB id: the payload carries the same deep-link identity a
	// book request decision does (foreign_id, pinned instance, title hint), so
	// the app's existing book tap routing applies unchanged.
	data := map[string]any{
		"type":        CategoryNewBook,
		"media_type":  "book",
		"foreign_id":  foreignID,
		"instance_id": instanceID,
		"title":       title,
	}
	if format != "" {
		data["book_format"] = format
	}
	n.sendWithOptions(client, recipients, "New book available", bookReadyBody(title, format), data, SendOptions{
		CollapseID: fmt.Sprintf("%s:%s:%s", CategoryNewBook, foreignID, format),
	})
}

// bookReadyBody derives the availability copy from the imported record's
// format, using the same eBook/Audiobook nomenclature as the decision copy.
func bookReadyBody(title, format string) string {
	switch format {
	case "ebook":
		return title + " eBook is ready to read"
	case "audiobook":
		return title + " Audiobook is ready to play"
	default:
		return title + " is ready"
	}
}

// NotifyUpgradedMovie pushes a "movie file was upgraded" alert to admins opted
// into the content_upgraded category (off by default). See
// notifyUpgradedContent for why the broadcast key is claimed first.
func (n *Notifier) NotifyUpgradedMovie(title string, tmdbID int, instanceID string) {
	n.notifyUpgradedContent(CategoryNewMovie, "movie", "Movie upgraded", title+" was replaced with a better version", title, tmdbID, instanceID)
}

// NotifyUpgradedEpisode pushes an "episode file was upgraded" alert to admins
// opted into the content_upgraded category (off by default).
func (n *Notifier) NotifyUpgradedEpisode(seriesTitle string, tmdbID int, instanceID string) {
	n.notifyUpgradedContent(CategoryNewEpisode, "tv", "Episode upgraded", "An episode of "+seriesTitle+" was replaced with a better version", seriesTitle, tmdbID, instanceID)
}

// NotifyUpgradedBook pushes a "book file was upgraded" alert to admins opted
// into the content_upgraded category (off by default). Unlike NotifyNewBook
// the audience is NOT narrowed to the instance's assigned readers: an upgrade
// is operational oversight (the issue_created class), not a "ready to read"
// call to action, so it takes the standard admin-scoped path. The payload
// still carries the same deep-link identity as new_book so the app's existing
// book tap routing applies unchanged.
func (n *Notifier) NotifyUpgradedBook(title, foreignID, instanceID, format string) {
	if title == "" {
		return
	}
	// Absorb the poller before anything else can bail: the silent claim must
	// land even when the gateway is unenrolled or the admin alert is deduped.
	// Byte-identical to NotifyNewBook's claim id for the same import.
	n.claimContentAlertSilently(CategoryNewBook, "book", contentClaimID(instanceID, foreignID+"|"+format), title)
	client := n.client()
	if client == nil {
		return
	}
	if !n.claimContentAlertScoped(CategoryContentUpgraded, "book", contentClaimID(instanceID, foreignID+"|"+format), title, stormScopeUpgrade) {
		return
	}
	recipients, err := n.prefs.usersOptedInto(CategoryContentUpgraded)
	if err != nil {
		n.logger.Error("push: resolve new-content recipients", "err", err, "category", CategoryContentUpgraded)
		return
	}
	if len(recipients) == 0 {
		return
	}
	data := map[string]any{
		"type":        CategoryContentUpgraded,
		"media_type":  "book",
		"foreign_id":  foreignID,
		"instance_id": instanceID,
		"title":       title,
	}
	if format != "" {
		data["book_format"] = format
	}
	n.sendWithOptions(client, recipients, "Book upgraded", bookUpgradedBody(title, format), data, SendOptions{
		CollapseID: fmt.Sprintf("%s:%s:%s", CategoryContentUpgraded, foreignID, format),
	})
}

// bookUpgradedBody is bookReadyBody's upgrade twin, keeping the same
// eBook/Audiobook nomenclature.
func bookUpgradedBody(title, format string) string {
	switch format {
	case "ebook":
		return title + " eBook was upgraded"
	case "audiobook":
		return title + " Audiobook was upgraded"
	default:
		return title + " was upgraded"
	}
}

// notifyUpgradedContent is the shared body for the movie/TV upgrade alerts.
// The broadcast category's dedupe key (byte-identical to the one
// notifyNewContent claims) is claimed silently FIRST: the queue poller
// re-witnesses the same import as a plain departure and would otherwise page
// the household with a false "New movie available" — the ledger claim is what
// keeps it quiet. Only then does the admin content_upgraded alert claim its
// own key and send under its own storm budget.
func (n *Notifier) notifyUpgradedContent(broadcastCategory, mediaType, title, body, mediaTitle string, tmdbID int, instanceID string) {
	if mediaTitle == "" {
		return
	}
	// Byte-identical to the id notifyNewContent claims for the same import on
	// the same library — that identity is what keeps the poller's re-witness
	// quiet.
	n.claimContentAlertSilently(broadcastCategory, mediaType, contentClaimID(instanceID, strconv.Itoa(tmdbID)), mediaTitle)
	client := n.client()
	if client == nil {
		return
	}
	if !n.claimContentAlertScoped(CategoryContentUpgraded, mediaType, contentClaimID(instanceID, strconv.Itoa(tmdbID)), mediaTitle, stormScopeUpgrade) {
		return
	}
	recipients, err := n.prefs.usersOptedInto(CategoryContentUpgraded)
	if err != nil {
		n.logger.Error("push: resolve new-content recipients", "err", err, "category", CategoryContentUpgraded)
		return
	}
	if len(recipients) == 0 {
		return
	}
	data := map[string]any{
		"type":       CategoryContentUpgraded,
		"tmdb_id":    tmdbID,
		"media_type": mediaType,
	}
	if instanceID != "" {
		data["instance_id"] = instanceID
	}
	n.sendWithOptions(client, recipients, title, body, data, SendOptions{
		CollapseID: fmt.Sprintf("%s:%d", CategoryContentUpgraded, tmdbID),
	})
}

// notifyNewContent is the shared body for the new-content notifications: it
// resolves the opted-in audience that can see the importing library and
// dispatches one collapsed push carrying the media identity for tap routing.
// The claim id carries the library so the same title importing on two
// libraries inside the window alerts BOTH libraries' audiences — with
// per-library audiences, one shared claim would silently drop the second
// library's alert on the floor.
func (n *Notifier) notifyNewContent(category, mediaType, serviceType, title, body, mediaTitle string, tmdbID int, instanceID string) {
	client := n.client()
	if client == nil || mediaTitle == "" {
		return
	}
	if !n.claimContentAlert(category, mediaType, contentClaimID(instanceID, strconv.Itoa(tmdbID)), mediaTitle) {
		return
	}
	recipients, err := n.prefs.usersOptedIntoNewVideo(category, serviceType, instanceID)
	if err != nil {
		n.logger.Error("push: resolve new-content recipients", "err", err, "category", category)
		return
	}
	if len(recipients) == 0 {
		return
	}
	data := map[string]any{
		"type":       category,
		"tmdb_id":    tmdbID,
		"media_type": mediaType,
	}
	if instanceID != "" {
		data["instance_id"] = instanceID
	}
	n.sendWithOptions(client, recipients, title, body, data, SendOptions{
		CollapseID: fmt.Sprintf("%s:%d", category, tmdbID),
	})
}

// contentClaimID scopes a content claim's identity to the importing library.
// The webhook and the queue poller announcing the same import name the same
// instance, so their dedupe still collapses; only genuinely different
// libraries' imports stop colliding. An empty instance id degrades to the
// legacy unscoped identity.
func contentClaimID(instanceID, id string) string {
	if instanceID == "" {
		return id
	}
	return instanceID + "|" + id
}

// contentAlertWindow is how long a new-content alert suppresses repeats of the
// same content: long enough to absorb a season pack's per-file webhooks plus
// the queue poll re-witnessing the same import, short enough that tonight's
// episode still alerts after last week's.
const contentAlertWindow = 10 * time.Minute

// contentAlertRetention bounds content_alert_claims without a sweeper goroutine:
// old rows are dropped opportunistically whenever a claim is granted. Well past
// contentAlertWindow, so it can never shorten the suppression it stores.
const contentAlertRetention = 24 * time.Hour

// contentAlertStormCap bounds how many distinct content alerts may fan out
// within one contentAlertWindow before the rest of the burst is suppressed. It
// is the live-path analogue of the poller's resumed-batch cap: a mass job — a
// bulk manual import firing one webhook per title, several instances resuming
// at once — would otherwise page every opted-in household member once per
// title, and the only remedy a user has is a permanent category opt-out. Past
// this rate the app's live view is the better answer. Sized above the poller's
// per-instance resumed cap so one full resumed batch never starves a
// concurrent organic alert; suppressed alerts are logged, never silent.
const contentAlertStormCap = 12

// Storm scopes partition content_alert_claims so each alert class is bounded
// by its own cap and a silent claim is bounded by none: a mass cutoff-upgrade
// sweep must never spend the broadcast budget of genuine new-content alerts.
const (
	stormScopeBroadcast = "broadcast"
	stormScopeUpgrade   = "upgrade"
	stormScopeNone      = "none"
)

// claimContentAlert reports whether this content alert should send, recording it
// so duplicates within contentAlertWindow are dropped. id is the content's
// stable identity — the tmdb id for movies/TV, foreignBookId plus format for
// books. The key includes the title so unresolved ids (tmdb 0) never collapse
// different content.
//
// The claim is durable so the window survives a restart: the queue poller
// resumes its departure diff across a restart, and without a persisted claim a
// completion the webhook already announced would be announced a second time.
// Durable is not permanent — the window is still a window, so a claim whose send
// failed is re-claimable once it lapses.
func (n *Notifier) claimContentAlert(category, mediaType, id, title string) bool {
	return n.claimContentAlertScoped(category, mediaType, id, title, stormScopeBroadcast)
}

// claimContentAlertSilently records a claim for an alert that deliberately will
// NOT send: a proven upgrade claims the broadcast key so the queue poller's
// re-witness of the same import finds the ledger already holding the claim and
// stays quiet. Scope 'none' keeps the silent claim out of every storm count.
// It runs even while the push gateway is unenrolled — the suppression must be
// recorded regardless, or a boot-resumption hold could later broadcast an
// upgrade the webhook already proved. A database error here is only logged:
// losing the silent claim errs toward alerting, never toward silence.
func (n *Notifier) claimContentAlertSilently(category, mediaType, id, title string) {
	n.claimContentAlertScoped(category, mediaType, id, title, stormScopeNone)
}

func (n *Notifier) claimContentAlertScoped(category, mediaType, id, title, scope string) bool {
	key := fmt.Sprintf("%s|%s|%s|%s", category, mediaType, id, title)
	if n.db == nil {
		// No ledger must never silence an alert.
		return true
	}
	// One statement: the conditional upsert grants the claim only when there is
	// no row or the existing one has lapsed, and the affected-row count is the
	// verdict. A SELECT-then-INSERT would race on the shared single-connection
	// pool without adding anything. A lapsed-window re-claim adopts the new
	// claim's scope.
	res, err := n.db.Exec(`
        INSERT INTO content_alert_claims (alert_key, storm_scope) VALUES (?, ?)
        ON CONFLICT(alert_key) DO UPDATE SET claimed_at = CURRENT_TIMESTAMP, storm_scope = excluded.storm_scope
        WHERE content_alert_claims.claimed_at <= datetime('now', ?)`,
		key, scope, sqliteOffset(-contentAlertWindow))
	if err != nil {
		// Fail open: a duplicate alert beats silencing the whole content surface
		// on a transient database error.
		n.logger.Error("push: claim content alert", "err", err, "category", category)
		return true
	}
	rows, err := res.RowsAffected()
	if err != nil {
		n.logger.Error("push: claim content alert rows", "err", err, "category", category)
		return true
	}
	if rows != 1 {
		return false
	}
	if _, err := n.db.Exec(`DELETE FROM content_alert_claims WHERE claimed_at <= datetime('now', ?)`,
		sqliteOffset(-contentAlertRetention)); err != nil {
		n.logger.Error("push: sweep content alert claims", "err", err)
	}
	if scope == stormScopeNone {
		// Nothing sends, so there is nothing to cap.
		return true
	}
	// Storm breaker: the claim above stays recorded (so the other witness of
	// the same import cannot re-try it), but past the burst cap the alert
	// itself is suppressed. Counting granted claims counts distinct content,
	// not devices, and the count includes this claim — the first cap-many in a
	// window deliver, the rest of the burst is dropped and logged. Each scope
	// spends only its own budget.
	var inWindow int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM content_alert_claims WHERE claimed_at > datetime('now', ?) AND storm_scope = ?`,
		sqliteOffset(-contentAlertWindow), scope).Scan(&inWindow); err != nil {
		// Fail open, same as the claim itself: a burst slipping through beats
		// silencing the surface on a transient database error.
		n.logger.Error("push: count content alert window", "err", err, "category", category)
		return true
	}
	if inWindow > contentAlertStormCap {
		n.logger.Warn("push: suppressing content alert burst",
			"category", category, "title", title, "in_window", inWindow, "cap", contentAlertStormCap)
		return false
	}
	return true
}

// sqliteOffset renders a duration as a SQLite datetime() modifier, so the Go
// constants above stay the single source of truth for both windows.
func sqliteOffset(d time.Duration) string {
	return fmt.Sprintf("%+d seconds", int64(d/time.Second))
}

// send dispatches a notification in the background with panic recovery, so a
// failed delivery (or a marshalling bug) can never take down the caller.
func (n *Notifier) send(client *Client, userIDs []int64, title, body string, data map[string]any) {
	n.sendWithOptions(client, userIDs, title, body, data, SendOptions{})
}

// sendWithOptions is send with explicit gateway options (e.g. a collapse id).
// After the gateway replies it prunes any local token the gateway reported
// dead, so push_tokens self-cleans without a separate sweep.
func (n *Notifier) sendWithOptions(client *Client, userIDs []int64, title, body string, data map[string]any, opts SendOptions) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				n.logger.Error("push: send panicked", "err", fmt.Sprint(r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := client.SendWithOptions(ctx, userIDs, title, body, data, opts)
		if err != nil {
			n.logger.Error("push: send notification", "err", err, "title", title)
			n.recordDeliveryFailure(err)
			return
		}
		n.recordDeliverySuccess()
		pruneDeadTokens(n.db, n.logger, resp)
	}()
}

// recordDeliveryFailure counts a failed send and, once the run is long enough
// to mean the gateway rather than the moment, opens or refreshes the admin
// issue. It keeps reporting past the threshold so the issue's occurrence count
// tracks how many notifications the outage actually swallowed.
func (n *Notifier) recordDeliveryFailure(cause error) {
	n.healthMu.Lock()
	n.consecutiveFails++
	failures := n.consecutiveFails
	sink := n.healthSink
	n.healthMu.Unlock()

	if sink == nil || failures < deliveryFailureThreshold {
		return
	}
	detail := fmt.Sprintf(
		"%d push notifications in a row failed to reach the gateway. The most recent error was: %v",
		failures, cause)
	if err := sink.RecordPushDeliveryHealth(false, detail); err != nil {
		n.logger.Error("push: record delivery health failure", "err", err)
	}
}

// recordDeliverySuccess clears the run and resolves any open issue. The sink is
// called on every success rather than only on a transition: the counter is
// in-memory and the issue is durable, so a restart mid-outage would otherwise
// strand an issue that nothing could ever close. The sink returns immediately
// when no issue is open, which is one indexed lookup on a path that runs per
// notification, not per poll.
func (n *Notifier) recordDeliverySuccess() {
	n.healthMu.Lock()
	n.consecutiveFails = 0
	sink := n.healthSink
	n.healthMu.Unlock()

	if sink == nil {
		return
	}
	if err := sink.RecordPushDeliveryHealth(true, ""); err != nil {
		n.logger.Error("push: record delivery health recovery", "err", err)
	}
}

// pruneDeadTokens deletes the local push_tokens row for every result the
// gateway flagged as pruned (token rejected by APNs as unregistered). Matching
// on token keeps it precise even if a device re-registered with a new token in
// the meantime. Best-effort: delete failures are logged, not surfaced. Shared
// by the Notifier (after each send) and the test-push handler.
func pruneDeadTokens(db *sql.DB, logger *slog.Logger, resp *SendResponse) {
	if resp == nil {
		return
	}
	pruned := 0
	for _, r := range resp.Results {
		if !r.Pruned || r.Token == "" {
			continue
		}
		if _, err := db.Exec("DELETE FROM push_tokens WHERE token = ?", r.Token); err != nil {
			logger.Error("push: prune dead token", "err", err, "device_id", r.DeviceID)
			continue
		}
		pruned++
	}
	if pruned > 0 {
		logger.Info("push: pruned dead device tokens", "count", pruned)
	}
}

// decisionMessage derives the alert title/body from a request_decision payload.
// An unrecognized decision yields an empty title so no notification is sent.
func decisionMessage(data map[string]interface{}) (title, body string) {
	mediaTitle := str(data["title"])
	if mediaTitle == "" {
		mediaTitle = "Your request"
	}
	subject, plural := decisionSubject(data, mediaTitle)
	switch str(data["decision"]) {
	case "approved":
		verb := " is"
		if plural {
			verb = " are"
		}
		return "Request approved", subject + verb + " on the way"
	case "denied":
		verb := " was"
		if plural {
			verb = " were"
		}
		body = subject + verb + " denied"
		if reason := str(data["reason"]); reason != "" {
			body += ": " + reason
		}
		return "Request denied", body
	default:
		return "", ""
	}
}

// decisionSubject preserves the existing whole-title copy for movies/TV and
// scopes book decisions to the concrete format coverage in the event. Partial
// approval events deliberately include only the formats that succeeded.
func decisionSubject(data map[string]interface{}, mediaTitle string) (string, bool) {
	if str(data["media_type"]) != "book" {
		return mediaTitle, false
	}
	switch str(data["book_format"]) {
	case "ebook":
		return mediaTitle + " eBook", false
	case "audiobook":
		return mediaTitle + " Audiobook", false
	case "both":
		return mediaTitle + " eBook and Audiobook", true
	default:
		return mediaTitle, false
	}
}

// passthrough copies the event fields the client uses to route a notification
// tap (type + media identity) into the APNs custom-data payload.
func passthrough(eventType string, data map[string]interface{}) map[string]any {
	out := map[string]any{"type": eventType}
	if v, ok := data["tmdb_id"]; ok {
		out["tmdb_id"] = v
	}
	if v := str(data["media_type"]); v != "" {
		out["media_type"] = v
	}
	// Books have no TMDB id. Keep the canonical identity, selected library, title
	// hint, and decision scope together so a tap cannot drift to whichever
	// Chaptarr instance happens to be active later. Movie/TV payloads remain
	// unchanged.
	if v := str(data["foreign_id"]); v != "" {
		out["foreign_id"] = v
	}
	if str(data["media_type"]) == "book" {
		if v := str(data["instance_id"]); v != "" {
			out["instance_id"] = v
		}
		if v := str(data["title"]); v != "" {
			out["title"] = v
		}
		if v := str(data["book_format"]); v != "" {
			out["book_format"] = v
		}
	}
	return out
}

// str reads a string value from a map[string]interface{}, returning "" when the
// key is absent or not a string.
func str(v interface{}) string {
	s, _ := v.(string)
	return s
}

// intval reads an int from a map value, tolerating the int/int64/float64 forms a
// value may take whether it arrived in-process or via a JSON round-trip.
func intval(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
