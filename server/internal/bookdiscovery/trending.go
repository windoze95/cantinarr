package bookdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/hardcover"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

// Trending is the one live book discovery feed: Hardcover's trending list,
// fetched with the token an admin connected to the requester's Chaptarr
// instance. Everything else in this package is a retired Open Library route.
//
// The feed is instance-scoped because the credential is: an admin connects
// Hardcover per Chaptarr instance, so a requester sees the list only through
// an instance they hold a grant on, and never learns whether a sibling
// instance is connected. Books carry no content rating, so kids accounts see
// the same list as everyone else who holds the grant.

// TrendingLimit is how many books the row fetches. Hardcover's `books_trending`
// pages by offset; one call of this size returned a full list live.
const TrendingLimit = 50

// trendingTTL bounds Hardcover traffic: the list is two calls per refresh and
// Hardcover's rate-limit budget is ten per window, so the row is served from
// this cache and refreshed at most once per TTL per instance, never per view.
const trendingTTL = 30 * time.Minute

// TrendingSource is what the handler needs from Hardcover (the real client,
// or a test double).
type TrendingSource interface {
	Trending(ctx context.Context, token string, limit int) ([]hardcover.Book, error)
}

type trendingEntry struct {
	books    []hardcover.Book
	fetched  time.Time
	revision int64
}

// TrendingHandler serves GET /api/discover/books/trending.
type TrendingHandler struct {
	store  *instance.Store
	source TrendingSource
	now    func() time.Time
	ttl    time.Duration

	mu    sync.Mutex
	cache map[string]*trendingEntry // by instance id
	// inflight coalesces concurrent misses for one instance so a busy screen
	// never spends two Hardcover calls where one would do.
	inflight   map[string]chan struct{}
	generation map[string]uint64
	resolve    func(context.Context, string, string) (string, error)
}

// NewTrendingHandler wires the feed to the instance store (grants + tokens)
// and a Hardcover source.
func NewTrendingHandler(store *instance.Store, source TrendingSource) *TrendingHandler {
	return &TrendingHandler{
		store:      store,
		source:     source,
		now:        time.Now,
		ttl:        trendingTTL,
		cache:      map[string]*trendingEntry{},
		inflight:   map[string]chan struct{}{},
		generation: map[string]uint64{},
		resolve:    func(_ context.Context, id, _ string) (string, error) { return store.HardcoverToken(id) },
	}
}

// Invalidate drops the cached list for an instance, e.g. after its Hardcover
// token changes.
func (h *TrendingHandler) Invalidate(instanceID string) {
	h.mu.Lock()
	delete(h.cache, instanceID)
	h.generation[instanceID]++
	h.mu.Unlock()
}

type trendingResponse struct {
	InstanceID string `json:"instance_id"`
	// Connected is false when the instance holds no Hardcover token. The row
	// then shows an admin the way to Settings instead of an empty list.
	Connected bool             `json:"connected"`
	Source    string           `json:"source"`
	Scope     string           `json:"scope"`
	Books     []hardcover.Book `json:"books"`
}

// Trending answers the row. Authorization mirrors the other book reads: the
// caller needs media:discover and, unless an admin, a grant on the resolved
// Chaptarr instance. Admins also need an instance here -- the token lives on
// it -- so admin catalog browsing without an instance does not apply.
func (h *TrendingHandler) Trending(w http.ResponseWriter, r *http.Request) {
	id, ok := h.authorize(w, r, r.URL.Query().Get("instance_id"))
	if !ok {
		return
	}
	for {
		books, connected, revision, err := h.trending(r.Context(), id)
		if err != nil {
			if errors.Is(err, hardcover.ErrUnauthorized) {
				fail(w, http.StatusBadGateway, "Hardcover no longer accepts the connection; reconnect it in the instance settings")
			} else if errors.Is(err, hardcover.ErrInsufficientScope) {
				fail(w, http.StatusBadGateway, hardcover.ErrInsufficientScope.Error())
			} else {
				log.Printf("bookdiscovery: hardcover trending for %s failed: %v", id, err)
				fail(w, http.StatusBadGateway, "could not reach Hardcover for the trending list; check the connection in instance settings and try again")
			}
			return
		}
		// Access and connection selection are rechecked after provider/cache waits.
		if _, ok := h.authorize(w, r, id); !ok {
			return
		}
		latest, err := h.store.HardcoverState(id)
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "could not read the Hardcover connection")
			return
		}
		if latest.Revision != revision {
			continue
		}
		writeJSON(w, trendingResponse{InstanceID: id, Connected: connected, Source: "Hardcover", Scope: "Trending on Hardcover right now", Books: books})
		return
	}
}

