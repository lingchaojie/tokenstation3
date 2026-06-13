//go:build embed

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// HTMLCache manages the cached index.html with injected settings.
// Cached HTML is keyed by the current public settings JSON hash so direct
// settings writers cannot leave stale window.__APP_CONFIG__ in memory.
type HTMLCache struct {
	mu           sync.RWMutex
	cachedHTML   []byte
	etag         string
	baseHTMLHash string // Hash of the original index.html (immutable after build)
	settingsHash string // Hash of the injected public settings JSON
}

// CachedHTML represents the cache state
type CachedHTML struct {
	Content      []byte
	ETag         string
	SettingsHash string
}

// NewHTMLCache creates a new HTML cache instance
func NewHTMLCache() *HTMLCache {
	return &HTMLCache{}
}

// SetBaseHTML initializes the cache with the base HTML template
func (c *HTMLCache) SetBaseHTML(baseHTML []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.baseHTMLHash = hashBytes(baseHTML)
}

// Invalidate marks the cache as stale
func (c *HTMLCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cachedHTML = nil
	c.etag = ""
	c.settingsHash = ""
}

// Get returns the cached HTML or nil if cache is stale for the current settings hash.
func (c *HTMLCache) Get(settingsHash string) *CachedHTML {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.cachedHTML == nil || c.settingsHash != settingsHash {
		return nil
	}
	return &CachedHTML{
		Content:      c.cachedHTML,
		ETag:         c.etag,
		SettingsHash: c.settingsHash,
	}
}

// Set updates the cache with new rendered HTML
func (c *HTMLCache) Set(html []byte, settingsJSON []byte) *CachedHTML {
	c.mu.Lock()
	defer c.mu.Unlock()

	settingsHash := hashBytes(settingsJSON)
	c.cachedHTML = html
	c.settingsHash = settingsHash
	c.etag = c.generateETag(settingsHash)

	return &CachedHTML{
		Content:      c.cachedHTML,
		ETag:         c.etag,
		SettingsHash: c.settingsHash,
	}
}

// generateETag creates an ETag from base HTML hash + settings hash
func (c *HTMLCache) generateETag(settingsHash string) string {
	return `"` + c.baseHTMLHash + "-" + settingsHash + `"`
}

func hashBytes(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:8]) // First 8 bytes for brevity
}
