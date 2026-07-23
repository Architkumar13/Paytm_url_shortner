// Package storage defines the persistence contract for links, plus the
// in-memory and Postgres implementations of it.
package storage

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a code has no mapping.
	ErrNotFound = errors.New("code not found")
	// ErrAliasTaken is returned when a requested custom alias already exists.
	ErrAliasTaken = errors.New("alias already taken")
	// ErrCodeExists is returned when an auto-generated code collides with an
	// existing code (e.g. one already claimed as a custom alias — the two share
	// the code namespace). It signals the service to retry with a fresh id; it
	// is never surfaced to clients.
	ErrCodeExists = errors.New("code already exists")
)

// Link is a stored short-code → URL mapping.
type Link struct {
	ID          int64     `json:"-"`
	Code        string    `json:"code"`
	OriginalURL string    `json:"original_url"`
	IsCustom    bool      `json:"is_custom"`
	CreatedAt   time.Time `json:"created_at"`
}

// Store is the persistence contract. Implementations must be safe for
// concurrent use by multiple goroutines.
type Store interface {
	// NextSequence returns a unique, strictly increasing number. Generated
	// codes are the base62 encoding of these values, which is what makes them
	// collision-free.
	NextSequence(ctx context.Context) (uint64, error)

	// CreateLink persists link and populates its ID/CreatedAt.
	//
	//   - Custom link (IsCustom == true): returns ErrAliasTaken if the code
	//     already exists.
	//   - Auto link (IsCustom == false): idempotent on OriginalURL. If a
	//     non-custom mapping for that URL already exists (e.g. a concurrent
	//     request created it first), *link is overwritten with the existing
	//     row and created is false. If the generated Code is already taken (by a
	//     custom alias or another mapping) it returns ErrCodeExists, so the
	//     caller can retry with a fresh id.
	//
	// created reports whether a new row was actually inserted.
	CreateLink(ctx context.Context, link *Link) (created bool, err error)

	// GetByURL returns the existing non-custom mapping for a URL, or
	// ErrNotFound. It backs idempotent de-duplication.
	GetByURL(ctx context.Context, originalURL string) (*Link, error)

	// GetByCode returns the mapping for a code, or ErrNotFound.
	GetByCode(ctx context.Context, code string) (*Link, error)

	// Ping verifies the datastore is reachable.
	Ping(ctx context.Context) error

	// Close releases any held resources.
	Close() error
}
