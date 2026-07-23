package storage

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// seqStart offsets the code sequence so the shortest generated code is a few
// characters long (base62(1_000_000) == "4C92") rather than "0"/"1".
const seqStart = uint64(1_000_000)

// MemoryStore is an in-memory Store implementation. It backs unit tests and
// lets the service run with zero external dependencies (DATABASE_URL unset).
// It is safe for concurrent use.
type MemoryStore struct {
	seq atomic.Uint64

	mu     sync.RWMutex
	nextID int64
	byCode map[string]*Link // code -> link
	byURL  map[string]*Link // original_url -> link (non-custom only)
}

// NewMemoryStore returns an empty, ready-to-use in-memory store.
func NewMemoryStore() *MemoryStore {
	m := &MemoryStore{
		byCode: make(map[string]*Link),
		byURL:  make(map[string]*Link),
	}
	m.seq.Store(seqStart - 1) // first NextSequence returns seqStart
	return m
}

func (m *MemoryStore) NextSequence(context.Context) (uint64, error) {
	return m.seq.Add(1), nil
}

func (m *MemoryStore) CreateLink(_ context.Context, link *Link) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if link.IsCustom {
		if _, exists := m.byCode[link.Code]; exists {
			return false, ErrAliasTaken
		}
	} else {
		if existing, exists := m.byURL[link.OriginalURL]; exists {
			*link = *existing // idempotent: return the existing mapping
			return false, nil
		}
		// A generated code can collide with a code already claimed as a custom
		// alias (both share the code namespace). Signal a retry with a fresh id.
		if _, exists := m.byCode[link.Code]; exists {
			return false, ErrCodeExists
		}
	}

	m.nextID++
	link.ID = m.nextID
	link.CreatedAt = time.Now().UTC()

	stored := *link // store a copy so callers can't mutate our state
	m.byCode[stored.Code] = &stored
	if !stored.IsCustom {
		m.byURL[stored.OriginalURL] = &stored
	}
	return true, nil
}

func (m *MemoryStore) GetByURL(_ context.Context, originalURL string) (*Link, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.byURL[originalURL]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *l
	return &cp, nil
}

func (m *MemoryStore) GetByCode(_ context.Context, code string) (*Link, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.byCode[code]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *l
	return &cp, nil
}

func (m *MemoryStore) Ping(context.Context) error { return nil }

func (m *MemoryStore) Close() error { return nil }