// SetCredentialResolver installs lazy OAuth renewal during startup.
func (h *TrendingHandler) SetCredentialResolver(resolve func(context.Context, string, string) (string, error)) {
	h.resolve = resolve
}

func (h *TrendingHandler) trending(ctx context.Context, id string) ([]hardcover.Book, bool, int64, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, 0, err
		}
		state, err := h.store.HardcoverState(id)
		if err != nil {
			return nil, false, 0, err
		}
		if !state.Configured {
			return []hardcover.Book{}, false, state.Revision, nil
		}
		h.mu.Lock()
		if entry, ok := h.cache[id]; ok && entry.revision == state.Revision && h.now().Sub(entry.fetched) < h.ttl {
			books := entry.books
			h.mu.Unlock()
			return books, true, state.Revision, nil
		}
		if done, busy := h.inflight[id]; busy {
			h.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, false, 0, ctx.Err()
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		h.inflight[id] = done
		generation := h.generation[id]
		h.mu.Unlock()

		token, err := h.resolve(ctx, id, "")
		var books []hardcover.Book
		if err == nil && token != "" {
			books, err = h.source.Trending(ctx, token, TrendingLimit)
			if errors.Is(err, hardcover.ErrUnauthorized) {
				renewed, renewErr := h.resolve(ctx, id, token)
				if renewErr != nil {
					err = renewErr
				} else if renewed != "" && renewed != token {
					books, err = h.source.Trending(ctx, renewed, TrendingLimit)
				}
			}
		}
		latest, stateErr := h.store.HardcoverState(id)
		h.mu.Lock()
		delete(h.inflight, id)
		stale := h.generation[id] != generation || stateErr == nil && latest.Revision != state.Revision
		if err == nil && stateErr != nil {
			err = stateErr
		}
		if err == nil && token == "" {
			stale = true
		}
		if err == nil && !stale {
			if books == nil {
				books = []hardcover.Book{}
			}
			h.cache[id] = &trendingEntry{books: books, fetched: h.now(), revision: state.Revision}
		}
		close(done)
		h.mu.Unlock()
		if stale {
			continue
		}
		return books, true, state.Revision, err
	}
}

// authorize resolves and checks the Chaptarr instance the caller may read
// through. It is the same rule the other requester book reads apply.
func (h *TrendingHandler) authorize(w http.ResponseWriter, r *http.Request, id string) (string, bool) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return "", false
	}
	if !auth.HasPermission(claims.Role, auth.PermissionMediaDiscover) {
		fail(w, http.StatusForbidden, "discovery is not available to you")
		return "", false
	}
	if h.store == nil {
		fail(w, http.StatusForbidden, "books are not available to you")
		return "", false
	}
	admin := claims.Role == auth.RoleAdmin
	if id == "" {
		var err error
		id, err = h.store.EffectiveDefaultInstanceID(claims.UserID, "chaptarr")
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "could not check book access")
			return "", false
		}
	}
	if id == "" {
		fail(w, http.StatusForbidden, "books are not available to you")
		return "", false
	}
	if !admin {
		allowed, err := h.store.UserCanAccessInstance(claims.UserID, id, "chaptarr")
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "could not check book access")
			return "", false
		}
		if !allowed {
			fail(w, http.StatusForbidden, "books are not available to you")
			return "", false
		}
	}
	inst, err := h.store.Get(id)
	if err != nil || inst == nil || inst.ServiceType != "chaptarr" {
		fail(w, http.StatusForbidden, "books are not available to you")
		return "", false
	}
	return id, true
}

func fail(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(body)
}
