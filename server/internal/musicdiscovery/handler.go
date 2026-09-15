package musicdiscovery

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

type Handler struct {
	store   *instance.Store
	service Catalog
}

func NewHandler(store *instance.Store) *Handler {
	return &Handler{store: store, service: NewService()}
}

// authorizeMetadata is shared by every music endpoint, including genres and covers.
// Lidarr is grant-only for every requester, including kids accounts. Admins
// may browse without an instance or select any configured Lidarr; there is no requester global fallback.
func (h *Handler) authorizeMetadata(w http.ResponseWriter, r *http.Request, id string) (string, bool) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		fail(w, 401, "unauthorized")
		return "", false
	}
	if !auth.HasPermission(claims.Role, auth.PermissionMediaDiscover) {
		fail(w, 403, "discovery is not available to you")
		return "", false
	}
	if claims.Role == auth.RoleAdmin && id == "" {
		return "", true
	}
	if h.store == nil {
		fail(w, 403, "music is not available to you")
		return "", false
	}
	admin := claims.Role == auth.RoleAdmin
	if id == "" {
		var err error
		id, err = h.store.EffectiveDefaultInstanceID(claims.UserID, "lidarr")
		if err != nil {
			fail(w, 503, "could not check music access")
			return "", false
		}
	}
	if id == "" {
		fail(w, 403, "music is not available to you")
		return "", false
	}
	if !admin {
		allowed, err := h.store.UserCanAccessInstance(claims.UserID, id, "lidarr")
		if err != nil {
			fail(w, 503, "could not check music access")
			return "", false
		}
		if !allowed {
			fail(w, 403, "music is not available to you")
			return "", false
		}
	}
	inst, err := h.store.Get(id)
	if err != nil || inst == nil || inst.ServiceType != "lidarr" {
		fail(w, 403, "music is not available to you")
		return "", false
	}
	return id, true
}

func fail(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, id string, body []byte, err error) {
	// Check the grant again after waiting on a provider or shared cache fill.
	if _, ok := h.authorizeMetadata(w, r, id); !ok {
		return
	}
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(body)
}

func (h *Handler) Feed(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizeMetadata(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	feed := chi.URLParam(r, "feed")
	period, genre := r.URL.Query().Get("period"), r.URL.Query().Get("genre")
	if period == "" {
		period = "this_week"
	}
	if period != "this_week" && period != "this_month" && period != "this_year" {
		fail(w, 400, "invalid music period")
		return
	}
	switch feed {
	case "popular", "new-releases":
		if genre != "" {
			fail(w, 400, "genre is only supported by the genre feed")
			return
		}
	case "genre":
		if _, ok := genreByID(genre); !ok {
			fail(w, 400, "choose a supported music genre")
			return
		}
	default:
		fail(w, 404, "music feed not found")
		return
	}
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		var err error
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 || page > maxPage {
			fail(w, 400, "invalid music page")
			return
		}
	}
	body, err := h.service.Feed(r.Context(), feed, period, genre, page)
	h.serve(w, r, id, body, err)
}

func (h *Handler) Genres(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizeMetadata(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	body, err := json.Marshal(map[string]any{"genres": Genres})
	h.serve(w, r, id, body, err)
}

func (h *Handler) Album(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizeMetadata(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	mbid := strings.ToLower(chi.URLParam(r, "mbid"))
	if !validID(mbid) {
		fail(w, 400, "invalid MusicBrainz release-group ID")
		return
	}
	body, err := h.service.Album(r.Context(), mbid)
	h.serve(w, r, id, body, err)
}

func (h *Handler) Artwork(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizeMetadata(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	mbid := strings.ToLower(chi.URLParam(r, "mbid"))
	if !validID(mbid) {
		fail(w, 400, "invalid MusicBrainz release-group ID")
		return
	}
	body, err := h.service.Artwork(r.Context(), mbid)
	if _, ok := h.authorizeMetadata(w, r, id); !ok {
		return
	}
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	if len(body) == 0 {
		fail(w, 404, "album artwork is not available")
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(body))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(body)
}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizeMetadata(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		var err error
		page, err = strconv.Atoi(raw)
		if err != nil {
			fail(w, 400, "invalid search page")
			return
		}
	}
	if query == "" || len(query) > 300 || page < 1 || page > maxPage {
		fail(w, 400, "enter a search query and valid page")
		return
	}
	singles := false
	if raw := r.URL.Query().Get("include_singles"); raw != "" {
		var err error
		singles, err = strconv.ParseBool(raw)
		if err != nil {
			fail(w, 400, "invalid include_singles")
			return
		}
	}
	body, err := h.service.Search(r.Context(), query, page, singles)
	h.serve(w, r, id, body, err)
}

func NewHandlerWithService(store *instance.Store, service Catalog) *Handler {
	if service == nil {
		service = NewService()
	}
	return &Handler{store: store, service: service}
}

func (h *Handler) Artists(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizeMetadata(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	page, err := musicPage(r)
	if err != nil {
		fail(w, 400, "invalid music page")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" || len(query) > 300 {
		fail(w, 400, "enter an artist search query")
		return
	}
	body, err := h.service.SearchArtists(r.Context(), query, page)
	h.serve(w, r, id, body, err)
}
func musicPage(r *http.Request) (int, error) {
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err := strconv.Atoi(raw)
		if err != nil || page < 1 || page > maxPage {
			return 0, fmt.Errorf("invalid page")
		}
		return page, nil
	}
	return 1, nil
}
func (h *Handler) Artist(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizeMetadata(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	mbid := strings.ToLower(chi.URLParam(r, "mbid"))
	if !validID(mbid) {
		fail(w, 400, "invalid MusicBrainz artist ID")
		return
	}
	body, err := h.service.Artist(r.Context(), mbid)
	h.serve(w, r, id, body, err)
}
func (h *Handler) ArtistAlbums(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorizeMetadata(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	mbid := strings.ToLower(chi.URLParam(r, "mbid"))
	if !validID(mbid) {
		fail(w, 400, "invalid MusicBrainz artist ID")
		return
	}
	page, err := musicPage(r)
	if err != nil {
		fail(w, 400, "invalid music page")
		return
	}
	body, err := h.service.ArtistAlbums(r.Context(), mbid, page)
	h.serve(w, r, id, body, err)
}
