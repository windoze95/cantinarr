// data_ai.go — canned AI chat scripts (the six ported film-history responses
// plus media-results, books, request-status, and admin configuration-change
// turns) and the seeded external-settings-changes configuration history.
// The SSE plumbing (aiStream) lives in ai.go; the history handlers live in
// ai_admin.go.
package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ─── Ported canned responses (old-demo data.go, verbatim)

const (
	aiRespWelcome    = "I'd love to help you discover some classic films! Our collection features iconic public domain movies spanning from the early 1900s to the 1960s. What genre are you in the mood for? We have horror classics like Nosferatu and Night of the Living Dead, sci-fi gems like Metropolis, and witty comedies like His Girl Friday."
	aiRespNosferatu  = "Great choice! Nosferatu (1922) is a masterpiece of German Expressionism directed by F.W. Murnau. It's an unauthorized adaptation of Bram Stoker's Dracula, and despite the studio being ordered to destroy all copies, it survived and became one of the most influential horror films ever made. Max Schreck's portrayal of Count Orlok is truly unforgettable."
	aiRespThriller   = "If you're looking for something thrilling, I'd recommend Charade (1963) starring Cary Grant and Audrey Hepburn. It's often called 'the best Hitchcock movie that Hitchcock never made.' The film combines romance, comedy, and suspense in a Parisian setting. Speaking of Hitchcock, we also have The 39 Steps (1935), one of his early British thrillers!"
	aiRespMetropolis = "For science fiction fans, Metropolis (1927) by Fritz Lang is an absolute must-watch. It's set in a futuristic city divided between wealthy industrialists and underground workers. The visual effects were groundbreaking for its time, and the film's themes about class division remain relevant today. The iconic robot design has influenced everything from C-3PO to modern art."
	aiRespZombie     = "Night of the Living Dead (1968) by George A. Romero essentially invented the modern zombie genre. Shot on a shoestring budget in rural Pennsylvania, it became a massive cultural phenomenon. The film entered the public domain because the distributor accidentally failed to include a copyright notice on the prints. That happy accident means we can share this masterpiece freely!"
	aiRespKeaton     = "The General (1926) starring Buster Keaton is widely considered one of the greatest comedies ever made. It features an incredible train chase sequence that Keaton performed himself — no stunt doubles! The physical comedy and timing are simply unmatched. If you enjoy silent film comedy, this is the perfect starting point."
)

// ─── Keyword routing ────────────────────────────────────

// aiQueryHas matches a keyword against the lowercased question: multiword
// phrases as substrings, single words on word boundaries (so "read" never
// matches "already").
func aiQueryHas(q, kw string) bool {
	if strings.Contains(kw, " ") {
		return strings.Contains(q, kw)
	}
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '-'
	})
	for _, w := range fields {
		if w == kw {
			return true
		}
	}
	return false
}

func aiAnyKeyword(q string, kws ...string) bool {
	for _, kw := range kws {
		if aiQueryHas(q, kw) {
			return true
		}
	}
	return false
}

// aiCannedTurn is one scripted chat turn: a matcher over the lowercased
// question plus the streaming script to run.
type aiCannedTurn struct {
	match func(q string, u *DemoUser) bool
	run   func(s *aiStream)
}

// aiKeywordMatch builds a role-agnostic keyword matcher.
func aiKeywordMatch(kws ...string) func(string, *DemoUser) bool {
	return func(q string, _ *DemoUser) bool { return aiAnyKeyword(q, kws...) }
}

// aiRunCannedTurn routes the question to the first matching canned turn
// (fallback: the welcome/overview response). Called from the chat handler
// after the conversation_id frame; the caller sends [DONE].
func aiRunCannedTurn(s *aiStream, question string) {
	q := strings.ToLower(question)
	for _, turn := range aiCannedTurns {
		if turn.match(q, s.user) {
			turn.run(s)
			return
		}
	}
	aiFilmTurn(aiRespWelcome, 653, 10331, 19, 3085)(s)
}

