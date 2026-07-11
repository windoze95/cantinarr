package websocket

import (
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// fakeOpener records every OpenAutoIssue call so a test can assert the hub's
// 2-consecutive-poll debounce: a problem must persist across two polls before it
// reaches the opener, and the opener is then called once per confirming poll
// (the real DB dedupe — exercised separately — collapses those into one issue).
type fakeOpener struct {
	mu         sync.Mutex
	calls      []openCall
	closes     []openCall
	reconciles int
}

type openCall struct {
	serviceType string
	instanceID  string
	downloadID  string
	problem     string
}

func (f *fakeOpener) OpenAutoIssue(serviceType, instanceID, downloadID string, media arr.QueueMediaContext, d arr.Diagnosis) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, openCall{serviceType, instanceID, downloadID, d.Problem})
}

func (f *fakeOpener) ReconcileAutoIssues(serviceType, instanceID string, activeDownloadIDs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconciles++
}

func (f *fakeOpener) CloseAutoIssue(serviceType, instanceID, downloadID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes = append(f.closes, openCall{serviceType, instanceID, downloadID, ""})
}

func (f *fakeOpener) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeOpener) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.closes)
}

// TestDispatchClosesWhenProblemClears asserts a confirmed problem that later
// drops out of a successful poll triggers exactly one CloseAutoIssue, and that a
// further absent poll doesn't re-close (the key already dropped from the map).
func TestDispatchClosesWhenProblemClears(t *testing.T) {
	f := &fakeOpener{}
	h := newTestHub(f)

	// Two confirming polls open the issue.
	h.dispatchDetailedItems("radarr", "inst1", []queueSignalItem{stuckItem("dl1")})
	h.dispatchDetailedItems("radarr", "inst1", []queueSignalItem{stuckItem("dl1")})
	if f.count() == 0 {
		t.Fatal("expected the stuck download to open after two polls")
	}

	// The download is gone on a successful poll → recovered → close once.
	h.dispatchDetailedItems("radarr", "inst1", nil)
	if f.closeCount() != 1 {
		t.Fatalf("close calls = %d, want 1 after the problem cleared", f.closeCount())
	}
	if c := f.closes[0]; c.downloadID != "dl1" || c.instanceID != "inst1" {
		t.Fatalf("close call = %+v, want dl1/inst1", c)
	}

	// Still gone next poll → no duplicate close (key already dropped out).
	h.dispatchDetailedItems("radarr", "inst1", nil)
	if f.closeCount() != 1 {
		t.Fatalf("close calls = %d, want still 1 (no re-close)", f.closeCount())
	}
}

// newTestHub builds a hub wired with only the given opener. The other
// dependencies are nil because the auto-dispatch dispatch path touches only the
// opener and the debounce map; NewHub still initializes prevArrProblems.
func newTestHub(opener IssueOpener) *Hub {
	return NewHub(nil, nil, nil, nil, opener)
}

// stuckItem is a queue item whose tracked-download state classifies as an error
// (a stalled torrent): warning/error severity, so it is eligible for dispatch.
func stuckItem(downloadID string) queueSignalItem {
	return queueSignalItem{
		downloadID: downloadID,
		signal: arr.QueueSignal{
			Status:                "warning",
			TrackedDownloadStatus: "error",
			ErrorMessage:          "The download is stalled with no connections",
		},
	}
}

// importPendingItem is a bare importPending item — the doctor classifies it as a
// warning, but it is normal for a few seconds post-download, which is exactly why
// the debounce exists.
func importPendingItem(downloadID string) queueSignalItem {
	return queueSignalItem{
		downloadID: downloadID,
		signal: arr.QueueSignal{
			Status:               "completed",
			TrackedDownloadState: "importPending",
		},
	}
}

// TestAutoDispatchDebounceFiresOnSecondPoll proves a problem must persist across
// two consecutive polls before the opener is called: the first poll only seeds
// the debounce baseline, the second (and each later) poll dispatches.
func TestAutoDispatchDebounceFiresOnSecondPoll(t *testing.T) {
	opener := &fakeOpener{}
	h := newTestHub(opener)

	items := []queueSignalItem{stuckItem("hashA")}

	// First poll: establishes the baseline, fires nothing.
	h.dispatchDetailedItems("radarr", "inst1", items)
	if opener.count() != 0 {
		t.Fatalf("after first poll, opener calls = %d, want 0 (debounce baseline only)", opener.count())
	}

	// Second poll: same problem persists -> dispatch once.
	h.dispatchDetailedItems("radarr", "inst1", items)
	if opener.count() != 1 {
		t.Fatalf("after second poll, opener calls = %d, want 1", opener.count())
	}
	got := opener.calls[0]
	if got.serviceType != "radarr" || got.instanceID != "inst1" || got.downloadID != "hashA" {
		t.Fatalf("dispatch = %+v, want radarr/inst1/hashA", got)
	}
}

// TestAutoDispatchTransientImportPendingOpensNone proves a problem seen on only
// ONE poll never reaches the opener. The item is problematic on the first poll
// (establishing a baseline) but is gone (imported) on the second, so the
// debounce never confirms it.
func TestAutoDispatchTransientImportPendingOpensNone(t *testing.T) {
	opener := &fakeOpener{}
	h := newTestHub(opener)

	// Poll 1: a bare importPending (transient post-download warning).
	h.dispatchDetailedItems("radarr", "inst1", []queueSignalItem{importPendingItem("hashB")})
	// Poll 2: the download imported and left the queue (empty queue).
	h.dispatchDetailedItems("radarr", "inst1", []queueSignalItem{})

	if opener.count() != 0 {
		t.Fatalf("transient importPending opened %d issue(s), want 0", opener.count())
	}
}

