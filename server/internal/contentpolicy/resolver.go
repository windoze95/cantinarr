package contentpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// RawGetter is the one TMDB method this package needs. *tmdb.Client
// satisfies it; tests hand in a fake.
type RawGetter interface {
	DoGetRaw(path string, params url.Values) ([]byte, error)
}

// RawGetterSource resolves the TMDB client per call, so a credential the
// admin adds or rotates is picked up without a restart. It returns nil while
// TMDB is unconfigured.
type RawGetterSource func() RawGetter

// statusCoder is implemented by upstream errors that carry the HTTP status
// (tmdb.StatusError), so a missing title reads as absence and rate limiting
// gets one retry while everything else stays an error.
type statusCoder interface {
	HTTPStatus() int
}

// retryAfterer is implemented by upstream errors that carry a Retry-After.
type retryAfterer interface {
	RetryAfterDuration() time.Duration
}

const (
	ratingTTL = 24 * time.Hour
	// lookupWorkers bounds concurrent rating lookups across every request
	// on the server, not per call: the tabs load half a dozen rows at once
	// and a cold cache would otherwise fan out into TMDB's rate limit.
	lookupWorkers = 8
	// maxRetryAfter caps how long one rate-limited lookup waits before its
	// single retry.
	maxRetryAfter = 2 * time.Second
)

// errLookup wraps a lookup that failed for a reason other than a missing
// title; the candidate is hidden and the caller answers as an error.
var errLookup = errors.New("rating lookup failed")

// ratingMap is one title's certification per region, as cached.
type ratingMap map[string]string

func ratingKey(mediaType string, id int) string {
	return fmt.Sprintf("cert:%s:%d", mediaType, id)
}

// get performs one bounded upstream read, retrying a rate-limited answer
// once after its Retry-After (capped).
func (s *Service) get(ctx context.Context, getter RawGetter, path string) ([]byte, error) {
	if err := s.acquire(ctx); err != nil {
		return nil, err
	}
	defer s.release()
	data, err := getter.DoGetRaw(path, nil)
	if err == nil {
		return data, nil
	}
	if httpStatus(err) != http.StatusTooManyRequests {
		return nil, err
	}
	wait := maxRetryAfter
	var ra retryAfterer
	if errors.As(err, &ra) && ra.RetryAfterDuration() > 0 && ra.RetryAfterDuration() < wait {
		wait = ra.RetryAfterDuration()
	}
	if err := s.sleep(ctx, wait); err != nil {
		return nil, err
	}
	return getter.DoGetRaw(path, nil)
}

func (s *Service) acquire(ctx context.Context) error {
	select {
	case s.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) release() { <-s.sem }

func httpStatus(err error) int {
	var sc statusCoder
	if errors.As(err, &sc) {
		return sc.HTTPStatus()
	}
	return 0
}

// ratings returns a title's certification per region, from cache or TMDB.
// A title TMDB does not know is an empty map (absence, cached); a failed
// read is an error (blindness, never cached).
func (s *Service) ratings(ctx context.Context, mediaType string, id int) (ratingMap, error) {
	key := ratingKey(mediaType, id)
	if data, ok := s.cache.Get(key); ok {
		var m ratingMap
		if err := json.Unmarshal(data, &m); err == nil {
			return m, nil
		}
	}
	getter := s.getter()
	if getter == nil {
		return nil, ErrUnavailable
	}
	var path string
	switch mediaType {
	case MediaMovie:
		path = fmt.Sprintf("/movie/%d/release_dates", id)
	case MediaTV:
		path = fmt.Sprintf("/tv/%d/content_ratings", id)
	default:
		return nil, fmt.Errorf("%w: unknown media type %q", errLookup, mediaType)
	}
	data, err := s.get(ctx, getter, path)
	if err != nil {
		if httpStatus(err) == http.StatusNotFound {
			m := ratingMap{}
			s.store(key, m)
			return m, nil
		}
		return nil, fmt.Errorf("%w: %v", errLookup, err)
	}
	m, err := parseRatings(mediaType, data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errLookup, err)
	}
	s.store(key, m)
	return m, nil
}

func (s *Service) store(key string, m ratingMap) {
	if data, err := json.Marshal(m); err == nil {
		s.cache.Set(key, data, ratingTTL)
	}
}

