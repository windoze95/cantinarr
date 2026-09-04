package contentpolicy

import (
	"context"
	"database/sql"
	"time"

	"github.com/windoze95/cantinarr-server/internal/cache"
)

// Service is the one object the rest of the server talks to: the store, the
// rating resolver, and the decision, wired over the shared TTL cache.
type Service struct {
	Store  *Store
	cache  *cache.Cache
	getter RawGetterSource
	sem    chan struct{}
	sleep  func(context.Context, time.Duration) error
}

// New builds the service over the database, a TMDB client source, and the
// shared cache. A nil cache gets a private one.
func New(db *sql.DB, src RawGetterSource, c *cache.Cache) *Service {
	if c == nil {
		c = cache.New()
	}
	if src == nil {
		src = func() RawGetter { return nil }
	}
	return &Service{
		Store:  NewStore(db),
		cache:  c,
		getter: src,
		sem:    make(chan struct{}, lookupWorkers),
		sleep:  sleepContext,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// PolicyFor returns the caller's policy, or nil for an unrestricted account.
// Admins never carry one, so their hot paths skip the read entirely.
func (s *Service) PolicyFor(userID int64, role string) (*Policy, error) {
	if role == roleAdmin || userID <= 0 {
		return nil, nil
	}
	return s.Store.Get(userID)
}

// EvaluatorFor compiles a policy against the lists it needs. A region other
// than US whose scheme cannot be read from any source is ErrUnavailable: a
// cap that cannot be ranked cannot be applied honestly.
func (s *Service) EvaluatorFor(ctx context.Context, p *Policy) (*Evaluator, error) {
	if p == nil {
		return nil, nil
	}
	region := normalizeCert(p.RatingRegion)
	if region == "" {
		region = "US"
	}
	movie, movieSource := s.certLists(ctx, MediaMovie)
	tv, tvSource := s.certLists(ctx, MediaTV)
	if region != "US" && (movieSource == SourceBuiltin || tvSource == SourceBuiltin) {
		return nil, ErrUnavailable
	}
	regionOrders := map[string]certOrders{
		MediaMovie: movie.orders(region),
		MediaTV:    tv.orders(region),
	}
	usOrders := map[string]certOrders{
		MediaMovie: movie.orders("US"),
		MediaTV:    tv.orders("US"),
	}
	genres := map[string]genreTable{
		MediaMovie: s.genres(ctx, MediaMovie),
		MediaTV:    s.genres(ctx, MediaTV),
	}
	return newEvaluator(*p, regionOrders, usOrders, genres), nil
}

// Filter decides every candidate for a policy. See FilterWith for the
// contract on the returned error.
func (s *Service) Filter(ctx context.Context, p *Policy, cands []Candidate) ([]bool, error) {
	ev, err := s.EvaluatorFor(ctx, p)
	if err != nil {
		return nil, err
	}
	if ev == nil {
		keep := make([]bool, len(cands))
		for i := range keep {
			keep[i] = true
		}
		return keep, nil
	}
	return s.FilterWith(ctx, ev, cands)
}

// Allows decides one title. A nil policy allows everything.
func (s *Service) Allows(ctx context.Context, p *Policy, c Candidate) (bool, error) {
	keep, err := s.Filter(ctx, p, []Candidate{c})
	if err != nil {
		return false, err
	}
	return keep[0], nil
}

// AllowedRecipients drops the kids accounts among userIDs that must not
// hear about a title. Non-children always stay. A title that cannot be
// identified (no TMDB id) or rated (lookup failure) drops every child; the
// error is returned for logging alongside the kept list.
func (s *Service) AllowedRecipients(ctx context.Context, userIDs []int64, mediaType string, tmdbID int) ([]int64, error) {
	policies, err := s.Store.PoliciesFor(userIDs)
	if err != nil {
		return nil, err
	}
	if len(policies) == 0 {
		return userIDs, nil
	}
	kept := make([]int64, 0, len(userIDs))
	var firstErr error
	verdicts := map[int64]bool{}
	for _, id := range userIDs {
		p, child := policies[id]
		if !child {
			kept = append(kept, id)
			continue
		}
		if tmdbID <= 0 {
			continue
		}
		allowed, err := s.Allows(ctx, p, Candidate{MediaType: mediaType, TMDBID: tmdbID})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		verdicts[id] = allowed
		if allowed {
			kept = append(kept, id)
		}
	}
	return kept, firstErr
}

// DescribeLimits renders a policy for the assistant's instructions. When
// the lists cannot be read it still names the caps, which is what the
// instruction needs.
func (s *Service) DescribeLimits(ctx context.Context, p *Policy) string {
	if p == nil {
		return ""
	}
	ev, err := s.EvaluatorFor(ctx, p)
	if err != nil || ev == nil {
		return newEvaluator(*p, map[string]certOrders{}, map[string]certOrders{}, builtinGenres).Describe()
	}
	return ev.Describe()
}
