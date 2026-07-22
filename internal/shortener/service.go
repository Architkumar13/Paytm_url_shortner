package shortener

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"urlshortener/internal/cache"
	"urlshortener/internal/storage"
	"urlshortener/internal/validate"
)

// maxCodeAttempts bounds how many times Shorten mints a fresh id when a
// generated code collides with an existing one (a custom alias occupying the
// same code namespace). Each attempt draws a distinct id, so a small bound
// tolerates several pre-claimed codes while still failing fast if something is
// pathologically wrong rather than looping forever.
const maxCodeAttempts = 10

// Service holds the URL-shortening business logic. It is transport-agnostic
// (no HTTP types leak in) and depends only on interfaces — storage.Store,
// IDGenerator and cache.Cache — which keeps it trivially unit-testable and lets
// each collaborator be swapped independently.
type Service struct {
	store    storage.Store
	idgen    IDGenerator
	cache    cache.Cache
	baseURL  string
	cacheTTL time.Duration
}

// New wires a Service. idgen supplies collision-free ids; cache is the
// read-through cache for redirects (pass cache.NewNoop() to disable).
func New(store storage.Store, idgen IDGenerator, c cache.Cache, baseURL string, cacheTTL time.Duration) *Service {
	return &Service{
		store:    store,
		idgen:    idgen,
		cache:    c,
		baseURL:  baseURL,
		cacheTTL: cacheTTL,
	}
}

// ShortenResult is the outcome of a Shorten call.
type ShortenResult struct {
	Link *storage.Link
	// Created is true when a new mapping was persisted, false when an existing
	// one was returned (idempotent de-duplication or alias race).
	Created bool
}

// Shorten validates rawURL and returns a short-code mapping.
//
// Duplicate-URL policy (deliberate):
//   - No alias: identical URLs de-duplicate to a single code. Repeated calls
//     return the same mapping with Created=false.
//   - Custom alias: always creates a new mapping, so one URL can have several
//     aliases. A clash on the alias returns storage.ErrAliasTaken.
func (s *Service) Shorten(ctx context.Context, rawURL, alias string) (*ShortenResult, error) {
	normalized, err := validate.NormalizeURL(rawURL)
	if err != nil {
		return nil, err
	}

	if alias != "" {
		if err := validate.Alias(alias); err != nil {
			return nil, err
		}
		link := &storage.Link{Code: alias, OriginalURL: normalized, IsCustom: true}
		created, err := s.store.CreateLink(ctx, link)
		if err != nil {
			return nil, err
		}
		return &ShortenResult{Link: link, Created: created}, nil
	}

	// Fast path: return an existing mapping without burning an id.
	if existing, err := s.store.GetByURL(ctx, normalized); err == nil {
		return &ShortenResult{Link: existing, Created: false}, nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}

	// Collision-free code = base62 of a unique id (see idgen.go). base62(id) is
	// unique across auto-generated codes by construction, but the code namespace
	// is shared with custom aliases, so a generated code can land on one a user
	// already claimed. In that (rare) case CreateLink returns ErrCodeExists and
	// we mint a fresh id and retry, bounded by maxCodeAttempts.
	for attempt := 0; attempt < maxCodeAttempts; attempt++ {
		id, err := s.idgen.NextID(ctx)
		if err != nil {
			return nil, err
		}
		link := &storage.Link{Code: Encode(id), OriginalURL: normalized, IsCustom: false}
		// CreateLink is idempotent on the URL, so a concurrent creator is handled:
		// link is overwritten with the winning row and Created reports false.
		created, err := s.store.CreateLink(ctx, link)
		if err == nil {
			return &ShortenResult{Link: link, Created: created}, nil
		}
		if errors.Is(err, storage.ErrCodeExists) {
			continue // generated code clashed with a custom alias; try a new id
		}
		return nil, err
	}
	return nil, fmt.Errorf("could not allocate a free short code after %d attempts", maxCodeAttempts)
}

// ResolveURL returns the original URL for a code, using the read-through cache:
// on a hit it avoids the datastore entirely; on a miss it loads from the store
// and populates the cache. This is the hot redirect path. Returns
// storage.ErrNotFound for an unknown code.
//
// The code->URL mapping is immutable once created, so cached entries never go
// stale and no invalidation is required.
func (s *Service) ResolveURL(ctx context.Context, code string) (string, error) {
	key := cache.Key(code)
	if url, found, err := s.cache.Get(ctx, key); err != nil {
		log.Printf("cache get %q: %v", code, err) // degrade gracefully to the store
	} else if found {
		return url, nil
	}

	link, err := s.store.GetByCode(ctx, code)
	if err != nil {
		return "", err
	}
	if err := s.cache.Set(ctx, key, link.OriginalURL, s.cacheTTL); err != nil {
		log.Printf("cache set %q: %v", code, err) // best-effort
	}
	return link.OriginalURL, nil
}

// RecordClick records analytics for a visit. It is best-effort: the caller
// should not fail a redirect if this errors.
func (s *Service) RecordClick(ctx context.Context, code string, click storage.Click) error {
	return s.store.RecordClick(ctx, code, click)
}

// Stats bundles a link with its most recent clicks for the stats endpoint.
// It reads from the datastore directly (not the cache) so counters are fresh.
type Stats struct {
	Link         *storage.Link
	RecentClicks []storage.Click
}

// Stats returns analytics for code, or storage.ErrNotFound.
func (s *Service) Stats(ctx context.Context, code string, limit int) (*Stats, error) {
	link, err := s.store.GetByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	clicks, err := s.store.RecentClicks(ctx, code, limit)
	if err != nil {
		return nil, err
	}
	return &Stats{Link: link, RecentClicks: clicks}, nil
}

// Ping reports health of the datastore and cache.
func (s *Service) Ping(ctx context.Context) error {
	if err := s.store.Ping(ctx); err != nil {
		return err
	}
	return s.cache.Ping(ctx)
}

// ShortURL builds the absolute short link for a code.
func (s *Service) ShortURL(code string) string {
	return s.baseURL + "/" + code
}
