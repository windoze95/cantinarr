package ai

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

// Handler provides HTTP handlers for AI chat endpoints.
type Handler struct {
	service *Service
}

// NewHandler creates a new AI handler. service may be nil if AI is not configured.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

type chatRequest struct {
	Messages []Message `json:"messages"`
}

// Chat handles POST /api/ai/chat with SSE streaming.
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		http.Error(w, `{"error":"AI is not configured"}`, http.StatusServiceUnavailable)
		return
	}

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

	onText := func(text string) {
		data, _ := json.Marshal(map[string]string{"text": text})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	err := h.service.SendMessage(r.Context(), req.Messages, claims.UserID, onText)
	if err != nil {
		log.Printf("ai chat error: %v", err)
		errData, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// Available handles GET /api/ai/available.
func (h *Handler) Available(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{
		"available": h.service != nil,
	})
}
