package websocket

import (
	"fmt"
	"testing"
	"time"
)

// advanceImportTestClock closes another full history second without sleeps.
// Keep synthetic polls in the recent past so persistence freshness checks still
// exercise their real wall-clock behavior, including across a replacement hub.
func advanceImportTestClock(h *Hub) {
	now := time.Now().Truncate(time.Second).Add(-30 * time.Second)
	for _, last := range h.lastPollAt {
		if next := last.Add(2 * time.Second); next.After(now) {
			now = next
		}
	}
	h.pollTime = func() time.Time { return now }
}

func nextImportTestTime(h *Hub) time.Time {
	var last time.Time
	for _, at := range h.lastPollAt {
		if at.After(last) {
			last = at
		}
	}
	return last.Add(time.Nanosecond).Truncate(time.Second)
}

func TestWholeSecondImportAfterPollIsCaughtOnce(t *testing.T) {
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		t.Run(service, func(t *testing.T) {
			b, h, content, poll := restartFixture(t, service)
			second := time.Now().Truncate(time.Second).Add(-10 * time.Second)
			now := second.Add(136 * time.Millisecond)
			h.pollTime = func() time.Time { return now }
			poll(h)
			assertImportCalls(t, content, 0)
			wantCursor := second.Add(-time.Nanosecond)
			if !h.lastPollAt[service].Equal(wantCursor) {
				t.Fatalf("seed cursor %s, want %s", h.lastPollAt[service], wantCursor)
			}
			// The native import occurs after the poll at .800, but its API timestamp
			// is truncated to .000. It must remain pending until that second closes.
			b.mu.Lock()
			b.queue = "[]"
			b.history = b.receipt(second)
			b.mu.Unlock()
			now = second.Add(900 * time.Millisecond)
			poll(h)
			assertImportCalls(t, content, 0)
			if !h.lastPollAt[service].Equal(wantCursor) {
				t.Fatal("partial second advanced the cursor")
			}
			resumed := NewHub(nil, nil, nil, h.witness.db, content, nil)
			resumed.restoreQueueWitness()
			resumed.pollTime = func() time.Time { return now }
			poll(resumed)
			assertImportCalls(t, content, 0)
			now = second.Add(time.Second)
			poll(resumed)
			assertImportCalls(t, content, 1)
			poll(resumed)
			assertImportCalls(t, content, 1)
			again := NewHub(nil, nil, nil, h.witness.db, content, nil)
			again.restoreQueueWitness()
			again.pollTime = func() time.Time { return now }
			poll(again)
			assertImportCalls(t, content, 1)
		})
	}
}

func TestImportPollCutoffPreservesFractionalLegacyCursor(t *testing.T) {
	h := NewHub(nil, nil, nil, nil, nil, nil)
	second := time.Now().Truncate(time.Second).Add(-10 * time.Second)
	legacy := second.Add(136 * time.Millisecond)
	h.lastPollAt["radarr"] = legacy
	now := second.Add(800 * time.Millisecond)
	h.pollTime = func() time.Time { return now }
	if got := h.importPollCutoff("radarr"); !got.Equal(legacy) {
		t.Fatalf("legacy cursor moved backward: %s", got)
	}
	now = second.Add(time.Second)
	if got := h.importPollCutoff("radarr"); !got.Equal(second.Add(time.Second - time.Nanosecond)) {
		t.Fatalf("closed second did not advance legacy cursor: %s", got)
	}
}

func TestWholeSecondImportAndUpgradeProofShareClosedWindow(t *testing.T) {
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		t.Run(service, func(t *testing.T) {
			b, h, content, poll := restartFixture(t, service)
			second := time.Now().Truncate(time.Second).Add(-10 * time.Second)
			now := second.Add(136 * time.Millisecond)
			h.pollTime = func() time.Time { return now }
			poll(h)
			field, event, id := "movieId", "movieFileDeleted", 7
			switch service {
			case "sonarr":
				field, event, id = "episodeId", "episodeFileDeleted", 71
			case "chaptarr":
				field, event = "bookId", "bookFileDeleted"
			case "lidarr":
				field, event = "albumId", "trackFileDeleted"
			}
			b.mu.Lock()
			b.history = b.receipt(second)
			b.deleteHistory = fmt.Sprintf(`[{"id":901,%q:%d,"eventType":%q,"date":%q,"data":{"reason":"Upgrade"}}]`, field, id, event, second.Format(time.RFC3339))
			b.mu.Unlock()
			upgradeCount := func() int {
				return len(content.upgradedMovieCalls()) + len(content.upgradedEpisodeCalls()) + len(content.upgradedBookCalls()) + len(content.upgradedMusicCalls())
			}
			now = second.Add(900 * time.Millisecond)
			poll(h)
			assertImportCalls(t, content, 0)
			if got := upgradeCount(); got != 0 {
				t.Fatalf("open-second upgrade alerts %d, want 0", got)
			}
			now = second.Add(time.Second)
			poll(h)
			assertImportCalls(t, content, 0)
			if got := upgradeCount(); got != 1 {
				t.Fatalf("closed-second upgrade alerts %d, want 1", got)
			}
			poll(h)
			if got := upgradeCount(); got != 1 {
				t.Fatalf("replayed upgrade alerts %d", got)
			}
		})
	}
}
