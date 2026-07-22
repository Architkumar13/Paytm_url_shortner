//go:build integration

// Package storage integration tests run only with `-tags=integration` and a
// reachable Postgres in TEST_DATABASE_URL, so the default `go test ./...` stays
// fast and dependency-free. See README for how to run them against the
// docker-compose database.
package storage

import (
	"context"
	"errors"
	"os"
	"testing"
)

func newPostgresForTest(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	store, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestPostgres_CreateDedupAliasClicks(t *testing.T) {
	ctx := context.Background()
	store := newPostgresForTest(t)

	// Unique URL per run so repeated test runs don't clash.
	url := "https://example.com/it/" + t.Name() + "/" + os.Getenv("HOSTNAME")

	seq, err := store.NextSequence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	link := &Link{Code: "it-" + itoa(seq), OriginalURL: url}
	created, err := store.CreateLink(ctx, link)
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}

	// Dedup: same URL, new code, returns the existing mapping.
	dup := &Link{Code: "it-other", OriginalURL: url}
	created, err = store.CreateLink(ctx, dup)
	if err != nil {
		t.Fatal(err)
	}
	if created || dup.Code != link.Code {
		t.Fatalf("expected dedup to existing code %q, got created=%v code=%q", link.Code, created, dup.Code)
	}

	// Clicks.
	if err := store.RecordClick(ctx, link.Code, Click{UserAgent: "it"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetByCode(ctx, link.Code)
	if err != nil || got.ClickCount != 1 {
		t.Fatalf("click_count=%d err=%v", got.ClickCount, err)
	}

	if _, err := store.GetByCode(ctx, "definitely-missing-code"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
