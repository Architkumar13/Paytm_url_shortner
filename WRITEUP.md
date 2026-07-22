# Write-up

> One-page reflection on how this was built. **Please review and edit so it reflects your own
> recollection and voice before submitting** — the decisions below are the ones actually made
> during the build, but the words should be yours.

## 1. What I asked the AI to do vs. what I decided myself

I used Claude Code as a pair-programmer and drove it top-down. **I owned the design decisions;
the AI did most of the typing.**

Decisions I made and handed to the AI as constraints:
- **Architecture boundaries** — a transport-agnostic service layer depending on a `Store`
  interface, with the HTTP layer kept thin. This keeps the core logic unit-testable without a
  database and makes the datastore swappable.
- **Datastore** — Postgres for real persistence, but behind an interface with an in-memory
  implementation so the whole test suite runs offline in ~1s.
- **Short-code strategy** — base62 of a monotonic sequence (see §3).
- **Duplicate-URL policy** — idempotent dedup for auto codes, always-new for custom aliases.
- **Analytics scope** — lightweight: a click counter, last-access time, per-visit rows, and one
  stats endpoint. Enough to honour the "& Link Analytics" in the title without ballooning scope.
- **Collision handling & read path** — collision-free codes are `base62(unique id)` rather than
  hash-plus-Bloom-filter, and the id source sits behind a swappable `IDGenerator` (centralized
  sequence by default; a Snowflake generator for distributed scale). A **Redis read-through cache**
  fronts the redirect path because reads dominate writes; the `code → URL` mapping is immutable, so
  the cache needs no invalidation.

The AI generated the base62 codec, the two store implementations, handlers, and the bulk of the
tests from those constraints. I reviewed every file, adjusted naming/structure, and verified the
behaviour with `go test -race` and a `docker compose up` smoke test.

## 2. Where I overrode / corrected the AI

- **Code generation.** The first instinct was random base62 + a collision check/retry loop. I
  threw that out: it only makes collisions *unlikely*, and I wanted a design I could prove. I
  switched to encoding a strictly increasing sequence, which is collision-free by construction.
- **Dedup had to be race-safe.** A naive "look up by URL, else insert" has a check-then-act race
  under concurrency. I pushed the guarantee into the schema — a *partial* unique index on
  `original_url WHERE is_custom = FALSE` plus `INSERT ... ON CONFLICT` — so two simultaneous
  requests for the same URL still converge on one code.
- **Analytics on the hot path.** I kept click recording **synchronous but best-effort** (a failed
  insert logs and still redirects) rather than accept a background-worker design that risked lost
  writes on shutdown — not worth the complexity at this scope.
- **Toolchain.** Pulling in `pgx/v5` bumped the required Go toolchain to 1.25. I accepted the bump
  (and pinned the Docker builder to `golang:1.25`) rather than downgrade to an older driver.

## 3. Biggest trade-offs & alternatives considered

1. **Sequential codes vs. unguessable codes.** base62-of-sequence gives a provable no-collision
   guarantee, but codes are enumerable. Alternatives: random + retry (unpredictable, but collision
   handling and a DB round-trip per create), or a keyed bijective/Feistel permutation of the
   counter (unguessable *and* collision-free, but more moving parts). I chose the simplest option
   that satisfies the stated requirement (no collisions) and documented the enumeration caveat.
2. **301 vs. 302 redirect.** The spec asks for 301. Browsers cache 301s hard, so repeat visits may
   never reach the server and analytics undercount. A 302 would count every hit but forfeits
   caching. I followed the spec and called out the caveat rather than silently "fixing" it.
3. **Postgres + interface vs. SQLite/in-memory.** The interface earns its keep by making tests
   fast and DB-free while still shipping real persistence for the Docker demo. The cost is a second
   implementation to keep in sync — acceptable, and it demonstrates the seam I'd extend later.

## 4. What's missing / what I'd do with another day

- **Auth & rate limiting** on `POST /shorten` (currently open).
- **Unguessable codes** via a keyed permutation, if enumeration matters.
- **Async, batched click ingestion** (channel + worker, or an outbox) to take writes off the
  redirect path, plus richer analytics (per-day rollups, referrer breakdown).
- **Real migration tooling** (golang-migrate) instead of embedded idempotent SQL, once there is
  more than one migration.
- **Link expiry / TTL** and per-user ownership.
- **More integration coverage** — the Postgres path has one guarded integration test; I'd expand it
  (ideally with testcontainers) and add load tests for the redirect path.
