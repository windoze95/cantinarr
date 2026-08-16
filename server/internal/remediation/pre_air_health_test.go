package remediation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// A season the service filled before its episodes aired is decidable at the
// moment the file lands, and the incident that prompted all of this ran for
// thirteen days and nine impossible files before a human noticed. What these
// tests hold in place is the shape of the answer rather than the fact that one
// is produced: ONE issue for the SEASON however many files arrive, opened where
// the agent will actually pick it up, and silence in every case where the
// server cannot honestly say the content is impossible.

const preAirSonarrID = "sonarr-pre-air"

// --- fake Sonarr ---

// preAirFake serves the four reads this feature makes (series lookup, one
// season's episodes, the series' files, recent history) and records every URI,
// so a test can assert not just what was concluded but what was asked.
type preAirFake struct {
	t *testing.T

	mu                sync.Mutex
	requests          []string
	series            []map[string]any
	episodes          []map[string]any
	files             []map[string]any
	history           []map[string]any
	queue             []map[string]any
	queueDeletes      []string
	queueDeleteStatus int
	queueGetStatus    int
	seriesHistory     []map[string]any
	indexers          []map[string]any
	failedGrabs       []string
	fileDeletes       []string
	episodeStatus     int
	historyStatus     int
}

func (f *preAirFake) start() *httptest.Server {
	f.t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.RequestURI())
		series := copyPreAirRecords(f.series)
		episodeStatus, historyStatus := f.episodeStatus, f.historyStatus
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query()
		switch {
		case r.URL.Path == "/api/v3/series" && query.Get("tvdbId") != "":
			_ = json.NewEncoder(w).Encode(matchingPreAirRecords(series, "tvdbId", query.Get("tvdbId")))
		case r.URL.Path == "/api/v3/series":
			_ = json.NewEncoder(w).Encode(series)
		case strings.HasPrefix(r.URL.Path, "/api/v3/series/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v3/series/")
			for _, record := range series {
				if fmt.Sprint(record["id"]) == id {
					_ = json.NewEncoder(w).Encode(record)
					return
				}
			}
			http.NotFound(w, r)
		case r.URL.Path == "/api/v3/episode":
			if episodeStatus != 0 {
				w.WriteHeader(episodeStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(f.episodesFor(query.Get("seriesId"), query.Get("seasonNumber")))
		case r.URL.Path == "/api/v3/episodefile":
			_ = json.NewEncoder(w).Encode(f.filesFor(query.Get("seriesId")))
		case r.URL.Path == "/api/v3/history":
			if historyStatus != 0 {
				w.WriteHeader(historyStatus)
				return
			}
			f.mu.Lock()
			records := copyPreAirRecords(f.history)
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": len(records), "records": records})
		case r.URL.Path == "/api/v3/history/series":
			f.mu.Lock()
			records := copyPreAirRecords(f.seriesHistory)
			f.mu.Unlock()
			// Bare JSON array — /history/series has no records envelope.
			_ = json.NewEncoder(w).Encode(records)
		case strings.HasPrefix(r.URL.Path, "/api/v3/history/failed/") && r.Method == http.MethodPost:
			f.mu.Lock()
			f.failedGrabs = append(f.failedGrabs, strings.TrimPrefix(r.URL.Path, "/api/v3/history/failed/"))
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.URL.Path == "/api/v3/config/downloadclient":
			_ = json.NewEncoder(w).Encode(map[string]any{"autoRedownloadFailed": true})
		case strings.HasPrefix(r.URL.Path, "/api/v3/episodefile/") && r.Method == http.MethodDelete:
			id := strings.TrimPrefix(r.URL.Path, "/api/v3/episodefile/")
			f.mu.Lock()
			f.fileDeletes = append(f.fileDeletes, id)
			keptFiles := f.files[:0]
			for _, file := range f.files {
				if fmt.Sprint(file["id"]) != id {
					keptFiles = append(keptFiles, file)
				}
			}
			f.files = keptFiles
			for _, ep := range f.episodes {
				if fmt.Sprint(ep["episodeFileId"]) == id {
					ep["hasFile"] = false
					delete(ep, "episodeFileId")
				}
			}
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.URL.Path == "/api/v3/indexer":
			f.mu.Lock()
			records := copyPreAirRecords(f.indexers)
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(records)
		case r.URL.Path == "/api/v3/queue" && r.Method == http.MethodGet:
			f.mu.Lock()
			records := copyPreAirRecords(f.queue)
			queueGetStatus := f.queueGetStatus
			f.mu.Unlock()
			if queueGetStatus != 0 {
				w.WriteHeader(queueGetStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"totalRecords": len(records), "records": records})
		case strings.HasPrefix(r.URL.Path, "/api/v3/queue/") && r.Method == http.MethodDelete:
			id := strings.TrimPrefix(r.URL.Path, "/api/v3/queue/")
			f.mu.Lock()
			if f.queueDeleteStatus != 0 {
				status := f.queueDeleteStatus
				f.mu.Unlock()
				w.WriteHeader(status)
				return
			}
			f.queueDeletes = append(f.queueDeletes, r.URL.RequestURI())
			kept := f.queue[:0]
			for _, row := range f.queue {
				if fmt.Sprint(row["id"]) != id {
					kept = append(kept, row)
				}
			}
			f.queue = kept
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			f.t.Errorf("unexpected sonarr request %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	f.t.Cleanup(srv.Close)
	return srv
}

func (f *preAirFake) episodesFor(seriesID, season string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0, len(f.episodes))
	for _, ep := range f.episodes {
		if fmt.Sprint(ep["seriesId"]) == seriesID && fmt.Sprint(ep["seasonNumber"]) == season {
			out = append(out, ep)
		}
	}
	return copyPreAirRecords(out)
}

// filesFor mirrors Sonarr: /episodefile is scoped to the SERIES, not the
// season, so the join that stamps an import time onto an episode has to be done
// by file id and not by position.
func (f *preAirFake) filesFor(seriesID string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0, len(f.files))
	for _, file := range f.files {
		if fmt.Sprint(file["seriesId"]) == seriesID {
			out = append(out, file)
		}
	}
	return copyPreAirRecords(out)
}

func (f *preAirFake) setLibrary(episodes, files []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.episodes, f.files = episodes, files
}

func (f *preAirFake) setHistory(records []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.history = records
}

func (f *preAirFake) setQueue(records []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = records
}

func (f *preAirFake) setQueueDeleteStatus(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queueDeleteStatus = code
}

// setQueueGetStatus makes every queue READ answer with an HTTP error — an
// expired API key (401) or an arr mid-restart (500) — without touching the
// mutation paths.
func (f *preAirFake) setQueueGetStatus(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queueGetStatus = code
}

func (f *preAirFake) setIndexers(records []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.indexers = records
}

func (f *preAirFake) queueDeletesSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queueDeletes...)
}

