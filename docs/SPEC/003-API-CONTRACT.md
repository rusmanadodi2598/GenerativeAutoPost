# 003: API Contract v1

- **Status:** Accepted
- **Date:** 2026-10-12
- **Depends on:** `001-BACKEND-ARCHITECTURE.md`, `002-TIKTOK-INTEGRATION.md`
- **Base URL:** `https://app-post.kentangtech.com/api/v1`

## 1. Conventions

| Topic | Rule |
|---|---|
| Versioning | Path version `/api/v1`. A breaking change is `/api/v2`, never an in-place edit (see `AGENTS.md` 2.4) |
| Content type | Requests and responses are `application/json; charset=utf-8`, except errors and chunk upload |
| Success envelope | `{"data": <payload>, "meta": {...}}` |
| Error type | RFC 9457 `application/problem+json` |
| Timestamps | RFC 3339 with UTC offset, for example `2026-10-12T09:30:00Z` |
| Identifiers | UUIDv7 as strings. Never sequential integers in a URL |
| Field names | `snake_case` in JSON |
| Auth | Server-side session cookie, set by app-serve. app-ui sends `credentials: include` |
| Idempotency | `Idempotency-Key` header accepted on every `POST` that creates remote state |

## 2. Success Envelope

```json
{
  "data": {
    "id": "0191e2c4-8a5b-7c3d-9e1f-0a1b2c3d4e5f",
    "status": "PUBLISH_COMPLETE"
  },
  "meta": {
    "request_id": "req_01J9Z8V2K4",
    "server_time": "2026-10-12T09:30:00Z"
  }
}
```

Paginated lists carry their cursor in `data`, not in `meta`. The video list returns a page object:

```json
{
  "data": {
    "videos": [ { "id": "..." } ],
    "cursor": "1735689600000",
    "has_more": true
  },
  "meta": {
    "request_id": "req_01J9Z8V2K4",
    "server_time": "2026-10-12T09:30:00Z"
  }
}
```

`cursor` is opaque to the client. For the video list it is the TikTok cursor, a UTC millisecond timestamp; `has_more` says whether the next page exists. The publish list returns `data` as a plain array and takes a `cursor` query parameter; the account list returns a plain array.

## 3. Error Response (RFC 9457)

```json
{
  "type": "https://app-post.kentangtech.com/problems/publish-privacy-mismatch",
  "title": "Privacy level rejected",
  "status": 422,
  "detail": "The selected privacy level is not available for this account.",
  "instance": "/api/v1/publish/videos",
  "code": "PUBLISH_PRIVACY_LEVEL_MISMATCH",
  "log_id": "202210112248442CB9319E1FB30C1073F3",
  "errors": []
}
```

| Member | Always present | Notes |
|---|---|---|
| `type` | yes | URI under our own domain. Stable per `code` |
| `title` | yes | Short, English, no trailing period |
| `status` | yes | Duplicates the HTTP status |
| `detail` | yes | English, safe to show. Never contains internal paths, SQL, or token values |
| `instance` | yes | Request path |
| `code` | yes | `SCREAMING_SNAKE_CASE`, stable. The contract for clients |
| `log_id` | no | TikTok `log_id` when TikTok returned one, else omitted |
| `errors` | no | Field-level validation errors, see section 5 |

`code` is the client contract. `detail` may be reworded between releases without a version bump; `code` may not.

## 4. Development vs Client Responses

`APP_ENV` controls how much of the failure is exposed. The difference is additive and never changes the shape of `data`.

| Aspect | `development` | `production` |
|---|---|---|
| `detail` | Full wrapped error, including the Go error chain | Sanitized, user-safe sentence |
| `errors` | Field name, tag, and parameter for each validator failure | Omitted, except for validation errors where the field name is the actionable part |
| `debug.stack` | Included for `500` | Never present |
| `debug.upstream` | TikTok HTTP status, error code, and `log_id` | Never present. `log_id` stays at the top level |
| Log sink | Human-readable `slog` text | JSON `slog` |

Example development body for a validation failure:

```json
{
  "type": "https://app-post.kentangtech.com/problems/validation-failed",
  "title": "Validation failed",
  "status": 422,
  "detail": "title: max 2200 UTF-16 runes; privacy_level: required",
  "instance": "/api/v1/publish/videos",
  "code": "VALIDATION_FAILED",
  "errors": [
    {"field": "title", "tag": "max", "param": "2200"},
    {"field": "privacy_level", "tag": "required"}
  ],
  "debug": {
    "upstream": {"http_status": 400, "code": "invalid_param", "log_id": "202210112248442CB9319E1FB30C1073F3"}
  }
}
```

