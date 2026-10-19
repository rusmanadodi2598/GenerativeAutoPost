# 001: Backend Architecture (app-serve)

- **Status:** Accepted
- **Date:** 2026-10-12
- **Owner:** Dodi Rusmana
- **Depends on:** `docs/DECISIONS/001-SCOPE-V1.md`
- **Governing rules:** `AGENTS.md`, `docs/RULLES/TDD.md`, `docs/RULLES/OWASP.md`

## 1. Purpose

`app-serve` is the only component that talks to TikTok. It owns the OAuth flow, the TikTok token vault, publish orchestration, webhook intake, and statistics reads, and it exposes one JSON API under `/api/v1` for `app-ui`.

`app-ui` never holds a TikTok token. It authenticates to `app-serve` with a server-side session cookie. This keeps the TikTok secret and refresh token on the server, which is the reason the cookie model was chosen over a Bearer token.

## 2. Architectural Style

Modular monolith, one binary, one PostgreSQL database, one Redis instance. Module boundaries are package boundaries, not network hops. The stack is Go 1.26, PostgreSQL 17, Redis 7, `go-playground/validator/v10`, Gin for the data plane routing, and `net/http` for upstream calls to TikTok.

Layer flow is strictly unidirectional, as required by `AGENTS.md` 1.5:

```
schema -> domain -> repository -> service -> handler -> router
```

| Layer | Owns | Must not |
|---|---|---|
| `schema` | Request and response DTOs, validation tags | Hold business rules |
| `domain` | Entities, value objects, aggregates, domain events, invariants | Import `net/http`, SQL, or Redis |
| `repository` | PostgreSQL and Redis access, row mapping | Import `service` or `handler` |
| `service` | Orchestration and business logic | Import `net/http`, run raw SQL, call Redis directly |
| `handler` | HTTP decode, validate, map errors, encode | Contain business logic or SQL |
| `router` | Route registration, middleware wiring | Contain logic |

## 3. Package Layout

```
app-serve/
  cmd/app-serve/main.go          composition root, wiring only
  internal/
    config/                      typed env config, fail fast at boot
    platform/
      httpapi/                   Gin engine, request id, access log, recovery
      postgres/                  pool, transaction helper, migration runner
      redisx/                    client, key namespacing
      logging/                   slog setup, request-scoped logger
      crypto/                    AES-256-GCM keyring, HMAC helpers
      idempotency/               claim, replay, complete, release over Redis
      ratelimit/                 fixed-window counter over Redis
      r2/                        SigV4 signing, Cloudflare R2 object requests
      googleoidc/                Google OIDC authorize, code exchange, id token verify
    apierror/                    RFC 9457 problem type, catalog, mapper
    tiktokclient/                outbound TikTok adapter (see 002)
    auth/                        Google sign in, TikTok connect, sessions, OAuth state
    operator/                    users row upsert for the signed in operator
    account/                     TikTokAccount aggregate and token vault
    publish/                     creator info, direct post, draft, status
    webhook/                     signature verify, event store, dispatcher
    insight/                     video list/query, user info, stats
    media/                       media store interface, chunk planner, URL signing
    compose/                     composition root: builds services, mounts route tables
  migrations/                    up/down pairs, numbered
  .env.example
```

Every hand-written Go file stays under 250 lines and carries the header block defined in `AGENTS.md` 1.2: `@file`, `@for`, `@uses`, `@reason`, `@author`, `@layer`, `@stability`, `@since`. Generated files are exempt from both rules.

## 4. Module Responsibilities

### 4.1 auth
Two flows share one package. Google OIDC is the only sign in: `GET /auth/google/authorize` mints a nonce, stores it as a single-use state in Redis, and redirects to Google; `GET /auth/google/callback` spends the state, exchanges the code, verifies the id token against Google's JWKS (RS256, `kid`, issuer, audience, expiry, nonce, `email_verified`), and mints an operator session only when the verified email matches `OPERATOR_EMAIL`. The first sign in upserts the `users` row. TikTok is a connect flow for an operator who already has a session: it builds the authorization redirect (`client_key`, comma-joined `scope`, `redirect_uri`, `state`, `response_type=code`, optional `disable_auto_auth`), binds `state` in Redis to the browser session that issued it as a single-use key with a short TTL, validates `state` on callback, exchanges `code` for tokens, and refreshes or revokes tokens. `state` and PKCE `code_verifier` are generated with `crypto/rand`. Both TikTok browser routes redirect a missing session to the result page instead of writing JSON, and the Google routes sit behind an IP throttle.

