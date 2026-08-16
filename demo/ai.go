// ai.go — the AI chat SSE endpoint, personal AI settings/credentials, and the
// personal Codex (OpenAI OAuth) device-flow simulation. Admin-side AI surfaces
// live in ai_admin.go; canned chat scripts and configuration-history seeds
// live in data_ai.go. All shared AI state (personal + shared profiles,
// conversations, device flows, chat admission) is guarded by aiMu and is
// domain-local — the core store is never touched from here except through the
// contract accessors.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ─── Domain state (guarded by aiMu) ─────────────────────

// aiPersonalState is one user's personal AI configuration: the optional
// selection override, stored key presence per provider (write-only — values
// are never kept), and the personal Codex OAuth link.
type aiPersonalState struct {
	Selected bool
	Provider string
	Model    string
	Keys     map[string]bool // anthropic/openai/gemini -> stored-key presence
	// Personal Codex (OpenAI OAuth) link.
	CodexLinked   bool
	CodexEmail    string
	CodexPlan     string
	CodexLinkedAt time.Time
}

// aiCodexFlow is one in-flight simulated device-login flow.
type aiCodexFlow struct {
	UserID  int
	Shared  bool // true = admin shared scope
	Created time.Time
	Polls   int
}

var (
	aiMu sync.Mutex

	aiPersonalStates = map[int]*aiPersonalState{}

	// Shared (admin) profile. Seeded per the resolved design decisions:
	// provider anthropic, anthropic key present, health check on.
	aiSharedProvider    = "anthropic"
	aiSharedModel       = "claude-opus-4-8"
	aiSharedCreds       = map[string]bool{"tmdb_access_token": true, "anthropic_key": true, "openai_key": false, "gemini_key": false, "trakt_client_id": true}
	aiHealthEnabled     = true
	aiHealthLastChecked = time.Now().Add(-6 * time.Hour)

	// Shared Codex OAuth account (honest not-connected default).
	aiSharedCodexLinked   = false
	aiSharedCodexEmail    = ""
	aiSharedCodexPlan     = ""
	aiSharedCodexLinkedAt time.Time

	// Per-user conversation-id map: known ids are echoed, unknown ids mint a
	// fresh 32-hex id (spec §1 conversation semantics).
	aiConvs = map[int]map[string]bool{}

	// Simulated Codex device flows, personal and shared.
	aiCodexFlows = map[string]*aiCodexFlow{}

	// Chat admission bookkeeping: one turn per user, 16 server-wide, 4 billed
	// to the shared source.
	aiActiveTurns  = map[int]bool{}
	aiActiveTotal  = 0
	aiActiveShared = 0
)

const (
	aiCodexVerificationURI = "https://auth.openai.com/activate"
	aiCodexFlowTTL         = 15 * time.Minute
	aiValidationDelay      = 1500 * time.Millisecond
	aiValidationRejected   = "The provider credential or account connection was rejected. Check or reconnect the provider credential. Nothing was saved."
)

// aiKeyProviders are the API-key providers; codex is OAuth-only.
var aiKeyProviders = map[string]string{
	"anthropic": "anthropic_key",
	"openai":    "openai_key",
	"gemini":    "gemini_key",
}

// ─── Provider catalog (spec §10; order matters) ─────────

