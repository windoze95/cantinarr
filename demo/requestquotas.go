// requestquotas.go — request allowances: how many movies, TV seasons, eBooks,
// audiobooks, and albums one account may ask for in a rolling window, the
// admin's defaults and per-user overrides, and the caller's own live counters.
// Admins are exempt. Nothing here refuses a demo request: the counters move,
// the screens are real, and a spent allowance answers 429 with the same
// `request_quota_exceeded` envelope the app parses.
//
// Prefix: rq…
package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// rqKey is one allowance category. BookFormat is empty for everything but
// books — media types describe media, and a book's two formats are counted
// separately because they are requested separately.
type rqKey struct {
	MediaType  string
	BookFormat string
}

// rqKeys is the frozen category order every allowance surface answers in.
var rqKeys = []rqKey{
	{MediaType: mediaTypeMovie},
	{MediaType: mediaTypeTV},
	{MediaType: mediaTypeBook, BookFormat: bookFormatEbook},
	{MediaType: mediaTypeBook, BookFormat: bookFormatAudiobook},
	{MediaType: mediaTypeMusic},
}

func (k rqKey) valid() bool {
	for _, known := range rqKeys {
		if known == k {
			return true
		}
	}
	return false
}

// rqRule is an allowance setting. Count nil means unlimited; zero refuses new
// work. WindowDays is 1, 7, or 30 — the only windows the server accepts.
type rqRule struct {
	Count      *int
	WindowDays int
}

var (
	rqMu sync.Mutex

	// rqDefaults is the server-wide allowance every account inherits.
	rqDefaults = map[rqKey]rqRule{}

	// rqOverrides is userID -> category -> rule. A present entry wins over
	// the default; "inherit" on a write deletes it.
	rqOverrides = map[int]map[rqKey]rqRule{}

	// rqSpends is userID -> category -> the times requests were accepted.
	// The rolling window is computed from these, so counters replenish on
	// their own instead of being stored as a number that drifts.
	rqSpends = map[int]map[rqKey][]time.Time{}
)

func init() {
	ten, five, three := 10, 5, 3
	rqDefaults[rqKey{MediaType: mediaTypeMovie}] = rqRule{Count: &ten, WindowDays: 7}
	rqDefaults[rqKey{MediaType: mediaTypeTV}] = rqRule{Count: &five, WindowDays: 7}
	rqDefaults[rqKey{MediaType: mediaTypeBook, BookFormat: bookFormatEbook}] = rqRule{Count: &five, WindowDays: 30}
	rqDefaults[rqKey{MediaType: mediaTypeBook, BookFormat: bookFormatAudiobook}] = rqRule{Count: &three, WindowDays: 30}
	rqDefaults[rqKey{MediaType: mediaTypeMusic}] = rqRule{Count: &ten, WindowDays: 7}

	// The seeded requester carries a generous movie override and some spent
	// allowance, so the screen opens on a real mix of default and override
	// rows with counters part-way through their windows.
	twenty := 20
	rqOverrides[2] = map[rqKey]rqRule{
		{MediaType: mediaTypeMovie}: {Count: &twenty, WindowDays: 7},
	}
	now := time.Now()
	rqSpends[2] = map[rqKey][]time.Time{
		{MediaType: mediaTypeMovie}: {
			now.Add(-50 * time.Hour), now.Add(-31 * time.Hour), now.Add(-6 * time.Hour),
		},
		{MediaType: mediaTypeTV}:                                    {now.Add(-70 * time.Hour)},
		{MediaType: mediaTypeBook, BookFormat: bookFormatAudiobook}: {now.Add(-9 * 24 * time.Hour)},
		{MediaType: mediaTypeMusic}:                                 {now.Add(-20 * time.Hour), now.Add(-3 * time.Hour)},
	}
	// The kid account inherits every default and has spent one movie.
	rqSpends[4] = map[rqKey][]time.Time{
		{MediaType: mediaTypeMovie}: {now.Add(-14 * time.Hour)},
	}
}

