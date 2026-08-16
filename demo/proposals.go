// proposals.go — the admin approval surface for quality-profile changes
// parked by external MCP agents (GET/decide /api/admin/profile-change-
// proposals). On the real server an external assistant has no server-witnessed
// chat turn, so its previewed change parks as a pending proposal and consent
// happens here: an admin reviews the stored diff and approves (re-validated,
// verified write) or rejects (nothing touched). The demo has no external MCP
// transport, so the store ships pre-parked fixtures instead of a park path,
// and approval simply marks the row applied — the fictional arrs never drift,
// which is why the live-applicability check always says "applicable".
//
// Wire shapes mirror mcp.ProfileChangeProposal (profile_proposal_store.go):
// timestamps are RFC3339 STRINGS (not raw time values), and every field the
// real struct tags omitempty is omitted here when empty. Domain prefix: prop…;
// the store is guarded by propMu.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// registerProposals mounts the profile-change-proposal routes on the
// authenticated /api router. Admin-gated: the real server requires
// PermissionInstancesManage, which the demo's role model folds into admin.
func registerProposals(r chi.Router) {
	r.With(requireAdmin).Get("/admin/profile-change-proposals", propHandleList)
	r.With(requireAdmin).Get("/admin/profile-change-proposals/{id}", propHandleGet)
	r.With(requireAdmin).Post("/admin/profile-change-proposals/{id}/approve", propHandleApprove)
	r.With(requireAdmin).Post("/admin/profile-change-proposals/{id}/reject", propHandleReject)
}

// evtProfileChangeDecided drains the other admins' badges after a decision.
// WS-only by design (like agent_action_decided): nobody needs a lock-screen
// alert that work disappeared. Declared here because types.go is frozen.
const evtProfileChangeDecided = "profile_change_decided"

// propProposalTTL bounds how long a parked proposal waits for a decision
// (mirrors the real 7-day window).
const propProposalTTL = 7 * 24 * time.Hour

// propMaxNoteBytes caps the stored reject note (silent truncation, like the
// real store's text cap).
const propMaxNoteBytes = 4 << 10

// propProposal is one parked profile change. Zero values are the "absent"
// sentinels the renderer omits (empty strings, nil DecidedAt, 0 setting
// change id), matching the real struct's omitempty fields.
type propProposal struct {
	ID              int
	Status          string // pending | applied | rejected | superseded | expired | failed
	Service         string
	InstanceID      string
	InstanceName    string
	ProfileID       int
	ProfileName     string
	ProposedByName  string
	SourceClient    string
	Diff            []string
	CreatedAt       time.Time
	ExpiresAt       time.Time
	DecidedByName   string
	DecidedAt       *time.Time
	RejectNote      string
	ResultText      string
	SettingChangeID int // 0 = absent
}

var (
	propMu        sync.Mutex
	propProposals = map[int]*propProposal{}

	// propNextSettingChangeID fabricates the configuration-history link an
	// approval records; the seeded applied proposal already holds 214.
	propNextSettingChangeID = 215
)

// Seeds: one pending proposal against the fictional Radarr (boost restored
// classics, penalize low-bitrate rips — the library is public-domain films)
// and one already-applied historical proposal against the fictional Sonarr.
func init() {
	now := time.Now().UTC()
	tp := func(t time.Time) *time.Time { return &t }

	appliedCreated := now.Add(-4 * 24 * time.Hour)
	appliedDecided := now.Add(-3 * 24 * time.Hour)
	propProposals[1] = &propProposal{
		ID: 1, Status: "applied", Service: serviceSonarr,
		InstanceID: instSonarr, InstanceName: "Sonarr",
		ProfileID: 4, ProfileName: "HD-1080p",
		ProposedByName: "admin", SourceClient: "MCP: Claude Desktop",
		Diff: []string{
			"upgrade policy: disabled -> enabled",
			`quality cutoff: "HDTV-720p" [4] -> "WEBDL-1080p" [3]`,
		},
		CreatedAt: appliedCreated, ExpiresAt: appliedCreated.Add(propProposalTTL),
		DecidedByName: "admin", DecidedAt: tp(appliedDecided),
		SettingChangeID: 214,
	}

	pendingCreated := now.Add(-26 * time.Hour)
	propProposals[2] = &propProposal{
		ID: 2, Status: "pending", Service: serviceRadarr,
		InstanceID: instRadarr, InstanceName: "Radarr",
		ProfileID: 4, ProfileName: "HD-1080p",
		ProposedByName: "admin", SourceClient: "MCP: Claude Desktop",
		Diff: []string{
			`custom format "Restored & Remastered" [3]: +0 -> +50`,
			`custom format "Low-Bitrate WEB" [5]: +0 -> -60`,
			`quality cutoff: "WEBDL-1080p" [3] -> "Bluray-1080p" [7]`,
		},
		CreatedAt: pendingCreated, ExpiresAt: pendingCreated.Add(propProposalTTL),
	}
}