Secrets, tokens, cookies, `client_secret`, and internal URLs are stripped in both environments by a redaction pass before the body is written. This is a property of the error writer, not a per-handler decision.

## 5. Validation Errors

All external input decodes into a `schema` struct and is validated with `go-playground/validator/v10` before the service is called. A validation failure is always `422` with `code: "VALIDATION_FAILED"` and a populated `errors` array. The `field` value is the JSON name, not the Go field name.

## 6. Status Codes

| Code | Meaning in this API |
|---|---|
| `200` | Read succeeded, or a synchronous action completed |
| `202` | Async work accepted, the body carries the `publish_id` to track |
| `204` | Action completed with no body (revoke, disconnect) |
| `400` | Malformed request: bad JSON, missing required header |
| `401` | No session, or an expired session |
| `403` | Authenticated but not allowed. Wrong owner, or the account lacks a scope |
| `404` | Resource does not exist or is not owned by the caller. Ownership failures return `404`, not `403`, so IDs cannot be probed |
| `409` | Conflict: idempotency key reused with a different body, or a publish is already in flight for the same key |
| `422` | Validation failed |
| `429` | Rate limited, locally or upstream. Carries `Retry-After` |
| `502` | TikTok returned an unusable response |
| `503` | TikTok unavailable after bounded retries |
| `500` | Our bug |

## 7. Endpoints

### 7.1 Authentication and accounts

| Method | Path | Body / params | Success | Notes |
|---|---|---|---|---|
| `GET` | `/auth/google/authorize` | none | `302` to Google | Mints the OIDC state. No session |
| `GET` | `/auth/google/callback` | `code`, `state` (from Google) | `302` to the console | Sets the session cookie. A refused email redirects to `status=error` |
| `GET` | `/auth/tiktok/authorize` | `scope` (optional subset) | `302` to TikTok | Session required. Sets the `state` in Redis. Browser-facing |
| `GET` | `/auth/tiktok/callback` | `code`, `state` (from TikTok) | `302` to app-ui | Session required. Validates `state`, exchanges the code, upserts the account |
| `POST` | `/auth/tiktok/refresh` | `{"account_id": "..."}` | `200` account | Manual refresh. Scheduled refresh is automatic |
| `POST` | `/auth/tiktok/revoke` | `{"account_id": "..."}` | `204` | Revokes at TikTok, clears local tokens |
| `POST` | `/auth/logout` | none | `204` | Clears the session cookie and the Redis session |
| `GET` | `/auth/session` | none | `200` operator | Current operator, no token material |
| `GET` | `/accounts` | none | `200` list | Connected TikTok accounts, token fields omitted |
| `GET` | `/accounts/{id}` | none | `200` account | `id` is our UUID, ownership checked |
| `DELETE` | `/accounts/{id}` | none | `204` | Same as revoke plus local deletion |

### 7.2 Creator info

| Method | Path | Body | Success |
|---|---|---|---|
| `POST` | `/accounts/{id}/creator-info` | none | `200` snapshot |

Response `data`: `creator_username`, `creator_nickname`, `creator_avatar_url`, `privacy_level_options`, `comment_disabled`, `duet_disabled`, `stitch_disabled`, `max_video_post_duration_sec`, `fetched_at`. Served from a short cache; `fetched_at` tells app-ui how fresh the options are.

### 7.3 Media

| Method | Path | Body | Success |
|---|---|---|---|
| `POST` | `/media/uploads` | `multipart/form-data` file | `201` media object |
| `GET` | `/media/{id}` | none | `200` metadata, or `302` to a signed URL |
| `DELETE` | `/media/{id}` | none | `204` |

The media object carries `id`, `kind` (`video` or `photo`), `content_type`, `byte_size`, `checksum_sha256`, `duration_ms`, `width`, `height`, `storage` (`disk` or `s3`), and `public_url` when the store exposes one.

Validation runs before storage: allowed content types only, byte-size cap, and for video a duration and frame-rate check against the creator's `max_video_post_duration_sec`. A file that cannot satisfy the chunk rules is rejected here, not after a TikTok init call.

### 7.4 Publishing

| Method | Path | Body | Success |
|---|---|---|---|
| `POST` | `/publish/videos` | see below | `202` attempt |
| `POST` | `/publish/photos` | see below | `202` attempt |
| `POST` | `/publish/drafts` | video draft (inbox) | `202` attempt |
| `PUT` | `/publish/{id}/chunks` | raw chunk bytes | `200` progress |
| `GET` | `/publish/{id}` | none | `200` attempt |
| `GET` | `/publish` | `status`, `account_id`, `cursor`, `limit` (default 20, max 100) | `200` array |
| `POST` | `/publish/{id}/reconcile` | none | `202` | Forces a status fetch. The polling fallback, on demand |