func (f *preAirFake) setSeriesHistory(records []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seriesHistory = records
}

func (f *preAirFake) mutationsSeen() (fileDeletes, failedGrabs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.fileDeletes...), append([]string(nil), f.failedGrabs...)
}

func (f *preAirFake) setEpisodeStatus(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.episodeStatus = code
}

func (f *preAirFake) setHistoryStatus(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.historyStatus = code
}

func (f *preAirFake) requestsSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *preAirFake) countRequests(substr string) int {
	n := 0
	for _, uri := range f.requestsSeen() {
		if strings.Contains(uri, substr) {
			n++
		}
	}
	return n
}

func copyPreAirRecords(records []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(records))
	for _, record := range records {
		clone := make(map[string]any, len(record))
		for key, value := range record {
			clone[key] = value
		}
		out = append(out, clone)
	}
	return out
}

func matchingPreAirRecords(records []map[string]any, field, want string) []map[string]any {
	out := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if fmt.Sprint(record[field]) == want {
			out = append(out, record)
		}
	}
	return out
}

// --- season fixtures ---

// preAirEpisode describes one episode relative to the real clock. The witness
// resolves "aired" from time.Now(), so a fixture pinned to a literal date would
// silently stop testing anything the day it went stale.
type preAirEpisode struct {
	number int
	// airsIn is measured from now: negative has aired, positive has not.
	airsIn  time.Duration
	hasFile bool
	// importedBeforeAir puts the file's arrival ahead of the episode's air time
	// — the sharper half of the evidence, and what a real bad grab looks like.
	importedBeforeAir bool
}

// buildPreAirSeason renders a season into the two records Sonarr actually
// serves: the episode list (air date, has-file) and the file list (import
// stamp). They are deliberately separate, because the join between them is
// where the whole finding lives.
func buildPreAirSeason(seriesID, season int, specs []preAirEpisode) (episodes, files []map[string]any) {
	now := time.Now().UTC()
	for _, spec := range specs {
		airsAt := now.Add(spec.airsIn)
		fileID := 50000 + season*100 + spec.number
		episode := map[string]any{
			"id":            seriesID*1000 + season*100 + spec.number,
			"seriesId":      seriesID,
			"seasonNumber":  season,
			"episodeNumber": spec.number,
			"title":         fmt.Sprintf("S%02dE%02d", season, spec.number),
			"airDateUtc":    airsAt.Format(time.RFC3339),
			"hasFile":       spec.hasFile,
		}
		if spec.hasFile {
			episode["episodeFileId"] = fileID
			importedAt := airsAt.Add(2 * time.Hour)
			if spec.importedBeforeAir {
				importedAt = airsAt.Add(-13 * 24 * time.Hour)
			}
			files = append(files, map[string]any{
				"id":           fileID,
				"seriesId":     seriesID,
				"seasonNumber": season,
				"dateAdded":    importedAt.Format(time.RFC3339),
				"sceneName":    fmt.Sprintf("Futurama.S%02dE%02d.1080p.DSNP.WEB-DL", season, spec.number),
			})
		}
		episodes = append(episodes, episode)
	}
	return episodes, files
}

// impossibleSeason is the live incident: nine files sitting in the library for
// a season whose first episode has not aired, plus the tenth episode with no
// file at all.
func impossibleSeason(seriesID, season int) (episodes, files []map[string]any) {
	specs := make([]preAirEpisode, 0, 10)
	for i := 1; i <= 10; i++ {
		specs = append(specs, preAirEpisode{
			number:            i,
			airsIn:            time.Duration(i) * 7 * 24 * time.Hour,
			hasFile:           i <= 9,
			importedBeforeAir: true,
		})
	}
	return buildPreAirSeason(seriesID, season, specs)
}

// --- service under test ---

func setupPreAirService(t *testing.T) (*Service, *fakeNotifier, *preAirFake) {
	t.Helper()
	fake := &preAirFake{
		t:      t,
		series: []map[string]any{{"id": 28, "title": "Futurama", "tvdbId": 73871, "tmdbId": 615}},
	}
	episodes, files := impossibleSeason(28, 11)
	fake.setLibrary(episodes, files)
	srv := fake.start()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x24}, 32))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	for _, inst := range []*instance.Instance{
		{ID: preAirSonarrID, ServiceType: "sonarr", Name: "TV", URL: srv.URL, APIKey: "key"},
		// A Radarr on the same fake: the fallback sweep must ask Sonarr and
		// nothing else, and an instance list that leaked movies would show up
		// here as a doubled history read rather than as a subtle miscount.
		{ID: "radarr-pre-air", ServiceType: "radarr", Name: "Movies", URL: srv.URL, APIKey: "key"},
	} {
		if err := store.Create(inst); err != nil {
			t.Fatalf("create %s instance: %v", inst.ServiceType, err)
		}
	}
	notifier := &fakeNotifier{}
	return NewService(database, instance.NewRegistry(store), nil, notifier), notifier, fake
}

