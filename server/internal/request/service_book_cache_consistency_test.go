package request

import (
	"testing"
	"time"
)

func TestBookCacheInvalidationWinsAgainstInFlightBuilders(t *testing.T) {
	service := NewService(nil, nil, nil, nil)
	const instanceID = "books"

	// Model a live-status or owned-library builder that fetched pre-mutation
	// truth and has not published it yet.
	projectionLock := service.projectionLock(instanceID)
	projectionLock.Lock()
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		service.invalidateBookCaches(instanceID)
		close(done)
	}()
	<-started

	select {
	case <-done:
		projectionLock.Unlock()
		t.Fatal("cache invalidation did not wait for the in-flight builder")
	case <-time.After(20 * time.Millisecond):
	}

	service.libraryCache.Set("book-library:"+instanceID, []byte(`{"stale":true}`), time.Minute)
	service.libraryCache.Set("book-live:"+instanceID, []byte(`{"stale":true}`), time.Minute)
	projectionLock.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cache invalidation stayed blocked after the builder completed")
	}
	for _, key := range []string{"book-library:" + instanceID, "book-live:" + instanceID} {
		if _, ok := service.libraryCache.Get(key); ok {
			t.Fatalf("stale cache entry %q survived mutation invalidation", key)
		}
	}
}
