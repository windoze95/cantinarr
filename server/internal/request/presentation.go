package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/mediametadata"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// PresentationSource reads catalog text only. It never owns library truth,
// title authorization, request identities, or provider mutations.
type PresentationSource interface {
	Presentation(context.Context, string, int, string) (*mediametadata.Presentation, error)
	Episode(context.Context, int, int, int, int, string) (*mediametadata.Presentation, error)
}

func (s *Service) SetMetadataService(source PresentationSource) { s.metadata = source }

type PresentationReference struct {
	Key        string `json:"key"`
	MediaType  string `json:"media_type"`
	TMDBID     int    `json:"tmdb_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	RecordID   int    `json:"record_id,omitempty"`
	EpisodeID  int    `json:"episode_id,omitempty"`
}

type PresentationBatch struct {
	Presentations map[string]*mediametadata.Presentation `json:"presentations"`
	Unavailable   map[string]string                      `json:"unavailable"`
	Scope         string                                 `json:"scope"`
}

func (ref PresentationReference) valid() bool {
	if strings.TrimSpace(ref.Key) == "" || len(ref.Key) > 200 || (ref.MediaType != "movie" && ref.MediaType != "tv") {
		return false
	}
	if ref.InstanceID == "" {
		return ref.TMDBID > 0 && ref.RecordID == 0 && ref.EpisodeID == 0
	}
	return ref.RecordID > 0 && ref.TMDBID == 0 && ref.EpisodeID >= 0 && (ref.MediaType == "tv" || ref.EpisodeID == 0)
}

// GetPresentations batches only the records a requester is displaying. Raw
// provider libraries remain untouched, including fields used for edits.
func (h *Handler) GetPresentations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		References []PresentationReference `json:"references"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || len(input.References) == 0 || len(input.References) > 50 {
		writeJSON(w, 400, map[string]string{"error": "provide between 1 and 50 exact metadata references"})
		return
	}
	keys := map[string]bool{}
	for _, ref := range input.References {
		if !ref.valid() || keys[ref.Key] {
			writeJSON(w, 400, map[string]string{"error": "invalid or duplicate metadata reference"})
			return
		}
		keys[ref.Key] = true
	}
	out, err := h.service.Presentations(r.Context(), claims.UserID, input.References, mediametadata.Language(r))
	if err != nil {
		writeJSON(w, requestErrorStatus(err), map[string]string{"code": "metadata_access_unavailable", "error": "metadata access unavailable; refresh and retry"})
		return
	}
	writeJSON(w, 200, out)
}