func openIssueCount(t *testing.T, svc *Service) int {
	t.Helper()
	issues, _, err := svc.ListIssues("", 0)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	return len(issues)
}

func soleIssue(t *testing.T, svc *Service) Issue {
	t.Helper()
	issues, _, err := svc.ListIssues("", 0)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %#v, want exactly one", issues)
	}
	return issues[0]
}

func issueProblemKind(t *testing.T, svc *Service, issueID int64) string {
	t.Helper()
	var kind *string
	if err := svc.db.QueryRow("SELECT problem_kind FROM issues WHERE id = ?", issueID).Scan(&kind); err != nil {
		t.Fatalf("read problem_kind: %v", err)
	}
	if kind == nil {
		return ""
	}
	return *kind
}

func issueDedupeKey(t *testing.T, svc *Service, issueID int64) string {
	t.Helper()
	var key *string
	if err := svc.db.QueryRow("SELECT dedupe_key FROM issues WHERE id = ?", issueID).Scan(&key); err != nil {
		t.Fatalf("read dedupe_key: %v", err)
	}
	if key == nil {
		return ""
	}
	return *key
}

func countRows(t *testing.T, svc *Service, table string) int {
	t.Helper()
	var n int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// --- the witness ---

// TestRecordPreAirImportOpensOneSeasonIssue pins every field an admin, a
// standing approval rule and the agent each depend on. The scope is the season
// with episode_number 0, which is what makes nine files one problem.
func TestRecordPreAirImportOpensOneSeasonIssue(t *testing.T) {
	svc, notifier, _ := setupPreAirService(t)

	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama")

	issue := soleIssue(t, svc)
	if issue.Source != SourceAuto {
		t.Errorf("source = %q, want %q", issue.Source, SourceAuto)
	}
	if issue.MediaType != "tv" {
		t.Errorf("media_type = %q, want tv", issue.MediaType)
	}
	if issue.SeasonNumber != 11 || issue.EpisodeNumber != 0 {
		t.Errorf("scope = S%dE%d, want season 11 / episode 0", issue.SeasonNumber, issue.EpisodeNumber)
	}
	if issue.InstanceID != preAirSonarrID {
		t.Errorf("instance_id = %q, want %q", issue.InstanceID, preAirSonarrID)
	}
	if issue.TmdbID != 615 || issue.TvdbID != 73871 {
		t.Errorf("identity = tmdb %d / tvdb %d, want 615 / 73871", issue.TmdbID, issue.TvdbID)
	}
	if issue.Title != "Futurama" {
		t.Errorf("title = %q, want Futurama", issue.Title)
	}
	if issue.Occurrences != 1 {
		t.Errorf("occurrences = %d, want 1", issue.Occurrences)
	}
	// problem_kind is the persisted key a standing auto-approval rule matches
	// on; leaving it NULL (as the system health sinks do) would make this class
	// permanently un-automatable.
	if kind := issueProblemKind(t, svc, issue.ID); kind != arr.ProblemPreAirSeasonFill {
		t.Errorf("problem_kind = %q, want %q", kind, arr.ProblemPreAirSeasonFill)
	}
	if key := issueDedupeKey(t, svc, issue.ID); key != preAirScopeKey(preAirSonarrID, 73871, 615, 11) {
		t.Errorf("dedupe_key = %q, want the season scope key", key)
	}
	// The detail is the detector's own sentence, so the admin approves a
	// deletion on the same words the finding was made in.
	for _, want := range []string{
		"Season 11 already has files for 9 of its 10 episodes, and 9 of those episodes have not aired yet",
		"Content that has not been released yet cannot be what those files hold",
	} {
		if !strings.Contains(issue.Detail, want) {
			t.Errorf("detail = %q, want it to carry %q", issue.Detail, want)
		}
	}
	// Held, not paged: a batch import fires a webhook per file within minutes.
	if len(notifier.adminEvents) != 0 {
		t.Errorf("admin events = %v, want the alert held for the hold-down", notifier.adminEvents)
	}
	if n := countRows(t, svc, "issue_alert_queue"); n != 1 {
		t.Errorf("queued alerts = %d, want 1", n)
	}
}

// TestNineImportsIntoOneSeasonOpenOneIssue is the single most important
// assertion here. A season pack imports as nine separate webhooks minutes
// apart; nine issues would mean nine agent runs and nine approval cards for one
// decision, which is precisely the shape the repair itself refuses to take.
func TestNineImportsIntoOneSeasonOpenOneIssue(t *testing.T) {
	svc, _, _ := setupPreAirService(t)

	for i := 0; i < 9; i++ {
		svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama")
	}

	if n := openIssueCount(t, svc); n != 1 {
		t.Fatalf("nine imports opened %d issues, want exactly 1", n)
	}
	issue := soleIssue(t, svc)
	if issue.Occurrences != 9 {
		t.Errorf("occurrences = %d, want 9 — the repeats are counted, not discarded", issue.Occurrences)
	}
	if n := countRows(t, svc, "issue_alert_queue"); n != 1 {
		t.Errorf("queued alerts = %d, want 1 — nine imports are one page", n)
	}
}

// TestPreAirSeasonCanBeReportedAgainAfterItCloses: the dedupe guarantee is
// "one OPEN issue per season", not "one ever". A season that was repaired and
// then filled early a second time is a new incident, and a key that silenced it
// would make the second occurrence invisible for good.
func TestPreAirSeasonCanBeReportedAgainAfterItCloses(t *testing.T) {
	svc, _, _ := setupPreAirService(t)

	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama")
	first := soleIssue(t, svc)
	if err := svc.DismissIssue(first.ID); err != nil {
		t.Fatalf("DismissIssue: %v", err)
	}

	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama")

	issues, _, err := svc.ListIssues("", 0)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("issues = %d, want a fresh row alongside the closed one", len(issues))
	}
	for _, issue := range issues {
		if issue.ID != first.ID && (issue.Status != IssueOpen || issue.ClosedAt != nil) {
			t.Fatalf("re-reported issue = %#v, want a fresh open row", issue)
		}
	}
}

