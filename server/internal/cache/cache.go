package cache

import (
	"sync"
	"time"
)

type entry struct {
	data      []byte
	expiresAt time.Time
}

// Cache is a simple in-memory TTL cache.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]entry
	stop    chan struct{}
}

// New creates a cache and starts a background eviction goroutine.
func New() *Cache {
	c := &Cache{
		entries: make(map[string]entry),
		stop:    make(chan struct{}),
	}
	go c.evictLoop()
	return c
}

// Get returns cached data if present and not expired.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.data, true
}

// Set stores data with the given TTL.
func (c *Cache) Set(key string, data []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry{data: data, expiresAt: time.Now().Add(ttl)}
}

// Close stops the eviction goroutine.
func (c *Cache) Close() {
	close(c.stop)
}

func (c *Cache) evictLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.evict()
		case <-c.stop:
			return
		}
	}
}

func (c *Cache) evict() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, k)
		}
	}
}