Direct post video body:

```json
{
  "account_id": "0191e2c4-...",
  "media_id": "0191e2c4-...",
  "media_type": "video",
  "post_mode": "DIRECT_POST",
  "source": "FILE_UPLOAD",
  "post_info": {
    "title": "caption text",
    "privacy_level": "SELF_ONLY",
    "disable_duet": false,
    "disable_comment": true,
    "disable_stitch": false,
    "video_cover_timestamp_ms": 1000,
    "brand_content_toggle": false,
    "brand_organic_toggle": false,
    "is_aigc": true
  }
}
```

Server-side rules before the TikTok call:

1. `privacy_level` must be present and must be one of the account's current `privacy_level_options`. No default is applied.
2. `title` is measured in UTF-16 runes, max 2200 for video and 90 for a photo title.
3. Commercial toggles are validated together: a branded content flag requires the public privacy level, per the TikTok rules in 002.
4. `source: "PULL_FROM_URL"` requires the media to be on the verified base URL. Otherwise the request is rejected with a message that names the constraint.
5. `is_aigc` is set by the server from the content origin when the media is generated, and cannot be turned off by the client.

For `FILE_UPLOAD`, the `202` body returns the `upload_url` window and the chunk plan so app-ui can stream chunks:

```json
{
  "data": {
    "id": "0191e2c4-...",
    "publish_id": "v_pub_file~v2-1.123456789",
    "status": "PROCESSING_UPLOAD",
    "upload": {
      "expires_at": "2026-10-12T10:30:00Z",
      "chunk_size": 10000000,
      "total_chunk_count": 5,
      "next_chunk_index": 0
    }
  },
  "meta": { "request_id": "req_01J9Z8V2K4", "server_time": "2026-10-12T09:30:00Z" }
}
```

The upstream `upload_url` is not exposed to app-ui. Chunks are proxied through `PUT /publish/{id}/chunks`, so the TikTok upload token stays server-side and the chunk order and `Content-Range` are computed in one place.

`PUT /publish/{id}/chunks` headers: `Content-Type` matching the media, `Content-Length` of the chunk, and an optional `X-Chunk-Index`. The server validates index order and range against its stored plan. Response `data`: `uploaded_bytes`, `total_bytes`, `next_chunk_index`, `complete`.

### 7.5 Statistics and reads

| Method | Path | Params / body | Success |
|---|---|---|---|
| `GET` | `/videos` | `account_id` (required), `cursor`, `max_count` (max 20) | `200` page `{videos, cursor, has_more}` |
| `POST` | `/videos/query` | `{"account_id": "...", "video_ids": ["..."]}` (max 20) | `200` list |
| `GET` | `/videos/{video_id}` | `account_id` | `200` video |
| `GET` | `/videos/{video_id}/stats` | `account_id`, `range` (`7d`, default `7d`) | `200` series `{ video_id, range, points, truncated }`, max 500 points, ascending by `fetched_at` |
| `POST` | `/videos/{video_id}/stats/refresh` | `account_id` | `202` | Fetches a new snapshot |
| `GET` | `/accounts/{id}/profile` | none | `200` profile and counts |

Video object shown to app-ui: `id`, `create_time`, `title`, `video_description`, `duration`, `width`, `height`, `cover_image_url`, `share_url`, `embed_link`, `is_aigc`, and counts `view_count`, `like_count`, `comment_count`, `share_count`.

The stats series is bounded on both ends. `range` is a closed enum of `7d`, so an unknown value is a `422` with `VALIDATION_FAILED` rather than a silent default, and the series returns at most 500 points. The read path is read-only: a background worker appends one point per live video on its tick and prunes rows older than the 7 day retention, so opening a chart does not fetch. The one exception is a first sighting: a video with no point inside the window gets one fetch and append. A first-sighting fetch that fails returns the upstream error rather than an empty series. History is appended, never refetched. `POST /videos/{video_id}/stats/refresh` is the explicit way to add a point on demand.

Counts carry the snapshot time. When TikTok returns no value for a field, the field is omitted, never sent as `0`. app-ui renders "not available" instead of a zero, which is the rule that keeps a missing statistic from reading as a real one.

Cover image URLs have a TTL. A stale URL is refreshed with `/videos/query` before it is handed to app-ui.

### 7.6 Webhook intake

