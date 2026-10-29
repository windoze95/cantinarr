// Package mediaavailability indexes video-server holdings and verifies them
// under the recipient's current authority. It never acquires media or manages
// accounts. The catalog index is evidence to locate candidates, not an ACL.
package mediaavailability

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"log/slog"
	"slices"
	"sync"
	"time"
)

var (
	ErrUnavailable  = errors.New("media availability is not available")
	ErrScanDisabled = errors.New("media availability scanning is disabled")
	ErrScanRunning  = errors.New("media availability scan is already running")
)

type ProviderFactory func(*instance.Instance) (mediaserver.Provider, error)
type Episode = mediaserver.InventoryEpisode
type Source struct {
	InstanceID  string `json:"instance_id"`
	Name        string `json:"name"`
	ServiceType string `json:"service_type"`
	State       string `json:"state"`
	AccessMode  string `json:"access_mode"`
	Is4K        bool   `json:"is_4k"`
}
type Result struct {
	ScopeRevision string    `json:"-"`
	Enabled       bool      `json:"enabled"`
	Known         bool      `json:"known"`
	Movie         bool      `json:"movie"`
	Is4K          bool      `json:"is_4k"`
	Episodes      []Episode `json:"episodes"`
	Sources       []Source  `json:"sources"`
}
type ScanStatus struct {
	InstanceID    string     `json:"instance_id"`
	State         string     `json:"state"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	ItemCount     int        `json:"item_count"`
	Error         string     `json:"error,omitempty"`
}
type cachedResult struct {
	result Result
	until  time.Time
}
type lookupFlight struct {
	done   chan struct{}
	result Result
	err    error
}
type cachedCatalog struct {
	items []mediaserver.InventoryItem
	until time.Time
}
type catalogFlight struct {
	done  chan struct{}
	items []mediaserver.InventoryItem
	err   error
}
type Service struct {
	db             *sql.DB
	store          *instance.Store
	logger         *slog.Logger
	providers      ProviderFactory
	mu             sync.Mutex
	running        map[string]bool
	cache          map[string]cachedResult
	flights        map[string]*lookupFlight
	catalogs       map[string]cachedCatalog
	catalogFlights map[string]*catalogFlight
	changed        func()
	generation     uint64
	wake           chan struct{}
	lookupSlots    chan struct{}
	now            func() time.Time
}

func NewService(db *sql.DB, store *instance.Store, providers ProviderFactory, logger *slog.Logger) *Service {
	if providers == nil {
		providers = instance.NewMediaServerProvider
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, store: store, providers: providers, logger: logger, running: map[string]bool{}, cache: map[string]cachedResult{}, flights: map[string]*lookupFlight{}, catalogs: map[string]cachedCatalog{}, catalogFlights: map[string]*catalogFlight{}, wake: make(chan struct{}, 1), lookupSlots: make(chan struct{}, 4), now: time.Now}
}
func (s *Service) SetChangedObserver(changed func()) { s.mu.Lock(); s.changed = changed; s.mu.Unlock() }
func (s *Service) notifyChanged() {
	s.mu.Lock()
	changed := s.changed
	s.mu.Unlock()
	if changed != nil {
		changed()
	}
}
func fingerprint(inst *instance.Instance) string {
	b, _ := json.Marshal([]any{inst.ID, inst.ServiceType, inst.URL, inst.APIKey, inst.MediaServerConfig})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (s *Service) videoInstance(id string) (*instance.Instance, error) {
	i, e := s.store.Get(id)
	if e != nil {
		return nil, e
	}
	if i == nil || !instance.IsVideoServerType(i.ServiceType) || i.MediaServerConfigInvalid {
		return nil, ErrUnavailable
	}
	return i, nil
}
func scanEnabled(i *instance.Instance) bool {
	c := i.MediaServerConfig.AvailabilityScan
	return c != nil && c.Enabled && len(c.LibraryIDs) > 0
}
func (s *Service) ScanLibraries(ctx context.Context, id string) ([]mediaserver.Library, error) {
	i, e := s.videoInstance(id)
	if e != nil {
		return nil, e
	}
	p, e := s.providers(i)
	if e != nil {
		return nil, e
	}
	r, ok := p.(mediaserver.InventoryProvider)
	if !ok {
		return nil, ErrUnavailable
	}
	libs, e := r.InventoryLibraries(ctx)
	if e != nil {
		return nil, e
	}
	current, e := s.videoInstance(id)
	if e != nil || fingerprint(current) != fingerprint(i) {
		return nil, ErrUnavailable
	}
	out := []mediaserver.Library{}
	for _, l := range libs {
		if mediaserver.VideoLibrary(l.CollectionType) {
			out = append(out, l)
		}
	}
	return out, nil
}
func (s *Service) Invalidate(_ string) {
	s.mu.Lock()
	s.cache = map[string]cachedResult{}
	s.catalogs = map[string]cachedCatalog{}
	s.generation++
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

type target struct {
	inst     *instance.Instance
	remote   string
	mode     string
	libs     []string
	revision int64
}

func (s *Service) targets(user int64) ([]target, error) {
	instances, e := s.store.ListAll()
	if e != nil {
		return nil, e
	}
	out := []target{}
	for _, i := range instances {
		if !instance.IsVideoServerType(i.ServiceType) || i.MediaServerConfigInvalid || !scanEnabled(&i) {
			continue
		}
		t := target{inst: &i, libs: append([]string{}, i.MediaServerConfig.AvailabilityScan.LibraryIDs...)}
		e = s.db.QueryRow("SELECT remote_user_id FROM user_media_server_accounts WHERE user_id=? AND instance_id=?", user, i.ID).Scan(&t.remote)
		if e == nil {
			var n int
			if e = s.db.QueryRow("SELECT COUNT(*) FROM user_instance_grants WHERE user_id=? AND instance_id=?", user, i.ID).Scan(&n); e != nil {
				return nil, e
			}
			if n == 0 || t.remote == "" {
				continue
			}
			t.mode = "linked_account"
		} else if errors.Is(e, sql.ErrNoRows) {
			var raw string
			e = s.db.QueryRow("SELECT library_ids,revision FROM media_availability_households WHERE user_id=? AND instance_id=?", user, i.ID).Scan(&raw, &t.revision)
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				return nil, e
			}
			var selected []string
			if json.Unmarshal([]byte(raw), &selected) != nil {
				return nil, ErrUnavailable
			}
			t.libs = nil
			for _, lib := range selected {
				if slices.Contains(i.MediaServerConfig.AvailabilityScan.LibraryIDs, lib) {
					t.libs = append(t.libs, lib)
				}
			}
			if len(t.libs) == 0 {
				continue
			}
			t.mode = "household"
		} else {
			return nil, e
		}
		out = append(out, t)
	}
	return out, nil
}

// ScopeRevision reads current local authorization without contacting a provider.
// Mutation guards use it to reject grant/configuration changes while reusing
// recent recipient evidence within a single bounded dispatch step.
func (s *Service) ScopeRevision(userID int64) (string, error) {
	if userID <= 0 {
		return "", ErrUnavailable
	}
	targets, err := s.targets(userID)
	if err != nil {
		return "", err
	}
	return targetKey(targets), nil
}

func targetKey(targets []target) string {
	parts := []any{}
	for _, t := range targets {
		parts = append(parts, []any{fingerprint(t.inst), t.remote, t.mode, t.libs, t.revision})
	}
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func cloneResult(r Result) Result {
	r.Episodes = append([]Episode{}, r.Episodes...)
	for i := range r.Episodes {
		r.Episodes[i].Copies = append([]mediaserver.InventoryCopy{}, r.Episodes[i].Copies...)
	}
	r.Sources = append([]Source{}, r.Sources...)
	return r
}

// Lookup bounds/coalesces recipient checks. Only display reads may reuse a
// completed result for 30 seconds; fresh mutation-boundary reads never do.
func (s *Service) Lookup(ctx context.Context, user int64, q mediaserver.ItemQuery, fresh bool) (Result, error) {
	empty := Result{ScopeRevision: targetKey(nil), Known: true, Episodes: []Episode{}, Sources: []Source{}}
	if user <= 0 || (q.MediaType != "movie" && q.MediaType != "tv") || (q.TMDBID <= 0 && q.TVDBID <= 0) {
		return empty, ErrUnavailable
	}
	targets, e := s.targets(user)
	if e != nil {
		return empty, e
	}
	if len(targets) == 0 {
		return empty, nil
	}
	key := fmt.Sprintf("%d:%s:%d:%d:%d:%s:%s:%t", user, q.MediaType, q.TMDBID, q.TVDBID, q.Year, q.Title, targetKey(targets), fresh)
	s.mu.Lock()
	generation := s.generation
	if c, ok := s.cache[key]; !fresh && ok && s.now().Before(c.until) {
		s.mu.Unlock()
		return cloneResult(c.result), nil
	}
	if f, ok := s.flights[key]; ok {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return empty, ctx.Err()
		case <-f.done:
			current, err := s.targets(user)
			if err != nil {
				return empty, err
			}
			if targetKey(current) != targetKey(targets) {
				return Result{ScopeRevision: targetKey(current), Enabled: true, Known: false, Episodes: []Episode{}, Sources: []Source{}}, nil
			}
			return cloneResult(f.result), f.err
		}
	}
	f := &lookupFlight{done: make(chan struct{})}
	s.flights[key] = f
	s.mu.Unlock()
	result, err := s.lookup(ctx, user, q, targets, fresh)
	s.mu.Lock()
	if generation != s.generation {
		result = Result{ScopeRevision: result.ScopeRevision, Enabled: true, Known: false, Episodes: []Episode{}, Sources: []Source{}}
	}
	f.result, f.err = result, err
	delete(s.flights, key)
	if err == nil {
		if len(s.cache) >= 1024 {
			s.cache = map[string]cachedResult{}
		}
		s.cache[key] = cachedResult{cloneResult(result), s.now().Add(30 * time.Second)}
	}
	close(f.done)
	s.mu.Unlock()
	return result, err
}
func (s *Service) candidates(i *instance.Instance, q mediaserver.ItemQuery) ([]mediaserver.InventoryItem, error) {
	var fp string
	if e := s.db.QueryRow("SELECT config_fingerprint FROM media_availability_scans WHERE instance_id=?", i.ID).Scan(&fp); errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	} else if e != nil {
		return nil, e
	}
	if fp != fingerprint(i) {
		return nil, nil
	}
	rows, e := s.db.Query("SELECT payload FROM media_availability_items WHERE instance_id=? AND media_type=? AND ((tmdb_id=? AND tmdb_id>0) OR (tvdb_id=? AND tvdb_id>0))", i.ID, q.MediaType, q.TMDBID, q.TVDBID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []mediaserver.InventoryItem{}
	for rows.Next() {
		var raw string
		if e := rows.Scan(&raw); e != nil {
			return nil, e
		}
		var item mediaserver.InventoryItem
		if json.Unmarshal([]byte(raw), &item) != nil {
			return nil, ErrUnavailable
		}
		if mediaserver.InventoryMatches(item, q) {
			out = append(out, item)
		}
	}
	return out, rows.Err()
}
func (s *Service) lookup(ctx context.Context, user int64, q mediaserver.ItemQuery, targets []target, fresh bool) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := Result{ScopeRevision: targetKey(targets), Enabled: true, Known: true, Episodes: []Episode{}, Sources: []Source{}}
	type answer struct {
		source Source
		items  []mediaserver.InventoryItem
		err    error
	}
	answers := make([]answer, len(targets))
	var wg sync.WaitGroup
	for n, t := range targets {
		wg.Add(1)
		go func(n int, t target) {
			defer wg.Done()
			a := answer{source: Source{InstanceID: t.inst.ID, Name: t.inst.Name, ServiceType: t.inst.ServiceType, AccessMode: t.mode, State: "unverified"}}
			defer func() {
				if a.err != nil && !errors.Is(a.err, mediaserver.ErrItemUnverified) && !errors.Is(a.err, mediaserver.ErrItemNotFound) && !errors.Is(a.err, ErrUnavailable) {
					a.source.State = "unreachable"
				}
				answers[n] = a
			}()
			select {
			case s.lookupSlots <- struct{}{}:
				defer func() { <-s.lookupSlots }()
			case <-ctx.Done():
				a.err = ctx.Err()
				return
			}
			p, e := s.providers(t.inst)
			if e != nil {
				a.err = e
				return
			}
			reader, ok := p.(mediaserver.InventoryProvider)
			if !ok {
				a.err = ErrUnavailable
				return
			}
			candidates, e := s.candidates(t.inst, q)
			if e != nil {
				a.err = e
				return
			}
			catalogRead := false
			refreshCandidates := func() error {
				catalog, ok := p.(mediaserver.InventoryCatalog)
				if !ok {
					return mediaserver.ErrItemUnverified
				}
				all, err := s.catalog(ctx, t, catalog, fresh)
				if err != nil {
					return err
				}
				catalogRead = true
				candidates = nil
				for _, item := range all {
					if mediaserver.InventoryIdentityConflict(item, q) {
						return mediaserver.ErrItemUnverified
					}
					if mediaserver.InventoryMatches(item, q) {
						candidates = append(candidates, item)
					}
				}
				return nil
			}
			// A missing-only TV decision needs every current copy of the series,
			// including a copy imported since the background snapshot.
			if len(candidates) == 0 || fresh && q.MediaType == "tv" {
				if a.err = refreshCandidates(); a.err != nil {
					return
				}
				if len(candidates) == 0 {
					a.source.State = "missing"
					return
				}
			}
			verify := func() {
				if verifier, ok := p.(mediaserver.InventoryVerifier); ok {
					a.items, a.err = verifier.VerifyInventory(ctx, t.libs, t.remote, q, candidates)
				} else {
					a.items, a.err = reader.Inventory(ctx, t.libs, t.remote, &q)
				}
			}
			verify()
			// A stale native ID is not absence. Re-enumerate complete identities
			// before allowing a request for a removed or moved catalog entry.
			if !catalogRead && (errors.Is(a.err, mediaserver.ErrItemUnverified) || errors.Is(a.err, mediaserver.ErrItemNotFound) || a.err == nil && len(a.items) == 0) {
				if a.err = refreshCandidates(); a.err != nil {
					return
				}
				if len(candidates) == 0 {
					a.items = nil
					a.source.State = "missing"
					return
				}
				verify()
			}
			if a.err != nil {
				if !errors.Is(a.err, mediaserver.ErrItemUnverified) && !errors.Is(a.err, mediaserver.ErrItemNotFound) {
					a.source.State = "unreachable"
				}
				return
			}
			a.source.State = "missing"
			for _, i := range a.items {
				if i.Movie || len(i.Episodes) > 0 {
					a.source.State = "found"
				}
				a.source.Is4K = a.source.Is4K || i.Is4K
			}
		}(n, t)
	}
	wg.Wait()
	current, e := s.targets(user)
	if e != nil {
		return result, e
	}
	if targetKey(current) != targetKey(targets) {
		return Result{ScopeRevision: targetKey(current), Enabled: true, Known: false, Episodes: []Episode{}, Sources: []Source{}}, nil
	}
	for n, a := range answers {
		result.Sources = append(result.Sources, a.source)
		if a.err != nil {
			result.Known = false
			continue
		}
		for _, item := range a.items {
			if !slices.Contains(targets[n].libs, item.LibraryID) || !mediaserver.InventoryMatches(item, q) {
				result.Known = false
				continue
			}
			result.Movie = result.Movie || item.Movie
			result.Is4K = result.Is4K || item.Is4K
			for _, ep := range item.Episodes {
				ep.InstanceID = targets[n].inst.ID
				result.Episodes = append(result.Episodes, ep)
			}
		}
	}
	return result, nil
}

func (s *Service) catalog(ctx context.Context, t target, provider mediaserver.InventoryCatalog, fresh bool) ([]mediaserver.InventoryItem, error) {
	key := fmt.Sprintf("%s:%t", targetKey([]target{t}), fresh)
	s.mu.Lock()
	generation := s.generation
	if entry, ok := s.catalogs[key]; !fresh && ok && s.now().Before(entry.until) {
		s.mu.Unlock()
		return entry.items, nil
	}
	if f, ok := s.catalogFlights[key]; ok {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.done:
			return f.items, f.err
		}
	}
	f := &catalogFlight{done: make(chan struct{})}
	s.catalogFlights[key] = f
	s.mu.Unlock()
	items, err := provider.InventoryCatalog(ctx, t.libs, t.remote)
	s.mu.Lock()
	if generation != s.generation {
		items = nil
		err = ErrUnavailable
	}
	f.items, f.err = items, err
	if err == nil {
		if len(s.catalogs) >= 64 {
			s.catalogs = map[string]cachedCatalog{}
		}
		s.catalogs[key] = cachedCatalog{items, s.now().Add(30 * time.Second)}
	}
	delete(s.catalogFlights, key)
	close(f.done)
	s.mu.Unlock()
	return items, err
}
