package shortener

import (
	"context"
	"errors"

	"urlshortener/internal/storage"
	"urlshortener/internal/validate"
)

// Service holds the URL-shortening business logic. It is transport-agnostic
// (no HTTP types leak in) and depends only on the storage.Store interface,
// which keeps it trivially unit-testable against the in-memory store.
type Service struct {
	store   storage.Store
	baseURL string
}

// New returns a Service backed by store, using baseURL to build absolute short
// links (e.g. "http://localhost:8080").
func New(store storage.Store, baseURL string) *Service {
	return &Service{store: store, baseURL: baseURL}
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

	// Fast path: return an existing mapping without burning a sequence value.
	if existing, err := s.store.GetByURL(ctx, normalized); err == nil {
		return &ShortenResult{Link: existing, Created: false}, nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return nil, err
	}

	seq, err := s.store.NextSequence(ctx)
	if err != nil {
		return nil, err
	}
	link := &storage.Link{Code: Encode(seq), OriginalURL: normalized, IsCustom: false}
	// CreateLink is idempotent on the URL, so a concurrent creator is handled:
	// link is overwritten with the winning row and Created reports false.
	created, err := s.store.CreateLink(ctx, link)
	if err != nil {
		return nil, err
	}
	return &ShortenResult{Link: link, Created: created}, nil
}

// Resolve returns the mapping for code, or storage.ErrNotFound.
func (s *Service) Resolve(ctx context.Context, code string) (*storage.Link, error) {
	return s.store.GetByCode(ctx, code)
}

// RecordClick records analytics for a visit. It is best-effort: the caller
// should not fail a redirect if this errors.
func (s *Service) RecordClick(ctx context.Context, code string, click storage.Click) error {
	return s.store.RecordClick(ctx, code, click)
}

// Stats bundles a link with its most recent clicks for the stats endpoint.
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

// Ping reports datastore health.
func (s *Service) Ping(ctx context.Context) error {
	return s.store.Ping(ctx)
}

// ShortURL builds the absolute short link for a code.
func (s *Service) ShortURL(code string) string {
	return s.baseURL + "/" + code
}