// propLockedJSON renders one proposal. currentStatus is non-empty only on the
// detail read of a pending proposal ("applicable" | "stale" | "unavailable").
// Call with propMu held.
func propLockedJSON(p *propProposal, currentStatus string) map[string]any {
	m := map[string]any{
		"id":            p.ID,
		"status":        p.Status,
		"service":       p.Service,
		"instance_id":   p.InstanceID,
		"instance_name": p.InstanceName,
		"profile_id":    p.ProfileID,
		"profile_name":  p.ProfileName,
		// The proposer is the ADMIN whose MCP client parked the change; the
		// client itself is source_client.
		"proposed_by_name": p.ProposedByName,
		"diff":             p.Diff,
		"created_at":       p.CreatedAt.UTC().Format(time.RFC3339),
		"expires_at":       p.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if p.SourceClient != "" {
		m["source_client"] = p.SourceClient
	}
	if p.DecidedByName != "" {
		m["decided_by_name"] = p.DecidedByName
	}
	if p.DecidedAt != nil {
		m["decided_at"] = p.DecidedAt.UTC().Format(time.RFC3339)
	}
	if p.RejectNote != "" {
		m["reject_note"] = p.RejectNote
	}
	if p.ResultText != "" {
		m["result_text"] = p.ResultText
	}
	if p.SettingChangeID != 0 {
		m["setting_change_id"] = p.SettingChangeID
	}
	if currentStatus != "" {
		m["current_status"] = currentStatus
	}
	return m
}

// propLockedSweep expires overdue pending proposals, so a listing never shows
// a proposal approval would refuse as expired. The real sweep's second half
// (aging out crash-interrupted approvals) has no demo equivalent — approval
// here never leaves a row executing. Call with propMu held.
func propLockedSweep(now time.Time) {
	for _, p := range propProposals {
		if p.Status == "pending" && !p.ExpiresAt.After(now) {
			p.Status = "expired"
			decided := now
			p.DecidedAt = &decided
			p.ResultText = "Expired before any admin decided."
		}
	}
}

// propLockedPendingCount reports how many proposals await a decision — the
// authoritative pending_count the decided event carries. Call with propMu held.
func propLockedPendingCount() int {
	n := 0
	for _, p := range propProposals {
		if p.Status == "pending" {
			n++
		}
	}
	return n
}

// propHandleList serves GET /admin/profile-change-proposals: pending only by
// default, the full recent history for ?status=all, newest first.
func propHandleList(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("status") == "all"
	propMu.Lock()
	propLockedSweep(time.Now().UTC())
	ids := make([]int, 0, len(propProposals))
	for id, p := range propProposals {
		if !all && p.Status != "pending" {
			continue
		}
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, propLockedJSON(propProposals[id], ""))
	}
	propMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"proposals": out})
}

// propFromRequest parses {id} and loads the proposal; a miss writes the
// error. Call with propMu held.
func propFromRequest(w http.ResponseWriter, r *http.Request) (*propProposal, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid proposal id")
		return nil, false
	}
	p := propProposals[id]
	if p == nil {
		writeErr(w, http.StatusNotFound, "proposal not found")
		return nil, false
	}
	return p, true
}

// propHandleGet serves the detail read. A pending proposal is annotated with
// the live-applicability check; the demo's fictional arrs never drift, so a
// pending row is always still "applicable".
func propHandleGet(w http.ResponseWriter, r *http.Request) {
	propMu.Lock()
	p, ok := propFromRequest(w, r)
	if !ok {
		propMu.Unlock()
		return
	}
	currentStatus := ""
	if p.Status == "pending" {
		currentStatus = "applicable"
	}
	payload := propLockedJSON(p, currentStatus)
	propMu.Unlock()
	writeJSON(w, http.StatusOK, payload)
}

// propHandleApprove executes one parked proposal after the admin's in-app
// consent. The demo skips the real re-validation/verified-write machinery
// (nothing can drift) and marks the row applied, recording the fabricated
// configuration-history link exactly like markApplied does.
func propHandleApprove(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	now := time.Now().UTC()
	propMu.Lock()
	p, ok := propFromRequest(w, r)
	if !ok {
		propMu.Unlock()
		return
	}
	// An overdue-but-unswept pending row fails the claim on the real server
	// (the next list read sweeps it to expired).
	if p.Status != "pending" || !p.ExpiresAt.After(now) {
		propMu.Unlock()
		writeErr(w, http.StatusConflict, "This proposal was already decided, replaced, or expired. Refresh the list.")
		return
	}
	p.Status = "applied"
	p.DecidedByName = u.Username
	decided := now
	p.DecidedAt = &decided
	p.SettingChangeID = propNextSettingChangeID
	propNextSettingChangeID++
	payload := propLockedJSON(p, "")
	pending := propLockedPendingCount()
	proposalID := p.ID
	propMu.Unlock()

	wsToAdmins(evtProfileChangeDecided, map[string]any{
		"proposal_id": proposalID, "pending_count": pending,
	})
	writeJSON(w, http.StatusOK, payload)
}

// propHandleReject declines a pending proposal without touching the arr. The
// body ({"note": …}) is decoded leniently like the real handler — a missing
// or malformed body simply means no note.
func propHandleReject(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
	note := strings.TrimSpace(body.Note)
	if len(note) > propMaxNoteBytes {
		note = note[:propMaxNoteBytes]
	}

	now := time.Now().UTC()
	propMu.Lock()
	p, ok := propFromRequest(w, r)
	if !ok {
		propMu.Unlock()
		return
	}
	if p.Status != "pending" {
		propMu.Unlock()
		writeErr(w, http.StatusConflict, "This proposal was already decided, replaced, or expired. Refresh the list.")
		return
	}
	p.Status = "rejected"
	p.DecidedByName = u.Username
	decided := now
	p.DecidedAt = &decided
	p.RejectNote = note
	payload := propLockedJSON(p, "")
	pending := propLockedPendingCount()
	proposalID := p.ID
	propMu.Unlock()

	wsToAdmins(evtProfileChangeDecided, map[string]any{
		"proposal_id": proposalID, "pending_count": pending,
	})
	writeJSON(w, http.StatusOK, payload)
}
