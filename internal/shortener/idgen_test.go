package shortener

import (
	"context"
	"sync"
	"testing"

	"urlshortener/internal/storage"
)

func TestSequenceGenerator_UniqueAndIncreasing(t *testing.T) {
	g := NewSequenceGenerator(storage.NewMemoryStore())
	ctx := context.Background()

	var prev uint64
	seen := make(map[uint64]bool)
	for i := 0; i < 1000; i++ {
		id, err := g.NextID(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("duplicate id %d", id)
		}
		if i > 0 && id <= prev {
			t.Fatalf("id not increasing: %d after %d", id, prev)
		}
		seen[id] = true
		prev = id
	}
}

func TestSnowflake_RejectsBadMachineID(t *testing.T) {
	if _, err := NewSnowflake(-1); err == nil {
		t.Fatal("expected error for negative machine id")
	}
	if _, err := NewSnowflake(maxMachineID + 1); err == nil {
		t.Fatal("expected error for too-large machine id")
	}
}

// TestSnowflake_UniqueUnderConcurrency generates many IDs from many goroutines
// and asserts they are all unique — the collision-free guarantee for the
// distributed generator.
func TestSnowflake_UniqueUnderConcurrency(t *testing.T) {
	gen, err := NewSnowflake(7)
	if err != nil {
		t.Fatal(err)
	}
	const goroutines, per = 16, 2000
	var wg sync.WaitGroup
	out := make(chan uint64, goroutines*per)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				id, err := gen.NextID(context.Background())
				if err != nil {
					t.Errorf("NextID: %v", err)
					return
				}
				out <- id
			}
		}()
	}
	wg.Wait()
	close(out)

	seen := make(map[uint64]bool, goroutines*per)
	for id := range out {
		if seen[id] {
			t.Fatalf("duplicate snowflake id %d", id)
		}
		seen[id] = true
	}
	if len(seen) != goroutines*per {
		t.Fatalf("got %d unique ids, want %d", len(seen), goroutines*per)
	}
}

// Both concrete generators satisfy the interface.
var (
	_ IDGenerator = (*SequenceGenerator)(nil)
	_ IDGenerator = (*Snowflake)(nil)
)
