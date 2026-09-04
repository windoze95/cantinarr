package contentpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// CertificationOption is one entry of a region's rating scheme, as TMDB
// lists it. Order ranks the entries within the region; 0 is the unrated
// placeholder ("NR") and is never offered as a cap.
type CertificationOption struct {
	Certification string `json:"certification"`
	Meaning       string `json:"meaning,omitempty"`
	Order         int    `json:"order"`
	// Default marks the entry the admin UI starts a new kids account on.
	Default bool `json:"default,omitempty"`
}

// CertificationsResponse is GET /api/admin/certifications: every region's
// movie and TV schemes, plus where the lists came from.
type CertificationsResponse struct {
	Movie  map[string][]CertificationOption `json:"movie"`
	TV     map[string][]CertificationOption `json:"tv"`
	Source string                           `json:"source"`
}

// Sources a certification list can come from, most to least current.
const (
	SourceTMDB    = "tmdb"    // fetched today, or cached within the day
	SourceCached  = "cached"  // TMDB unreachable; the last good copy, up to a month old
	SourceBuiltin = "builtin" // no copy at all; the US scheme as shipped
)

// certList is one media type's schemes: region -> options sorted by order.
type certList map[string][]CertificationOption

const (
	certListTTL     = 24 * time.Hour
	certListLastTTL = 30 * 24 * time.Hour
)

// suggestedDefaults is where a fresh kids account starts, per region. Only
// the schemes we know well enough to pick for get one; the app falls back to
// the second-lowest entry elsewhere.
var suggestedDefaults = map[string]map[string]string{
	MediaMovie: {"US": "PG"},
	MediaTV:    {"US": "TV-PG"},
}

// builtinUS is the US scheme as TMDB publishes it, the fallback when neither
// a fresh nor a cached list can be had. It serves only region US.
var builtinUS = map[string][]CertificationOption{
	MediaMovie: {
		{Certification: "NR", Order: 0, Meaning: "No rating information."},
		{Certification: "G", Order: 1, Meaning: "All ages admitted. There is no content that would be objectionable to most parents."},
		{Certification: "PG", Order: 2, Meaning: "Some material may not be suitable for children under 10."},
		{Certification: "PG-13", Order: 3, Meaning: "Some material may be inappropriate for children under 13."},
		{Certification: "R", Order: 4, Meaning: "Under 17 requires accompanying parent or adult guardian 21 or older."},
		{Certification: "NC-17", Order: 5, Meaning: "No one 17 and under admitted."},
	},
	MediaTV: {
		{Certification: "NR", Order: 0, Meaning: "No rating information."},
		{Certification: "TV-Y", Order: 1, Meaning: "Designed to be appropriate for all children."},
		{Certification: "TV-Y7", Order: 2, Meaning: "Designed for children age 7 and above."},
		{Certification: "TV-G", Order: 3, Meaning: "Most parents would find this program suitable for all ages."},
		{Certification: "TV-PG", Order: 4, Meaning: "Parental guidance suggested."},
		{Certification: "TV-14", Order: 5, Meaning: "Parents strongly cautioned. May be unsuitable for children under 14."},
		{Certification: "TV-MA", Order: 6, Meaning: "Mature audiences only."},
	},
}

// ErrUnavailable means the lists a decision needs could not be read from
// any source. Callers answer 503 rather than guessing.
var ErrUnavailable = errors.New("content limits are temporarily unavailable")

// ValidationError is a policy the admin cannot save as written; the message
// is safe to show.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// certLists loads one media type's schemes: fresh (or within the day),
// then the last good copy, then the built-in US scheme.
func (s *Service) certLists(ctx context.Context, mediaType string) (certList, string) {
	key := "certlist:" + mediaType
	if data, ok := s.cache.Get(key); ok {
		if list, err := decodeCertList(data); err == nil {
			return list, SourceTMDB
		}
	}
	if getter := s.getter(); getter != nil {
		if data, err := s.get(ctx, getter, "/certification/"+mediaType+"/list"); err == nil {
			if list, err := decodeCertList(data); err == nil && len(list) > 0 {
				s.cache.Set(key, data, certListTTL)
				s.cache.Set(key+":last", data, certListLastTTL)
				return list, SourceTMDB
			}
		}
	}
	if data, ok := s.cache.Get(key + ":last"); ok {
		if list, err := decodeCertList(data); err == nil {
			return list, SourceCached
		}
	}
	return certList{"US": builtinUS[mediaType]}, SourceBuiltin
}

