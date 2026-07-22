package shortener

import (
	"context"

	"urlshortener/internal/storage"
)

// IDGenerator produces the unique integers that back collision-free short
// codes. Because every ID is unique and Encode is a bijection,
// base62(NextID()) can never collide — no hashing, Bloom filter, or existence
// check is needed on the write path.
//
// Keeping this an interface lets the generation strategy be swapped without
// touching the service: a centralized DB sequence for a single instance, or a
// coordination-free generator (Snowflake) when many instances mint IDs.
type IDGenerator interface {
	NextID(ctx context.Context) (uint64, error)
}

// SequenceGenerator sources IDs from the datastore's monotonic sequence
// (a Postgres SEQUENCE, or an atomic counter in the in-memory store). This is
// the default: simple and strictly increasing, ideal for a single instance.
type SequenceGenerator struct {
	store storage.Store
}

// NewSequenceGenerator returns a generator backed by store.NextSequence.
func NewSequenceGenerator(store storage.Store) *SequenceGenerator {
	return &SequenceGenerator{store: store}
}

func (g *SequenceGenerator) NextID(ctx context.Context) (uint64, error) {
	return g.store.NextSequence(ctx)
}
