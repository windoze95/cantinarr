package remediation

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// The reliability-scenario world. Every scenario in scenario_replay_test.go and
// scenario_synthetic_test.go runs through this file's virtual clock and
// recording notifier so its assertions are on OBSERVABLE behavior — which admin
// pages went out, when (on the scenario clock), carrying what — never on
// internal calls. The pipeline between the fake Sonarr and those assertions is
// production code, same as pipeline_harness_test.go.
//
// Clock contract: every timed production seam in this package already takes the
// caller's clock (observeQueueSnapshot, queueIssueAlert, flushIssueAlerts,
// flushActionAlerts, sweepAutoApprovals), so the world advances a virtual clock
// and fires those seams exactly on production's cadence — the 30s decision tick
// and the 1m observation tick — without any source seam. CANTINARR_SCENARIO_SPEED
// selects pacing for the SAME scenarios and assertions: unset/0 runs each tick
// back-to-back (accelerated), "1" sleeps real tick intervals (1× soak), "60"
// sleeps 1/60th, and so on. SQL DEFAULT CURRENT_TIMESTAMP audit columns still
// take real wall time, which is why no scenario may assert on them.
type scenarioClock struct {
	now    time.Time
	factor float64
}

func newScenarioClock(start time.Time) *scenarioClock {
	factor := 0.0
	if raw := os.Getenv("CANTINARR_SCENARIO_SPEED"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err == nil && parsed > 0 {
			factor = parsed
		}
	}
	return &scenarioClock{now: start.UTC(), factor: factor}
}

// step advances the virtual clock and, in soak mode, holds back the real one by
// the same (scaled) amount.
func (c *scenarioClock) step(d time.Duration) time.Time {
	c.now = c.now.Add(d)
	if c.factor > 0 {
		time.Sleep(time.Duration(float64(d) / c.factor))
	}
	return c.now
}

// sentPage is one recorded notifier delivery, stamped with the scenario clock —
// the send log production does not keep (OBSERVABILITY_GAPS G1), reconstructed
// here at the only boundary a test can see it.
type sentPage struct {
	At    time.Time
	Event string
	Data  map[string]interface{}
	User  int64 // 0 for admin fan-outs
}

type recordingNotifier struct {
	mu    sync.Mutex
	clock *scenarioClock
	sends []sentPage
}

func (r *recordingNotifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, sentPage{At: r.clock.now, Event: eventType, Data: data, User: userID})
}

func (r *recordingNotifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, sentPage{At: r.clock.now, Event: eventType, Data: data})
}

// pages returns the recorded deliveries of one event type, admin fan-outs only.
func (r *recordingNotifier) pages(eventType string) []sentPage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sentPage, 0, len(r.sends))
	for _, s := range r.sends {
		if s.Event == eventType && s.User == 0 {
			out = append(out, s)
		}
	}
	return out
}

type scenarioWorld struct {
	t     *testing.T
	svc   *Service
	fake  *preAirFake
	rec   *recordingNotifier
	clock *scenarioClock

	// lastDecisionTick / lastObserveTick track which production cadences have
	// fired as the clock advances, mirroring main.go's independent tickers.
	lastDecisionTick time.Time
	lastObserveTick  time.Time
}

const scenarioSonarrID = "sonarr-scenario"

// scenarioSeries is the fixture identity for the replay scenarios. The ids are
// the production incident's (The Big Comfy Couch, tvdb 70805 / tmdb 4422) so
// incidents.jsonl rows map onto scenario runs by inspection.
const (
	scenarioSeriesID = 300
	scenarioTvdbID   = 70805
	scenarioTmdbID   = 4422
)

func newScenarioWorld(t *testing.T, start time.Time) *scenarioWorld {
	t.Helper()
	fake := &preAirFake{
		t: t,
		series: []map[string]any{{
			"id": scenarioSeriesID, "title": "The Big Comfy Couch",
			"tvdbId": scenarioTvdbID, "tmdbId": scenarioTmdbID,
		}},
	}
	srv := fake.start()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open scenario db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x37}, 32))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	if err := store.Create(&instance.Instance{
		ID: scenarioSonarrID, ServiceType: "sonarr", Name: "TV", URL: srv.URL, APIKey: "key",
	}); err != nil {
		t.Fatalf("create scenario instance: %v", err)
	}

	clock := newScenarioClock(start)
	rec := &recordingNotifier{clock: clock}
	svc := NewService(database, instance.NewRegistry(store), nil, rec)
	if _, err := svc.SetSettings(Settings{
		Enabled: true, AutoDispatch: true, Mode: ModeSupervised,
		MaxSteps: 12, MaxTurnTokens: 1024, MaxWallClockSecs: 30, DailyRunCap: 50,
	}); err != nil {
		t.Fatalf("set scenario settings: %v", err)
	}
	if _, err := svc.db.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (1, 'admin', '', 'admin')"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return &scenarioWorld{
		t: t, svc: svc, fake: fake, rec: rec, clock: clock,
		lastDecisionTick: clock.now, lastObserveTick: clock.now,
	}
}

