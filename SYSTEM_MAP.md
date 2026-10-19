# SYSTEM_MAP.md

Single source of truth for topology, domain boundaries, data flows, and async
topology across the Kentang Tech services. Update it in the same change that
alters any of those, per `AGENTS.md` section 1.9. If a change touches this
map, say so in the change description; "N/A, no structural change" is an
acceptable answer and a missing answer is not.

- **Service in this map:** `app-serve` (Go, the only service with code today).
- **Last reviewed:** 2026-10-18, against `HEAD` after the operator Google
  login. Covers `docs/DECISIONS/001` to `004`: scope v1, upload idempotency
  with the R2 client and stats series, stats retention and the refresh
  worker, and the public host `app-post.kentangtech.com`.
- **Specs this map summarises:** `docs/SPEC/001-BACKEND-ARCHITECTURE.md`,
  `002-TIKTOK-INTEGRATION.md`, `003-API-CONTRACT.md`.

---

## 1. Runtime topology

```
                    ┌──────────────────────────────┐
  browser ──TLS──►  │ nginx (app-post.kentangtech) │
                    └───────┬──────────────┬────────┘
                            │ /            │ /api/v1
                            ▼              ▼
                     app-ui:3000     app-serve:8080  ──► TikTok Open API
                     (not built)     (cmd/app-serve)      (outbound HTTP)
                                          │
                            ┌─────────────┴─────────────┐
                            ▼                           ▼
                    PostgreSQL 17                 Redis 7
                    (9 tables, migrations/)       (keys in section 6)
```

| Component | Built from | Notes |
|---|---|---|
| app-serve | `app-serve/Dockerfile` | One binary, one process, modular monolith |
| app-ui | none yet | `app-ui/` holds only `.env.example`; nginx upstream 502s until it lands |
| PostgreSQL | `postgres:17` | `deployment/docker-compose.yml` |
| Redis | `redis:7` | Sessions, rate limits, idempotency, dedupe, event stream |
| nginx | `deployment/nginx/app-post.conf` | TLS edge, `/api/v1` proxy, `/webhooks/tiktok` bypasses versioning |
| Cloudflare R2 | `internal/platform/r2` | SigV4 signed; `MEDIA_DRIVER=s3` proves credentials with a write probe at boot |

`/webhooks/tiktok` is mounted outside `/api/v1` on purpose: it is TikTok's
contract, and it must not move when our version changes.

## 2. Domain boundaries (package map)

Layer flow is one direction: `schema -> domain -> repository -> service ->
handler -> router`. A package may only import the layer to its left.

| Package | Layer | Owns |
|---|---|---|
| `internal/config` | config | Typed env, validated at boot, fail fast |
| `internal/apierror` | util | RFC 9457 problem shape and the error code catalog |
| `internal/platform/crypto` | util | AES-256-GCM keyring, HMAC helpers |
| `internal/platform/postgres` | repository | Pool, pool limits, migration runner |
| `internal/platform/redisx` | repository | Redis client and the key namespaces |
| `internal/platform/ratelimit` | repository | Fixed-window counter over Redis |
| `internal/platform/idempotency` | repository | Claim, replay, complete, release over Redis |
| `internal/platform/r2` | repository | AWS SigV4 signing and Cloudflare R2 object requests |
| `internal/platform/googleoidc` | repository | Google OIDC client: authorize URL, code exchange, JWKS verify |
| `internal/platform/httpapi` | router | Engine, request id, access log, recovery |
| `internal/platform/logging` | util | slog setup |
| `internal/tiktokclient` | repository | Outbound TikTok calls, per-account limiter, error map |
| `internal/auth` | service | Google sign in and TikTok connect, Redis sessions, OAuth state |
| `internal/operator` | repository | Users row upsert for the signed in operator |
| `internal/account` | service | Account aggregate, token vault, ownership checks |
| `internal/publish` | service | Init for video, photo, draft, chunk proxy, status reconcile |
| `internal/webhook` | worker | Signature verify, idempotent intake, dispatcher, worker pool, sweep |
| `internal/insight` | service | Video list and query, stats series, creator profile, background stats sweep and retention |
| `internal/media` | service | Upload validation, disk and S3 stores, chunk planner |
| `internal/compose` | router | Composition root: constructs services, mounts route tables |
| `cmd/app-serve` | config | Process entrypoint, signal handling, worker supervision |

