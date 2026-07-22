package shortener

import (
	"context"
	"errors"
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

func TestStats_TracksClicks(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	res, _ := svc.Shorten(ctx, "https://example.com", "")
	code := res.Link.Code

	for i := 0; i < 5; i++ {
		if err := svc.RecordClick(ctx, code, storage.Click{UserAgent: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := svc.Stats(ctx, code, 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.Link.ClickCount != 5 {
		t.Fatalf("ClickCount = %d, want 5", st.Link.ClickCount)
	}
	if len(st.RecentClicks) != 5 {
		t.Fatalf("RecentClicks = %d, want 5", len(st.RecentClicks))
	}
}