// aiProviderCatalog builds the full provider/model catalog. The first model
// of each provider is its default. codex serializes "credential_key": ""
// (no omitempty on the real struct tag).
func aiProviderCatalog() []map[string]any {
	model := func(id, label, desc string) map[string]any {
		return map[string]any{"id": id, "label": label, "description": desc}
	}
	return []map[string]any{
		{
			"id": "anthropic", "label": "Anthropic", "auth_type": "api_key",
			"credential_key": "anthropic_key",
			"models": []map[string]any{
				model("claude-opus-4-8", "Claude Opus 4.8", "Most capable Claude Opus-tier model"),
				model("claude-fable-5", "Claude Fable 5", "Highest-capability Claude model"),
				model("claude-sonnet-5", "Claude Sonnet 5", "Latest balanced Claude model"),
				model("claude-sonnet-4-6", "Claude Sonnet 4.6", "Balanced speed and intelligence"),
				model("claude-haiku-4-5", "Claude Haiku 4.5", "Fastest, lowest-cost Claude option"),
			},
		},
		{
			"id": "openai", "label": "OpenAI", "auth_type": "api_key",
			"credential_key": "openai_key",
			"models": []map[string]any{
				model("gpt-5.5", "GPT-5.5", "Flagship OpenAI model"),
				model("gpt-5.4", "GPT-5.4", "Affordable frontier model"),
				model("gpt-5.4-mini", "GPT-5.4 mini", "Lower latency and cost"),
				model("gpt-5.4-nano", "GPT-5.4 nano", "Smallest current GPT-5.4 model"),
				model("gpt-4.1", "GPT-4.1", "Stable previous-generation model"),
				model("gpt-4.1-mini", "GPT-4.1 mini", "Fast previous-generation model"),
			},
		},
		{
			"id": "gemini", "label": "Google Gemini", "auth_type": "api_key",
			"credential_key": "gemini_key",
			"models": []map[string]any{
				model("gemini-3.5-flash", "Gemini 3.5 Flash", "Current stable Gemini Flash model"),
				model("gemini-3.1-flash-lite", "Gemini 3.1 Flash-Lite", "Current stable low-cost Gemini model"),
				model("gemini-3.1-pro-preview", "Gemini 3.1 Pro Preview", "Preview model optimized for agentic and coding workflows"),
				model("gemini-3.1-pro-preview-customtools", "Gemini 3.1 Pro Preview Custom Tools", "Gemini 3.1 Pro endpoint tuned for custom tool-heavy workflows"),
				model("gemini-2.5-pro", "Gemini 2.5 Pro", "Advanced reasoning and coding"),
				model("gemini-2.5-flash", "Gemini 2.5 Flash", "Low-latency reasoning"),
				model("gemini-2.5-flash-lite", "Gemini 2.5 Flash-Lite", "Fastest budget Gemini option"),
			},
		},
		{
			"id": "codex", "label": "OpenAI (OAuth)", "auth_type": "user_oauth",
			"credential_key": "",
			"models": []map[string]any{
				model("default", "OpenAI recommended", "Uses the current model recommended by Codex"),
				model("gpt-5.6-sol", "GPT-5.6 Sol", "Highest-quality GPT-5.6 model for complex work"),
				model("gpt-5.6-terra", "GPT-5.6 Terra", "Pragmatic GPT-5.6 model for everyday work"),
				model("gpt-5.6-luna", "GPT-5.6 Luna", "Fast GPT-5.6 model for clear, repeatable work"),
			},
		},
	}
}

// aiProviderIDs reports whether id is a catalog provider.
func aiProviderKnown(id string) bool {
	switch id {
	case "anthropic", "openai", "gemini", "codex":
		return true
	}
	return false
}

// aiDefaultModel is the first catalog model of a provider.
func aiDefaultModel(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-opus-4-8"
	case "openai":
		return "gpt-5.5"
	case "gemini":
		return "gemini-3.5-flash"
	case "codex":
		return "default"
	}
	return ""
}

// ─── Locked helpers ─────────────────────────────────────

// aiLockedPersonal lazily creates and returns the user's personal state.
// Callers must hold aiMu.
func aiLockedPersonal(uid int) *aiPersonalState {
	p := aiPersonalStates[uid]
	if p == nil {
		p = &aiPersonalState{Keys: map[string]bool{}}
		aiPersonalStates[uid] = p
	}
	return p
}

// aiLockedSharedUsable reports whether the shared profile can serve a turn
// right now (key present / shared OAuth linked). Callers must hold aiMu.
func aiLockedSharedUsable() bool {
	if aiSharedProvider == "codex" {
		return aiSharedCodexLinked
	}
	key, ok := aiKeyProviders[aiSharedProvider]
	if !ok {
		return false
	}
	return aiSharedCreds[key]
}