Boundaries the compiler enforces: `internal/` cannot be imported from outside
the module, so no other service can reach into app-serve. Boundaries it does
not enforce: cross-domain reach-ins inside the module. Two rules that apply
today:

- `media` does not import `publish`. The shared key claim lives in
  `platform/idempotency`, and `media` declares its own `UploadGuard`.
- `insight` owns the privacy option cache; `publish` reads it through the
  `compose.creatorOptions` adapter.

## 3. HTTP surface

All console routes are session guarded. State-changing routes also require
`X-CSRF-Token`. Everything below is under `/api/v1` unless noted.

| Method | Path | Module | Notes |
|---|---|---|---|
| GET | `/healthz` | compose | Root, no version prefix |
| GET | `/auth/google/authorize` | auth | Browser redirect, no session |
| GET | `/auth/google/callback` | auth | Sets the session, a refused email redirects to `status=error` |
| GET | `/auth/tiktok/authorize` | auth | Browser redirect, session guarded |
| GET | `/auth/tiktok/callback` | auth | Browser redirect, session guarded |
| POST | `/auth/tiktok/refresh` | auth | Session plus CSRF |
| POST | `/auth/tiktok/revoke` | auth | Session plus CSRF |
| POST | `/auth/logout` | auth | Session plus CSRF |
| GET | `/auth/session` | auth | Session only |
| GET | `/accounts` | account | List owned accounts |
| GET | `/accounts/{account_id}` | account | Show one owned account |
| DELETE | `/accounts/{account_id}` | account | Clears token material, keeps the row |
| GET | `/accounts/{account_id}/profile` | insight | Creator profile and counts |
| POST | `/accounts/{account_id}/creator-info` | insight | Cache-first creator record |
| GET | `/videos` | insight | One page, `account_id` required, `max_count` max 20 |
| POST | `/videos/query` | insight | Up to 20 ids |
| GET | `/videos/{video_id}` | insight | Refreshes a stale cover URL |
| GET | `/videos/{video_id}/stats` | insight | Series, `range` 7d, default 7d |
| POST | `/videos/{video_id}/stats/refresh` | insight | Always appends a point |
| POST | `/media/uploads` | media | `Idempotency-Key` plus `Checksum-SHA256` |
| GET | `/media/{id}` | media | 302 to a signed URL when the store signs |
| DELETE | `/media/{id}` | media | 204, idempotent |
| POST | `/publish/videos` | publish | Direct post init |
| POST | `/publish/photos` | publish | Photo post init |
| POST | `/publish/drafts` | publish | Draft upload init |
| PUT | `/publish/{id}/chunks` | publish | Proxied chunk, upstream URL never leaves the server |
| POST | `/publish/{id}/reconcile` | publish | Manual status sweep |
| GET | `/publish` | publish | List attempts |
| GET | `/publish/{id}` | publish | Show one attempt |
| POST | `/webhooks/tiktok` | webhook | Root path, HMAC signature, no session, per-IP throttle |

## 4. Data model

Nine tables, all primary keys UUIDv7, all timestamps `timestamptz`. Migration
pairs live in `migrations/`.

| Table | Migration | Notes |
|---|---|---|
| `users` | 000001, 000009 | Operator accounts, upserted at Google sign in |
| `sessions` | 000001 | Declared for audit; sessions actually live in Redis |
| `tiktok_accounts` | 000001, 000004 | Token columns are ciphertext plus `key_id`; `needs_reauth` |
| `publish_attempts` | 000001, 000006, 000008 | `reconcile_failures`, nullable `media_id` |
| `publish_chunks` | 000002 | Transfer bookkeeping for `FILE_UPLOAD` |
| `webhook_events` | 000002 | Unique `dedupe_key`, the webhook idempotency guard |
| `outbox` | 000002 | Transactional outbox, `published_at IS NULL` means pending |
| `video_stats` | 000003, 000005 | Append-only 7 day series, nullable counts; pruned by the stats sweep |
| `creator_info_snapshots` | 000003, 000007 | Short TTL cache for the publish form |

