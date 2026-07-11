package mcp

import (
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

func TestFilterRadarrQueueIntersectsCandidateQueueAndTMDB(t *testing.T) {
	items := []radarr.DetailedQueueItem{
		{ID: 7, DownloadID: "old", Movie: &radarr.MovieContext{ID: 1, TmdbID: 99}},
		{ID: 8, DownloadID: "current", Movie: &radarr.MovieContext{ID: 2, TmdbID: 42}},
	}
	matched, err := filterRadarrQueue(nil, items, mediaReadScope{QueueID: 7, TmdbID: 42})
	if err != nil {
		t.Fatalf("filterRadarrQueue: %v", err)
	}
	if len(matched) != 0 {
		t.Fatalf("unrelated candidate queue row survived: %+v", matched)
	}
	matched, err = filterRadarrQueue(nil, items, mediaReadScope{TmdbID: 42})
	if err != nil || len(matched) != 1 || matched[0].ID != 8 {
		t.Fatalf("matching movie filter = %+v, %v", matched, err)
	}
	matched, err = filterRadarrQueue(nil, items, mediaReadScope{QueueID: 8, DownloadID: "stale", TmdbID: 42})
	if err != nil || len(matched) != 0 {
		t.Fatalf("reassigned download survived filter = %+v, %v", matched, err)
	}
}

func TestReleaseCandidateMetadataUsesOneWayReference(t *testing.T) {
	const secret = "indexer-capability-secret"
	rawGUID := "https://indexer.invalid/download/signed-path-sentinel?id=7&apikey=" + secret
	release := radarr.Release{
		GUID:      rawGUID,
		IndexerID: 3, Indexer: "Example", Title: "Movie.2026.1080p", Size: 1024,
		Protocol: "usenet", Rejected: true, Rejections: []string{"Not an upgrade"},
	}
	release.Quality.Quality.Name = "WEBDL-1080p"
	candidates := radarrReleaseCandidates([]radarr.Release{release})
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	got := candidates[0]
	if got.Reference != releaseGUIDReference(rawGUID) || !isReleaseGUIDReference(got.Reference) ||
		strings.Contains(got.Reference, secret) || strings.Contains(got.Reference, "signed-path-sentinel") {
		t.Fatalf("unsafe release reference = %q", got.Reference)
	}
	if got.Title != release.Title || got.Quality != "WEBDL-1080p" || got.Size != 1024 ||
		got.Protocol != "usenet" || got.Indexer != "Example" || !got.Rejected || len(got.Rejections) != 1 {
		t.Fatalf("candidate metadata = %+v", got)
	}
}

func TestFilterSonarrQueueIntersectsSeriesAndEpisode(t *testing.T) {
	series := &sonarr.SeriesContext{ID: 1, TmdbID: 42, TvdbID: 4242}
	items := []sonarr.DetailedQueueItem{
		{ID: 7, Series: series, Episode: &sonarr.EpisodeContext{SeasonNumber: 2, EpisodeNumber: 7}},
		{ID: 8, Series: series, Episode: &sonarr.EpisodeContext{SeasonNumber: 2, EpisodeNumber: 8}},
		{ID: 9, Series: &sonarr.SeriesContext{ID: 2, TmdbID: 99, TvdbID: 9999}, Episode: &sonarr.EpisodeContext{SeasonNumber: 2, EpisodeNumber: 7}},
	}
	matched, err := filterSonarrQueue(nil, items, mediaReadScope{TmdbID: 42, TvdbID: 4242, SeasonNumber: 2, EpisodeNumber: 7})
	if err != nil {
		t.Fatalf("filterSonarrQueue: %v", err)
	}
	if len(matched) != 1 || matched[0].ID != 7 {
		t.Fatalf("scoped TV matches = %+v, want only queue 7", matched)
	}
}

func TestQueueTargetVerificationRequiresExactScope(t *testing.T) {
	if got := queueTargetVerification(false, 0); got != nil {
		t.Fatalf("unscoped queue read produced verification: %+v", got)
	}
	absent := queueTargetVerification(true, 0)
	if absent == nil || absent.Kind != VerificationQueueTarget || !absent.ExactScope || absent.TargetPresent {
		t.Fatalf("exact absent verification = %+v", absent)
	}
	present := queueTargetVerification(true, 1)
	if present == nil || !present.TargetPresent {
		t.Fatalf("exact present verification = %+v", present)
	}
}