// aiCannedTurns is evaluated in order; specific subjects come before the
// generic recommendation and fallback turns.
var aiCannedTurns = []aiCannedTurn{
	{
		// Admin-only configuration-change turn (quality profile via chat).
		match: func(q string, u *DemoUser) bool {
			return u != nil && u.Role == roleAdmin &&
				aiAnyKeyword(q, "quality profile", "custom format", "cutoff", "upgrade")
		},
		run: aiConfigChangeTurn,
	},
	{
		match: aiKeywordMatch("nosferatu", "vampire", "dracula", "orlok"),
		run:   aiFilmTurn(aiRespNosferatu, 653),
	},
	{
		match: aiKeywordMatch("metropolis", "sci-fi", "science fiction", "robot", "fritz lang"),
		run:   aiFilmTurn(aiRespMetropolis, 19),
	},
	{
		match: aiKeywordMatch("zombie", "living dead", "romero", "horror", "scary"),
		run:   aiFilmTurn(aiRespZombie, 10331),
	},
	{
		match: aiKeywordMatch("keaton", "the general", "comedy", "funny", "laugh"),
		run:   aiFilmTurn(aiRespKeaton, 961),
	},
	{
		match: aiKeywordMatch("charade", "hitchcock", "thriller", "thrilling", "suspense", "39 steps"),
		run:   aiFilmTurn(aiRespThriller, 4808, 260),
	},
	{
		match: aiKeywordMatch("book", "books", "read", "reading", "novel", "audiobook", "author"),
		run:   aiBooksTurn,
	},
	{
		match: aiKeywordMatch("status", "request", "requests", "requested", "download", "downloads", "queue"),
		run:   aiStatusTurn,
	},
	{
		match: aiKeywordMatch("recommend", "recommendation", "suggest", "suggestion", "trending", "popular", "watch", "tonight", "something"),
		run:   aiRecommendTurn,
	},
}

// ─── Media item builders ────────────────────────────────

// aiMovieItem builds one MediaResultItem for a catalog movie (ids resolve via
// findMovie so a tap routes to a working detail screen).
func aiMovieItem(tmdbID int) (map[string]any, bool) {
	m, ok := findMovie(tmdbID)
	if !ok {
		return nil, false
	}
	item := map[string]any{
		"id":         m.TmdbID,
		"title":      m.Title,
		"media_type": mediaTypeMovie,
	}
	if y := m.Year(); y > 0 {
		item["year"] = strconv.Itoa(y)
	}
	if m.PosterPath != "" {
		item["poster_path"] = m.PosterPath
	}
	if m.VoteAverage > 0 {
		item["vote_average"] = m.VoteAverage
	}
	if m.Overview != "" {
		item["overview"] = m.Overview
	}
	return item, true
}

// aiShowItem builds one MediaResultItem for a catalog TV show.
func aiShowItem(tmdbID int) (map[string]any, bool) {
	sh, ok := findShow(tmdbID)
	if !ok {
		return nil, false
	}
	item := map[string]any{
		"id":         sh.TmdbID,
		"title":      sh.Name,
		"media_type": mediaTypeTV,
	}
	if y := sh.Year(); y > 0 {
		item["year"] = strconv.Itoa(y)
	}
	if sh.PosterPath != "" {
		item["poster_path"] = sh.PosterPath
	}
	if sh.VoteAverage > 0 {
		item["vote_average"] = sh.VoteAverage
	}
	if sh.Overview != "" {
		item["overview"] = sh.Overview
	}
	return item, true
}

// aiBookItem builds one MediaResultItem for a catalog book: no TMDB id,
// foreign_id for routing, poster_url resolved through the chaptarr proxy.
func aiBookItem(b *DemoBook) map[string]any {
	item := map[string]any{
		"title":      b.Title,
		"media_type": mediaTypeBook,
		"foreign_id": b.ForeignID,
	}
	if b.Year > 0 {
		item["year"] = strconv.Itoa(b.Year)
	}
	if b.Overview != "" {
		item["overview"] = b.Overview
	}
	if cp := b.CoverPath(); cp != "" {
		item["poster_url"] = demoServerURL + "/api/instances/" + instChaptarr + cp
	}
	return item
}

