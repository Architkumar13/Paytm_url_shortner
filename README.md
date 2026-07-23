# URL Shortener

A small Go service that turns long URLs into short codes and 301-redirects visitors to the
original link. Persisted in Postgres, runnable with a single `docker compose up`.

- **Language:** Go 1.25 (standard-library `net/http` routing — no web framework)
- **Datastore:** Postgres 16, behind a `Store` interface with an in-memory implementation for tests
- **Cache:** Redis read-through cache on the redirect (read) path; disabled automatically when unset
- **Run:** Docker Compose (app + Postgres + Redis) or `go run` (in-memory, no cache)

---

## Architecture

### High-level design

```mermaid
flowchart LR
    Client([Client / Browser])

    subgraph App["URL Shortener service (stateless, Go)"]
        direction TB
        HTTP["HTTP router + middleware"]
        SVC["shortener.Service"]
        IDG["IDGenerator<br/>sequence · snowflake"]
        HTTP --> SVC
        SVC --> IDG
    end

    Redis[("Redis<br/>read-through cache<br/>code → URL")]
    PG[("PostgreSQL<br/>links<br/>source of truth")]

    Client -->|POST /shorten| HTTP
    Client -->|GET /code| HTTP
    SVC -->|"read (redirect): cache-first"| Redis
    SVC -->|"write + cache miss"| PG
```

Postgres is the source of truth; Redis is a read-through cache on the hot redirect path. The service
holds no local state, so it scales horizontally behind a load balancer — set `ID_GENERATOR=snowflake`
so multiple instances mint ids without coordinating on a shared counter.

### Request flow — `POST /shorten`

```mermaid
sequenceDiagram
    actor C as Client
    participant A as App (Service)
    participant DB as PostgreSQL

    C->>A: POST /shorten {url, alias?}
    A->>A: validate + normalize URL
    alt custom alias provided
        A->>DB: INSERT code = alias (is_custom = true)
        DB-->>A: OK or unique violation
        A-->>C: 201 Created  /  409 Conflict
    else auto-generated code
        A->>DB: SELECT existing non-custom row by URL
        alt URL already shortened
            DB-->>A: existing row
            A-->>C: 200 OK (same code, dedup)
        else new URL
            A->>A: id = IDGenerator.NextID()
            A->>A: code = base62(id)
            A->>DB: INSERT ... ON CONFLICT DO NOTHING
            DB-->>A: created (or existing on race)
            A-->>C: 201 Created (200 on race)
        end
    end
```

### Request flow — `GET /{code}` (redirect)

```mermaid
sequenceDiagram
    actor C as Client
    participant A as App (Service)
    participant R as Redis
    participant DB as PostgreSQL

    C->>A: GET /{code}
    A->>R: GET url:{code}
    alt cache hit
        R-->>A: original URL
    else cache miss
        R-->>A: (nil)
        A->>DB: SELECT original_url WHERE code = ?
        alt code not found
            DB-->>A: no rows
            A-->>C: 404 Not Found
        else found
            DB-->>A: original URL
            A->>R: SET url:{code} (TTL = CACHE_TTL)
        end
    end
    A-->>C: 301 Moved Permanently (Location: original URL)
```

---

## Quick start (Docker — recommended)

```bash
git clone <repo-url>
cd Pautm_url_shortner
docker compose up --build
```

The service comes up on `http://localhost:8080`. Compose starts Postgres, waits until it is
healthy, then starts the app, which applies its migrations on boot.

Stop and wipe data:

```bash
docker compose down -v
```

## Run locally without Docker

With no `DATABASE_URL` set, the service uses the in-memory store (data is not persisted) — handy
for a quick look:

```bash
go run ./cmd/server         # or: make run
# -> listening on :8080
```

To run locally against Postgres, set `DATABASE_URL`:

```bash
DATABASE_URL="postgres://shortener:shortener@localhost:5432/shortener?sslmode=disable" go run ./cmd/server
```

## Tests

```bash
go test ./...          # unit tests — no Docker/Postgres needed
make test-race         # with the race detector
make cover             # with total coverage
```

Optional Postgres integration tests (require a running database) are guarded by a build tag and an
env var, so they are skipped by default:

```bash
# with the compose Postgres running (docker compose up db -d):
TEST_DATABASE_URL="postgres://shortener:shortener@localhost:5432/shortener?sslmode=disable" \
  go test -tags=integration ./internal/storage/
```

---

## API

### `POST /shorten`

Request body:

```json
{ "url": "https://example.com/some/long/path", "alias": "optional-custom-alias" }
```

| Status | When |
|--------|------|
| `201 Created` | A new mapping was created |
| `200 OK`      | The URL was already shortened (dedup) — existing code returned |
| `400 Bad Request` | Invalid URL, invalid alias, or malformed body |
| `409 Conflict`    | Requested custom alias is already taken |

Response:

```json
{
  "code": "4C92",
  "short_url": "http://localhost:8080/4C92",
  "original_url": "https://example.com/some/long/path",
  "created_at": "2026-07-22T12:00:00Z"
}
```

