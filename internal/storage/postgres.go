package storage

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// uniqueViolation is the Postgres SQLSTATE for a unique-constraint breach.
const uniqueViolation = "23505"

// PostgresStore is the production Store implementation backed by a pgx pool.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore connects to dsn and verifies connectivity.
func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

// Migrate applies the embedded SQL migrations in filename order. Each file is
// idempotent (IF NOT EXISTS), so running it repeatedly is safe.
func (s *PostgresStore) Migrate(ctx context.Context) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		// No args => pgx uses the simple protocol, which allows the multiple
		// statements in a migration file to run in one round trip.
		if _, err := s.pool.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return nil
}

func (s *PostgresStore) NextSequence(ctx context.Context) (uint64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT nextval('link_code_seq')`).Scan(&n); err != nil {
		return 0, err
	}
	return uint64(n), nil
}

func (s *PostgresStore) CreateLink(ctx context.Context, link *Link) (bool, error) {
	if link.IsCustom {
		err := s.pool.QueryRow(ctx,
			`INSERT INTO links (code, original_url, is_custom)
			 VALUES ($1, $2, TRUE)
			 RETURNING id, created_at`,
			link.Code, link.OriginalURL,
		).Scan(&link.ID, &link.CreatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return false, ErrAliasTaken
			}
			return false, err
		}
		return true, nil
	}

	// Auto-generated: idempotent on original_url via the partial unique index.
	err := s.pool.QueryRow(ctx,
		`INSERT INTO links (code, original_url, is_custom)
		 VALUES ($1, $2, FALSE)
		 ON CONFLICT (original_url) WHERE is_custom = FALSE DO NOTHING
		 RETURNING id, created_at`,
		link.Code, link.OriginalURL,
	).Scan(&link.ID, &link.CreatedAt)
	if err == nil {
		return true, nil
	}
	// A unique violation here is on the code index: the original_url conflict is
	// absorbed by ON CONFLICT DO NOTHING (which yields pgx.ErrNoRows, handled
	// below), so the only constraint left to break is links_code_key — the
	// generated code collided with a code already claimed as a custom alias.
	// Signal the service to retry with a fresh id.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return false, ErrCodeExists
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	// Conflict: a mapping already exists (e.g. concurrent creator). Return it.
	existing, err := s.GetByURL(ctx, link.OriginalURL)
	if err != nil {
		return false, err
	}
	*link = *existing
	return false, nil
}

const linkColumns = `id, code, original_url, is_custom, created_at`

func scanLink(row pgx.Row) (*Link, error) {
	var l Link
	err := row.Scan(&l.ID, &l.Code, &l.OriginalURL, &l.IsCustom, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func (s *PostgresStore) GetByURL(ctx context.Context, originalURL string) (*Link, error) {
	return scanLink(s.pool.QueryRow(ctx,
		`SELECT `+linkColumns+` FROM links
		 WHERE original_url = $1 AND is_custom = FALSE LIMIT 1`, originalURL))
}

func (s *PostgresStore) GetByCode(ctx context.Context, code string) (*Link, error) {
	return scanLink(s.pool.QueryRow(ctx,
		`SELECT `+linkColumns+` FROM links WHERE code = $1`, code))
}

func (s *PostgresStore) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}
