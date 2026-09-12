package mediaaccess

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// ListeningBooks is supplied by the request service, which owns Chaptarr
// authorization and canonical book identity. Revision guards repoints and
// revocations across both the source resolution and destination lookup.
type ListeningBooks interface {
	ResolveListeningBook(context.Context, int64, string, string) (mediaserver.BookQuery, error)
	ListeningLibraryRevision(int64, string) (instance.ArrSettingsFingerprint, error)
}

func (h *Handler) SetListeningBooks(source ListeningBooks) { h.listeningBooks = source }

type ListenItem struct {
	mediaserver.BookItem
	URL string `json:"url"`
}

type ListenLink struct {
	InstanceID    string                 `json:"instance_id"`
	Name          string                 `json:"name"`
	ServiceType   string                 `json:"service_type"`
	State         string                 `json:"state"`
	Items         []ListenItem           `json:"items"`
	FallbackURL   string                 `json:"fallback_url,omitempty"`
	ListeningApps instance.ListeningApps `json:"listening_apps"`
}

// Listen answers a per-title, no-store read. [] means no eligible granted
// Audiobookshelf server or no available audiobook in this Chaptarr library.
// An unmatched identifier search is always unverified, never title absence.
func (h *Handler) Listen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	instanceID, foreignID := strings.TrimSpace(r.URL.Query().Get("instance_id")), strings.TrimSpace(r.URL.Query().Get("foreign_book_id"))
	if instanceID == "" || foreignID == "" || len(instanceID) > 256 || len(foreignID) > 256 {
		writeJSON(w, 400, map[string]string{"error": "instance_id and foreign_book_id are required"})
		return
	}
	fail := func(err error) {
		if errors.Is(err, mediaserver.ErrBookAccess) {
			writeJSON(w, 403, map[string]string{"error": "book library is not available"})
			return
		}
		writeJSON(w, 503, map[string]string{"error": "could not check audiobook access, retry shortly"})
	}
	if h.listeningBooks == nil {
		fail(ErrUpstream)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	before, err := h.listeningBooks.ListeningLibraryRevision(claims.UserID, instanceID)
	if err != nil {
		fail(err)
		return
	}
	query, err := h.listeningBooks.ResolveListeningBook(ctx, claims.UserID, instanceID, foreignID)
	if err != nil {
		fail(err)
		return
	}
	links, err := h.svc.ListenLinks(ctx, claims.UserID, query)
	if err != nil {
		fail(err)
		return
	}
	after, err := h.listeningBooks.ListeningLibraryRevision(claims.UserID, instanceID)
	if err != nil {
		fail(err)
		return
	}
	if before != after || ctx.Err() != nil {
		fail(ErrUpstream)
		return
	}
	writeJSON(w, 200, links)
}

func (s *Service) ListenLinks(ctx context.Context, userID int64, q mediaserver.BookQuery) ([]ListenLink, error) {
	out := []ListenLink{}
	if !q.Available {
		return out, nil
	}
	ids, err := s.grantedMediaServers(userID)
	if err != nil {
		return nil, err
	}
	type target struct {
		inst     *instance.Instance
		row      *accountRow
		provider mediaserver.Provider
	}
	targets := []target{}
	for _, id := range ids {
		inst, err := s.store.Get(id)
		if err != nil {
			return nil, err
		}
		if inst == nil || inst.ServiceType != "audiobookshelf" || inst.MediaServerConfigInvalid || inst.MediaServerConfig.PublicAddress == "" {
			continue
		}
		provider, err := s.providers(inst)
		if err != nil {
			return nil, err
		}
		row, err := s.getAccount(userID, id)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target{inst, row, provider})
	}
	results := make([]ListenLink, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			link := ListenLink{InstanceID: t.inst.ID, Name: t.inst.Name, ServiceType: t.inst.ServiceType, State: WatchUnverified, Items: []ListenItem{}, FallbackURL: t.inst.MediaServerConfig.PublicAddress}
			defer func() { results[i] = link }()
			finder, ok := t.provider.(mediaserver.BookFinder)
			if !ok || t.row == nil {
				return
			}
			lookup, cancel := context.WithTimeout(ctx, watchTimeout)
			defer cancel()
			items, err := finder.FindBooks(lookup, t.row.RemoteUserID, q)
			if err != nil {
				if !errors.Is(err, mediaserver.ErrItemUnverified) && !errors.Is(err, mediaserver.ErrUserNotFound) {
					link.State = WatchUnreachable
				}
				return
			}
			if len(items) == 0 {
				return
			}
			link.State, link.FallbackURL = WatchFound, ""
			for _, item := range items {
				link.Items = append(link.Items, ListenItem{BookItem: item, URL: strings.TrimRight(t.inst.MediaServerConfig.PublicAddress, "/") + "/item/" + url.PathEscape(item.ID)})
			}
		}(i, t)
	}
	wg.Wait()
	preferences, err := s.listeningAppPreferences(userID)
	if err != nil {
		return nil, err
	}
	// Recheck all local eligibility after the slow calls. An account unlink,
	// revoked grant, API-key rotation or repointed server invalidates its result.
	granted, err := s.grantedMediaServers(userID)
	if err != nil {
		return nil, err
	}
	for i, t := range targets {
		if !contains(granted, t.inst.ID) {
			continue
		}
		current, err := s.lookupTargetCurrent(userID, t.inst, t.row)
		if err != nil {
			return nil, err
		}
		if !current {
			continue
		}
		results[i].ListeningApps = preferences.WithDefaults(t.inst.MediaServerConfig.ListeningApps)
		out = append(out, results[i])
	}
	return out, nil
}