// TestRepeatedPreAirImportRefreshesRatherThanInserts: the second report of a
// season that has since grown a tenth impossible file must update what the
// issue says, not open a second row saying it.
func TestRepeatedPreAirImportRefreshesRatherThanInserts(t *testing.T) {
	svc, _, fake := setupPreAirService(t)

	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama")
	first := soleIssue(t, svc)
	if !strings.Contains(first.Detail, "files for 9 of its 10 episodes") {
		t.Fatalf("first detail = %q", first.Detail)
	}

	// The tenth file lands.
	specs := make([]preAirEpisode, 0, 10)
	for i := 1; i <= 10; i++ {
		specs = append(specs, preAirEpisode{
			number: i, airsIn: time.Duration(i) * 7 * 24 * time.Hour, hasFile: true, importedBeforeAir: true,
		})
	}
	fake.setLibrary(buildPreAirSeason(28, 11, specs))

	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama")

	second := soleIssue(t, svc)
	if second.ID != first.ID {
		t.Fatalf("issue id = %d, want the original %d", second.ID, first.ID)
	}
	if second.Occurrences != 2 {
		t.Errorf("occurrences = %d, want 2", second.Occurrences)
	}
	if !strings.Contains(second.Detail, "files for 10 of its 10 episodes") {
		t.Errorf("detail = %q, want it refreshed to the current count", second.Detail)
	}
}

// TestTwoSeasonsOfOneSeriesAreTwoProblems guards the other half of the scope
// key. Season-scoped must not collapse into series-scoped: two seasons filled
// early are two repairs, deleting two different sets of files, and folding them
// into one issue would silently drop one of them.
func TestTwoSeasonsOfOneSeriesAreTwoProblems(t *testing.T) {
	svc, _, fake := setupPreAirService(t)

	s11Episodes, s11Files := impossibleSeason(28, 11)
	s12Episodes, s12Files := impossibleSeason(28, 12)
	fake.setLibrary(append(s11Episodes, s12Episodes...), append(s11Files, s12Files...))

	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama")
	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 12, "Futurama")

	if n := openIssueCount(t, svc); n != 2 {
		t.Fatalf("two impossible seasons opened %d issues, want 2", n)
	}
	if preAirScopeKey(preAirSonarrID, 73871, 615, 11) == preAirScopeKey(preAirSonarrID, 73871, 615, 12) {
		t.Fatal("two seasons of one series share a scope key")
	}
	// The same season on a different Sonarr is still a different problem.
	if preAirScopeKey(preAirSonarrID, 73871, 615, 11) == preAirScopeKey("sonarr-other", 73871, 615, 11) {
		t.Fatal("two instances share a scope key")
	}
}

// TestPreAirWitnessDefersToTheDetectorsThreshold: the webhook only established
// that ONE file arrived early, which is an everyday air-date slip — a wrong
// date on the metadata service, a region that posted ahead of schedule. The
// threshold that separates that from a season of content nobody has released is
// the detector's, and the witness must not have a second opinion.
func TestPreAirWitnessDefersToTheDetectorsThreshold(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	fake.setLibrary(buildPreAirSeason(28, 11, []preAirEpisode{
		{number: 1, airsIn: -21 * 24 * time.Hour, hasFile: true},
		{number: 2, airsIn: -14 * 24 * time.Hour, hasFile: true},
		{number: 3, airsIn: -7 * 24 * time.Hour, hasFile: true},
		// Exactly one unaired episode holding a file: the near miss.
		{number: 4, airsIn: 7 * 24 * time.Hour, hasFile: true, importedBeforeAir: true},
		{number: 5, airsIn: 14 * 24 * time.Hour},
	}))

	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama")

	if n := openIssueCount(t, svc); n != 0 {
		t.Fatalf("one early file opened %d issues, want 0", n)
	}
}

// TestPreAirWitnessIgnoresASeasonThatArrivedOnTime is the false-positive guard
// at the other end: every file landed after the episode it holds had aired,
// which is what a healthy library looks like.
func TestPreAirWitnessIgnoresASeasonThatArrivedOnTime(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	specs := make([]preAirEpisode, 0, 6)
	for i := 1; i <= 6; i++ {
		specs = append(specs, preAirEpisode{
			number: i, airsIn: -time.Duration(30-i*3) * 24 * time.Hour, hasFile: true,
		})
	}
	fake.setLibrary(buildPreAirSeason(28, 11, specs))

	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama")

	if n := openIssueCount(t, svc); n != 0 {
		t.Fatalf("a healthy season opened %d issues, want 0", n)
	}
}