// aiDisplayMedia wraps items in the polished display_media sequence
// (tool_start → media_results → tool_end).
func aiDisplayMedia(s *aiStream, items []map[string]any) {
	if len(items) == 0 {
		return
	}
	s.toolStart("display_media", "Preparing results")
	s.pause(400 * time.Millisecond)
	s.media(items)
	s.toolEnd("display_media", true)
}

// aiFilmTurn streams one of the ported film-history responses, then shows
// the mentioned titles in the carousel.
func aiFilmTurn(text string, movieIDs ...int) func(*aiStream) {
	return func(s *aiStream) {
		s.text(text)
		items := []map[string]any{}
		for _, id := range movieIDs {
			if item, ok := aiMovieItem(id); ok {
				items = append(items, item)
			}
		}
		aiDisplayMedia(s, items)
	}
}

// ─── Scripted turns ─────────────────────────────────────

// aiRecommendTurn: "recommend me something" — trending check, a curated
// text answer, then a display_media carousel whose order matches the text.
func aiRecommendTurn(s *aiStream) {
	s.toolStart("get_trending", "Checking what's trending")
	s.pause(700 * time.Millisecond)
	s.toolEnd("get_trending", true)

	text := "Happy to help! Based on what's popular with members right now, here are my picks. " +
		"Metropolis (1927) is Fritz Lang's towering science-fiction epic about a city divided between workers and planners. " +
		"The General (1926) is Buster Keaton at his very best — the locomotive chase is still breathtaking a century later. " +
		"And Charade (1963) pairs Cary Grant with Audrey Hepburn in the best Hitchcock film Hitchcock never made."
	items := []map[string]any{}
	for _, id := range []int{19, 961, 4808} {
		if item, ok := aiMovieItem(id); ok {
			items = append(items, item)
		}
	}
	if show, ok := aiShowItem(90001); ok {
		text += " If a series sounds better tonight, " + show["title"].(string) + " is a great binge."
		items = append(items, show)
	}
	s.text(text)
	aiDisplayMedia(s, items)
}

// aiBooksTurn: searches the book server and showcases up to three titles
// (foreign_id + poster_url — books never build TMDB poster URLs).
func aiBooksTurn(s *aiStream) {
	s.toolStart("search_books", "Searching books")
	s.pause(600 * time.Millisecond)
	books := allBooks()
	if len(books) > 3 {
		books = books[:3]
	}
	s.toolEnd("search_books", true)

	if len(books) == 0 {
		s.text("I couldn't find any titles on the book server just yet. Ask an admin to finish the Books setup, and I'll be able to search and request ebooks and audiobooks for you.")
		return
	}
	var names []string
	for _, b := range books {
		if b.AuthorName != "" {
			names = append(names, fmt.Sprintf("%s by %s", b.Title, b.AuthorName))
		} else {
			names = append(names, b.Title)
		}
	}
	text := "The library has some wonderful public-domain reads. I'd start with " + strings.Join(names, ", ") +
		". Tap a cover below to see formats — I can request the ebook, the audiobook, or both for you."
	s.text(text)
	items := []map[string]any{}
	for _, b := range books {
		items = append(items, aiBookItem(b))
	}
	aiDisplayMedia(s, items)
}

// aiFriendlyStatus maps a REST request status (+ 0..1 progress) to requester
// vocabulary — never arr jargon.
func aiFriendlyStatus(status string, progress float64) string {
	switch status {
	case statusAvailable:
		return "ready to watch"
	case statusDownloading:
		pct := int(progress * 100)
		if pct < 1 {
			pct = 1
		}
		return fmt.Sprintf("downloading — about %d%% done", pct)
	case statusRequested:
		return "requested and waiting in the queue"
	case statusPending:
		return "waiting for an admin's approval"
	case statusDenied:
		return "declined by an admin"
	case statusPartial:
		return "partly available"
	default:
		return "not in the library yet — you can request it right from its detail page"
	}
}

