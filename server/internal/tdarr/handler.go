package tdarr

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"

	"github.com/go-chi/chi/v5"
)

type InstanceSource interface {
	LookupServiceType(string) (string, bool, error)
}

type ClientSource interface {
	GetTdarrClient(string) (*Client, error)
}

type Handler struct {
	store   InstanceSource
	clients ClientSource
}

func NewHandler(store InstanceSource, clients ClientSource) *Handler { return &Handler{store, clients} }

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (h *Handler) client(w http.ResponseWriter, r *http.Request) *Client {
	id := chi.URLParam(r, "instanceID")
	typ, found, err := h.store.LookupServiceType(id)
	if err != nil {
		writeError(w, 503, "instance information is temporarily unavailable")
		return nil
	}
	if !found {
		writeError(w, 404, "instance not found")
		return nil
	}
	if typ != "tdarr" {
		writeError(w, 400, "instance is not a Tdarr instance")
		return nil
	}
	c, err := h.clients.GetTdarrClient(id)
	if err != nil {
		writeError(w, 503, "Tdarr connection is temporarily unavailable")
		return nil
	}
	return c
}

func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	c := h.client(w, r)
	if c == nil {
		return
	}
	var result any
	var err error
	switch path.Base(r.URL.Path) {
	case "activity":
		result, err = c.Activity(r.Context())
	case "libraries":
		result, err = c.Libraries(r.Context())
	case "stats":
		result, err = c.Stats(r.Context(), r.URL.Query().Get("library_id"))
	default:
		writeError(w, 404, "Tdarr view not found")
		return
	}
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ErrLibraryNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