// aiResolution is the effective AI resolution for one user.
type aiResolution struct {
	Available bool
	Source    string // "personal" | "shared" | "none"
	Provider  string
	Model     string
	Reason    string
}

// aiResolveFor resolves the user's effective AI source: a personal selection
// is an absolute override (never silently falls back to shared); otherwise
// the shared grant decides.
func aiResolveFor(u *DemoUser) aiResolution {
	aiMu.Lock()
	defer aiMu.Unlock()
	return aiLockedResolveFor(u)
}

func aiLockedResolveFor(u *DemoUser) aiResolution {
	p := aiLockedPersonal(u.ID)
	if p.Selected {
		res := aiResolution{Source: "personal", Provider: p.Provider, Model: p.Model}
		if p.Provider == "codex" {
			if p.CodexLinked {
				res.Available = true
			} else {
				res.Reason = "personal_codex_disconnected"
			}
			return res
		}
		if p.Keys[p.Provider] {
			res.Available = true
		} else {
			res.Reason = "personal_credential_missing"
		}
		return res
	}
	if !u.AISharedEnabled {
		return aiResolution{Source: "none", Reason: "shared_access_disabled"}
	}
	res := aiResolution{Source: "shared", Provider: aiSharedProvider, Model: aiSharedModel}
	if aiSharedProvider == "codex" {
		if aiSharedCodexLinked {
			res.Available = true
		} else {
			res.Reason = "shared_codex_disconnected"
		}
		return res
	}
	if aiLockedSharedUsable() {
		res.Available = true
	} else {
		res.Reason = "shared_credential_missing"
	}
	return res
}

// aiPurgeConversations drops a user's stored conversations (codex
// link/unlink semantics). uid 0 purges everyone (shared account replaced).
func aiPurgeConversations(uid int) {
	aiMu.Lock()
	defer aiMu.Unlock()
	if uid == 0 {
		aiConvs = map[int]map[string]bool{}
		return
	}
	delete(aiConvs, uid)
}

// aiNoStore marks a response uncacheable (all AI settings/codex/credentials
// responses set it).
func aiNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

// ─── Settings document ──────────────────────────────────

// aiSettingsDocJSON builds the full GET /api/ai/settings document for a user
// (also the PUT response body).
func aiSettingsDocJSON(u *DemoUser) map[string]any {
	aiMu.Lock()
	defer aiMu.Unlock()
	p := aiLockedPersonal(u.ID)
	res := aiLockedResolveFor(u)

	var personalConfig any // JSON null when nothing is selected
	if p.Selected {
		personalConfig = map[string]any{"provider": p.Provider, "model": p.Model}
	}
	return map[string]any{
		"providers": aiProviderCatalog(),
		// The zero-config pair the UI preselects when nothing is chosen yet.
		"default_provider": "codex",
		"default_model":    "gpt-5.6-luna",
		"personal": map[string]any{
			"selected": p.Selected,
			"config":   personalConfig,
			"credentials": map[string]bool{
				"anthropic": p.Keys["anthropic"],
				"openai":    p.Keys["openai"],
				"gemini":    p.Keys["gemini"],
				"codex":     p.CodexLinked,
			},
			"reason": "",
		},
		"shared": map[string]any{
			"granted":    u.AISharedEnabled,
			"configured": aiLockedSharedUsable(),
			"config":     map[string]any{"provider": aiSharedProvider, "model": aiSharedModel},
			"reason":     "",
		},
		"effective": map[string]any{
			"available": res.Available,
			"source":    res.Source,
			"provider":  res.Provider,
			"model":     res.Model,
			"reason":    res.Reason,
		},
	}
}

// ─── SSE stream helpers ─────────────────────────────────

// aiStream is the per-turn SSE emitter handed to the canned scripts in
// data_ai.go. Every frame is "data: <json>\n\n" (bare LF — CRLF breaks the
// app's parser) and is flushed immediately. ctx is the request context: a
// disconnected client stops the word-by-word pacing early so the per-user
// admission slot frees promptly.
type aiStream struct {
	w    http.ResponseWriter
	f    http.Flusher
	ctx  context.Context
	user *DemoUser
}

