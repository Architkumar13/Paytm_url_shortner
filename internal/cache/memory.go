package cache

import (
	"context"
	"sync"
	"time"
)

// Memory is an in-process Cache. It backs unit tests and can serve as a
// single-instance cache. It is safe for concurrent use.
type Memory struct {
	mu sync.RWMutex
	m  map[string]item
}

type item struct {
	value string
	exp   time.Time // zero => never expires
}

// NewMemory returns an empty in-memory cache.
func NewMemory() *Memory {
	return &Memory{m: make(map[string]item)}
}

func (c *Memory) Get(_ context.Context, key string) (string, bool, error) {
	c.mu.RLock()
	it, ok := c.m[key]
	c.mu.RUnlock()
	if !ok {
		return "", false, nil
	}
	if !it.exp.IsZero() && time.Now().After(it.exp) {
		c.mu.Lock()
		delete(c.m, key)
		c.mu.Unlock()
		return "", false, nil
	}
	return it.value, true, nil
}

func (c *Memory) Set(_ context.Context, key, value string, ttl time.Duration) error {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	c.mu.Lock()
	c.m[key] = item{value: value, exp: exp}
	c.mu.Unlock()
	return nil
}

func (c *Memory) Ping(context.Context) error { return nil }

func (c *Memory) Close() error { return nil }
