package lib

import (
	"net/http"
	"time"
)

type CacheEntry struct {
	Data      []byte
	CreatedAt time.Time
	ExpiresIn *time.Duration
	Headers   http.Header
}

func (c *CacheEntry) Expired() bool {
	if c.ExpiresIn == nil {
		return false
	}
	return time.Since(c.CreatedAt) > *c.ExpiresIn
}

type Cache struct {
	entries map[string]*CacheEntry
}

func NewCache() *Cache {
	return &Cache{
		entries: make(map[string]*CacheEntry),
	}
}

func (c *Cache) Get(key string) *CacheEntry {
	entry, ok := c.entries[key]

	if !ok {
		return nil
	}

	if entry.Expired() {
		c.Delete(key)
		return nil
	}

	return entry
}

func (c *Cache) Set(key string, entry *CacheEntry) {
	c.entries[key] = entry
}

func (c *Cache) Delete(key string) {
	delete(c.entries, key)
}

func (c *Cache) Clear() {
	c.entries = make(map[string]*CacheEntry)
}