// closed reports whether the client has gone away.
func (s *aiStream) closed() bool {
	select {
	case <-s.ctx.Done():
		return true
	default:
		return false
	}
}

func (s *aiStream) frame(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(s.w, "data: %s\n\n", b)
	s.f.Flush()
}

// text streams a passage word by word (~35 ms per word), preserving single
// spaces, exactly like the real streamed provider deltas.
func (s *aiStream) text(passage string) {
	words := strings.Fields(passage)
	for i, wd := range words {
		if s.closed() {
			return
		}
		chunk := wd
		if i < len(words)-1 {
			chunk += " "
		}
		s.frame(map[string]string{"text": chunk})
		time.Sleep(35 * time.Millisecond)
	}
}

func (s *aiStream) toolStart(name, label string) {
	s.frame(map[string]any{"tool_start": map[string]any{"name": name, "label": label}})
}

func (s *aiStream) toolEnd(name string, ok bool) {
	s.frame(map[string]any{"tool_end": map[string]any{"name": name, "ok": ok}})
}

func (s *aiStream) media(items []map[string]any) {
	if items == nil {
		items = []map[string]any{}
	}
	s.frame(map[string]any{"media_results": items})
}

func (s *aiStream) configurationChange(change map[string]any) {
	s.frame(map[string]any{"configuration_change": change})
}

// pause sleeps, emitting a ": keepalive" comment frame if the silence would
// exceed ten seconds (clients ignore comment lines).
func (s *aiStream) pause(d time.Duration) {
	for d > 0 {
		if s.closed() {
			return
		}
		step := d
		if step > 9*time.Second {
			step = 9 * time.Second
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(step):
		}
		d -= step
		if d > 0 {
			fmt.Fprint(s.w, ": keepalive\n\n")
			s.f.Flush()
		}
	}
}

func (s *aiStream) done() {
	fmt.Fprint(s.w, "data: [DONE]\n\n")
	s.f.Flush()
}

// ─── Chat request decoding ──────────────────────────────

type aiChatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type aiChatRequest struct {
	Messages       []aiChatMessage `json:"messages"`
	ConversationID string          `json:"conversation_id"`
}

// aiMessageText extracts plain text from a message content value: a JSON
// string, or an array of {"type":"text","text":…} blocks.
func aiMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// aiLastUserText returns the latest non-empty user message text (leading
// assistant welcome bubbles are dropped, matching the real handler).
func aiLastUserText(msgs []aiChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			continue
		}
		if t := strings.TrimSpace(aiMessageText(msgs[i].Content)); t != "" {
			return t
		}
	}
	return ""
}

// ─── Chat admission ─────────────────────────────────────

// aiAdmitTurn applies the admission rules (1/user → 429, 16 global and
// 4 shared → 503, all non-blocking). It returns a release func on success,
// or a non-zero status.
func aiAdmitTurn(uid int, shared bool) (release func(), status int) {
	aiMu.Lock()
	defer aiMu.Unlock()
	if aiActiveTurns[uid] {
		return nil, http.StatusTooManyRequests
	}
	if aiActiveTotal >= 16 {
		return nil, http.StatusServiceUnavailable
	}
	if shared && aiActiveShared >= 4 {
		return nil, http.StatusServiceUnavailable
	}
	aiActiveTurns[uid] = true
	aiActiveTotal++
	if shared {
		aiActiveShared++
	}
	return func() {
		aiMu.Lock()
		defer aiMu.Unlock()
		delete(aiActiveTurns, uid)
		aiActiveTotal--
		if shared {
			aiActiveShared--
		}
	}, 0
}

// aiConversationID echoes a known conversation id for the user or mints a
// fresh 32-hex id, recording it.
func aiConversationID(uid int, requested string) string {
	aiMu.Lock()
	defer aiMu.Unlock()
	convs := aiConvs[uid]
	if convs == nil {
		convs = map[string]bool{}
		aiConvs[uid] = convs
	}
	if requested != "" && convs[requested] {
		return requested
	}
	if len(convs) >= 200 { // stand-in for the real store's conversation cap
		aiConvs[uid] = map[string]bool{}
		convs = aiConvs[uid]
	}
	id := randomHex(16) // 32 hex chars
	convs[id] = true
	return id
}