func decodeCertList(data []byte) (certList, error) {
	var out struct {
		Certifications map[string][]CertificationOption `json:"certifications"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode certification list: %w", err)
	}
	list := certList{}
	for region, options := range out.Certifications {
		region = strings.ToUpper(strings.TrimSpace(region))
		if region == "" {
			continue
		}
		kept := make([]CertificationOption, 0, len(options))
		for _, o := range options {
			o.Certification = strings.TrimSpace(o.Certification)
			if o.Certification == "" {
				continue
			}
			kept = append(kept, o)
		}
		sort.SliceStable(kept, func(i, j int) bool { return kept[i].Order < kept[j].Order })
		list[region] = kept
	}
	return list, nil
}

// orders compiles one region's options into the lookup the evaluator uses.
func (l certList) orders(region string) certOrders {
	out := certOrders{}
	for _, o := range l[strings.ToUpper(region)] {
		out[normalizeCert(o.Certification)] = o.Order
	}
	return out
}

// canonical returns the list's own spelling of a certification with a
// positive order, so a policy stores "PG-13" however the admin typed it.
func (l certList) canonical(region, cert string) (string, bool) {
	want := normalizeCert(cert)
	for _, o := range l[strings.ToUpper(region)] {
		if normalizeCert(o.Certification) == want && o.Order > 0 {
			return o.Certification, true
		}
	}
	return "", false
}

// weakestSource returns the less current of two sources.
func weakestSource(a, b string) string {
	rank := map[string]int{SourceTMDB: 0, SourceCached: 1, SourceBuiltin: 2}
	if rank[a] >= rank[b] {
		return a
	}
	return b
}

// Certifications is the admin route's payload: both schemes for every
// region, with the suggested starting point marked.
func (s *Service) Certifications(ctx context.Context) CertificationsResponse {
	movie, movieSource := s.certLists(ctx, MediaMovie)
	tv, tvSource := s.certLists(ctx, MediaTV)
	return CertificationsResponse{
		Movie:  withDefaults(movie, MediaMovie),
		TV:     withDefaults(tv, MediaTV),
		Source: weakestSource(movieSource, tvSource),
	}
}

func withDefaults(list certList, mediaType string) map[string][]CertificationOption {
	out := make(map[string][]CertificationOption, len(list))
	for region, options := range list {
		copied := make([]CertificationOption, len(options))
		copy(copied, options)
		if want, ok := suggestedDefaults[mediaType][region]; ok {
			for i := range copied {
				if normalizeCert(copied[i].Certification) == normalizeCert(want) {
					copied[i].Default = true
				}
			}
		}
		out[region] = copied
	}
	return out
}

// Validate checks a policy an admin submitted against the live schemes and
// normalises it in place (upper-cased region, the list's own spelling of
// each cap, sorted genre ids). A region other than US cannot be validated
// while the lists are unreachable; that is ErrUnavailable, not a rejection.
func (s *Service) Validate(ctx context.Context, p *Policy) error {
	if p == nil {
		return &ValidationError{Message: "missing policy"}
	}
	region := strings.ToUpper(strings.TrimSpace(p.RatingRegion))
	if region == "" {
		region = "US"
	}
	if len(region) != 2 || strings.ToUpper(region) != region || strings.IndexFunc(region, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
		return &ValidationError{Message: "unknown ratings region"}
	}
	movie, movieSource := s.certLists(ctx, MediaMovie)
	tv, tvSource := s.certLists(ctx, MediaTV)
	if region != "US" && (movieSource == SourceBuiltin || tvSource == SourceBuiltin) {
		return ErrUnavailable
	}
	if _, ok := movie[region]; !ok {
		return &ValidationError{Message: "unknown ratings region"}
	}
	if _, ok := tv[region]; !ok {
		return &ValidationError{Message: "unknown ratings region"}
	}
	movieCap, ok := movie.canonical(region, p.MaxMovieRating)
	if !ok {
		return &ValidationError{Message: fmt.Sprintf("movie rating %q is not part of the %s scheme", strings.TrimSpace(p.MaxMovieRating), region)}
	}
	tvCap, ok := tv.canonical(region, p.MaxTVRating)
	if !ok {
		return &ValidationError{Message: fmt.Sprintf("TV rating %q is not part of the %s scheme", strings.TrimSpace(p.MaxTVRating), region)}
	}
	p.RatingRegion = region
	p.MaxMovieRating = movieCap
	p.MaxTVRating = tvCap
	p.BlockedMovieGenres = normalizeGenreIDs(p.BlockedMovieGenres)
	p.BlockedTVGenres = normalizeGenreIDs(p.BlockedTVGenres)
	return nil
}