### `GET /{code}`

`301 Moved Permanently` to the original URL, or `404 Not Found` for an unknown code.

### `GET /healthz`

`200 OK` when the datastore is reachable (used as the container health check), else `503`.

### Try it

```bash
# create
curl -s -XPOST localhost:8080/shorten \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/a/b?x=1"}'

# same URL again -> 200 + same code (dedup)
curl -s -XPOST localhost:8080/shorten -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/a/b?x=1"}'

# custom alias
curl -s -XPOST localhost:8080/shorten -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","alias":"promo"}'

# redirect (show headers)
curl -sI localhost:8080/promo
```

---

## Design notes

### Short-code generation — why it won't collide

There are two common strategies; I weighed both:

1. **Hash + collision resolution** — hash the URL, take the first 7 base62 chars, and on a clash
   re-hash (append a fixed salt) until the code is unused, using a **Bloom filter** to keep the
   existence check cheap. Downside: collision handling logic and a probabilistic filter on the
   write path.
2. **Base62 of a unique ID** — a **unique ID generator** yields a globally unique integer and the
   code is `base62(id)`. **No collisions by construction:** every ID is unique and base62 is a
   bijection, so no two codes can ever clash — no existence check, no Bloom filter.

I use **option 2**. The unique-ID source sits behind an `IDGenerator` interface
(`internal/shortener/idgen.go`), so the strategy is swappable:

- `SequenceGenerator` **(default)** — a centralized monotonic counter (Postgres `SEQUENCE`, or an
  atomic counter in-memory). Ideal for a single instance.
- `Snowflake` — a coordination-free 64-bit generator (timestamp + machine id + per-ms sequence)
  for distributed deployments. Enable with `ID_GENERATOR=snowflake` for horizontal scale.

The base62 alphabet (`0-9A-Za-z`) is URL-safe, so codes never need percent-encoding. Custom aliases
are used verbatim, with uniqueness enforced by a database `UNIQUE` constraint (a clash returns
`409`).

Auto-generated codes are collision-free *within their own space*, but they share the `code`
namespace with custom aliases, so a generated code can land on one a user already claimed. That is
detected (the store returns `ErrCodeExists` on the `code` unique constraint) and the service mints a
fresh id and retries — bounded by `maxCodeAttempts` — so the clash never surfaces to the client.

Trade-off: sequential codes are enumerable/guessable. Since the requirement is *no collisions* (not
unguessability), the simple approach is used; mitigations are in `WRITEUP.md`.

### Caching (Redis) — the read path

Reads (redirects) vastly outnumber writes, so a **read-through cache** fronts the
redirect path. `GET /{code}` checks Redis for `code → original URL`; on a hit it redirects without
touching Postgres, and on a miss it loads from Postgres, populates the cache (TTL `CACHE_TTL`, default
24h) and redirects. The `code → URL` mapping is **immutable** once created, so cached entries never go
stale and **no invalidation is needed**. When `REDIS_URL` is unset the service uses a no-op cache and
reads straight from the store, so it still runs with zero cache infrastructure.

### Duplicate-URL handling (deliberate)

- **No alias:** identical URLs de-duplicate to a single code. Re-shortening returns the existing
  mapping with `200`.
- **Custom alias:** always creates a new mapping, so one URL can have several aliases; alias clashes
  return `409`.

This is enforced with a **partial unique index** on `original_url WHERE is_custom = FALSE`, which
also makes de-duplication race-safe under concurrent requests (via `INSERT ... ON CONFLICT`).

### Data model

`links` (`id`, `code` UNIQUE, `original_url`, `is_custom`, `created_at`). See
`internal/storage/migrations/001_init.sql`.

### Layout

```
cmd/server        entrypoint: config -> store/cache/idgen -> service -> http, graceful shutdown
internal/config   env configuration
internal/shortener  base62 codec, IDGenerator (sequence + snowflake), business logic
internal/storage    Store interface + in-memory and Postgres implementations + migrations
internal/cache      Cache interface + Redis, no-op and in-memory implementations
internal/validate   URL and alias validation
internal/httpapi    HTTP handlers, routing, middleware
```

### Redirects

`GET /{code}` returns `301 Moved Permanently` as specified. The `code → URL` mapping is permanent,
so `301` is the correct semantic and lets browsers cache the redirect.

## Configuration

| Env var | Default | Purpose |
|---------|---------|---------|
| `PORT` | `8080` | HTTP listen port |
| `DATABASE_URL` | _(empty)_ | Postgres DSN; empty ⇒ in-memory store |
| `BASE_URL` | `http://localhost:<PORT>` | Origin used to build `short_url` |
| `REDIS_URL` | _(empty)_ | `redis://` URL for the read cache; empty ⇒ caching disabled |
| `CACHE_TTL` | `24h` | TTL for cached `code → URL` entries |
| `ID_GENERATOR` | `sequence` | `sequence` or `snowflake` |
| `MACHINE_ID` | `0` | Snowflake machine id (0–1023) when `ID_GENERATOR=snowflake` |