// ─── Routes ─────────────────────────────────────────────

// registerAI mounts the user-facing AI surfaces on the authenticated /api
// router: the chat SSE endpoint, availability, personal settings and
// credentials, and the personal Codex device flow.
func registerAI(r chi.Router) {
	r.Post("/ai/chat", aiHandleChat)
	r.Get("/ai/available", aiHandleAvailable)

	r.Get("/ai/settings", aiHandleGetSettings)
	r.Put("/ai/settings", aiHandlePutSettings)
	r.Delete("/ai/settings", aiHandleDeleteSettings)

	r.Put("/ai/credentials/{provider}", aiHandlePutCredential)
	r.Delete("/ai/credentials/{provider}", aiHandleDeleteCredential)

	r.Get("/ai/codex/status", aiHandleCodexStatus)
	r.Post("/ai/codex/device/begin", aiHandleCodexBegin)
	r.Get("/ai/codex/device/{flowID}", aiHandleCodexPoll)
	r.Delete("/ai/codex/device/{flowID}", aiHandleCodexCancel)
	r.Delete("/ai/codex", aiHandleCodexUnlink)
}

// ─── POST /api/ai/chat ──────────────────────────────────

func aiHandleChat(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)

	res := aiResolveFor(u)
	if !res.Available {
		switch res.Source {
		case "none":
			writeErr(w, http.StatusServiceUnavailable, "AI access is not available. Add a personal provider in Settings or ask an admin to include shared access.")
		case "personal":
			writeErr(w, http.StatusServiceUnavailable, "Your personal AI provider needs attention in Settings. Cantinarr will not silently use the shared provider instead.")
		default:
			writeErr(w, http.StatusServiceUnavailable, "Included AI is temporarily unavailable. Ask an admin to check the shared provider.")
		}
		return
	}

	var req aiChatRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Messages) == 0 {
		writeErr(w, http.StatusBadRequest, "messages required")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	question := aiLastUserText(req.Messages)
	if question == "" {
		writeErr(w, http.StatusBadRequest, "no usable messages in request")
		return
	}

	release, status := aiAdmitTurn(u.ID, res.Source == "shared")
	if status != 0 {
		w.Header().Set("Retry-After", "2")
		writeErr(w, status, "AI is busy; try again shortly")
		return
	}
	defer release()

	convID := aiConversationID(u.ID, req.ConversationID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	s := &aiStream{w: w, f: flusher, ctx: r.Context(), user: u}
	// The conversation_id frame is ALWAYS first; [DONE] is always last.
	s.frame(map[string]string{"conversation_id": convID})
	aiRunCannedTurn(s, question)
	s.done()
}

// ─── GET /api/ai/available ──────────────────────────────

func aiHandleAvailable(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	res := aiResolveFor(u)
	aiNoStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"available": res.Available,
		"provider":  res.Provider,
		"model":     res.Model,
		"source":    res.Source,
		"reason":    res.Reason,
	})
}

// ─── GET|PUT|DELETE /api/ai/settings ────────────────────

func aiHandleGetSettings(w http.ResponseWriter, r *http.Request) {
	aiNoStore(w)
	writeJSON(w, http.StatusOK, aiSettingsDocJSON(userFrom(r)))
}