// TestPreAirWitnessIsQuietWhenItCannotLook. This runs off a webhook that has to
// answer the service quickly and can never usefully report a failure back to
// it, so every one of these is a logged no-op. What must never happen is an
// issue opened on a season nobody actually read.
func TestPreAirWitnessIsQuietWhenItCannotLook(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*preAirFake)
		call    func(*Service)
	}{
		{
			name: "a series Sonarr no longer holds",
			call: func(svc *Service) { svc.RecordPreAirImport(preAirSonarrID, 999999, 424242, 11, "Deleted Show") },
		},
		{
			name: "an instance that has since been removed",
			call: func(svc *Service) { svc.RecordPreAirImport("sonarr-gone", 73871, 615, 11, "Futurama") },
		},
		{
			name:    "an arr that will not answer",
			prepare: func(f *preAirFake) { f.setEpisodeStatus(http.StatusInternalServerError) },
			call:    func(svc *Service) { svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama") },
		},
		{
			name: "no instance at all",
			call: func(svc *Service) { svc.RecordPreAirImport("", 73871, 615, 11, "Futurama") },
		},
		{
			name: "a nonsense season",
			call: func(svc *Service) { svc.RecordPreAirImport(preAirSonarrID, 73871, 615, -1, "Futurama") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, fake := setupPreAirService(t)
			if tc.prepare != nil {
				tc.prepare(fake)
			}
			tc.call(svc)
			if n := openIssueCount(t, svc); n != 0 {
				t.Fatalf("opened %d issues, want 0", n)
			}
		})
	}
}

// TestPreAirIssueOpensForTheAgentNotTheAdminQueue is the difference between a
// finding that gets worked and one that sits. recoverWork only ever enqueues
// open and investigating issues, so the three system health sinks'
// needs_admin — correct for "your AI provider is down", which no agent can fix
// — would leave this class permanently unattended. And it deliberately writes
// no issue_observations row: this is not a queue incident, it never had a queue
// row, and the observation sweeper must not adopt it.
func TestPreAirIssueOpensForTheAgentNotTheAdminQueue(t *testing.T) {
	svc, _, _ := setupPreAirService(t)

	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama")

	issue := soleIssue(t, svc)
	if issue.Status != IssueOpen {
		t.Fatalf("status = %q, want %q so the agent picks it up", issue.Status, IssueOpen)
	}
	if issue.Status == IssueNeedsAdmin {
		t.Fatal("a pre-air finding parked in the admin queue never gets an agent run")
	}
	if n := countRows(t, svc, "issue_observations"); n != 0 {
		t.Fatalf("issue_observations rows = %d, want 0 — this is not a queue incident", n)
	}
}

// --- the fallback sweep ---

func preAirHistoryRecord(id int64, seriesID, season, episode int, eventType string, at time.Time) map[string]any {
	return map[string]any{
		"id": id, "seriesId": seriesID, "episodeId": seriesID*1000 + season*100 + episode,
		"eventType":   eventType,
		"date":        at.UTC().Format(time.RFC3339),
		"sourceTitle": fmt.Sprintf("Futurama.S%02dE%02d.1080p.DSNP.WEB-DL", season, episode),
		"series":      map[string]any{"id": seriesID, "title": "Futurama", "tvdbId": 73871, "tmdbId": 615},
		"episode": map[string]any{
			"id": seriesID*1000 + season*100 + episode, "seriesId": seriesID,
			"seasonNumber": season, "episodeNumber": episode,
		},
	}
}

// TestSweepPreAirImportsCoversAnInstanceWithNoWebhook: the instant path is a
// Connect webhook Cantinarr only installs when an admin asks, so an instance
// that never got one would otherwise be entirely uncovered. Recent history
// names the seasons something actually imported into — there is no library scan
// here and there is none anywhere in this server.
func TestSweepPreAirImportsCoversAnInstanceWithNoWebhook(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	fake.setHistory([]map[string]any{
		preAirHistoryRecord(9001, 28, 11, 9, sonarrImportEventType, time.Now().UTC().Add(-time.Hour)),
	})

	svc.SweepPreAirImports()

	issue := soleIssue(t, svc)
	if issue.SeasonNumber != 11 || issue.EpisodeNumber != 0 || issue.InstanceID != preAirSonarrID {
		t.Fatalf("swept issue = %#v, want the season scope on the sonarr instance", issue)
	}
	if kind := issueProblemKind(t, svc, issue.ID); kind != arr.ProblemPreAirSeasonFill {
		t.Errorf("problem_kind = %q, want %q", kind, arr.ProblemPreAirSeasonFill)
	}
	// Only Sonarr is swept: the Radarr on the same fake must not have been asked
	// for a history page it has no seasons in.
	if n := fake.countRequests("/api/v3/history?"); n != 1 {
		t.Errorf("history reads = %d, want exactly one (sonarr only)", n)
	}
}

// TestSweepPreAirImportsWatermarkStopsAtTheLastPass. Normally this is ONE arr
// call per instance per pass and no season reads at all; a sweep that
// re-examined its own window would turn a quiet library into a poll of every
// season it has ever imported into.
func TestSweepPreAirImportsWatermarkStopsAtTheLastPass(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	// Truncated because the watermark is read back out of the wire format the
	// arr answers in, which carries whole seconds.
	importedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	fake.setHistory([]map[string]any{
		preAirHistoryRecord(9001, 28, 11, 9, sonarrImportEventType, importedAt),
	})

	svc.SweepPreAirImports()
	firstPassSeasonReads := fake.countRequests("/api/v3/episode?seriesId=28&seasonNumber=11")
	if firstPassSeasonReads != 1 {
		t.Fatalf("first pass read the season %d times, want 1", firstPassSeasonReads)
	}
	if got := svc.preAirSweep.since(preAirSonarrID, time.Now().UTC()); !got.Equal(importedAt) {
		t.Fatalf("watermark = %s, want the newest import %s", got, importedAt)
	}

	svc.SweepPreAirImports()

	if got := fake.countRequests("/api/v3/episode?seriesId=28&seasonNumber=11"); got != firstPassSeasonReads {
		t.Fatalf("second pass read the season again (%d total), want no further reads", got)
	}
	if got := fake.countRequests("/api/v3/series/28"); got != 1 {
		t.Fatalf("second pass re-read the series (%d total), want 1", got)
	}
	if n := openIssueCount(t, svc); n != 1 {
		t.Fatalf("issues = %d, want 1", n)
	}
	// The history page is still read every pass — that is the one call that
	// tells the sweep whether anything happened.
	if n := fake.countRequests("/api/v3/history?"); n != 2 {
		t.Fatalf("history reads = %d, want one per pass", n)
	}
}