// Presentations authorizes each reference before reading shared metadata and
// rechecks every reference after all enrichment, so a slow sibling cannot
// return another record after its grant or content policy was revoked.
func (s *Service) Presentations(ctx context.Context, userID int64, refs []PresentationReference, language string) (*PresentationBatch, error) {
	ctx = PresentationContext(ctx)
	enrichment, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := &PresentationBatch{Presentations: map[string]*mediametadata.Presentation{}, Unavailable: map[string]string{}, Scope: "Only the supplied exact catalog or library identities were checked. Missing presentation means translation or identity could not be resolved; it does not mean the title is absent."}
	if len(refs) > 50 {
		return nil, errors.New("too many metadata references")
	}
	type result struct {
		p   *mediametadata.Presentation
		err error
	}
	results := make([]result, len(refs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, ref := range refs {
		wg.Add(1)
		go func(i int, ref PresentationReference) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-enrichment.Done():
				return
			}
			if ref.InstanceID != "" {
				results[i].p, results[i].err = s.NativePresentation(enrichment, userID, ref.MediaType, ref.InstanceID, ref.RecordID, ref.EpisodeID, language)
			} else {
				results[i].p, results[i].err = s.catalogPresentation(enrichment, userID, ref.MediaType, ref.TMDBID, language)
			}
		}(i, ref)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	for i, ref := range refs {
		if results[i].err != nil {
			return nil, results[i].err
		}
		if err := s.recheckPresentationAccess(ctx, userID, ref); err != nil {
			return nil, err
		}
		if p := results[i].p; p != nil && p.TMDBID > 0 {
			if err := s.checkContentPolicy(userID, s.userIsAdmin(userID), p.MediaType, p.TMDBID); err != nil {
				return nil, err
			}
		}
		if results[i].p == nil {
			out.Unavailable[ref.Key] = "metadata_or_identity_unavailable"
		} else {
			out.Presentations[ref.Key] = results[i].p
		}
	}
	return out, nil
}

func (s *Service) recheckPresentationAccess(ctx context.Context, userID int64, ref PresentationReference) error {
	if ref.InstanceID == "" {
		return s.checkContentPolicy(userID, s.userIsAdmin(userID), ref.MediaType, ref.TMDBID)
	}
	checkGrant := func() error {
		if ref.MediaType == "movie" {
			_, _, err := s.resolveRadarr(userID, ref.InstanceID)
			return err
		}
		_, _, err := s.resolveSonarr(userID, ref.InstanceID)
		return err
	}
	if err := checkGrant(); err != nil {
		return err
	}
	policy, err := s.contentPolicyFor(userID, s.userIsAdmin(userID))
	if err != nil || policy == nil {
		return err
	}
	// Optional translation may fail, be skipped, or have no catalog parent.
	// None of those outcomes proves that source-language fallback is safe for
	// a child. Use only completed native reads from this response; a missing
	// proof fails closed without starting another round of provider work.
	if ref.MediaType == "movie" {
		movie, ok := completedPresentationRead[*radarr.Movie](ctx, fmt.Sprintf("movie:%s:%d", ref.InstanceID, ref.RecordID))
		if !ok || movie == nil || movie.ID != ref.RecordID || movie.TmdbID <= 0 {
			return ErrContentPolicyUnavailable
		}
		if err := s.checkContentPolicy(userID, false, "movie", movie.TmdbID); err != nil {
			return err
		}
		return checkGrant()
	}
	series, ok := completedPresentationRead[*sonarr.Series](ctx, fmt.Sprintf("series:%s:%d", ref.InstanceID, ref.RecordID))
	if !ok || series == nil || series.ID != ref.RecordID {
		return ErrContentPolicyUnavailable
	}
	if ref.EpisodeID > 0 {
		episodes, ok := completedPresentationRead[[]sonarr.Episode](ctx, fmt.Sprintf("episodes:%s:%d", ref.InstanceID, ref.RecordID))
		if !ok {
			return ErrContentPolicyUnavailable
		}
		count := 0
		for _, episode := range episodes {
			if episode.ID == ref.EpisodeID {
				if episode.SeriesID != ref.RecordID {
					return ErrContentPolicyUnavailable
				}
				count++
			}
		}
		if count != 1 {
			return ErrContentPolicyUnavailable
		}
	}
	s.tvMatchMu.Lock()
	corrected, candidates, err := s.importTVMatches(series)
	s.tvMatchMu.Unlock()
	if err != nil {
		return ErrContentPolicyUnavailable
	}
	configuration, _ := json.Marshal([]any{corrected, candidates})
	ids := map[int]bool{}
	if !corrected {
		if series.TmdbID <= 0 {
			return ErrContentPolicyUnavailable
		}
		ids[series.TmdbID] = true
	} else {
		for _, season := range series.Seasons {
			if season.SeasonNumber <= 0 {
				continue
			}
			owner, count := 0, 0
			for _, candidate := range candidates {
				for _, target := range candidate.SeasonMap {
					if target == season.SeasonNumber {
						owner, count = candidate.TmdbID, count+1
					}
				}
			}
			if count != 1 || owner <= 0 {
				return ErrContentPolicyUnavailable
			}
			ids[owner] = true
		}
		if len(ids) == 0 {
			return ErrContentPolicyUnavailable
		}
	}
	for id := range ids {
		if err := s.checkContentPolicy(userID, false, "tv", id); err != nil {
			return err
		}
	}
	if err := checkGrant(); err != nil {
		return err
	}
	// Rating reads may contact TMDB. Correction edits remain unlocked during
	// those reads, then invalidate the proof if its ownership changed.
	s.tvMatchMu.Lock()
	currentCorrected, currentCandidates, err := s.importTVMatches(series)
	s.tvMatchMu.Unlock()
	current, _ := json.Marshal([]any{currentCorrected, currentCandidates})
	if err != nil || string(current) != string(configuration) {
		return ErrContentPolicyUnavailable
	}
	return nil
}

func completedPresentationRead[T any](ctx context.Context, key string) (T, bool) {
	var zero T
	reads, _ := ctx.Value(presentationContextKey{}).(*nativePresentationReads)
	if reads == nil {
		return zero, false
	}
	reads.mu.Lock()
	entry := reads.entries[key]
	reads.mu.Unlock()
	if entry == nil {
		return zero, false
	}
	select {
	case <-entry.done:
		value, ok := entry.value.(T)
		return value, ok && entry.err == nil
	default:
		return zero, false
	}
}

func (s *Service) catalogPresentation(ctx context.Context, userID int64, kind string, id int, language string) (*mediametadata.Presentation, error) {
	if err := s.checkContentPolicy(userID, s.userIsAdmin(userID), kind, id); err != nil {
		return nil, err
	}
	if s.metadata == nil || language == "" {
		return nil, nil
	}
	p, err := s.metadata.Presentation(ctx, kind, id, language)
	// Catalog outages only remove optional display text. A failed policy read
	// is handled above/below and never becomes a source-language fallback.
	if err != nil {
		p = nil
	}
	if p != nil {
		copy := *p
		p = &copy
		p.TMDBID = id
		p.MediaType = kind
	}
	if err := s.checkContentPolicy(userID, s.userIsAdmin(userID), kind, id); err != nil {
		return nil, err
	}
	return p, nil
}

// NativePresentation resolves identities on the server. A positive Sonarr
// tmdbId alone does not make a corrected/composite parent a catalog title.
func (s *Service) NativePresentation(ctx context.Context, userID int64, kind, instanceID string, recordID, episodeID int, language string) (out *mediametadata.Presentation, resultErr error) {
	if instanceID == "" || recordID <= 0 {
		return nil, ErrArrInstanceInvalid
	}
	ctx = PresentationContext(ctx)
	defer func() {
		if resultErr == nil {
			ref := PresentationReference{MediaType: kind, InstanceID: instanceID, RecordID: recordID, EpisodeID: episodeID}
			if err := s.recheckPresentationAccess(ctx, userID, ref); err != nil {
				out, resultErr = nil, err
			}
		}
	}()
	if kind == "movie" {
		client, _, err := s.resolveRadarr(userID, instanceID)
		if err != nil {
			return nil, err
		}
		if client == nil {
			return nil, ErrArrInstanceInvalid
		}
		movie, err := presentationRead(ctx, fmt.Sprintf("movie:%s:%d", instanceID, recordID), func() (*radarr.Movie, error) { return client.GetMovieContext(ctx, recordID) })
		if err != nil || movie == nil || movie.ID != recordID || movie.TmdbID <= 0 {
			return nil, nil
		}
		p, err := s.catalogPresentation(ctx, userID, kind, movie.TmdbID, language)
		if err != nil {
			return nil, err
		}
		if _, _, err = s.resolveRadarr(userID, instanceID); err != nil {
			return nil, err
		}
		return p, nil
	}
	if kind != "tv" {
		return nil, ErrArrInstanceInvalid
	}
	client, _, err := s.resolveSonarr(userID, instanceID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, ErrArrInstanceInvalid
	}
	series, err := presentationRead(ctx, fmt.Sprintf("series:%s:%d", instanceID, recordID), func() (*sonarr.Series, error) { return client.GetSeriesContext(ctx, recordID) })
	if err != nil || series == nil || series.ID != recordID {
		return nil, nil
	}
	s.tvMatchMu.Lock()
	corrected, candidates, err := s.importTVMatches(series)
	s.tvMatchMu.Unlock()
	if err != nil {
		return nil, nil
	}
	configuration, _ := json.Marshal([]any{corrected, candidates})
	id := series.TmdbID
	var native *sonarr.Episode
	sourceSeason := 0
	if episodeID > 0 {
		episodes, e := presentationRead(ctx, fmt.Sprintf("episodes:%s:%d", instanceID, recordID), func() ([]sonarr.Episode, error) { return client.GetAllEpisodesContext(ctx, series.ID) })
		if e != nil {
			return nil, nil
		}
		for i := range episodes {
			if episodes[i].ID == episodeID {
				if native != nil {
					return nil, nil
				}
				copy := episodes[i]
				native = &copy
			}
		}
		if native == nil || native.SeriesID != series.ID || native.TVDBID <= 0 {
			return nil, nil
		}
		if _, e = resolveCanonicalEpisode(CanonicalEpisode{TVDBID: native.TVDBID}, series.ID, episodes); e != nil {
			return nil, nil
		}
		sourceSeason = native.SeasonNumber
		if corrected {
			id = 0
			var owner *TVMatch
			for i := range candidates {
				for source, target := range candidates[i].SeasonMap {
					if target == native.SeasonNumber {
						if owner != nil {
							return nil, nil
						}
						copy := candidates[i]
						owner = &copy
						sourceSeason = source
					}
				}
			}
			if owner == nil {
				return nil, nil
			}
			// The configured mapping nominates a candidate only. Episode below
			// proves its exact external identity against this live native series;
			// no whole-season catalog scan or fuzzy series lookup is needed.
			verified, _, e := configuredTVMatch(s.db, owner.TmdbID)
			if e != nil || verified.State == "paused" || verified.Revision != owner.Revision || verified.TVDBID != series.TvdbID || verified.SeasonMap[sourceSeason] != native.SeasonNumber {
				return nil, nil
			}
			id = verified.TmdbID
		}
	} else if corrected {
		return nil, nil
	}
	if id <= 0 {
		return nil, nil
	}
	p, err := s.catalogPresentation(ctx, userID, kind, id, language)
	if err != nil {
		return nil, err
	}
	if p != nil {
		copy := *p
		p = &copy
	}
	if corrected {
		p = nil
	} // a story's artwork/name never replaces its combined parent
	if native != nil && s.metadata != nil && language != "" {
		ep, e := s.metadata.Episode(ctx, id, sourceSeason, native.EpisodeNumber, native.TVDBID, language)
		if e == nil && ep != nil && ep.Title != "" {
			if p == nil {
				p = &mediametadata.Presentation{TMDBID: id, MediaType: kind}
			}
			p.EpisodeTitle = ep.Title
		}
	}
	if _, _, err = s.resolveSonarr(userID, instanceID); err != nil {
		return nil, err
	}
	if err = s.checkContentPolicy(userID, s.userIsAdmin(userID), kind, id); err != nil {
		return nil, err
	}
	// No provider/network reads hold the global correction mutex. Recheck the
	// captured configuration only after enrichment, before exposing its label.
	s.tvMatchMu.Lock()
	currentCorrected, currentCandidates, err := s.importTVMatches(series)
	s.tvMatchMu.Unlock()
	if err != nil {
		return nil, nil
	}
	current, _ := json.Marshal([]any{currentCorrected, currentCandidates})
	if string(current) != string(configuration) {
		return nil, nil
	}
	return p, nil
}
