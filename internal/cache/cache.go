// Package cache provides the read-through cache abstraction used on the hot
// redirect path. Reads (redirects) far outnumber writes, so caching code->URL
// lookups is the highest-leverage optimization. Redis is the production
// implementation; a no-op is used when caching is disabled and an in-memory
// one backs tests.
package cache

import (
	"context"
	"time"
)

// Cache stores short string values (code -> original URL) with a TTL.
// Implementations must be safe for concurrent use.
type Cache interface {
	// Get returns the value for key. found is false on a cache miss.
	Get(ctx context.Context, key string) (value string, found bool, err error)
	// Set stores value under key for ttl (ttl <= 0 means no expiry).
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	// Ping verifies the cache is reachable.
	Ping(ctx context.Context) error
	// Close releases resources.
	Close() error
}

// Key builds the cache key for a short code. Centralized so the key scheme is
// consistent across the codebase.
func Key(code string) string { return "url:" + code }