// TestSweepPreAirImportsKeepsItsWindowWhenHistoryFails: a pass that could not
// read history learned nothing, and advancing the watermark anyway would step
// silently over every import in that window and never look at them again.
func TestSweepPreAirImportsKeepsItsWindowWhenHistoryFails(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	importedAt := time.Now().UTC().Add(-time.Hour)
	fake.setHistory([]map[string]any{
		preAirHistoryRecord(9001, 28, 11, 9, sonarrImportEventType, importedAt),
	})
	fake.setHistoryStatus(http.StatusInternalServerError)

	svc.SweepPreAirImports()

	if n := openIssueCount(t, svc); n != 0 {
		t.Fatalf("a failed pass opened %d issues, want 0", n)
	}
	now := time.Now().UTC()
	if got := svc.preAirSweep.since(preAirSonarrID, now); !got.Equal(now.Add(-preAirCatchUpWindow)) {
		t.Fatalf("watermark = %s, want the untouched catch-up window %s", got, now.Add(-preAirCatchUpWindow))
	}

	// The next pass re-examines the same window and finds what the failure hid.
	fake.setHistoryStatus(0)
	svc.SweepPreAirImports()

	if n := openIssueCount(t, svc); n != 1 {
		t.Fatalf("the recovered pass opened %d issues, want 1", n)
	}
}

// TestSweepPreAirImportsCountsOnlyImportsAndCapsSeasons. A grab is not a file
// on disk, and a pass busy enough to name nine seasons is a bulk re-import
// rather than the batch of impossible files this detector exists for.
func TestSweepPreAirImportsCountsOnlyImportsAndCapsSeasons(t *testing.T) {
	svc, _, fake := setupPreAirService(t)

	now := time.Now().UTC()
	records := []map[string]any{
		// Not an import: a grab names a season nothing has landed in yet.
		preAirHistoryRecord(8000, 28, 99, 1, "grabbed", now.Add(-10*time.Minute)),
		preAirHistoryRecord(8001, 28, 98, 1, "downloadFailed", now.Add(-11*time.Minute)),
	}
	// Ten distinct seasons imported into; only season 11 is actually impossible.
	for season := 11; season <= 20; season++ {
		records = append(records, preAirHistoryRecord(
			int64(9000+season), 28, season, 1, sonarrImportEventType, now.Add(-time.Duration(season)*time.Minute)))
		// Two files of the same season are one season, not two.
		records = append(records, preAirHistoryRecord(
			int64(9500+season), 28, season, 2, sonarrImportEventType, now.Add(-time.Duration(season)*time.Minute)))
	}
	fake.setHistory(records)

	svc.SweepPreAirImports()

	if got := fake.countRequests("/api/v3/episode?seriesId=28&seasonNumber="); got != preAirSweepMaxSeasons {
		t.Fatalf("season reads = %d, want the cap of %d", got, preAirSweepMaxSeasons)
	}
	for _, unwanted := range []string{"seasonNumber=99", "seasonNumber=98"} {
		if fake.countRequests(unwanted) != 0 {
			t.Fatalf("a non-import record was treated as one: %v", fake.requestsSeen())
		}
	}
	if n := openIssueCount(t, svc); n != 1 {
		t.Fatalf("issues = %d, want 1 (only season 11 is impossible)", n)
	}
}

// TestSweepPreAirImportsRepeatsAreFree: the sweep is safe on any schedule and
// after any restart, because the open-dedupe index makes a second open of the
// same season a no-op rather than a second issue.
func TestSweepPreAirImportsRepeatsAreFree(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	fake.setHistory([]map[string]any{
		preAirHistoryRecord(9001, 28, 11, 9, sonarrImportEventType, time.Now().UTC().Add(-time.Hour)),
	})

	svc.SweepPreAirImports()
	// Forget the watermark the way a restart does, so the second pass really
	// re-examines the same finding instead of skipping it.
	svc.preAirSweep.mu.Lock()
	svc.preAirSweep.seen = nil
	svc.preAirSweep.mu.Unlock()
	svc.SweepPreAirImports()

	if n := openIssueCount(t, svc); n != 1 {
		t.Fatalf("two passes over one finding opened %d issues, want 1", n)
	}
	if issue := soleIssue(t, svc); issue.Occurrences != 2 {
		t.Errorf("occurrences = %d, want 2", issue.Occurrences)
	}
}

// --- the recovery proof ---

