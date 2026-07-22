// Package config loads runtime configuration from the environment.
package config

import (
	"os"
	"strings"
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
	}
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