// ─── Resolution ─────────────────────────────────────────

// rqRuleFor resolves one category for one user: the override when present,
// else the server default. Caller holds rqMu.
func rqRuleFor(userID int, k rqKey) (rqRule, string) {
	if byUser := rqOverrides[userID]; byUser != nil {
		if rule, ok := byUser[k]; ok {
			return rule, "user"
		}
	}
	if rule, ok := rqDefaults[k]; ok {
		return rule, "default"
	}
	return rqRule{WindowDays: 7}, "default"
}

// rqExempt reports an account allowances never apply to. Admins ask for
// whatever they like; the screen says so rather than showing empty counters.
func rqExempt(u *DemoUser) bool { return u != nil && u.Role == roleAdmin }

// rqLive returns the spends still inside the window, oldest first. Caller
// holds rqMu.
func rqLive(userID int, k rqKey, window time.Duration, now time.Time) []time.Time {
	kept := []time.Time{}
	for _, at := range rqSpends[userID][k] {
		if now.Sub(at) < window {
			kept = append(kept, at)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Before(kept[j]) })
	return kept
}

// rqAllowanceView is one row of the allowances list. requestedUnits is the
// units a preview would charge (0 on a plain read).
func rqAllowanceView(userID int, k rqKey, now time.Time, requestedUnits int) map[string]any {
	rule, source := rqRuleFor(userID, k)
	window := time.Duration(rule.WindowDays) * 24 * time.Hour
	live := rqLive(userID, k, window, now)

	row := map[string]any{
		"media_type":  k.MediaType,
		"window_days": rule.WindowDays,
		"source":      source,
		"used":        len(live),
	}
	if k.BookFormat != "" {
		row["book_format"] = k.BookFormat
	}
	if rule.Count == nil {
		// Unlimited: count and remaining are both null, never 0 — zero is a
		// refusal, and the two must never be rendered the same way.
		row["count"] = nil
		row["remaining"] = nil
	} else {
		remaining := *rule.Count - len(live)
		if remaining < 0 {
			remaining = 0
		}
		row["count"] = *rule.Count
		row["remaining"] = remaining
	}
	if len(live) > 0 {
		row["next_replenishes_at"] = live[0].Add(window).UTC().Format(time.RFC3339)
		row["fully_replenishes_at"] = live[len(live)-1].Add(window).UTC().Format(time.RFC3339)
	}
	if requestedUnits > 0 {
		row["requested_units"] = requestedUnits
	}
	return row
}

// rqView is one account's whole allowance picture.
func rqView(u *DemoUser, requested map[rqKey]int) map[string]any {
	now := time.Now()
	rows := []map[string]any{}
	var nextChange *time.Time
	for _, k := range rqKeys {
		row := rqAllowanceView(u.ID, k, now, requested[k])
		rows = append(rows, row)
		if raw, ok := row["next_replenishes_at"].(string); ok {
			if at, err := time.Parse(time.RFC3339, raw); err == nil {
				if nextChange == nil || at.Before(*nextChange) {
					nextChange = &at
				}
			}
		}
	}
	out := map[string]any{
		"exempt":     rqExempt(u),
		"as_of":      now.UTC().Format(time.RFC3339),
		"allowances": rows,
	}
	if nextChange != nil {
		out["next_change_at"] = nextChange.UTC().Format(time.RFC3339)
	}
	return out
}

// rqRuleRow is the settings-only shape (no counters) the admin defaults
// endpoint answers with.
func rqRuleRow(k rqKey, rule rqRule) map[string]any {
	row := map[string]any{"media_type": k.MediaType, "window_days": rule.WindowDays}
	if k.BookFormat != "" {
		row["book_format"] = k.BookFormat
	}
	if rule.Count == nil {
		row["count"] = nil
	} else {
		row["count"] = *rule.Count
	}
	return row
}

// ─── Cross-domain hooks ─────────────────────────────────

