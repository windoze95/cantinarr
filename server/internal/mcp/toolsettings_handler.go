package mcp

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ToolSettingsHandler exposes admin REST endpoints for per-tool AI toggles.
type ToolSettingsHandler struct {
	server *ToolServer
}

// NewToolSettingsHandler creates a handler bound to the given tool server.
func NewToolSettingsHandler(server *ToolServer) *ToolSettingsHandler {
	return &ToolSettingsHandler{server: server}
}

// List handles GET /api/admin/ai-tools.
func (h *ToolSettingsHandler) List(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"tools": h.server.ListToolStatuses(),
	})
}

// Update handles PUT /api/admin/ai-tools/{name} with body {"enabled": bool}.
func (h *ToolSettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Enabled == nil {
		http.Error(w, `{"error":"body must be {\"enabled\": bool}"}`, http.StatusBadRequest)
		return
	}

	if findToolDefinition(name) == nil {
		http.Error(w, `{"error":"unknown tool"}`, http.StatusNotFound)
		return
	}

	if err := h.server.SetToolEnabled(name, *body.Enabled); err != nil {
		http.Error(w, `{"error":"failed to update tool"}`, http.StatusInternalServerError)
		return
	}

	def := findToolDefinition(name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ToolStatus{
		Name:        def.Name,
		Description: def.Description,
		Enabled:     h.server.IsToolEnabled(name),
		AdminOnly:   def.AdminOnly,
	})
}
