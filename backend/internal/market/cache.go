package market

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Cache stores fetched series so restarts and repeated analyses don't hammer
// free data providers (Yahoo rate-limits aggressively). The app uses the
// Postgres implementation (store.PriceCache); DiskCache serves tests.
type Cache interface {
	// Get returns the series and whether it is younger than maxAge. A stale
	// series is still returned (fresh=false) so callers can fall back to it.
	Get(key string, maxAge time.Duration) (*Series, bool)
	Put(key string, s *Series) error
}

// DiskCache stores fetched series as JSON files.
type DiskCache struct {
	dir string
	mu  sync.Mutex
}

func NewDiskCache(dir string) (*DiskCache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &DiskCache{dir: dir}, nil
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func (c *DiskCache) path(key string) string {
	return filepath.Join(c.dir, unsafeChars.ReplaceAllString(key, "_")+".json")
}

// Get returns a cached series if it exists and is younger than maxAge.
func (c *DiskCache) Get(key string, maxAge time.Duration) (*Series, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := os.ReadFile(c.path(key))
	if err != nil {
		return nil, false
	}
	var s Series
	if json.Unmarshal(b, &s) != nil || len(s.Bars) == 0 {
		return nil, false
	}
	if maxAge > 0 && time.Since(s.FetchedAt) > maxAge {
		return &s, false // stale but returned so callers can fall back to it
	}
	return &s, true
}

func (c *DiskCache) Put(key string, s *Series) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := c.path(key) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path(key))
}