// aiStatusTurn: checks live request state through the requests domain hook,
// so the answer tracks the actual demo lifecycle.
func aiStatusTurn(s *aiStream) {
	s.toolStart("list_my_requests", "Fetching your requests")
	s.pause(600 * time.Millisecond)
	nightStatus, nightProg := requestStatusForTmdb(10331, mediaTypeMovie)
	nosfStatus, nosfProg := requestStatusForTmdb(653, mediaTypeMovie)
	s.toolEnd("list_my_requests", true)

	text := fmt.Sprintf(
		"Here's the latest from the library: Night of the Living Dead is %s, and Nosferatu is %s. "+
			"You can follow live progress on the Requests tab, and I'll be happy to request anything else you're missing — just name a title.",
		aiFriendlyStatus(nightStatus, nightProg),
		aiFriendlyStatus(nosfStatus, nosfProg),
	)
	s.text(text)
	items := []map[string]any{}
	for _, id := range []int{10331, 653} {
		if item, ok := aiMovieItem(id); ok {
			items = append(items, item)
		}
	}
	aiDisplayMedia(s, items)
}

// aiConfigChangeTurn (admin only): previews and "applies" a quality-profile
// update via chat, emitting a configuration_change receipt that also lands
// in the /api/admin/external-settings-changes history.
func aiConfigChangeTurn(s *aiStream) {
	s.toolStart("preview_profile_change", "Previewing profile change")
	s.pause(800 * time.Millisecond)
	s.toolEnd("preview_profile_change", true)
	s.text("Here's the change you asked for on Sonarr's WEB-1080p profile: raise the cutoff from HDTV-720p to WEB 1080p and allow upgrades, so existing episodes get replaced as better releases appear. Applying it now.")

	s.toolStart("apply_profile_change", "Applying profile change")
	s.pause(900 * time.Millisecond)
	change := aiRecordChatProfileChange(s.user)
	s.configurationChange(change)
	s.toolEnd("apply_profile_change", true)
	s.text("Done — the profile is updated, and I've recorded a before/after entry in Configuration history so you can review or restore it any time.")
}

// aiRecordChatProfileChange appends the chat-applied quality-profile update
// to the configuration history and returns its detail JSON (the SSE
// configuration_change payload is the bare change object).
func aiRecordChatProfileChange(u *DemoUser) map[string]any {
	now := time.Now()
	completed := now
	aiChMu.Lock()
	defer aiChMu.Unlock()
	rec := &aiChangeRec{
		ID:           aiChangeNextID,
		ActorUserID:  u.ID,
		ActorName:    u.Username,
		Source:       "ai_chat",
		ServiceType:  serviceSonarr,
		InstanceID:   instSonarr,
		InstanceName: "Sonarr",
		ResourceType: "quality_profile",
		ResourceID:   "4",
		ResourceName: "WEB-1080p",
		Operation:    "update",
		Status:       "applied",
		Summary:      fmt.Sprintf("Quality profile update: %q", "WEB-1080p"),
		Changes: []aiFieldDiff{
			{Key: "cutoff", Label: "Cutoff", Before: "HDTV-720p", After: "WEB 1080p"},
			{Key: "upgrade_allowed", Label: "Upgrades allowed", Before: "false", After: "true"},
		},
		CreatedAt:   now,
		CompletedAt: &completed,
	}
	aiChangeNextID++
	aiChanges = append(aiChanges, rec)
	return aiChangeJSON(rec, true)
}

// ─── Configuration history storage & seeds ──────────────

// aiFieldDiff is one before/after field projection of a settings change.
type aiFieldDiff struct {
	Key    string
	Label  string
	Before string
	After  string
}