func aiHandlePutSettings(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var body struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		APIKey   string `json:"api_key"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !aiProviderKnown(body.Provider) || len(body.Model) > 256 {
		writeErr(w, http.StatusBadRequest, "invalid AI provider or model")
		return
	}
	if body.Provider == "codex" && strings.TrimSpace(body.APIKey) != "" {
		writeErr(w, http.StatusBadRequest, "OAuth providers do not accept API keys")
		return
	}
	model := body.Model
	if model == "" {
		model = aiDefaultModel(body.Provider)
	}
	apiKey := strings.TrimSpace(body.APIKey)

	// Simulated validation turn (the real server runs a live provider probe).
	time.Sleep(aiValidationDelay)

	aiMu.Lock()
	p := aiLockedPersonal(u.ID)
	if body.Provider == "codex" {
		if !p.CodexLinked {
			aiMu.Unlock()
			aiNoStore(w)
			writeErr(w, http.StatusUnprocessableEntity, aiValidationRejected)
			return
		}
	} else if apiKey == "" && !p.Keys[body.Provider] {
		// No key supplied and none stored — the validation turn fails and
		// nothing is saved.
		aiMu.Unlock()
		aiNoStore(w)
		writeErr(w, http.StatusUnprocessableEntity, aiValidationRejected)
		return
	}
	if apiKey != "" {
		p.Keys[body.Provider] = true
	}
	p.Selected = true
	p.Provider = body.Provider
	p.Model = model
	aiMu.Unlock()

	aiNoStore(w)
	writeJSON(w, http.StatusOK, aiSettingsDocJSON(u))
}

func aiHandleDeleteSettings(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	aiMu.Lock()
	p := aiLockedPersonal(u.ID)
	p.Selected = false
	p.Provider = ""
	p.Model = ""
	aiMu.Unlock()
	aiNoStore(w)
	w.WriteHeader(http.StatusNoContent)
}

// ─── PUT|DELETE /api/ai/credentials/{provider} ──────────

func aiHandlePutCredential(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	provider := chi.URLParam(r, "provider")
	if _, ok := aiKeyProviders[provider]; !ok {
		writeErr(w, http.StatusBadRequest, "provider does not accept an API key")
		return
	}
	var body struct {
		APIKey string `json:"api_key"`
		Model  string `json:"model"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(body.APIKey) == "" {
		writeErr(w, http.StatusBadRequest, "api_key is required")
		return
	}

	// Simulated validation turn; on success the encrypted key is "stored"
	// (presence only). The selected provider is not changed.
	time.Sleep(aiValidationDelay)

	aiMu.Lock()
	aiLockedPersonal(u.ID).Keys[provider] = true
	aiMu.Unlock()
	aiNoStore(w)
	w.WriteHeader(http.StatusNoContent)
}

func aiHandleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	provider := chi.URLParam(r, "provider")
	if _, ok := aiKeyProviders[provider]; !ok {
		writeErr(w, http.StatusBadRequest, "provider does not accept an API key")
		return
	}
	aiMu.Lock()
	aiLockedPersonal(u.ID).Keys[provider] = false
	aiMu.Unlock()
	aiNoStore(w)
	w.WriteHeader(http.StatusNoContent)
}

// ─── Personal Codex device flow ─────────────────────────

// aiCodexRateLimitsJSON builds a plausible connected-account usage block
// (resets_at is unix seconds; used everywhere a connected status renders).
func aiCodexRateLimitsJSON() map[string]any {
	now := time.Now()
	return map[string]any{
		"primary": map[string]any{
			"used_percent":         12.5,
			"resets_at":            now.Add(5 * time.Hour).Unix(),
			"window_duration_mins": int64(300),
		},
		"secondary": map[string]any{
			"used_percent":         3.0,
			"resets_at":            now.Add(6 * 24 * time.Hour).Unix(),
			"window_duration_mins": int64(10080),
		},
	}
}

func aiHandleCodexStatus(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	aiMu.Lock()
	p := aiLockedPersonal(u.ID)
	res := aiLockedResolveFor(u)
	personalSelected := p.Selected && p.Provider == "codex"
	// Compat quirk: with no personal selection, a shared Codex profile that
	// is not currently usable for this user forces selected true so old
	// clients surface the personal Connect screen.
	sharedCodexUnusable := aiSharedProvider == "codex" && !(u.AISharedEnabled && aiSharedCodexLinked)
	selected := personalSelected || (!p.Selected && sharedCodexUnusable)
	out := map[string]any{
		"available":         true,
		"selected":          selected,
		"personal_selected": personalSelected,
		"connected":         p.CodexLinked,
		"effective":         res.Available && res.Source == "personal" && res.Provider == "codex",
	}
	if p.CodexLinked {
		out["account_email"] = p.CodexEmail
		out["plan_type"] = p.CodexPlan
		out["stale"] = false
		if !p.CodexLinkedAt.IsZero() {
			out["updated_at"] = p.CodexLinkedAt.UTC().Format(time.RFC3339)
		}
		out["rate_limits"] = aiCodexRateLimitsJSON()
	}
	aiMu.Unlock()
	aiNoStore(w)
	writeJSON(w, http.StatusOK, out)
}