// runner mirrors pipelineHarness.runner: real tool server, scripted model.
func (w *scenarioWorld) runner(script *scriptedTurn) *Runner {
	toolServer := mcp.NewToolServer(nil, nil, w.svc.registry, nil)
	toolServer.SetIssueStore(w.svc)
	return &Runner{
		db:         w.svc.db,
		svc:        w.svc,
		toolServer: toolServer,
		turns:      scriptedTurnResolver(script),
		procToken:  "scenario",
	}
}

// observe ingests the fake's CURRENT queue at the CURRENT virtual clock through
// the production fetch+parse path.
func (w *scenarioWorld) observe() {
	w.t.Helper()
	items, err := w.svc.fetchQueueSnapshot("sonarr", scenarioSonarrID)
	if err != nil {
		w.t.Fatalf("fetch queue snapshot: %v", err)
	}
	if err := w.svc.observeQueueSnapshot("sonarr", scenarioSonarrID, items, w.clock.now); err != nil {
		w.t.Fatalf("observe queue snapshot: %v", err)
	}
}

// advance moves the virtual clock forward, firing production cadences exactly
// as main.go's goroutines do: the 1m observation sweep (re-reading the fake's
// queue) and the 30s decision tick (rule sweep, then both alert flushes — the
// ordering is load-bearing, observation.go:1788-1790).
func (w *scenarioWorld) advance(d time.Duration) {
	w.t.Helper()
	deadline := w.clock.now.Add(d)
	for w.clock.now.Before(deadline) {
		step := 30 * time.Second
		if remaining := deadline.Sub(w.clock.now); remaining < step {
			step = remaining
		}
		now := w.clock.step(step)
		if !now.Before(w.lastObserveTick.Add(time.Minute)) {
			w.lastObserveTick = now
			w.observe()
		}
		if !now.Before(w.lastDecisionTick.Add(30 * time.Second)) {
			w.lastDecisionTick = now
			w.svc.sweepAutoApprovals(now)
			w.svc.flushIssueAlerts(now)
			w.svc.flushActionAlerts(now)
		}
	}
}

// advanceFlushOnly is advance without the observation cadence, for scenarios
// where the arr is deliberately unreachable (an observe would fail the test
// harness rather than the system under test). The decision tick — rule sweep
// and alert flushes — still fires on production cadence.
func (w *scenarioWorld) advanceFlushOnly(d time.Duration) {
	w.t.Helper()
	deadline := w.clock.now.Add(d)
	for w.clock.now.Before(deadline) {
		step := 30 * time.Second
		if remaining := deadline.Sub(w.clock.now); remaining < step {
			step = remaining
		}
		now := w.clock.step(step)
		if !now.Before(w.lastDecisionTick.Add(30 * time.Second)) {
			w.lastDecisionTick = now
			w.svc.sweepAutoApprovals(now)
			w.svc.flushIssueAlerts(now)
			w.svc.flushActionAlerts(now)
		}
	}
}

// --- fixtures ---

// scenarioEpisode renders one library episode + (optionally) its current file.
// fileID <= 0 means the episode has no file.
func scenarioEpisode(season, episode int, fileID int64, importedAt time.Time) (ep map[string]any, file map[string]any) {
	epID := 25000 + season*100 + episode
	ep = map[string]any{
		"id": epID, "seriesId": scenarioSeriesID, "seasonNumber": season, "episodeNumber": episode,
		"title":      fmt.Sprintf("S%02dE%02d", season, episode),
		"airDateUtc": importedAt.Add(-30 * 24 * time.Hour).Format(time.RFC3339),
		"hasFile":    fileID > 0,
	}
	if fileID > 0 {
		ep["episodeFileId"] = fileID
		file = map[string]any{
			"id": fileID, "seriesId": scenarioSeriesID, "seasonNumber": season,
			"dateAdded": importedAt.Format(time.RFC3339),
			"sceneName": fmt.Sprintf("The.Big.Comfy.Couch.S%02dE%02d.480p.AMZN.WEB-DL", season, episode),
		}
	}
	return ep, file
}