// Prime records a title's ratings from a detail body that already carries
// them (release_dates for a movie, content_ratings for a show), so the
// detail read that just happened is not followed by a second one.
func (s *Service) Prime(mediaType string, id int, detail []byte) {
	if id <= 0 || !knownMediaType(mediaType) {
		return
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(detail, &envelope); err != nil {
		return
	}
	field := "release_dates"
	if mediaType == MediaTV {
		field = "content_ratings"
	}
	raw, ok := envelope[field]
	if !ok {
		return
	}
	m, err := parseRatings(mediaType, raw)
	if err != nil {
		return
	}
	s.store(ratingKey(mediaType, id), m)
}

// parseRatings reads TMDB's release_dates (movie) or content_ratings (TV)
// payload into a region -> certification map. A movie's certification for a
// region is the theatrical release's when it has one, else the first
// release that carries one.
func parseRatings(mediaType string, data []byte) (ratingMap, error) {
	m := ratingMap{}
	switch mediaType {
	case MediaMovie:
		var out struct {
			Results []struct {
				Region   string `json:"iso_3166_1"`
				Releases []struct {
					Certification string `json:"certification"`
					Type          int    `json:"type"`
				} `json:"release_dates"`
			} `json:"results"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("decode release dates: %w", err)
		}
		for _, r := range out.Results {
			region := normalizeCert(r.Region)
			if region == "" {
				continue
			}
			var first, theatrical string
			for _, rel := range r.Releases {
				cert := normalizeCert(rel.Certification)
				if cert == "" {
					continue
				}
				if first == "" {
					first = cert
				}
				if rel.Type == 3 && theatrical == "" {
					theatrical = cert
				}
			}
			if theatrical != "" {
				m[region] = theatrical
			} else if first != "" {
				m[region] = first
			}
		}
	case MediaTV:
		var out struct {
			Results []struct {
				Region string `json:"iso_3166_1"`
				Rating string `json:"rating"`
			} `json:"results"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("decode content ratings: %w", err)
		}
		for _, r := range out.Results {
			region := normalizeCert(r.Region)
			cert := normalizeCert(r.Rating)
			if region == "" || cert == "" {
				continue
			}
			if _, dup := m[region]; !dup {
				m[region] = cert
			}
		}
	default:
		return nil, fmt.Errorf("unknown media type %q", mediaType)
	}
	return m, nil
}

// FilterWith decides every candidate against a compiled evaluator. Adult
// titles, hidden genres, and titles with no TMDB id are hidden without a
// lookup; the rest are rated concurrently through the shared worker bound,
// one lookup per distinct title. The returned error reports that at least
// one lookup failed: those titles are hidden, and the caller must answer as
// an error rather than serve the thinner list as complete.
func (s *Service) FilterWith(ctx context.Context, ev *Evaluator, cands []Candidate) ([]bool, error) {
	keep := make([]bool, len(cands))
	type pending struct {
		mediaType string
		id        int
		indexes   []int
	}
	byTitle := map[string]*pending{}
	var order []*pending
	for i, c := range cands {
		if c.Adult || !knownMediaType(c.MediaType) || c.TMDBID <= 0 || ev.BlockedGenre(c.MediaType, c.GenreIDs) {
			continue
		}
		key := ratingKey(c.MediaType, c.TMDBID)
		p, ok := byTitle[key]
		if !ok {
			p = &pending{mediaType: c.MediaType, id: c.TMDBID}
			byTitle[key] = p
			order = append(order, p)
		}
		p.indexes = append(p.indexes, i)
	}
	if len(order) == 0 {
		return keep, nil
	}
	region := normalizeCert(ev.policy.RatingRegion)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for _, p := range order {
		wg.Add(1)
		go func(p *pending) {
			defer wg.Done()
			m, err := s.ratings(ctx, p.mediaType, p.id)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			cert, known := m[region]
			allowed := ev.Allows(p.mediaType, Rating{Certification: cert, Known: known && cert != ""}, false, nil)
			if !allowed {
				return
			}
			mu.Lock()
			for _, i := range p.indexes {
				keep[i] = true
			}
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return keep, firstErr
}
