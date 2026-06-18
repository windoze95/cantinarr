package ai

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// Handler provides HTTP handlers for AI chat endpoints.
type Handler struct {
	creds         *credentials.Registry
	toolServer    *mcp.ToolServer
	conversations *conversationStore
}

// NewHandler creates a new AI handler.
func NewHandler(creds *credentials.Registry, toolServer *mcp.ToolServer) *Handler {
	return &Handler{creds: creds, toolServer: toolServer, conversations: newConversationStore()}
}

type chatRequest struct {
	Messages []Message `json:"messages"`
	// ConversationID resumes a server-stored transcript that retains tool
	// context across turns. Empty for new conversations or legacy clients.
	ConversationID string `json:"conversation_id,omitempty"`
}

// Chat handles POST /api/ai/chat with SSE streaming.
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	aiConfig := h.creds.GetAIConfig()
	apiKey := h.creds.GetCredential(credentials.AIKeyCredentialKey(aiConfig.Provider))
	if apiKey == "" {
		http.Error(w, `{"error":"AI is not configured"}`, http.StatusServiceUnavailable)
		return
	}

	claims := auth.GetClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if len(req.Messages) == 0 {
		http.Error(w, `{"error":"messages required"}`, http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Tool calls and thinking can leave the SSE stream silent long enough for
	// client inactivity timeouts to fire; comment-frame keepalives prevent
	// that. writeMu serializes them with real frames.
	var writeMu sync.Mutex
	emit := func(payload any) {
		data, _ := json.Marshal(payload)
		writeMu.Lock()
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		writeMu.Unlock()
	}

	keepaliveDone := make(chan struct{})
	defer close(keepaliveDone)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-keepaliveDone:
				return
			case <-ticker.C:
				writeMu.Lock()
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
				writeMu.Unlock()
			}
		}
	}()

	callbacks := StreamCallbacks{
		OnText: func(text string) {
			emit(map[string]string{"text": text})
		},
		OnToolStart: func(name, label string) {
			emit(map[string]any{"tool_start": map[string]string{"name": name, "label": label}})
		},
		OnToolEnd: func(name string, ok bool) {
			emit(map[string]any{"tool_end": map[string]any{"name": name, "ok": ok}})
		},
		OnToolResult: func(toolName string, structuredData any) {
			emit(map[string]any{"media_results": structuredData})
		},
	}

	chatCtx := ChatContext{
		UserID:   claims.UserID,
		Username: claims.Username,
		Role:     claims.Role,
		Services: h.configuredServices(),
	}

	// Resolve history: prefer the server-stored transcript (which keeps tool
	// context across turns) and append the new user message; fall back to
	// the client's plain-text transcript for new or expired conversations.
	convID := req.ConversationID
	var history []anthropic.MessageParam
	if convID != "" {
		if stored, ok := h.conversations.Get(convID, claims.UserID); ok {
			if text := latestUserText(req.Messages); text != "" {
				history = append(stored, anthropic.NewUserMessage(anthropic.NewTextBlock(text)))
			}
		}
	}
	if history == nil {
		// New conversation, or a client-supplied id we couldn't validate
		// (expired, unknown, or owned by another user): always mint a fresh
		// id so an attacker-supplied id can never overwrite someone else's
		// stored conversation.
		convID = newConversationID()
		history = toSDKMessages(req.Messages)
	}
	if len(history) == 0 {
		http.Error(w, `{"error":"no usable messages in request"}`, http.StatusBadRequest)
		return
	}

	emit(map[string]string{"conversation_id": convID})

	var err error
	switch aiConfig.Provider {
	case credentials.AIProviderAnthropic:
		service := NewService(apiKey, aiConfig.Model, h.toolServer)
		var finalHistory []anthropic.MessageParam
		finalHistory, err = service.SendMessage(r.Context(), history, chatCtx, callbacks)
		if err == nil {
			h.conversations.Put(convID, claims.UserID, sanitizeTranscript(finalHistory))
		}
	case credentials.AIProviderOpenAI:
		service := NewOpenAIService(apiKey, aiConfig.Model, h.toolServer)
		err = service.SendMessage(r.Context(), req.Messages, chatCtx, callbacks)
	case credentials.AIProviderGemini:
		service := NewGeminiService(apiKey, aiConfig.Model, h.toolServer)
		err = service.SendMessage(r.Context(), req.Messages, chatCtx, callbacks)
	default:
		err = fmt.Errorf("unsupported AI provider: %s", aiConfig.Provider)
	}
	if err != nil {
		// Drop Anthropic stored state rather than persist a possibly poisoned
		// transcript; the client's retry falls back to its own transcript.
		if aiConfig.Provider == credentials.AIProviderAnthropic {
			h.conversations.Delete(convID)
		}
		log.Printf("ai chat error: %v", err)
		emit(map[string]string{"error": err.Error()})
	}

	writeMu.Lock()
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
	writeMu.Unlock()
}

// configuredServices reports which backends are available, for system-prompt context.
func (h *Handler) configuredServices() []string {
	var services []string
	if h.creds.IsConfigured(credentials.KeyTMDBAccessToken) {
		services = append(services, "TMDB (discovery)")
	}
	if h.creds.IsConfigured(credentials.KeyTraktClientID) {
		services = append(services, "Trakt (trending)")
	}
	if h.toolServer.GetRadarr() != nil {
		services = append(services, "Radarr (movies)")
	}
	if h.toolServer.GetSonarr() != nil {
		services = append(services, "Sonarr (TV)")
	}
	return services
}

// Available handles GET /api/ai/available.
func (h *Handler) Available(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	cfg := h.creds.GetAIConfig()
	json.NewEncoder(w).Encode(map[string]any{
		"available": h.creds.IsConfigured(credentials.AIKeyCredentialKey(cfg.Provider)),
		"provider":  cfg.Provider,
		"model":     cfg.Model,
	})
}