// importPendingQueueRow is the exact incident shape from 2026-08-10: a finished
// usenet download in Sonarr's brief pre-import window. queueFileID is the file
// the episode held WHEN THE SNAPSHOT WAS TAKEN — for an upgrade observed before
// its import lands, that is the old file, never the one the import creates.
func importPendingQueueRow(queueID int64, downloadID string, season, episode int, queueFileID int64, added time.Time) map[string]any {
	epID := 25000 + season*100 + episode
	return map[string]any{
		"id":       queueID,
		"seriesId": scenarioSeriesID,
		"series": map[string]any{
			"id": scenarioSeriesID, "title": "The Big Comfy Couch",
			"tvdbId": scenarioTvdbID, "tmdbId": scenarioTmdbID,
		},
		"episodeId": epID,
		"episode": map[string]any{
			"id": epID, "seriesId": scenarioSeriesID, "seasonNumber": season, "episodeNumber": episode,
			"episodeFileId": queueFileID, "hasFile": queueFileID > 0,
			"title": fmt.Sprintf("S%02dE%02d", season, episode),
		},
		"episodeHasFile":        queueFileID > 0,
		"title":                 fmt.Sprintf("The.Big.Comfy.Couch.S%02dE%02d.480p.AMZN.WEB-DL", season, episode),
		"status":                "completed",
		"trackedDownloadStatus": "ok",
		"trackedDownloadState":  "importPending",
		"downloadId":            downloadID,
		"protocol":              "usenet",
		"size":                  400000000.0,
		"sizeleft":              0.0,
		"added":                 added.Format(time.RFC3339),
	}
}

// importReceiptHistory is Sonarr's genuine receipt for a completed import: the
// downloadFolderImported event binding download → episode → new file, exactly
// what /api/v3/history returned for all seven 2026-08-10 incidents.
func importReceiptHistory(historyID int64, downloadID string, season, episode int, newFileID int64, importedAt time.Time) []map[string]any {
	epID := 25000 + season*100 + episode
	return []map[string]any{
		{
			"id": historyID, "eventType": "downloadFolderImported",
			"episodeId": epID, "seriesId": scenarioSeriesID,
			"date":       importedAt.Format(time.RFC3339),
			"downloadId": downloadID,
			"data":       map[string]any{"fileId": fmt.Sprint(newFileID), "importedPath": "/tv/big-comfy-couch"},
			"series":     map[string]any{"id": scenarioSeriesID, "title": "The Big Comfy Couch", "tvdbId": scenarioTvdbID, "tmdbId": scenarioTmdbID},
			"episode":    map[string]any{"id": epID, "seriesId": scenarioSeriesID, "seasonNumber": season, "episodeNumber": episode},
		},
		{
			"id": historyID - 30, "eventType": "grabbed",
			"episodeId": epID, "seriesId": scenarioSeriesID,
			"date":       importedAt.Add(-21 * time.Second).Format(time.RFC3339),
			"downloadId": downloadID,
			"data":       map[string]any{},
		},
	}
}

// --- assertion helpers ---

// requireObservedDownload asserts the staged race reproduced: the observer
// recorded the tracked download with the OLD queue-time file id. If this fails,
// the scenario is no longer reproducing the 2026-08-10 mechanism and its
// verdict is meaningless — fail loudly rather than pass hollowly.
func (w *scenarioWorld) requireObservedDownload(issueID int64, downloadID string, queueFileID int64) {
	w.t.Helper()
	var gotFile int64
	err := w.svc.db.QueryRow(
		"SELECT COALESCE(queue_file_id, -1) FROM issue_observation_downloads WHERE issue_id = ? AND download_id = ?",
		issueID, downloadID,
	).Scan(&gotFile)
	if err != nil {
		w.t.Fatalf("observed download row for issue %d download %s: %v", issueID, downloadID, err)
	}
	if gotFile != queueFileID {
		w.t.Fatalf("observed queue_file_id = %d, want the snapshot-time file %d (race precondition lost)", gotFile, queueFileID)
	}
}

func (w *scenarioWorld) issueByID(issueID int64) Issue {
	w.t.Helper()
	issue, err := w.svc.GetIssue(issueID)
	if err != nil {
		w.t.Fatalf("load issue %d: %v", issueID, err)
	}
	return *issue
}

func (w *scenarioWorld) soleOpenAutoIssueID() int64 {
	w.t.Helper()
	var id int64
	if err := w.svc.db.QueryRow(
		"SELECT id FROM issues WHERE source = ? AND closed_at IS NULL ORDER BY id DESC LIMIT 1", SourceAuto,
	).Scan(&id); err != nil {
		w.t.Fatalf("find open auto issue: %v", err)
	}
	return id
}

// pageTimes formats recorded pages for failure messages.
func pageTimes(pages []sentPage) string {
	out := ""
	for _, p := range pages {
		out += fmt.Sprintf("[%s %s data=%v] ", p.At.Format("15:04:05"), p.Event, p.Data)
	}
	return out
}
