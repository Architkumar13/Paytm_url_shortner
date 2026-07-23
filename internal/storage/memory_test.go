package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestMemoryStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()

	link := &Link{Code: "abc", OriginalURL: "https://example.com", IsCustom: false}
	created, err := m.CreateLink(ctx, link)
	if err != nil || !created {
		t.Fatalf("CreateLink: created=%v err=%v", created, err)
	}
	if link.ID == 0 || link.CreatedAt.IsZero() {
		t.Fatalf("CreateLink did not populate ID/CreatedAt: %+v", link)
	}

	got, err := m.GetByCode(ctx, "abc")
	if err != nil {
		t.Fatalf("GetByCode: %v", err)
	}
	if got.OriginalURL != "https://example.com" {
		t.Fatalf("GetByCode returned %q", got.OriginalURL)
	}
}

func TestMemoryStore_GetByCodeNotFound(t *testing.T) {
	if _, err := NewMemoryStore().GetByCode(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_DedupNonCustom(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()

	first := &Link{Code: "code1", OriginalURL: "https://dup.com"}
	if _, err := m.CreateLink(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := &Link{Code: "code2", OriginalURL: "https://dup.com"}
	created, err := m.CreateLink(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("expected second insert of same URL to be idempotent (created=false)")
	}
	if second.Code != "code1" {
		t.Fatalf("expected existing code %q, got %q", "code1", second.Code)
	}
}

func TestMemoryStore_AliasTaken(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()

	if _, err := m.CreateLink(ctx, &Link{Code: "promo", OriginalURL: "https://a.com", IsCustom: true}); err != nil {
		t.Fatal(err)
	}
	_, err := m.CreateLink(ctx, &Link{Code: "promo", OriginalURL: "https://b.com", IsCustom: true})
	if !errors.Is(err, ErrAliasTaken) {
		t.Fatalf("expected ErrAliasTaken, got %v", err)
	}
}

// TestMemoryStore_AutoCodeCollisionReturnsErrCodeExists asserts that an
// auto-generated (non-custom) insert whose code is already taken by a custom
// alias reports ErrCodeExists — distinct from ErrAliasTaken — so the service
// can retry with a fresh id rather than surfacing the clash to the client.
func TestMemoryStore_AutoCodeCollisionReturnsErrCodeExists(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()

	// A custom alias occupies code "4C92".
	if _, err := m.CreateLink(ctx, &Link{Code: "4C92", OriginalURL: "https://a.com", IsCustom: true}); err != nil {
		t.Fatal(err)
	}

	// An auto-generated code lands on the same string but a different URL.
	_, err := m.CreateLink(ctx, &Link{Code: "4C92", OriginalURL: "https://b.com", IsCustom: false})
	if !errors.Is(err, ErrCodeExists) {
		t.Fatalf("expected ErrCodeExists, got %v", err)
	}
}

// TestMemoryStore_NextSequenceUnique hammers NextSequence concurrently and
// asserts every value is unique — the foundation of collision-free codes.
func TestMemoryStore_NextSequenceUnique(t *testing.T) {
	m := NewMemoryStore()
	const goroutines, per = 20, 500
	var wg sync.WaitGroup
	results := make(chan uint64, goroutines*per)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				v, _ := m.NextSequence(context.Background())
				results <- v
			}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[uint64]bool, goroutines*per)
	for v := range results {
		if seen[v] {
			t.Fatalf("duplicate sequence value %d", v)
		}
		seen[v] = true
	}
	if len(seen) != goroutines*per {
		t.Fatalf("got %d unique values, want %d", len(seen), goroutines*per)
	}
}