// aiChangeRec is one ExternalSettingChange history record. RevertedBy links
// to the inverse record appended by a revert (history is never edited).
type aiChangeRec struct {
	ID           int
	ParentID     int
	ActorUserID  int
	ActorName    string
	Source       string // "ai_chat" | "admin_revert"
	ServiceType  string
	InstanceID   string
	InstanceName string
	ResourceType string // "quality_profile" | "custom_format"
	ResourceID   string
	ResourceName string
	Operation    string // "update" | "create" | "revert"
	Status       string // "applied" | "failed" | ...
	Summary      string
	Changes      []aiFieldDiff
	ErrorText    string
	CreatedAt    time.Time
	CompletedAt  *time.Time
	RevertedBy   int
}

var (
	aiChMu         sync.Mutex
	aiChanges      []*aiChangeRec
	aiChangeNextID = 5
)

// init seeds the configuration history: one applied quality-profile update
// (revertable), one failed update with error_text, and one linked revert
// pair. Only frozen constants are referenced (no state accessors in init).
func init() {
	now := time.Now()
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	done1 := at(26*time.Hour - 3*time.Second)
	done2 := at(20*time.Hour - 2*time.Second)
	done3 := at(72*time.Hour - 4*time.Second)
	done4 := at(70*time.Hour - 2*time.Second)

	aiChanges = []*aiChangeRec{
		{
			ID: 1, ActorUserID: 1, ActorName: "admin", Source: "ai_chat",
			ServiceType: serviceRadarr, InstanceID: instRadarr, InstanceName: "Radarr",
			ResourceType: "quality_profile", ResourceID: "6", ResourceName: "HD-1080p",
			Operation: "update", Status: "applied",
			Summary: `Quality profile update: "HD-1080p"`,
			Changes: []aiFieldDiff{
				{Key: "upgrade_allowed", Label: "Upgrades allowed", Before: "false", After: "true"},
				{Key: "cutoff", Label: "Cutoff", Before: "HD-720p", After: "HD-1080p"},
			},
			CreatedAt: at(26 * time.Hour), CompletedAt: &done1,
		},
		{
			ID: 2, ActorUserID: 1, ActorName: "admin", Source: "ai_chat",
			ServiceType: serviceRadarr, InstanceID: instRadarr, InstanceName: "Radarr",
			ResourceType: "quality_profile", ResourceID: "5", ResourceName: "Ultra-HD",
			Operation: "update", Status: "failed",
			Summary: `Quality profile update: "Ultra-HD"`,
			Changes: []aiFieldDiff{
				{Key: "cutoff", Label: "Cutoff", Before: "HD-1080p", After: "Remux-2160p"},
			},
			ErrorText: "Radarr rejected the update: cutoff must be one of the profile's allowed qualities.",
			CreatedAt: at(20 * time.Hour), CompletedAt: &done2,
		},
		{
			ID: 3, ActorUserID: 1, ActorName: "admin", Source: "ai_chat",
			ServiceType: serviceSonarr, InstanceID: instSonarr, InstanceName: "Sonarr",
			ResourceType: "quality_profile", ResourceID: "3", ResourceName: "WEB-720p",
			Operation: "update", Status: "applied",
			Summary: `Quality profile update: "WEB-720p"`,
			Changes: []aiFieldDiff{
				{Key: "upgrade_allowed", Label: "Upgrades allowed", Before: "true", After: "false"},
			},
			CreatedAt: at(72 * time.Hour), CompletedAt: &done3,
			RevertedBy: 4,
		},
		{
			ID: 4, ParentID: 3, ActorUserID: 1, ActorName: "admin", Source: "admin_revert",
			ServiceType: serviceSonarr, InstanceID: instSonarr, InstanceName: "Sonarr",
			ResourceType: "quality_profile", ResourceID: "3", ResourceName: "WEB-720p",
			Operation: "revert", Status: "applied",
			Summary: `Quality profile restore: "WEB-720p"`,
			Changes: []aiFieldDiff{
				{Key: "upgrade_allowed", Label: "Upgrades allowed", Before: "false", After: "true"},
			},
			CreatedAt: at(70 * time.Hour), CompletedAt: &done4,
		},
	}
}