// TestAutoDispatchHealthyItemsNeverDispatch proves an ok-severity item is never
// a candidate no matter how many polls it survives.
func TestAutoDispatchHealthyItemsNeverDispatch(t *testing.T) {
	opener := &fakeOpener{}
	h := newTestHub(opener)

	healthy := []queueSignalItem{{
		downloadID: "hashC",
		signal:     arr.QueueSignal{Status: "downloading", TrackedDownloadStatus: "ok"},
	}}
	for i := 0; i < 4; i++ {
		h.dispatchDetailedItems("radarr", "inst1", healthy)
	}
	if opener.count() != 0 {
		t.Fatalf("healthy item dispatched %d time(s), want 0", opener.count())
	}
}

// TestAutoDispatchSkipsItemsWithoutDownloadID proves an item with no
// download-client id (not yet handed to the client) is never dispatched — it has
// no stable dedupe key and is expected to be transient.
func TestAutoDispatchSkipsItemsWithoutDownloadID(t *testing.T) {
	opener := &fakeOpener{}
	h := newTestHub(opener)

	noID := []queueSignalItem{stuckItem("")}
	h.dispatchDetailedItems("radarr", "inst1", noID)
	h.dispatchDetailedItems("radarr", "inst1", noID)
	if opener.count() != 0 {
		t.Fatalf("item without download id dispatched %d time(s), want 0", opener.count())
	}
}

// TestAutoDispatchNilOpenerNoPanic proves the dispatch path is a clean no-op when
// no opener is wired (the feature-off case).
func TestAutoDispatchNilOpenerNoPanic(t *testing.T) {
	h := newTestHub(nil)
	// Must not panic and must touch nothing.
	h.dispatchDetailedItems("radarr", "inst1", []queueSignalItem{stuckItem("hashD"), stuckItem("hashD")})
}

// TestAutoDispatchPerInstanceIsolation proves the debounce map is keyed per
// service:instance:download, so two instances each confirm independently and a
// download id reused across instances does not cross-confirm on a single poll.
func TestAutoDispatchPerInstanceIsolation(t *testing.T) {
	opener := &fakeOpener{}
	h := newTestHub(opener)

	itemsA := []queueSignalItem{stuckItem("dup")}

	// inst1 poll 1 (baseline), inst2 poll 1 (baseline): no dispatch yet.
	h.dispatchDetailedItems("radarr", "inst1", itemsA)
	h.dispatchDetailedItems("radarr", "inst2", itemsA)
	if opener.count() != 0 {
		t.Fatalf("after first poll of each instance, calls = %d, want 0", opener.count())
	}

	// inst1 poll 2: confirms for inst1 only.
	h.dispatchDetailedItems("radarr", "inst1", itemsA)
	if opener.count() != 1 {
		t.Fatalf("after inst1 second poll, calls = %d, want 1", opener.count())
	}
	if opener.calls[0].instanceID != "inst1" {
		t.Fatalf("first dispatch instance = %q, want inst1", opener.calls[0].instanceID)
	}

	// inst2 poll 2: confirms for inst2 independently.
	h.dispatchDetailedItems("radarr", "inst2", itemsA)
	if opener.count() != 2 {
		t.Fatalf("after inst2 second poll, calls = %d, want 2", opener.count())
	}
}

// TestQueueSignalMappers confirms the real radarr/sonarr DetailedQueueItem types
// project into the neutral classifier signal carrying the download id and the
// tracked-download/error fields the lightweight GetQueue lacks — the reason the
// auto-dispatch path needs the detailed queue.
func TestQueueSignalMappers(t *testing.T) {
	r := radarrQueueSignal(radarr.DetailedQueueItem{
		ID:                   41,
		DownloadID:           "rdl",
		TrackedDownloadState: "importPending",
		Protocol:             "usenet",
		Movie:                &radarr.MovieContext{Title: "Movie", TmdbID: 101},
	})
	if r.downloadID != "rdl" || r.signal.TrackedDownloadState != "importPending" {
		t.Fatalf("radarr mapping = %+v, want downloadID=rdl state=importPending", r)
	}
	if r.media.QueueID != 41 || r.media.TmdbID != 101 || r.media.Title != "Movie" {
		t.Fatalf("radarr media context = %+v", r.media)
	}
	if d := arr.Diagnose(r.signal); d.Severity != arr.SeverityWarning {
		t.Fatalf("radarr importPending severity = %q, want warning", d.Severity)
	}

	s := sonarrQueueSignal(sonarr.DetailedQueueItem{
		ID:                    42,
		DownloadID:            "sdl",
		TrackedDownloadStatus: "error",
		ErrorMessage:          "The download is stalled with no connections",
		Series:                &sonarr.SeriesContext{Title: "Show", TvdbID: 202, TmdbID: 303},
		Episode:               &sonarr.EpisodeContext{SeasonNumber: 1, EpisodeNumber: 2},
	})
	if s.downloadID != "sdl" {
		t.Fatalf("sonarr mapping downloadID = %q, want sdl", s.downloadID)
	}
	if s.media.QueueID != 42 || s.media.TmdbID != 303 || s.media.TvdbID != 202 ||
		s.media.SeasonNumber != 1 || s.media.EpisodeNumber != 2 || s.media.Title != "Show" {
		t.Fatalf("sonarr media context = %+v", s.media)
	}
	if d := arr.Diagnose(s.signal); d.Severity != arr.SeverityError {
		t.Fatalf("sonarr stalled severity = %q, want error", d.Severity)
	}
}
