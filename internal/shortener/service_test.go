package shortener

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"urlshortener/internal/cache"
	"urlshortener/internal/storage"
	"urlshortener/internal/validate"
)

func newService() *Service {
	store := storage.NewMemoryStore()
	return New(store, NewSequenceGenerator(store), cache.NewNoop(), "http://short.test", time.Minute)
}

// scriptedIDGen returns a fixed list of ids in order, so tests can force the
// exact codes Shorten will try (independent of the store's sequence).
type scriptedIDGen struct {
	ids []uint64
	i   int
}

func (g *scriptedIDGen) NextID(context.Context) (uint64, error) {
	if g.i >= len(g.ids) {
		return 0, fmt.Errorf("scriptedIDGen exhausted after %d ids", g.i)
	}
	id := g.ids[g.i]
	g.i++
	return id, nil
}

func TestShorten_NewURL(t *testing.T) {
	svc := newService()
	res, err := svc.Shorten(context.Background(), "https://example.com/page", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created {
		t.Fatal("expected Created=true for a new URL")
	}
	if res.Link.Code == "" {
		t.Fatal("expected a non-empty code")
	}
	if got := svc.ShortURL(res.Link.Code); got != "http://short.test/"+res.Link.Code {
		t.Fatalf("ShortURL = %q", got)
	}
}

func TestShorten_DeduplicatesSameURL(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	first, err := svc.Shorten(ctx, "https://example.com/same", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Shorten(ctx, "https://example.com/same", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.Created {
		t.Fatal("expected Created=false on duplicate URL")
	}
	if first.Link.Code != second.Link.Code {
		t.Fatalf("expected same code for duplicate URL: %q vs %q", first.Link.Code, second.Link.Code)
	}
}

func TestShorten_CustomAliasAlwaysNewMapping(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	res, err := svc.Shorten(ctx, "https://example.com", "promo")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Link.Code != "promo" || !res.Link.IsCustom {
		t.Fatalf("unexpected result: %+v", res.Link)
	}

	// Same URL, different alias -> a second, independent mapping.
	res2, err := svc.Shorten(ctx, "https://example.com", "promo2")
	if err != nil {
		t.Fatal(err)
	}
	if res2.Link.Code != "promo2" {
		t.Fatalf("expected code promo2, got %q", res2.Link.Code)
	}
}

func TestShorten_AliasConflict(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	if _, err := svc.Shorten(ctx, "https://a.com", "dup"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Shorten(ctx, "https://b.com", "dup")
	if !errors.Is(err, storage.ErrAliasTaken) {
		t.Fatalf("expected ErrAliasTaken, got %v", err)
	}
}

// TestShorten_RetriesWhenGeneratedCodeClashesWithAlias covers the case where a
// user has claimed a custom alias equal to a code the generator will later
// produce. The generated code shares the alias's namespace, so the service must
// skip the taken code and mint a working one — not fail the request.
func TestShorten_RetriesWhenGeneratedCodeClashesWithAlias(t *testing.T) {
	store := storage.NewMemoryStore()
	// First id -> a code we pre-claim as an alias; second id -> a free code.
	gen := &scriptedIDGen{ids: []uint64{1_000_000, 1_000_001}}
	svc := New(store, gen, cache.NewNoop(), "http://short.test", time.Minute)
	ctx := context.Background()

	claimed := Encode(1_000_000)
	if _, err := svc.Shorten(ctx, "https://example.com/claimed", claimed); err != nil {
		t.Fatalf("claim alias %q: %v", claimed, err)
	}

	res, err := svc.Shorten(ctx, "https://example.com/auto", "")
	if err != nil {
		t.Fatalf("auto shorten must succeed by retrying past the taken code, got: %v", err)
	}
	if !res.Created {
		t.Fatal("expected a newly created mapping")
	}
	if res.Link.Code == claimed {
		t.Fatalf("generated code collided with claimed alias %q", claimed)
	}
	if want := Encode(1_000_001); res.Link.Code != want {
		t.Fatalf("code = %q, want the retried id's code %q", res.Link.Code, want)
	}

	// The freshly created code must round-trip.
	got, err := svc.ResolveURL(ctx, res.Link.Code)
	if err != nil || got != "https://example.com/auto" {
		t.Fatalf("resolve %q = %q err=%v", res.Link.Code, got, err)
	}
}

// TestShorten_FailsWhenAllGeneratedCodesClash asserts the retry is bounded: if
// every code the generator produces is already taken, Shorten returns an error
// rather than looping forever.
func TestShorten_FailsWhenAllGeneratedCodesClash(t *testing.T) {
	store := storage.NewMemoryStore()
	ids := make([]uint64, maxCodeAttempts)
	for i := range ids {
		ids[i] = 1_000_000 + uint64(i)
	}
	gen := &scriptedIDGen{ids: ids}
	svc := New(store, gen, cache.NewNoop(), "http://short.test", time.Minute)
	ctx := context.Background()

	// Pre-claim every code the generator will produce, as custom aliases.
	for _, id := range ids {
		code := Encode(id)
		if _, err := svc.Shorten(ctx, "https://example.com/"+code, code); err != nil {
			t.Fatalf("claim alias %q: %v", code, err)
		}
	}

	if _, err := svc.Shorten(ctx, "https://example.com/auto", ""); err == nil {
		t.Fatal("expected shorten to fail after exhausting code attempts, got nil")
	}
}

func TestShorten_InvalidInputs(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	if _, err := svc.Shorten(ctx, "not-a-url", ""); err == nil {
		t.Fatal("expected validation error for bad URL")
	} else {
		var ve *validate.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected *validate.ValidationError, got %v", err)
		}
	}

	if _, err := svc.Shorten(ctx, "https://ok.com", "bad alias!"); err == nil {
		t.Fatal("expected validation error for bad alias")
	}
}

func TestResolveURL_UnknownReturnsNotFound(t *testing.T) {
	svc := newService()
	if _, err := svc.ResolveURL(context.Background(), "nope"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestResolveURL_ReadThroughCache(t *testing.T) {
	store := storage.NewMemoryStore()
	c := cache.NewMemory()
	svc := New(store, NewSequenceGenerator(store), c, "http://short.test", time.Minute)
	ctx := context.Background()

	res, err := svc.Shorten(ctx, "https://example.com/cached", "")
	if err != nil {
		t.Fatal(err)
	}
	code := res.Link.Code

	// First resolve: cache miss -> store -> cache populated.
	url, err := svc.ResolveURL(ctx, code)
	if err != nil || url != "https://example.com/cached" {
		t.Fatalf("resolve = %q err=%v", url, err)
	}
	if v, found, _ := c.Get(ctx, cache.Key(code)); !found || v != "https://example.com/cached" {
		t.Fatalf("expected cache populated: found=%v v=%q", found, v)
	}

	// Second resolve is served correctly (from cache).
	if url2, _ := svc.ResolveURL(ctx, code); url2 != url {
		t.Fatalf("second resolve = %q, want %q", url2, url)
	}
}

// TestShorten_CodesAreUnique shortens many distinct URLs and asserts every
// generated code is unique — the end-to-end collision-free guarantee.
func TestShorten_CodesAreUnique(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	seen := make(map[string]bool)
	for i := 0; i < 5000; i++ {
		url := "https://example.com/page/" + Encode(uint64(i))
		res, err := svc.Shorten(ctx, url, "")
		if err != nil {
			t.Fatal(err)
		}
		if seen[res.Link.Code] {
			t.Fatalf("duplicate code %q generated", res.Link.Code)
		}
		seen[res.Link.Code] = true
	}
}
