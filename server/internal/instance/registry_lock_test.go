package instance

import (
	"testing"
	"time"
)

func TestInstanceConfigWriteWaitsForMutationReaders(t *testing.T) {
	registry := NewRegistry(nil)
	releaseRead := registry.LockInstanceConfigRead("chaptarr-1")
	started := make(chan struct{})
	acquired := make(chan struct{})
	go func() {
		close(started)
		releaseWrite := registry.LockInstanceConfigWrite("chaptarr-1")
		close(acquired)
		releaseWrite()
	}()
	<-started
	select {
	case <-acquired:
		t.Fatal("configuration writer passed an active mutation reader")
	case <-time.After(25 * time.Millisecond):
	}
	releaseRead()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("configuration writer did not resume after mutation reader released")
	}
}

func TestInstanceConfigLocksAreScopedByInstance(t *testing.T) {
	registry := NewRegistry(nil)
	releaseRead := registry.LockInstanceConfigRead("chaptarr-1")
	defer releaseRead()

	acquired := make(chan struct{})
	go func() {
		releaseWrite := registry.LockInstanceConfigWrite("chaptarr-2")
		close(acquired)
		releaseWrite()
	}()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("unrelated instance configuration was globally serialized")
	}
}