### 4.2 account
Owns the `TikTokAccount` aggregate: `open_id`, `union_id`, display name, avatar, granted scopes, token material, expiry, and revoke state. Token material is encrypted at rest by `platform/crypto`. The aggregate exposes behavior (`RotateTokens`, `Revoke`, `HasScope`) instead of exported mutable fields.

### 4.3 publish
Orchestrates creator info lookup, direct post for video and photo, and draft upload. Decides `FILE_UPLOAD` versus `PULL_FROM_URL`, builds the chunk plan, and drives chunk transfer. Records a `PublishAttempt` for every call and returns a `publish_id` to the caller.

### 4.4 webhook
Verifies `TikTok-Signature`, acknowledges with `200`, then processes asynchronously and idempotently. This module is the primary source of publish state. Details are in `002-TIKTOK-INTEGRATION.md`.

### 4.5 insight
Reads video lists, video details, and user info, and stores periodic stats snapshots. All reads are bounded and paginated. A background worker refreshes one point per live video on `STATS_REFRESH_INTERVAL` and prunes points older than `STATS_RETENTION`.

### 4.6 media
Defines a `Store` interface with two implementations: local disk and S3-compatible (Cloudflare R2). Local disk is the default for development, S3 is enabled by configuration. The module also computes a chunk plan that satisfies TikTok constraints and signs public URLs for `PULL_FROM_URL` from the verified domain.

## 5. Data Model

All primary keys are UUIDv7 unless noted. All timestamps are `timestamptz`.

| Table | Key columns | Notes |
|---|---|---|
| `users` | `id`, `email`, `created_at` | Operator row; the Google login upserts it on first sign in, `OPERATOR_ID` comes from env |
| `sessions` | `id`, `user_id`, `expires_at`, `csrf_token` | Declared for audit; sessions actually live in Redis |
| `tiktok_accounts` | `id`, `user_id`, `open_id` UNIQUE, `union_id`, `display_name`, `avatar_url`, `scopes TEXT[]`, `access_token_enc`, `access_token_expires_at`, `refresh_token_enc`, `refresh_expires_at`, `revoked_at`, `needs_reauth` | Token columns are ciphertext plus `key_id` |
| `publish_attempts` | `id`, `account_id`, `publish_id` UNIQUE, `post_mode`, `media_type`, `source`, `status`, `privacy_level`, `title`, `description`, `fail_reason`, `post_id`, `uploaded_bytes`, `media_id`, `reconcile_failures`, `created_at`, `completed_at` | One row per initialized publish; `media_id` is nullable |
| `publish_chunks` | `id`, `publish_attempt_id`, `chunk_index`, `byte_start`, `byte_end`, `content_range`, `uploaded_at` | Transfer bookkeeping for `FILE_UPLOAD` |
| `webhook_events` | `id`, `dedupe_key` UNIQUE, `event_type`, `publish_id`, `post_id`, `payload JSONB`, `received_at`, `processed_at` | Idempotency guard |
| `video_stats` | `id`, `video_id`, `view_count`, `like_count`, `comment_count`, `share_count`, `fetched_at` | Append-only series with nullable counts; pruned to a 7 day window |
| `creator_info_snapshots` | `id`, `account_id`, `creator_username`, `creator_nickname`, `creator_avatar_url`, `privacy_level_options TEXT[]`, `comment_disabled`, `duet_disabled`, `stitch_disabled`, `max_video_post_duration_sec`, `fetched_at` | Cached, short TTL |
| `outbox` | `id`, `aggregate_type`, `aggregate_id`, `event_type`, `payload JSONB`, `published_at` | Transactional outbox to Redis Streams |

Indexes as they exist in `migrations/`: `users(email)` unique, `sessions(user_id)`, `tiktok_accounts(open_id)` unique, `tiktok_accounts(user_id)`, `publish_attempts(publish_id)` unique, `publish_attempts(account_id, status)`, `publish_attempts(media_id)`, `webhook_events(dedupe_key)` unique, `webhook_events(received_at)` partial where `processed_at IS NULL`, `outbox(id)` partial where `published_at IS NULL`, `video_stats(video_id, fetched_at DESC)`, `creator_info_snapshots(account_id, fetched_at DESC)`.

Destructive migrations require explicit sign-off and a working `down` before merge.

## 6. Redis Usage

