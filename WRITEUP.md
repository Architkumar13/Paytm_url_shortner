# Write-up

## 1. What I asked the AI to do vs. what I decided myself

I used Claude Code as a pair-programmer and drove it top-down: I owned the design
decisions and the AI did most of the typing, which I reviewed file by file.

Decisions I made and handed down as constraints:

- **Architecture** — a transport-agnostic service layer depending on a `Store` interface, with a
  thin HTTP layer. The core logic stays unit-testable without a database and the datastore is
  swappable.
- **Datastore** — Postgres for real persistence, behind an interface with an in-memory
  implementation so the whole suite runs offline in ~1s.
- **Short-code strategy** — `base62(unique id)` rather than hash-plus-collision-handling (see §3).
- **Duplicate policy** — idempotent dedup for auto-generated codes, always-new for custom aliases.
- **Read path** — a Redis read-through cache in front of redirects, because reads dominate writes;
  the `code → URL` mapping is immutable, so the cache needs no invalidation.
- **ID source behind an interface** — a centralized sequence by default, and a coordination-free
  Snowflake generator for horizontal scale.

The AI generated the base62 codec, both store implementations, the handlers, and the bulk of the
tests from those constraints. I verified the behaviour with `go test -race` and a
`docker compose up` smoke test.

## 2. Where I overrode / corrected the AI

- **Code generation.** The first cut was random base62 + a collision-check/retry loop. I threw it
  out: it only makes collisions *unlikely*, and I wanted a guarantee I could prove. Encoding a
  strictly increasing sequence is collision-free by construction.
- **Dedup had to be race-safe.** A naive "look up by URL, else insert" has a check-then-act race
  under concurrency. I pushed the guarantee into the schema — a *partial* unique index on
  `original_url WHERE is_custom = FALSE` plus `INSERT ... ON CONFLICT` — so two simultaneous
  requests for the same URL still converge on one code.
- **I built click analytics, then removed it.** My first service cut included a `stats` endpoint and
  a per-redirect click counter (it's in the commit history). I pulled it before finishing: a
  synchronous counter increment on the hot redirect path was the wrong shape, and analytics isn't in
  the stated requirements. I'd rather ship a clean core than a half-built feature that taxes every
  redirect — §4 covers how I'd actually build it.
- **Toolchain.** Pulling in `pgx/v5` bumped the required Go toolchain to 1.25. I accepted the bump
  (and pinned the Docker builder to `golang:1.25`) rather than downgrade to an older driver.

## 3. Biggest trade-offs & alternatives considered

1. **Sequential codes vs. unguessable codes.** `base62(sequence)` gives a provable no-collision
   guarantee, but the codes are enumerable. Alternatives: random + retry (unpredictable, but needs
   collision handling and a DB round-trip per create), or a keyed bijective/Feistel permutation of
   the counter (unguessable *and* collision-free, but more moving parts). I chose the simplest
   option that satisfies the stated requirement — no collisions — and documented the enumeration
   caveat.
2. **301 vs. 302 on redirect.** The brief specifies 301, so that's what I return, and it lets
   browsers and proxies cache the redirect. The trade-off I'm consciously accepting: a cached 301
   means repeat visits never reach the service, which would undercount click analytics and blunt any
   future link expiry/revocation. The moment either of those matters I'd switch to 302 and give up
   the edge caching.
3. **Postgres + interface vs. SQLite/in-memory only.** The interface earns its keep by keeping tests
   fast and DB-free while still shipping real persistence for the Docker demo. The cost is a second
   implementation to keep in sync — acceptable, and it demonstrates the seam I'd extend later.

## 4. What's missing / what I'd do with another day

- **Click analytics, done properly.** The feature I removed, rebuilt off the hot path: async,
  batched ingestion (channel + worker, or an outbox) so redirect latency is unaffected, plus a
  stats endpoint. This is also what would push me from 301 to 302 (see §3).
- **Abuse & safety on the open endpoints.** `POST /shorten` has no auth or rate limiting, and URL
  validation only checks scheme/host — so the service will currently redirect to internal addresses
  (`http://169.254.169.254/…`, `localhost`). I'd add SSRF protection (reject private/link-local
  ranges), auth, and rate limiting.
- **Read-path hardening.** Unknown codes bypass the cache and hit Postgres every time, so enumerating
  codes is a cheap way to load the database. I'd add negative caching for misses and a per-IP rate
  limit on the redirect path.
- **Unguessable codes** via a keyed permutation, if enumeration turns out to matter.
- **More persistence coverage.** The Postgres path has one guarded integration test; I'd move it to
  testcontainers so the dedup and code-collision branches run in CI, and add load tests for the
  redirect path.
- **Operational polish** — real migration tooling (golang-migrate) once there is more than one
  migration, link expiry/TTL, and per-user ownership.
