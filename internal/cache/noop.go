package cache

import (
	"context"
	"time"
)

// Noop is a Cache that stores nothing — every Get is a miss. It is used when no
// REDIS_URL is configured, so the service transparently falls back to reading
// straight from the datastore.
type Noop struct{}

// NewNoop returns a disabled cache.
func NewNoop() *Noop { return &Noop{} }

func (Noop) Get(context.Context, string) (string, bool, error) { return "", false, nil }

func (Noop) Set(context.Context, string, string, time.Duration) error { return nil }

func (Noop) Ping(context.Context) error { return nil }

func (Noop) Close() error { return nil }