// TestPreAirRepairProvenRequiresNothingUnairedToHoldAFile. This issue class can
// reach neither existing proof: exactRecoveryProven and upgradeAbandonProven
// both read issue_observations and both fail closed on episode_number == 0. Its
// own proof is deliberately STRICTER than the detector — the finding needs two
// impossible files to trip, so "the finding no longer trips" would call a season
// with one file left behind repaired.
func TestPreAirRepairProvenRequiresNothingUnairedToHoldAFile(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 11, "Futurama")
	issue, err := svc.GetIssue(soleIssue(t, svc).ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	// Nine impossible files still on disk.
	if proven, err := svc.preAirRepairProven(issue); err != nil || proven {
		t.Fatalf("unrepaired season proven = %v (err %v), want false", proven, err)
	}

	// The repair ran but missed one. The detector's threshold is two, so this
	// season no longer trips the FINDING — and it is still not repaired.
	oneLeft := []preAirEpisode{{number: 9, airsIn: 63 * 24 * time.Hour, hasFile: true, importedBeforeAir: true}}
	for i := 1; i <= 8; i++ {
		oneLeft = append(oneLeft, preAirEpisode{number: i, airsIn: time.Duration(i) * 7 * 24 * time.Hour})
	}
	episodes, files := buildPreAirSeason(28, 11, oneLeft)
	fake.setLibrary(episodes, files)
	if proven, err := svc.preAirRepairProven(issue); err != nil || proven {
		t.Fatalf("one impossible file left proven = %v (err %v), want false", proven, err)
	}

	// Nothing unaired holds a file any more.
	clean := make([]preAirEpisode, 0, 10)
	for i := 1; i <= 10; i++ {
		clean = append(clean, preAirEpisode{number: i, airsIn: time.Duration(i) * 7 * 24 * time.Hour})
	}
	fake.setLibrary(buildPreAirSeason(28, 11, clean))
	if proven, err := svc.preAirRepairProven(issue); err != nil || !proven {
		t.Fatalf("repaired season proven = %v (err %v), want true", proven, err)
	}
}

// TestPreAirRepairProvenIsScopedToItsOwnProblemKind. This proof re-reads the
// live season and asks a question that is only meaningful for this finding, so
// it must never answer for an issue that is about something else — a queue
// incident on the same season would otherwise close itself on evidence about a
// different problem entirely.
func TestPreAirRepairProvenIsScopedToItsOwnProblemKind(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	// A season with nothing unaired holding a file: the proof's own condition is
	// satisfied, so anything that still reads false is refusing on identity.
	clean := make([]preAirEpisode, 0, 10)
	for i := 1; i <= 10; i++ {
		clean = append(clean, preAirEpisode{number: i, airsIn: time.Duration(i) * 7 * 24 * time.Hour})
	}
	fake.setLibrary(buildPreAirSeason(28, 11, clean))

	seed := func(t *testing.T, mediaType, problemKind string, season int) *Issue {
		t.Helper()
		res, err := svc.db.Exec(
			`INSERT INTO issues (source, status, media_type, tmdb_id, tvdb_id, title, season_number,
			                     episode_number, instance_id, problem_kind)
			 VALUES (?, ?, ?, 615, 73871, 'Futurama', ?, 0, ?, ?)`,
			SourceAuto, IssueOpen, mediaType, season, preAirSonarrID, sqlNullStr(problemKind),
		)
		if err != nil {
			t.Fatalf("seed issue: %v", err)
		}
		id, _ := res.LastInsertId()
		issue, err := svc.GetIssue(id)
		if err != nil {
			t.Fatalf("GetIssue: %v", err)
		}
		return issue
	}

	cases := []struct {
		name  string
		issue func(*testing.T) *Issue
	}{
		{
			name:  "another problem on the same season",
			issue: func(t *testing.T) *Issue { return seed(t, "tv", "Stalled download", 11) },
		},
		{
			name:  "an issue with no problem label at all",
			issue: func(t *testing.T) *Issue { return seed(t, "tv", "", 11) },
		},
		{
			name:  "a movie can never be a season fill",
			issue: func(t *testing.T) *Issue { return seed(t, "movie", arr.ProblemPreAirSeasonFill, 0) },
		},
		{
			name: "an issue bound to no instance",
			issue: func(t *testing.T) *Issue {
				issue := seed(t, "tv", arr.ProblemPreAirSeasonFill, 11)
				issue.InstanceID = ""
				return issue
			},
		},
		{name: "no issue at all", issue: func(*testing.T) *Issue { return nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proven, err := svc.preAirRepairProven(tc.issue(t))
			if err != nil {
				t.Fatalf("preAirRepairProven: %v", err)
			}
			if proven {
				t.Fatal("proved a repair for an issue this proof does not answer for")
			}
		})
	}

	// The control: the same clean season under the right label does prove.
	issue := seed(t, "tv", arr.ProblemPreAirSeasonFill, 11)
	if proven, err := svc.preAirRepairProven(issue); err != nil || !proven {
		t.Fatalf("the pre-air issue on a clean season proven = %v (err %v), want true", proven, err)
	}
}

