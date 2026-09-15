// Package bookdiscovery preserves retired catalog entry points for older clients.
// Book discovery and requests now use the selected Chaptarr instance directly.
package bookdiscovery

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

const Code = "catalog_retired"
const Message = "Open Library book discovery has retired. Search your Chaptarr library and select the book to request."

var ErrRetired = errors.New(Message)

func Retirement() map[string]string {
	return map[string]string{"code": Code, "error": Message, "message": Message, "search_path": "/dashboard/books"}
}
func WriteRetired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusGone)
	json.NewEncoder(w).Encode(Retirement())
}

var workPattern = regexp.MustCompile(`^OL[1-9][0-9]{0,11}W$`)

func WorkID(raw string) string {
	id := strings.TrimPrefix(strings.TrimPrefix(raw, "ol:"), "/works/")
	if !workPattern.MatchString(id) {
		return ""
	}
	return id
}

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }
func (*Handler) retired(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	if !auth.HasPermission(claims.Role, auth.PermissionMediaDiscover) {
		http.Error(w, "discovery is not available to you", 403)
		return
	}
	WriteRetired(w)
}
func (h *Handler) Feed(w http.ResponseWriter, r *http.Request)          { h.retired(w, r) }
func (h *Handler) Search(w http.ResponseWriter, r *http.Request)        { h.retired(w, r) }
func (h *Handler) Book(w http.ResponseWriter, r *http.Request)          { h.retired(w, r) }
func (h *Handler) Genres(w http.ResponseWriter, r *http.Request)        { h.retired(w, r) }
func (h *Handler) RequestTarget(w http.ResponseWriter, r *http.Request) { h.retired(w, r) }
