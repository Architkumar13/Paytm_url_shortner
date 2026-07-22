# URL Shortener & Link Analytics

A small Go service that turns long URLs into short codes, 301-redirects visitors to the
original link, and tracks lightweight click analytics. Persisted in Postgres, runnable with
a single `docker compose up`.

- **Language:** Go 1.25 (standard-library `net/http` routing — no web framework)
- **Datastore:** Postgres 16, behind a `Store` interface with an in-memory implementation for tests
- **Run:** Docker Compose (app + Postgres) or `go run` (in-memory)

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

### `GET /api/links/{code}/stats`

```json
{
  "code": "4C92",
  "short_url": "http://localhost:8080/4C92",
  "original_url": "https://example.com/...",
  "is_custom": false,
  "click_count": 3,
  "created_at": "2026-07-22T12:00:00Z",
  "last_access_at": "2026-07-22T12:05:00Z",
  "recent_clicks": [ { "clicked_at": "...", "referer": "...", "user_agent": "...", "ip": "..." } ]
}
```

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

# analytics
curl -s localhost:8080/api/links/promo/stats
```

---

## Design notes

### Short-code generation — why it won't collide

Auto-generated codes are the **base62 encoding of a unique, strictly increasing sequence value**
(a Postgres `SEQUENCE`; an atomic counter in the in-memory store). base62 encoding is a bijection
over the integers, so distinct sequence values always produce distinct codes. Because the sequence
never repeats a value, **two generated codes can never collide — by construction, not by luck.** No
retry loop or existence check is needed. The base62 alphabet (`0-9A-Za-z`) is URL-safe, so codes
never need percent-encoding.

Custom aliases are used verbatim and their uniqueness is enforced by a database `UNIQUE` constraint;
a clash returns `409`.

Trade-off: sequential codes are enumerable/guessable. Since the requirement is *no collisions* (not
unguessability), the simple sequence approach is used; mitigations are listed in `WRITEUP.md`.

### Duplicate-URL handling (deliberate)

- **No alias:** identical URLs de-duplicate to a single code. Re-shortening returns the existing
  mapping with `200`.
- **Custom alias:** always creates a new mapping, so one URL can have several aliases; alias clashes
  return `409`.

This is enforced with a **partial unique index** on `original_url WHERE is_custom = FALSE`, which
also makes de-duplication race-safe under concurrent requests (via `INSERT ... ON CONFLICT`).

### Data model

`links` (`id`, `code` UNIQUE, `original_url`, `is_custom`, `click_count`, `created_at`,
`last_access_at`) and `clicks` (per-visit rows referencing `links`, `ON DELETE CASCADE`). See
`internal/storage/migrations/001_init.sql`.

### Layout

```
cmd/server        entrypoint: config -> store -> service -> http, graceful shutdown
internal/config   env configuration
internal/shortener  base62 codec + transport-agnostic business logic
internal/storage    Store interface + in-memory and Postgres implementations + migrations
internal/validate   URL and alias validation
internal/httpapi    HTTP handlers, routing, middleware
```

### Analytics & the 301 caveat

Redirects use `301` as specified. Browsers cache 301s aggressively, so repeat visits may not reach
the server and can undercount clicks. A `302` would count every visit at the cost of no caching.
This is discussed in `WRITEUP.md`.

## Configuration

| Env var | Default | Purpose |
|---------|---------|---------|
| `PORT` | `8080` | HTTP listen port |
| `DATABASE_URL` | _(empty)_ | Postgres DSN; empty ⇒ in-memory store |
| `BASE_URL` | `http://localhost:<PORT>` | Origin used to build `short_url` |
