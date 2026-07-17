package cache

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// waitFor polls cond until it returns true or the deadline passes. It exists
// so TTL tests never use a raw sleep as the assertion.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s: %s", timeout, msg)
}

func TestGetSetRoundTrip(t *testing.T) {
	c := New()
	t.Cleanup(c.Close)

	if data, ok := c.Get("missing"); ok {
		t.Fatalf("Get(missing) = (%q, true), want a miss", data)
	}

	c.Set("k", []byte(`{"a":1}`), time.Minute)
	data, ok := c.Get("k")
	if !ok || string(data) != `{"a":1}` {
		t.Fatalf("Get(k) = (%q, %v), want the cached payload", data, ok)
	}

	// Set on an existing key replaces the payload.
	c.Set("k", []byte("v2"), time.Minute)
	if data, ok := c.Get("k"); !ok || string(data) != "v2" {
		t.Fatalf("Get(k) after overwrite = (%q, %v), want v2", data, ok)
	}

	c.Delete("k")
	if data, ok := c.Get("k"); ok {
		t.Fatalf("Get(k) after Delete = (%q, true), want a miss", data)
	}
	// Deleting a missing key is a no-op, not a panic.
	c.Delete("k")
}

func TestGetHonorsTTLExpiry(t *testing.T) {
	c := New()
	t.Cleanup(c.Close)

	// Generous TTL so a scheduler stall between Set and Get cannot flake the
	// still-fresh assertion; expiry is then awaited, not raced.
	c.Set("k", []byte("v"), 250*time.Millisecond)
	if _, ok := c.Get("k"); !ok {
		t.Fatal("entry expired immediately after Set with a positive TTL")
	}
	waitFor(t, 2*time.Second, func() bool {
		_, ok := c.Get("k")
		return !ok
	}, "entry to expire after its TTL")

	// A non-positive TTL is already expired.
	c.Set("dead", []byte("v"), -time.Second)
	if data, ok := c.Get("dead"); ok {
		t.Fatalf("Get(dead) = (%q, true), want an already-expired miss", data)
	}
}

func TestSetRefreshesTTL(t *testing.T) {
	c := New()
	t.Cleanup(c.Close)

	start := time.Now()
	c.Set("k", []byte("v1"), 20*time.Millisecond)
	// Re-set with a long TTL before the short one lapses.
	c.Set("k", []byte("v2"), time.Hour)

	// Wait until the original TTL has definitely lapsed, then confirm the
	// refreshed entry is still served.
	waitFor(t, 2*time.Second, func() bool {
		return time.Since(start) > 50*time.Millisecond
	}, "original TTL to lapse")
	if data, ok := c.Get("k"); !ok || string(data) != "v2" {
		t.Fatalf("Get(k) after TTL refresh = (%q, %v), want v2 still cached", data, ok)
	}
}

// TestEvictRemovesOnlyExpiredEntries drives the eviction sweep directly (the
// background loop runs it on a 60s ticker, far too slow for a test) and checks
// that it drops expired entries while keeping live ones.
func TestEvictRemovesOnlyExpiredEntries(t *testing.T) {
	c := New()
	t.Cleanup(c.Close)

	c.Set("stale", []byte("old"), time.Nanosecond)
	c.Set("live", []byte("new"), time.Hour)
	waitFor(t, 2*time.Second, func() bool {
		_, ok := c.Get("stale")
		return !ok
	}, "stale entry to expire")

	c.evict()

	c.mu.RLock()
	_, staleKept := c.entries["stale"]
	_, liveKept := c.entries["live"]
	c.mu.RUnlock()
	if staleKept {
		t.Fatal("evict kept an expired entry in the map")
	}
	if !liveKept {
		t.Fatal("evict dropped a live entry")
	}
}

// TestCloseStopsEvictionGoroutine pins that Close terminates the background
// loop New starts, so short-lived caches don't leak goroutines.
func TestCloseStopsEvictionGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()

	caches := make([]*Cache, 10)
	for i := range caches {
		caches[i] = New()
	}
	for _, c := range caches {
		c.Close()
	}

	waitFor(t, 5*time.Second, func() bool {
		return runtime.NumGoroutine() <= before
	}, "eviction goroutines to exit after Close")
}

// TestConcurrentAccessIsRaceFree hammers Get/Set/Delete and the eviction
// sweep from many goroutines over a shared key space; go test -race proves
// the locking.
func TestConcurrentAccessIsRaceFree(t *testing.T) {
	c := New()
	t.Cleanup(c.Close)

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				key := fmt.Sprintf("k%d", i%10)
				switch (worker + i) % 4 {
				case 0:
					c.Set(key, []byte("v"), time.Millisecond*time.Duration(i%3))
				case 1:
					if data, ok := c.Get(key); ok && string(data) != "v" {
						t.Errorf("Get(%s) returned corrupted payload %q", key, data)
					}
				case 2:
					c.Delete(key)
				case 3:
					c.evict()
				}
			}
		}(worker)
	}
	wg.Wait()
}