| Key pattern | Purpose | TTL |
|---|---|---|
| `genauto:session:<id>` | Server-side operator session, sliding expiry | Session lifetime |
| `genauto:oauth:state:<state>` | Single-use CSRF state | 10 minutes |
| `genauto:auth:refresh-lock:<account_id>` | Serializes token refresh per account | Short |
| `genauto:ratelimit:<family>:<account_id>:<minute>` | Outbound per-account TikTok budget | Fixed window |
| `genauto:ratelimit:webhook:<ip>` | Inbound webhook throttle | Fixed window |
| `genauto:ratelimit:auth:<ip>` | Per-IP budget for the two Google sign in routes | Fixed window |
| `genauto:idempotency:<key>:hash` / `:resp` | Request claim and stored replay | 24 hours |
| `genauto:webhook:dedupe:<dedupe_key>` | Replay guard before the DB row | Delivery window |
| `genauto:stream:events` | Outbox relay target | Trimmed by length |

Redis never holds the only copy of anything important. A Redis flush degrades rate limiting and replay defense, but the durable `webhook_events.dedupe_key` unique index still prevents double processing.

## 7. Concurrency, Timeouts, and Errors

- Every outbound HTTP call takes an explicit `context` with a timeout. No `context.Background()` reaches an HTTP client.
- Every goroutine recovers from panic and logs the stack, per `AGENTS.md` 1.6.
- Goroutines have a termination condition, driven by context cancellation and a wait group.
- Outbound calls retry only on network errors, `429`, and `5xx`, with exponential backoff plus jitter, bounded attempts, and a documented policy per call site. `4xx` domain errors never retry.
- Per-account rate limits are enforced in one place: 6 requests per minute for `video/init` and `inbox/video/init`, 20 for `creator_info/query`, 30 for `status/fetch`, and 30 for `video/list`. The limiter is a fixed-window counter in Redis, not an in-process counter, so it holds across replicas.

## 8. Error Model

One error type crosses the wire, defined in `apierror`, serialized as RFC 9457 `application/problem+json`:

```json
{
  "type": "https://app-post.kentangtech.com/problems/publish-privacy-mismatch",
  "title": "Privacy level rejected",
  "status": 422,
  "detail": "The selected privacy level is not available for this account.",
  "instance": "/api/v1/publish/videos",
  "code": "PUBLISH_PRIVACY_LEVEL_MISMATCH",
  "log_id": "202210112248442CB9319E1FB30C1073F3"
}
```

`code` is machine-parseable and stable. `detail` is human-readable English. `log_id` is present only when TikTok returned one, and is empty otherwise.

The production response carries no internal detail. In `APP_ENV=development` the body adds a `debug` object with validator field errors and the wrapped error chain. Secrets, tokens, and internal URLs never enter any response at any environment.

TikTok error codes map to our `code` catalog in one table inside `apierror`, so a new TikTok code is a one-line addition, not a new branch per handler.

## 9. Security Baseline

Mapped to `docs/RULLES/OWASP.md` (OWASP Top 10:2025).

| Category | Control |
|---|---|
| A01 Broken Access Control | Every account-scoped read and write re-checks ownership in one shared policy function. Deny by default. Opaque UUID identifiers, never sequential. |
| A02 Security Misconfiguration | Typed config validated at boot, fail fast. Secure cookie flags, explicit CORS allowlist, no debug endpoint in production. |
| A03 Supply Chain | Pinned module versions, `go.sum` verified, dependency audit gate in CI. |
| A04 Cryptographic Failures | AES-256-GCM at rest with a keyring and `key_id` for rotation. TLS for all outbound. Secrets never logged. |
| A05 Injection | Parameterized SQL only, `go-playground/validator/v10` on every external input, no string-built queries. |
| A06 Insecure Design | Threat model recorded per module; publish and webhook paths have explicit abuse cases. |
| A07 Authentication Failures | Session cookie with `HttpOnly`, `Secure`, `SameSite=Lax`, CSRF token per session, refresh-token rotation, replay guard on OAuth state. |
| A08 Integrity Failures | Webhook HMAC-SHA256 verification before parsing, timestamp tolerance, idempotent event storage. |
| A09 Logging Failures | Structured `slog` with request ID, account ID, and error code on every error path. No bare prints. |
| A10 Exceptional Conditions | Panic recovery at every goroutine boundary, explicit timeouts, bounded retries, no silent error swallowing. |

## 10. Testing Strategy