Invariants worth not breaking:

- A count TikTok omits is stored as SQL NULL and omitted on the wire. A zero
  is a real zero, never a stand-in for "not available".
- Snapshot tables are append-only, except the `video_stats` retention prune.
  Reads take the newest row, or the window for `video_stats`; writes insert.
- `is_aigc` is set by the server for generated content, not by the client.
- Deleting an account clears the ciphertext and stamps `revoked_at`; it never
  deletes the row, so the audit trail survives.

Indexes as they exist in `migrations/`:

| Index | Table | Shape |
|---|---|---|
| `ux_users_email` | `users` | unique on `email` |
| `ix_sessions_user_id` | `sessions` | `user_id` |
| `ux_tiktok_accounts_open_id` | `tiktok_accounts` | unique on `open_id` |
| `ix_tiktok_accounts_user_id` | `tiktok_accounts` | `user_id` |
| `ux_publish_attempts_publish_id` | `publish_attempts` | unique on `publish_id` |
| `ix_publish_attempts_account_status` | `publish_attempts` | `(account_id, status)` |
| `ix_publish_attempts_media_id` | `publish_attempts` | `media_id` |
| `ux_webhook_events_dedupe_key` | `webhook_events` | unique on `dedupe_key` |
| `ix_webhook_events_unprocessed` | `webhook_events` | `received_at`, partial where `processed_at IS NULL` |
| `ix_outbox_unpublished` | `outbox` | `id`, partial where `published_at IS NULL` |
| `ix_video_stats_video_fetched` | `video_stats` | `(video_id, fetched_at DESC)` |
| `ix_creator_info_snapshots_account_fetched` | `creator_info_snapshots` | `(account_id, fetched_at DESC)` |

## 5. External integrations

| Target | Used by | Auth | Timeout |
|---|---|---|---|
| Google OIDC (authorize, token, JWKS) | auth | `GOOGLE_CLIENT_ID` plus `GOOGLE_CLIENT_SECRET`, id token verified against the JWKS | per call |
| TikTok OAuth (`/v2/oauth/`) | auth | client key plus secret | per call |
| TikTok creator info, init, status | publish | account access token, decrypted from the vault | per call |
| TikTok video list, query, user info | insight | account access token | per call |
| Cloudflare R2 (S3 API) | media | `S3_ACCESS_KEY` plus `S3_SECRET_KEY` SigV4 | per call |
| Local disk | media | none | n/a |

Every TikTok call runs under a context with an explicit timeout and goes
through `tiktokclient`, which applies the per-account rate limiter and maps
upstream errors to the catalog. A 4xx never retries; 429 and 5xx back off with
jitter and a bounded attempt count. The R2 client sets its own dial, TLS, and
transfer timeouts and does not go through `tiktokclient`.

## 6. Redis keys

| Prefix | Owner | Purpose | TTL |
|---|---|---|---|
| `genauto:session:` | auth | Operator session state, sliding expiry | session TTL |
| `genauto:oauth:state:` | auth | OAuth state, single use | short |
| `genauto:auth:refresh-lock:` | auth | Serializes token refresh per account | short |
| `genauto:ratelimit:` | tiktokclient | Outbound per-account, per-family budget | window |
| `genauto:ratelimit:webhook:` | webhook | Per-IP delivery budget | window |
| `genauto:ratelimit:auth:` | auth | Per-IP budget for the two Google sign in routes | window |
| `genauto:idempotency:` | platform/idempotency | `<key>:hash` and `<key>:resp` | 24h |
| `genauto:webhook:dedupe:` | webhook | Per-delivery duplicate filter | delivery window |
| `genauto:stream:events` | webhook outbox relay | Outbox rows appended for consumers | trimmed by length |

## 7. Async topology

Webhook intake is the primary publish-state signal. Nothing below is
request-blocking except intake.

```
TikTok ──POST /webhooks/tiktok──► throttle ──► HMAC verify ──► dedupe (Redis)
                                                                   │
                                                          in-process queue
                                                                   ▼
                                                         worker pool (size WORKER_SIZE)
                                                                   │
                                            one transaction: state update + outbox row
                                                                   │
                                                    ┌──────────────┴──────────────┐
                                                    ▼                             ▼
                                        reconcile sweep (stale open     outbox relay (ticker)
                                         attempts, backoff, cap)               │
                                                                               ▼
                                                                  XADD genauto:stream:events
```