// The truncated-import sentinel: a file that IMPORTS at a fraction of the
// show's own runtime is invisible to every queue-shaped detector — the queue
// looks healthy and the library looks full. The arr's ffprobe runtime is the
// evidence; no analysis, no verdict; one notice per episode-file, advisory.
func TestSuspectImportSentinel(t *testing.T) {
	svc, _, fake := setupPreAirService(t)
	if _, err := svc.SetSettings(Settings{Enabled: true, AutoDispatch: true, Mode: ModeSupervised}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	fake.mu.Lock()
	fake.series = []map[string]any{{"id": 28, "title": "Futurama", "tvdbId": 73871, "tmdbId": 615, "runtime": 42}}
	fake.mu.Unlock()
	episodes := []map[string]any{
		{"id": 28203, "seriesId": 28, "seasonNumber": 2, "episodeNumber": 3, "hasFile": true, "episodeFileId": 50203, "airDateUtc": "2026-07-01T00:00:00Z"},
		{"id": 28204, "seriesId": 28, "seasonNumber": 2, "episodeNumber": 4, "hasFile": true, "episodeFileId": 50204, "airDateUtc": "2026-07-08T00:00:00Z"},
		{"id": 28205, "seriesId": 28, "seasonNumber": 2, "episodeNumber": 5, "hasFile": true, "episodeFileId": 50205, "airDateUtc": "2026-07-15T00:00:00Z"},
	}
	files := []map[string]any{
		{"id": 50203, "seriesId": 28, "seasonNumber": 2, "dateAdded": "2026-08-01T00:00:00Z",
			"mediaInfo": map[string]any{"runTime": "12:00", "resolution": "1080p"}},
		{"id": 50204, "seriesId": 28, "seasonNumber": 2, "dateAdded": "2026-08-01T00:00:00Z",
			"mediaInfo": map[string]any{"runTime": "41:30", "resolution": "1080p"}},
		{"id": 50205, "seriesId": 28, "seasonNumber": 2, "dateAdded": "2026-08-01T00:00:00Z"},
	}
	fake.setLibrary(episodes, files)

	// 12 minutes of a 42-minute show: flagged, once, with the SAMPLE label.
	svc.RecordSuspectImport(preAirSonarrID, 73871, 615, 2, 3, "Futurama")
	svc.RecordSuspectImport(preAirSonarrID, 73871, 615, 2, 3, "Futurama")
	var count int
	_ = svc.db.QueryRow("SELECT COUNT(1) FROM issues WHERE closed_at IS NULL").Scan(&count)
	if count != 1 {
		t.Fatalf("open issues after duplicate reports = %d, want 1", count)
	}
	issue := soleIssue(t, svc)
	if issue.Status != IssueNeedsAdmin || issue.EpisodeNumber != 3 {
		t.Fatalf("sentinel issue = %q S%dE%d, want advisory needs_admin on E3", issue.Status, issue.SeasonNumber, issue.EpisodeNumber)
	}
	if kind := issueProblemKind(t, svc, issue.ID); kind != arr.ProblemSample {
		t.Fatalf("problem_kind = %q, want the sample label (one cause, one label)", kind)
	}

	// A healthy runtime and an unanalyzed file each produce nothing.
	svc.RecordSuspectImport(preAirSonarrID, 73871, 615, 2, 4, "Futurama")
	svc.RecordSuspectImport(preAirSonarrID, 73871, 615, 2, 5, "Futurama")
	_ = svc.db.QueryRow("SELECT COUNT(1) FROM issues WHERE closed_at IS NULL").Scan(&count)
	if count != 1 {
		t.Fatalf("issues after healthy + unanalyzed = %d, want still 1", count)
	}
}

// stubSeason renders explicit air/import offsets from now into the fake's two
// record shapes, for cases the preAirEpisode fixtures cannot express (margins
// measured in minutes rather than days).
func stubSeason(season int, specs []struct {
	number    int
	airsIn    time.Duration
	hasFile   bool
	importedIn time.Duration
}) (episodes, files []map[string]any) {
	now := time.Now().UTC()
	for _, spec := range specs {
		fileID := 90000 + season*100 + spec.number
		ep := map[string]any{
			"id": 28*10000 + season*100 + spec.number, "seriesId": 28,
			"seasonNumber": season, "episodeNumber": spec.number,
			"title":      fmt.Sprintf("S%02dE%02d", season, spec.number),
			"airDateUtc": now.Add(spec.airsIn).Format(time.RFC3339),
			"hasFile":    spec.hasFile,
		}
		if spec.hasFile {
			ep["episodeFileId"] = fileID
			files = append(files, map[string]any{
				"id": fileID, "seriesId": 28, "seasonNumber": season,
				"dateAdded": now.Add(spec.importedIn).Format(time.RFC3339),
				"sceneName": fmt.Sprintf("Show.S%02dE%02d.1080p.WEB", season, spec.number),
			})
		}
		episodes = append(episodes, ep)
	}
	return episodes, files
}

// TestPreAirWitnessIgnoresPremiereStagger replays issue 858 (Reacher S4,
// 2026-08-12) through the production witness: a three-at-once premiere that
// TheTVDB stamps as a runtime-staggered linear schedule, grabbed and imported
// between the first and second nominal air times. Margins of 22 and 71
// minutes are the calendar convention, not pre-air fill — the witness must
// not open an issue, propose deletions, or page anyone.
func TestPreAirWitnessIgnoresPremiereStagger(t *testing.T) {
	svc, notifier, fake := setupPreAirService(t)
	episodes, files := stubSeason(12, []struct {
		number    int
		airsIn    time.Duration
		hasFile   bool
		importedIn time.Duration
	}{
		{1, -27 * time.Minute, true, -1 * time.Minute},
		{2, 22 * time.Minute, true, -1 * time.Minute},
		{3, 71 * time.Minute, true, -30 * time.Second},
		{4, 7 * 24 * time.Hour, false, 0},
	})
	fake.setLibrary(episodes, files)

	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 12, "Futurama")

	if got := openIssueCount(t, svc); got != 0 {
		t.Fatalf("open issues after a premiere-stagger import = %d, want 0", got)
	}
	if len(notifier.adminEvents) != 0 {
		t.Fatalf("admin events for a premiere = %v, want none", notifier.adminEvents)
	}

	// Control: the same entry point with genuinely pre-air files (margins in
	// days) must still open the finding — the floor must not blunt the
	// detector it protects.
	episodes, files = stubSeason(13, []struct {
		number    int
		airsIn    time.Duration
		hasFile   bool
		importedIn time.Duration
	}{
		{1, 2 * 24 * time.Hour, true, -1 * time.Hour},
		{2, 3 * 24 * time.Hour, true, -1 * time.Hour},
		{3, 9 * 24 * time.Hour, false, 0},
	})
	fake.setLibrary(episodes, files)

	svc.RecordPreAirImport(preAirSonarrID, 73871, 615, 13, "Futurama")

	if got := openIssueCount(t, svc); got != 1 {
		t.Fatalf("open issues after a genuine pre-air fill = %d, want 1", got)
	}
}