Per `docs/RULLES/TDD.md`, tests are written before or alongside implementation, table-driven with at least three to five variations per case, including a typical, a boundary, an empty, and an extreme value. No `t.Skip()`. `go test -race ./...` is a required gate for any package touching concurrency.

| Layer | Approach |
|---|---|
| `domain` | Pure unit tests, no I/O |
| `service` | Unit tests against fake repositories and a fake TikTok client |
| `repository` | Integration tests against a real PostgreSQL 17 and Redis 7, via Testcontainers |
| `handler` | `net/http/httptest`, one happy path, one validation failure, one auth failure per route |
| `tiktokclient` | `httptest` fake TikTok server replaying real endpoints, error codes, and rate limits |
| webhook | Duplicate delivery, invalid signature, stale timestamp, reordered events |

The fake TikTok server is what makes strict TDD possible without a live TikTok account, and it is the single place where TikTok response shapes are defined for tests.

## 11. Configuration

Typed config struct validated at boot. No `os.Getenv` outside `internal/config`.

| Variable | Purpose |
|---|---|
| `APP_ENV` | `development` or `production`, controls debug detail |
| `APP_PORT` | Listen port |
| `DATABASE_URL` | PostgreSQL 17 DSN |
| `DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME` | Pool limits, set explicitly per `AGENTS.md` 1.7 |
| `REDIS_URL` | Redis 7 DSN |
| `TIKTOK_CLIENT_KEY`, `TIKTOK_CLIENT_SECRET` | App credentials, both required at boot; the secret is also the webhook HMAC key |
| `TIKTOK_REDIRECT_URI` | Must match the registered URI exactly |
| `TIKTOK_SCOPES` | Comma-joined scope list |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | Google OAuth client for operator sign in, both required at boot |
| `GOOGLE_REDIRECT_URI` | Must match the registered Google redirect URI exactly |
| `OPERATOR_EMAIL` | The one Google account allowed to sign in |
| `APP_UI_BASE_URL` | Console origin for browser redirects, empty means same-origin |
| `TRUSTED_PROXIES` | Comma-joined proxy IPs/CIDRs allowed to set `X-Forwarded-For`, empty trusts no proxy |
| `TOKEN_ENC_KEY`, `TOKEN_ENC_KEY_ID` | AES-256-GCM master key (32 bytes) and rotation id |
| `OPERATOR_ID` | Operator UUID the login and TikTok connect attach to |
| `MEDIA_DRIVER` | `disk` or `s3` |
| `MEDIA_ROOT` | Local object root, required when `MEDIA_DRIVER=disk` |
| `MEDIA_PUBLIC_BASE_URL` | Verified domain used for `PULL_FROM_URL` |
| `MEDIA_MAX_BYTES`, `MEDIA_MAX_DURATION_MS`, `MEDIA_ALLOWED_TYPES` | Upload caps and the allowed content-type list |
| `CHUNK_MAX_BYTES` | Bound on one chunk body, default `64 MiB` |
| `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_REGION` | Cloudflare R2 settings; region defaults `auto` |
| `WORKER_SIZE` | Webhook worker pool size, default `4` |
| `WEBHOOK_THROTTLE_WINDOW`, `WEBHOOK_THROTTLE_MAX` | Per-IP webhook budget, defaults `2m` and `120` |
| `SESSION_COOKIE_NAME`, `SESSION_TTL` | Session settings |
| `STATS_REFRESH_INTERVAL` | Stats worker sweep cadence, default `3h` |
| `STATS_RETENTION` | Stats point age cutoff, default `168h` |
| `RUN_MIGRATIONS`, `MIGRATIONS_DIR` | Apply migrations then exit; dir required when enabled |
| `LOG_LEVEL` | slog level |

`.env` lives at `app-serve/.env`. `app-serve/.env.example` is committed with the same keys and no values.

## 12. Deployment Shape

`deployment/` holds Docker Compose (app-serve, app-ui, PostgreSQL 17, Redis 7) and an Nginx reverse proxy that terminates TLS for `app-post.kentangtech.com`, serves app-ui, proxies `/api/v1` to app-serve, and routes `/webhooks/tiktok` to the webhook handler.

## 13. Open Items

Every v1 open item is closed by the implementation:

1. Draft upload shipped in v1.
2. Scope requests are per feature: `GET /auth/tiktok/authorize` takes an optional `scope` subset and falls back to `TIKTOK_SCOPES`.
3. The operator is configured with `OPERATOR_ID`, and the `users` row is created at sign in by the Google callback; there is no separate seed or bootstrap command.