// rqCharge records one accepted request against the caller's allowance, and
// reports whether it fit. Admins always fit and are never charged.
func rqCharge(u *DemoUser, mediaType, bookFormat string, units int) bool {
	if rqExempt(u) || units <= 0 {
		return true
	}
	k := rqKey{MediaType: mediaType, BookFormat: bookFormat}
	if !k.valid() {
		return true
	}
	rqMu.Lock()
	defer rqMu.Unlock()
	rule, _ := rqRuleFor(u.ID, k)
	if rule.Count != nil {
		window := time.Duration(rule.WindowDays) * 24 * time.Hour
		if len(rqLive(u.ID, k, window, time.Now()))+units > *rule.Count {
			return false
		}
	}
	if rqSpends[u.ID] == nil {
		rqSpends[u.ID] = map[rqKey][]time.Time{}
	}
	now := time.Now()
	for i := 0; i < units; i++ {
		rqSpends[u.ID][k] = append(rqSpends[u.ID][k], now)
	}
	wsBroadcast(evtRequestQuotaChanged, map[string]any{"user_id": u.ID})
	return true
}

// rqExceeded is the 429 body the app recognises by its `code`.
func rqExceeded(w http.ResponseWriter, u *DemoUser, k rqKey) {
	rqMu.Lock()
	row := rqAllowanceView(u.ID, k, time.Now(), 0)
	rqMu.Unlock()
	body := map[string]any{
		"code":             "request_quota_exceeded",
		"error":            "Request limit reached.",
		"allowances":       []map[string]any{row},
		"reduce_selection": false,
	}
	if at, ok := row["next_replenishes_at"].(string); ok {
		body["earliest_fits_at"] = at
	}
	writeJSON(w, http.StatusTooManyRequests, body)
}

// ─── Routes ─────────────────────────────────────────────

func registerRequestQuotas(r chi.Router) {
	r.Get("/me/request-quotas", rqMineHandler)
	r.Post("/requests/preview", rqPreviewHandler)

	admin := r.With(requireAdmin)
	admin.Get("/admin/request-quotas", rqAdminHandler)
	admin.Put("/admin/request-quotas", rqAdminHandler)
	admin.Get("/admin/users/{userID}/request-quotas", rqAdminHandler)
	admin.Put("/admin/users/{userID}/request-quotas", rqAdminHandler)
	admin.Post("/admin/users/{userID}/request-quotas/reset", rqResetHandler)
}

func rqMineHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	rqMu.Lock()
	defer rqMu.Unlock()
	writeJSON(w, http.StatusOK, rqView(u, nil))
}