| Method | Path | Body | Success |
|---|---|---|---|
| `POST` | `/webhooks/tiktok` | TikTok event envelope | `200` with an empty body |

This route is not under `/api/v1` by design: it is TikTok's contract, not ours, and must never move when our API version changes. It authenticates by signature, not by session, so it is excluded from the session middleware and guarded by its own HMAC check and IP throttle. Every outcome is `200` once the signature is valid, including duplicates and unknown event types, because a non-`200` triggers up to 72 hours of retries.

## 8. Auth and Session

- Login sets an `HttpOnly`, `Secure`, `SameSite=Lax` cookie. The cookie carries an opaque session ID, never a token.
- Session state lives in Redis with a TTL, and is refreshed on activity.
- State-changing requests require a CSRF token, sent as the `X-CSRF-Token` header and compared with the session value.
- `SameSite=Lax` plus the CSRF check is what allows the OAuth callback redirect to work while still blocking cross-site form posts.
- The API is same-origin behind one Nginx host, so there is no CORS allowance by default. Any added origin goes through an explicit allowlist.
- `GET /auth/google/callback` is the login entry, gated by the `OPERATOR_EMAIL` allowlist and the id_token verification. The TikTok callback is a connect step, not a login.

## 9. Rate Limits

| Scope | Limit | Response |
|---|---|---|
| Per session, general | Configured per route | `429` with `Retry-After` |
| Publish init, per account | Mirrors TikTok: 6 per minute | `429` before the upstream call |
| Status fetch, per account | Mirrors TikTok: 30 per minute | `429` before the upstream call |
| Creator info, per account | Mirrors TikTok: 20 per minute | Served from cache when fresh |
| Video list, per account | TikTok publishes none, so 30 per minute | Shared by the stats sweep, which pages with a delay |
| Webhook, per IP | Fixed window | `429`, logged |

Local limits exist so we fail fast instead of burning the upstream quota. A local `429` and an upstream `429` are distinct codes: `RATE_LIMITED` and `TIKTOK_RATE_LIMITED`. The first is our discipline, the second is TikTok's.

## 10. Idempotency

`POST /publish/*` and `POST /media/uploads` accept `Idempotency-Key`. The key and a hash of the body are stored for 24 hours. A repeat with the same key and body returns the stored response. A repeat with the same key and a different body returns `409` with `IDEMPOTENCY_KEY_REUSED`.

`POST /media/uploads` accepts `Checksum-SHA256`: the hex SHA-256 of the file bytes, not of the multipart framing, in 64 hex characters of either case, normalized to lowercase. A key sent without it is a `422` with `VALIDATION_FAILED`, and so is a malformed value; nothing is stored. The header exists because the body is streamed up to 200 MB, so a body hash is only known after the upload and cannot guard the write. With both headers, the claim is keyed on `Idempotency-Key` plus checksum before any byte is stored, and the server recomputes the digest from the streamed bytes before it stores anything, so a wrong header leaves no object. A repeat with the same key and checksum replays the stored response; a repeat with the same key and a different checksum is `409 IDEMPOTENCY_KEY_REUSED`; a repeat while the first attempt still runs is `409 CONFLICT`. A failed upload releases its claim, so a corrected retry under the same key is accepted.

## 11. Contract Testing

Each route gets a table-driven test for the happy path, a validation failure, and an auth failure, per `AGENTS.md` 2.1. Contract tests assert the envelope shape, the `code` catalog, and that no response body contains a token, a secret, or an upstream URL. A golden-file test pins the RFC 9457 shape so an error-format regression fails the build.

## 12. Open Items

1. Upload size caps per content type ship as config: `MEDIA_MAX_BYTES`, `MEDIA_MAX_DURATION_MS`, `MEDIA_ALLOWED_TYPES`, and `CHUNK_MAX_BYTES`.
2. ~~Operator login is TikTok OAuth only.~~ Google OIDC gated by the `OPERATOR_EMAIL` allowlist: the `users` row is upserted at sign in by `internal/operator`. There is still no password or SSO route, and `OPERATOR_ID` stays required config.
3. ~~The `range` options for `/videos/{id}/stats`.~~ Closed by `docs/DECISIONS/002-UPLOAD-IDEMPOTENCY-R2-STATS-SERIES.md`, then amended by `docs/DECISIONS/003-VIDEO-STATS-RETENTION-WORKER.md`: `7d`, default `7d`.
4. Downsampling for `video_stats` rows. Retention is closed by `docs/DECISIONS/003-VIDEO-STATS-RETENTION-WORKER.md`; a window longer than 7 days still needs a storage decision.