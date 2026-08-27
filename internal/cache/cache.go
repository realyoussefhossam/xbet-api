// Package cache provides a small in-memory TTL cache for feed responses.
package cache

import (
	"sync"
	"time"
)

type entry struct {
	value  any
	expiry time.Time
}

// Cache is a concurrency-safe TTL cache.
type Cache struct {
	mu   sync.Mutex
	data map[string]entry
	ttl  time.Duration
}

// New creates a cache with the given TTL. TTL <= 0 disables expiry.
func New(ttl time.Duration) *Cache {
	return &Cache{data: map[string]entry{}, ttl: ttl}
}

// Get returns the cached value for key and whether it was found.
func (c *Cache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[key]
	if !ok {
		return nil, false
	}
	if c.ttl > 0 && time.Now().After(e.expiry) {
		delete(c.data, key)
		return nil, false
	}
	return e.value, true
}

// Set stores value under key with the cache TTL.
func (c *Cache) Set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = entry{value: value, expiry: time.Now().Add(c.ttl)}
}

// GetOrLoad returns the cached value, or computes and stores it via load.
func (c *Cache) GetOrLoad(key string, load func() (any, error)) (any, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	v, err := load()
	if err != nil {
		return nil, err
	}
	c.Set(key, v)
	return v, nil
}

// Len returns the number of cached entries (for stats/debug).
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.data)
}