| Loop | Entry | Termination | Retry |
|---|---|---|---|
| Worker pool | `Worker.Run`, in-process bounded queue | ctx cancel plus in-flight drain | bounded attempts, exponential backoff with jitter |
| Reconcile sweep | `Reconciler.Run`, bounded ticker | ctx cancel | bounded, then `RECONCILE_EXHAUSTED` under status `FAILED` |
| Outbox relay | `Reconciler.RunOutbox`, bounded ticker | ctx cancel | row stays pending when the sink fails |
| Stats sweep | `StatsWorker.Run`, bounded ticker | ctx cancel | per account and per video logged and skipped; the next tick retries |

The stats sweep is a second background path. It runs on
`STATS_REFRESH_INTERVAL` (default 3 hours), reads the connected account ids
through the `compose.accountIDs` adapter, which drops revoked accounts, then
for each account refreshes the token through `auth.Service.Refresh`, pages
the newest catalog through the shared limiter, and appends one `video_stats`
row per live video. After every account is walked it deletes rows older than
`STATS_RETENTION` (default 168 hours). A per-account failure is logged and
the sweep moves on, so one dead token cannot stall the others.

Intake answers `200` once the signature is valid, including duplicates and
unknown event types, because a non-`200` triggers up to 72 hours of TikTok
retries. A full worker queue answers `503` so TikTok retries instead of the
delivery being dropped. Every worker goroutine recovers from panic and is
supervised by `main.supervised`.

## 8. Key data flows

**Operator sign in.** authorize -> Google -> single-use `state` plus nonce ->
callback spends `state` -> code exchanged -> id token verified against
Google's JWKS -> allowlist check on the `email_verified` email against
`OPERATOR_EMAIL` -> `users` row upserted -> session id set in an `HttpOnly`,
`Secure`, `SameSite=Lax` cookie.

**TikTok account connect.** Requires the session -> authorize binds the
`state` to the browser session that issued it -> callback spends `state` and
rejects a state from another session -> code exchanged -> token encrypted with
the keyring -> account upserted -> no cookie written. Ownership is checked
before any account-scoped read or write; a foreign id answers `404`.

**Direct post, video or photo.** Client sends the init body with an optional
`Idempotency-Key`. Ownership and privacy options are resolved, the init is
sent upstream, the attempt row is stored, and the idempotency outcome is
recorded. `PULL_FROM_URL` is accepted only for a host under
`MEDIA_PUBLIC_BASE_URL`. The upstream upload URL never reaches the client.

**Chunked upload.** The init returns a plan. The client PUTs chunks to our
proxy, which validates index and range against the stored plan before
forwarding. A duplicate index resolves to the next expected index; the unique
`chunk_index` is the backstop.

**Media upload.** `POST /media/uploads` takes `Idempotency-Key` plus
`Checksum-SHA256` (hex SHA-256 of the file bytes, not the multipart framing).
The claim happens before the body is read. The service recomputes the digest
from the streamed bytes and refuses to store on a mismatch, so a wrong header
leaves no object. A failed upload releases its claim so a corrected retry is
accepted, not answered `409`. A request with a key but no digest is `422`.

**Stats series.** `GET /videos/{id}/stats` serves
`{video_id, range, points, truncated}` from stored rows. The stats sweep is
the steady writer, so a read fetches only on a first sighting: a video with no
point inside the window gets one fetch and append, and history is never
refetched. Points are capped at 500, read newest first, so a truncated window
loses its oldest points.

## 9. Known gaps

Structure that this map records as absent on purpose, so a reader does not
assume it exists.

1. **No event stream consumer.** The outbox relay appends to
   `genauto:stream:events`; nothing reads the stream yet. The publish state
   machine does not depend on a consumer.
2. **No `sessions` mirror.** The table exists for audit; the code writes
   sessions to Redis only.
3. **app-ui not started.** Blocked on owner direction in `DESIGN.md`
   (antislop R-37).
