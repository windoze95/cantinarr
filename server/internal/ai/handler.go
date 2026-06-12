package ai

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

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
	apiKey := h.creds.GetCredential(credentials.KeyAnthropicKey)
	if apiKey == "" {
		http.Error(w, `{"error":"AI is not configured"}`, http.StatusServiceUnavailable)
		return
	}
	service := NewService(apiKey, h.toolServer)

	claims := auth.GetClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

	emit := func(payload any) {
		data, _ := json.Marshal(payload)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

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
			history = stored
			if text := latestUserText(req.Messages); text != "" {
				history = append(history, anthropic.NewUserMessage(anthropic.NewTextBlock(text)))
			}
		}
	}
	if history == nil {
		if convID == "" {
			convID = newConversationID()
		}
		history = toSDKMessages(req.Messages)
	}

	emit(map[string]string{"conversation_id": convID})

	finalHistory, err := service.SendMessage(r.Context(), history, chatCtx, callbacks)
	h.conversations.Put(convID, claims.UserID, finalHistory)
	if err != nil {
		log.Printf("ai chat error: %v", err)
		errData, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
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
	json.NewEncoder(w).Encode(map[string]bool{
		"available": h.creds.IsConfigured(credentials.KeyAnthropicKey),
	})
}