// rqPreviewHandler answers "would this fit?" without charging anything.
func rqPreviewHandler(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body struct {
		MediaType  string `json:"media_type"`
		BookFormat string `json:"book_format"`
		Seasons    []int  `json:"seasons"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	k := rqKey{MediaType: body.MediaType, BookFormat: body.BookFormat}
	if body.MediaType == mediaTypeBook && body.BookFormat == bookFormatBoth {
		// "Both" charges each format separately; preview the ebook side and
		// say so by returning both rows in the view below.
		k.BookFormat = bookFormatEbook
	}
	units := 1
	if body.MediaType == mediaTypeTV && len(body.Seasons) > 0 {
		units = len(body.Seasons)
	}

	rqMu.Lock()
	defer rqMu.Unlock()
	requested := map[rqKey]int{}
	if k.valid() {
		requested[k] = units
	}
	out := rqView(u, requested)
	fits := true
	if k.valid() && !rqExempt(u) {
		rule, _ := rqRuleFor(u.ID, k)
		if rule.Count != nil {
			window := time.Duration(rule.WindowDays) * 24 * time.Hour
			used := len(rqLive(u.ID, k, window, time.Now()))
			fits = used+units <= *rule.Count
			if !fits {
				if room := *rule.Count - used; room > 0 && body.MediaType == mediaTypeTV {
					out["reduce_selection"] = true
					out["seasons"] = body.Seasons[:room]
				}
			}
		}
	}
	out["fits"] = fits
	if _, ok := out["reduce_selection"]; !ok {
		out["reduce_selection"] = false
	}
	if !fits {
		for _, row := range out["allowances"].([]map[string]any) {
			if row["media_type"] == k.MediaType {
				if at, ok := row["next_replenishes_at"].(string); ok {
					out["earliest_fits_at"] = at
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// rqAdminHandler serves all four admin shapes: the server defaults (no
// userID) and one user's live view, each readable and writable.
func rqAdminHandler(w http.ResponseWriter, r *http.Request) {
	userID := 0
	if raw := chi.URLParam(r, "userID"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid user id")
			return
		}
		userID = id
	}
	var target *DemoUser
	if userID != 0 {
		if target = userByID(userID); target == nil {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
	}

	rqMu.Lock()
	defer rqMu.Unlock()

	if r.Method == http.MethodPut {
		var body struct {
			Allowances []struct {
				MediaType  string `json:"media_type"`
				BookFormat string `json:"book_format"`
				Count      *int   `json:"count"`
				WindowDays int    `json:"window_days"`
				Inherit    bool   `json:"inherit"`
			} `json:"allowances"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil {
			writeErr(w, http.StatusBadRequest, "invalid allowance rules")
			return
		}
		for _, raw := range body.Allowances {
			k := rqKey{MediaType: raw.MediaType, BookFormat: raw.BookFormat}
			if !k.valid() || (raw.Inherit && userID == 0) {
				writeErr(w, http.StatusBadRequest, "invalid request allowance")
				return
			}
			if raw.Inherit {
				delete(rqOverrides[userID], k)
				continue
			}
			if raw.WindowDays != 1 && raw.WindowDays != 7 && raw.WindowDays != 30 {
				writeErr(w, http.StatusBadRequest, "allowance window must be 1, 7, or 30 days")
				return
			}
			if raw.Count != nil && (*raw.Count < 0 || *raw.Count > 1000000) {
				writeErr(w, http.StatusBadRequest,
					"allowance count must be between 0 and 1000000, or null for unlimited")
				return
			}
			rule := rqRule{Count: raw.Count, WindowDays: raw.WindowDays}
			if userID == 0 {
				rqDefaults[k] = rule
				continue
			}
			if rqOverrides[userID] == nil {
				rqOverrides[userID] = map[rqKey]rqRule{}
			}
			rqOverrides[userID][k] = rule
		}
		wsBroadcast(evtRequestQuotaChanged, map[string]any{"user_id": userID})
	}

	if target != nil {
		writeJSON(w, http.StatusOK, rqView(target, nil))
		return
	}
	rows := []map[string]any{}
	for _, k := range rqKeys {
		rule, _ := rqRuleFor(0, k)
		rows = append(rows, rqRuleRow(k, rule))
	}
	writeJSON(w, http.StatusOK, map[string]any{"allowances": rows})
}

// rqResetHandler clears the spent counters for the named categories, which
// is how an admin hands somebody their allowance back early.
func rqResetHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "userID"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	target := userByID(id)
	if target == nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	var body struct {
		Allowances []struct {
			MediaType  string `json:"media_type"`
			BookFormat string `json:"book_format"`
		} `json:"allowances"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&body) != nil {
		writeErr(w, http.StatusBadRequest, "select allowances to reset")
		return
	}
	rqMu.Lock()
	defer rqMu.Unlock()
	for _, raw := range body.Allowances {
		k := rqKey{MediaType: raw.MediaType, BookFormat: raw.BookFormat}
		if !k.valid() {
			writeErr(w, http.StatusBadRequest, "invalid request allowance")
			return
		}
		delete(rqSpends[id], k)
	}
	wsBroadcast(evtRequestQuotaChanged, map[string]any{"user_id": id})
	writeJSON(w, http.StatusOK, rqView(target, nil))
}
