package dns

import (
	"sync"
	"time"
)

type cacheEntry struct {
	ips       []string
	expiresAt time.Time
}

type Cache struct {
	mu       sync.RWMutex
	entries  map[string]*cacheEntry
	ttl      time.Duration
}

func NewCache(ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	c := &Cache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
	go c.cleanup()
	return c
}

func (c *Cache) Set(domain string, ips []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[domain] = &cacheEntry{
		ips:       ips,
		expiresAt: time.Now().Add(c.ttl),
	}
}

func (c *Cache) Get(domain string) ([]string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[domain]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.ips, true
}

func (c *Cache) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}