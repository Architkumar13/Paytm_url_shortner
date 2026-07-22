package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemoryCache_MissThenHit(t *testing.T) {
	ctx := context.Background()
	c := NewMemory()

	if _, found, _ := c.Get(ctx, "url:x"); found {
		t.Fatal("expected miss on empty cache")
	}
	if err := c.Set(ctx, "url:x", "https://example.com", time.Minute); err != nil {
		t.Fatal(err)
	}
	v, found, err := c.Get(ctx, "url:x")
	if err != nil || !found {
		t.Fatalf("expected hit: found=%v err=%v", found, err)
	}
	if v != "https://example.com" {
		t.Fatalf("got %q", v)
	}
}

func TestMemoryCache_Expiry(t *testing.T) {
	ctx := context.Background()
	c := NewMemory()
	if err := c.Set(ctx, "k", "v", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, found, _ := c.Get(ctx, "k"); found {
		t.Fatal("expected entry to have expired")
	}
}

func TestKey(t *testing.T) {
	if Key("abc") != "url:abc" {
		t.Fatalf("Key(abc) = %q", Key("abc"))
	}
}

var _ Cache = (*Memory)(nil)
var _ Cache = (*Noop)(nil)