// aiNewUserCode fabricates a device-login user code ("A3F2-9C0D" style).
func aiNewUserCode() string {
	return strings.ToUpper(randomHex(2)) + "-" + strings.ToUpper(randomHex(2))
}

func aiHandleCodexBegin(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	aiMu.Lock()
	if aiLockedPersonal(u.ID).CodexLinked {
		aiMu.Unlock()
		aiNoStore(w)
		writeErr(w, http.StatusConflict, "Disconnect the current OpenAI OAuth account before linking another one")
		return
	}
	flowID := randomHex(16)
	aiCodexFlows[flowID] = &aiCodexFlow{UserID: u.ID, Created: time.Now()}
	aiMu.Unlock()
	aiNoStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"flow_id":          flowID,
		"verification_uri": aiCodexVerificationURI,
		"user_code":        aiNewUserCode(),
		"expires_in":       900,
		"interval":         2,
	})
}

func aiHandleCodexPoll(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	flowID := chi.URLParam(r, "flowID")
	aiMu.Lock()
	flow := aiCodexFlows[flowID]
	if flow == nil || flow.Shared || flow.UserID != u.ID {
		aiMu.Unlock()
		aiNoStore(w)
		writeErr(w, http.StatusNotFound, "ChatGPT sign-in flow not found")
		return
	}
	if time.Since(flow.Created) > aiCodexFlowTTL {
		delete(aiCodexFlows, flowID)
		aiMu.Unlock()
		aiNoStore(w)
		writeJSON(w, http.StatusOK, map[string]any{"status": "expired"})
		return
	}
	flow.Polls++
	if flow.Polls < 2 {
		aiMu.Unlock()
		aiNoStore(w)
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
		return
	}
	// Connected: link the account, purge the user's conversations, and
	// auto-select a personal codex profile — status, settings, and config
	// must all flip together (the app refetches them in the same breath).
	delete(aiCodexFlows, flowID)
	p := aiLockedPersonal(u.ID)
	p.CodexLinked = true
	p.CodexEmail = u.Username + "@example.com"
	p.CodexPlan = "plus"
	p.CodexLinkedAt = time.Now()
	if !(p.Selected && p.Provider == "codex") {
		p.Selected = true
		p.Provider = "codex"
		p.Model = "default"
	}
	delete(aiConvs, u.ID)
	email, plan := p.CodexEmail, p.CodexPlan
	aiMu.Unlock()
	aiNoStore(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "connected",
		"account": map[string]any{"email": email, "plan_type": plan},
	})
}

func aiHandleCodexCancel(w http.ResponseWriter, r *http.Request) {
	flowID := chi.URLParam(r, "flowID")
	aiMu.Lock()
	if flow := aiCodexFlows[flowID]; flow != nil && !flow.Shared {
		delete(aiCodexFlows, flowID)
	}
	aiMu.Unlock()
	aiNoStore(w)
	w.WriteHeader(http.StatusNoContent)
}

func aiHandleCodexUnlink(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	aiMu.Lock()
	p := aiLockedPersonal(u.ID)
	p.CodexLinked = false
	p.CodexEmail = ""
	p.CodexPlan = ""
	p.CodexLinkedAt = time.Time{}
	// Selection is retained: a selected codex profile now fails closed with
	// reason personal_codex_disconnected (no silent fallback to shared).
	delete(aiConvs, u.ID)
	aiMu.Unlock()
	aiNoStore(w)
	w.WriteHeader(http.StatusNoContent)
}
