package remediation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

func TestValidateQueueItemRejectsUnrelatedMovieForUserIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v3/queue") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"totalRecords": 2,
			"records": []map[string]any{
				{"id": 11, "movieId": 1, "downloadId": "wrong", "movie": map[string]any{"id": 1, "tmdbId": 99}},
				{"id": 12, "movieId": 2, "downloadId": "right", "movie": map[string]any{"id": 2, "tmdbId": 42}},
			},
		})
	}))
	defer server.Close()

	executor := &Executor{}
	client := radarr.NewClient(server.URL, "test")
	ic := issueContext{mediaType: "movie", tmdbID: 42}
	if err := executor.validateQueueItem("movie", 11, ic, client, nil, nil); err == nil || !strings.Contains(err.Error(), "not this issue's TMDB 42") {
		t.Fatalf("unrelated movie validation error = %v", err)
	}
	if err := executor.validateQueueItem("movie", 12, ic, client, nil, nil); err != nil {
		t.Fatalf("matching movie rejected: %v", err)
	}
}

func TestValidateQueueItemResolvesMissingEmbeddedMovieIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v3/queue"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"totalRecords": 1,
				"records":      []map[string]any{{"id": 12, "movieId": 2, "downloadId": "right"}},
			})
		case r.URL.Path == "/api/v3/movie/2":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 2, "tmdbId": 42})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	executor := &Executor{}
	if err := executor.validateQueueItem("movie", 12,
		issueContext{mediaType: "movie", tmdbID: 42}, radarr.NewClient(server.URL, "test"), nil, nil); err != nil {
		t.Fatalf("matching fallback identity rejected: %v", err)
	}
}

func TestValidateQueueItemBindsSonarrTitleSeasonAndEpisode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v3/queue") {
			http.NotFound(w, r)
			return
		}
		series := map[string]any{"id": 4, "tmdbId": 42, "tvdbId": 4242}
		otherSeries := map[string]any{"id": 5, "tmdbId": 99, "tvdbId": 9999}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"totalRecords": 3,
			"records": []map[string]any{
				{"id": 21, "seriesId": 4, "episodeId": 201, "series": series, "episode": map[string]any{"seasonNumber": 2, "episodeNumber": 7}},
				{"id": 22, "seriesId": 4, "episodeId": 202, "series": series, "episode": map[string]any{"seasonNumber": 2, "episodeNumber": 8}},
				{"id": 23, "seriesId": 5, "episodeId": 203, "series": otherSeries, "episode": map[string]any{"seasonNumber": 2, "episodeNumber": 7}},
			},
		})
	}))
	defer server.Close()

	executor := &Executor{}
	client := sonarr.NewClient(server.URL, "test")
	ic := issueContext{mediaType: "tv", tmdbID: 42, tvdbID: 4242, seasonNumber: 2, episodeNumber: 7}
	if err := executor.validateQueueItem("tv", 21, ic, nil, client, nil); err != nil {
		t.Fatalf("matching episode rejected: %v", err)
	}
	if err := executor.validateQueueItem("tv", 22, ic, nil, client, nil); err == nil || !strings.Contains(err.Error(), "S02E07") {
		t.Fatalf("wrong episode validation error = %v", err)
	}
	if err := executor.validateQueueItem("tv", 23, ic, nil, client, nil); err == nil || !strings.Contains(err.Error(), "not this issue's TMDB 42") {
		t.Fatalf("wrong series validation error = %v", err)
	}
}

func TestValidateQueueItemFailsClosedWhenRecordedDownloadIdentityDisappears(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"totalRecords": 1,
			"records": []map[string]any{{
				"id": 12, "movieId": 2, "downloadId": "",
				"movie": map[string]any{"id": 2, "tmdbId": 42},
			}},
		})
	}))
	defer server.Close()

	err := (&Executor{}).validateQueueItem("movie", 12,
		issueContext{mediaType: "movie", tmdbID: 42, downloadID: "recorded"},
		radarr.NewClient(server.URL, "test"), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "reassigned") {
		t.Fatalf("missing live download identity error = %v", err)
	}
}

