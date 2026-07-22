// Package config loads runtime configuration from the environment.
package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the service.
type Config struct {
	// Port is the TCP port the HTTP server listens on.
	Port string
	// DatabaseURL is the Postgres DSN. When empty, the service falls back to an
	// in-memory store, which keeps `go run` and unit tests dependency-free.
	DatabaseURL string
	// BaseURL is the public origin used to build absolute short links in
	// responses, e.g. "http://localhost:8080".
	BaseURL string
	// RedisURL is the redis:// URL for the read-through cache. When empty,
	// caching is disabled (a no-op cache is used).
	RedisURL string
	// CacheTTL is how long code->URL entries live in the cache.
	CacheTTL time.Duration
	// IDGenerator selects the collision-free id source: "sequence" (default,
	// datastore counter) or "snowflake" (coordination-free, distributed).
	IDGenerator string
	// MachineID identifies this instance for the snowflake generator (0..1023).
	MachineID int64
}

// Load reads configuration from the environment, applying sensible defaults so
// the service is runnable with zero configuration during development.
func Load() Config {
	port := getenv("PORT", "8080")
	base := getenv("BASE_URL", "http://localhost:"+port)
	return Config{
		Port:        port,
		DatabaseURL: strings.TrimSpace(os.Getenv("DATABASE_URL")),
		BaseURL:     strings.TrimRight(base, "/"),
		RedisURL:    strings.TrimSpace(os.Getenv("REDIS_URL")),
		CacheTTL:    getdur("CACHE_TTL", 24*time.Hour),
		IDGenerator: strings.ToLower(getenv("ID_GENERATOR", "sequence")),
		MachineID:   getint("MACHINE_ID", 0),
	}
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func getdur(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("config: invalid %s=%q, using default %s", key, v, def)
		return def
	}
	return d
}

func getint(key string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		log.Printf("config: invalid %s=%q, using default %d", key, v, def)
		return def
	}
	return n
}