func TestValidateQueueItemRejectsDifferentRecordedQueueBeforeFetching(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	err := (&Executor{}).validateQueueItem("movie", 12,
		issueContext{mediaType: "movie", tmdbID: 42, arrQueueID: 7},
		radarr.NewClient(server.URL, "test"), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "issue's queue item 7") {
		t.Fatalf("queue invariant error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("validation fetched arr %d time(s) after exact queue mismatch", requests)
	}
}

func TestValidateGrabReleaseCandidateRequiresFreshScopedMovieTuple(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie":
			if r.URL.Query().Get("tmdbId") != "42" {
				t.Fatalf("movie lookup query = %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 5, "tmdbId": 42}})
		case "/api/v3/release":
			if r.URL.Query().Get("movieId") != "5" {
				t.Fatalf("release search query = %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"guid": "scoped-guid", "indexerId": 3}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	executor := &Executor{}
	client := radarr.NewClient(server.URL, "test")
	ic := issueContext{mediaType: "movie", tmdbID: 42}
	if liveGUID, err := executor.validateGrabReleaseCandidate(
		GrabReleaseParams{MediaType: "movie", GUID: "scoped-guid", IndexerID: 3}, ic, client, nil, nil,
	); err != nil || liveGUID != "scoped-guid" {
		t.Fatalf("fresh scoped release rejected: %v", err)
	}
	if _, err := executor.validateGrabReleaseCandidate(
		GrabReleaseParams{MediaType: "movie", GUID: "invented-guid", IndexerID: 3}, ic, client, nil, nil,
	); err == nil || !strings.Contains(err.Error(), "not present in a fresh search") {
		t.Fatalf("invented release validation error = %v", err)
	}
}

func TestValidateGrabReleaseCandidateResolvesScrubbedCapabilityAtDispatch(t *testing.T) {
	const liveGUID = "https://indexer.invalid/download?id=77&apikey=credential-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 5, "tmdbId": 42}})
		case "/api/v3/release":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"guid": liveGUID, "indexerId": 3}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	for _, safeReference := range []string{secrets.RedactText(liveGUID), releaseGUIDFingerprint(liveGUID)} {
		resolved, err := (&Executor{}).validateGrabReleaseCandidate(
			GrabReleaseParams{MediaType: "movie", GUID: safeReference, IndexerID: 3},
			issueContext{mediaType: "movie", tmdbID: 42}, radarr.NewClient(server.URL, "test"), nil, nil,
		)
		if err != nil || resolved != liveGUID {
			t.Fatalf("safe reference %q resolved to %q, %v", safeReference, resolved, err)
		}
	}
}

func TestValidateGrabReleaseCandidateRejectsChangedObservedMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 5, "tmdbId": 42}})
		case "/api/v3/release":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"guid": "candidate", "indexerId": 3, "indexer": "Example", "title": "Movie.1080p",
				"size": 2048, "protocol": "usenet",
				"quality": map[string]any{"quality": map[string]any{"name": "WEBDL-1080p"}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p := GrabReleaseParams{
		MediaType: "movie", GUID: "candidate", IndexerID: 3,
		ReleaseTitle: "Movie.1080p", Quality: "WEBDL-1080p", Size: 2048,
		Protocol: "usenet", Indexer: "Example",
	}
	executor := &Executor{}
	client := radarr.NewClient(server.URL, "test")
	if _, err := executor.validateGrabReleaseCandidate(p,
		issueContext{mediaType: "movie", tmdbID: 42}, client, nil, nil); err != nil {
		t.Fatalf("matching candidate metadata rejected: %v", err)
	}
	p.Quality = "HDTV-1080p"
	if _, err := executor.validateGrabReleaseCandidate(p,
		issueContext{mediaType: "movie", tmdbID: 42}, client, nil, nil); err == nil || !strings.Contains(err.Error(), "metadata changed") {
		t.Fatalf("changed candidate metadata error = %v", err)
	}
}

func TestValidateGrabReleaseCandidateUsesEpisodeSpecificSonarrSearch(t *testing.T) {
	searches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/series":
			if r.URL.Query().Get("tvdbId") != "4242" {
				t.Fatalf("series lookup query = %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 4, "tmdbId": 42, "tvdbId": 4242}})
		case "/api/v3/episode":
			if r.URL.Query().Get("seriesId") != "4" || r.URL.Query().Get("seasonNumber") != "2" {
				t.Fatalf("episode lookup query = %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 207, "seriesId": 4, "seasonNumber": 2, "episodeNumber": 7},
				{"id": 208, "seriesId": 4, "seasonNumber": 2, "episodeNumber": 8},
			})
		case "/api/v3/release":
			searches++
			if r.URL.Query().Get("episodeId") != "207" || r.URL.Query().Get("seriesId") != "" {
				t.Fatalf("release search was not episode-scoped: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"guid": "episode-guid", "indexerId": 6}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := (&Executor{}).validateGrabReleaseCandidate(
		GrabReleaseParams{MediaType: "tv", GUID: "episode-guid", IndexerID: 6},
		issueContext{mediaType: "tv", tmdbID: 42, tvdbID: 4242, seasonNumber: 2, episodeNumber: 7},
		nil, sonarr.NewClient(server.URL, "test"), nil,
	)
	if err != nil {
		t.Fatalf("episode-scoped release rejected: %v", err)
	}
	if searches != 1 {
		t.Fatalf("episode searches = %d, want 1", searches)
	}
}
