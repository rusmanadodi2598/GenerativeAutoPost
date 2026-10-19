# 002: TikTok Integration

- **Status:** Accepted
- **Date:** 2026-10-12
- **Depends on:** `001-SCOPE-V1.md` (decision), `001-BACKEND-ARCHITECTURE.md` (architecture)
- **Source:** developers.tiktok.com docs, pages reviewed on 2026-10-12. TikTok changes docs without notice, so re-verify before implementation.

## 1. Base Endpoints

| Host | Used for |
|---|---|
| `https://www.tiktok.com` | Browser redirect for authorization |
| `https://open.tiktokapis.com` | All server-to-server API calls |
| `https://open-upload.tiktokapis.com` | Chunk upload target, returned in `upload_url` |

Every API call uses `Authorization: Bearer <access_token>` and `Content-Type: application/json; charset=UTF-8`, except chunk upload (see 4.4).

## 2. Authentication (Login Kit, Web)

### 2.1 Authorization request

`GET https://www.tiktok.com/v2/auth/authorize/` with query parameters:

| Parameter | Value | Notes |
|---|---|---|
| `client_key` | `TIKTOK_CLIENT_KEY` | |
| `scope` | comma-separated, e.g. `user.info.basic,video.publish` | Only scopes enabled for the app |
| `redirect_uri` | `TIKTOK_REDIRECT_URI` | Must match a registered URI exactly. Max 10 URIs, HTTPS, no query, no fragment |
| `state` | random, single-use | Stored in Redis, 10 min TTL, deleted on first use |
| `response_type` | `code` | Fixed |
| `disable_auto_auth` | `0` or `1` | Optional. `1` forces the consent screen |

The `state` value is generated with `crypto/rand` (at least 32 bytes, base64url). It is the only CSRF defense on the callback, so a missing or mismatched `state` aborts the flow.

### 2.2 Callback

TikTok redirects to `redirect_uri` with `code`, `scopes`, `state`, or with `error` and `error_description`.

Handling rules:

1. Compare `state` with the Redis key, then delete it. Reject if absent (replay or expired).
2. If `error` is present, record the outcome and redirect the browser to app-ui with a generic failure. Do not echo `error_description` to the browser.
3. Check that `scopes` contains every scope the account needs. Missing scopes are stored as-is, and the feature that needs them stays disabled.
4. Exchange `code` (see 2.3) and create or update the `TikTokAccount` for the session user.

### 2.3 Token exchange and refresh

`POST https://open.tiktokapis.com/v2/oauth/token/`, `Content-Type: application/x-www-form-urlencoded`.

| Grant | Form fields |
|---|---|
| `authorization_code` | `client_key`, `client_secret`, `code`, `grant_type=authorization_code`, `redirect_uri` |
| `refresh_token` | `client_key`, `client_secret`, `grant_type=refresh_token`, `refresh_token` |

Success response `data`: `access_token`, `expires_in` (86400 = 24h), `refresh_token`, `refresh_expires_in` (31536000 = 365d), `open_id`, `scope`, `token_type`.

Rules:

- Refresh before `access_token` expiry with a 5-minute margin, under a per-account lock so two requests never refresh at once.
- A refresh returns a new `refresh_token`. Store the new one and discard the old one in the same transaction.
- A refresh that fails with an invalid-token error marks the account `needs_reauth`. It never retries in a loop.

### 2.4 Revoke

`POST https://open.tiktokapis.com/v2/oauth/revoke/` with `client_key`, `client_secret`, `access_token` (form-encoded). Run on user disconnect. Local token material is deleted even if the revoke call fails, and the failure is logged.

### 2.5 Scopes requested by this app

| Scope | Enables | Audit needed for public use |
|---|---|---|
| `user.info.basic` | open_id, union_id, avatar, display_name | No |
| `user.info.profile` | bio, profile link, verification | No |
| `user.info.stats` | follower, following, likes, video counts | No |
| `video.list` | `video/list`, `video/query` | Yes |
| `video.publish` | creator info, direct post | Yes |
| `video.upload` | draft upload, status | Yes |

Scope requests are per feature, not all at once at login. `GET /auth/tiktok/authorize` takes an optional `scope` subset and falls back to `TIKTOK_SCOPES`, so the console can ask for one feature's scope when it is first used. This keeps the consent screen honest and limits what an unaudited client asks for.

## 3. Creator Info (required before every direct post)

`POST /v2/post/publish/creator_info/query/`, scope `video.publish`, body empty. Limit: 20 requests per minute per token.

Response `data` used by the app:

| Field | Use |
|---|---|
| `creator_username`, `creator_nickname` | Shown on the publish page so the user knows the target account |
| `creator_avatar_url` | Display only, TTL 2 hours, do not store long-term |
| `privacy_level_options` | The only values allowed for `privacy_level` |
| `comment_disabled`, `duet_disabled`, `stitch_disabled` | Disable and grey out the matching toggle |
| `max_video_post_duration_sec` | Client-side duration check before upload |

Rules from TikTok's UX requirements, enforced in both backend validation and app-ui:

- `privacy_level` has no default value. The user must pick one.
- Interaction toggles (comment, duet, stitch) are off by default and the user turns them on.
- Commercial content disclosure is part of the request. At least one option must be chosen when the toggle is on.
- If the creator cannot post at the moment (`spam_risk_*` errors), stop and tell the user to try later.
- The consent declaration appears before the publish button: "By posting, you agree to TikTok's Music Usage Confirmation" with the matching policy links. The exact wording comes from the content sharing guidelines and is copied verbatim into app-ui during implementation.

`creator_info` snapshots are cached per account for a short TTL (suggested 5 minutes) to stay under the rate limit. A cache miss is not an error.

## 4. Publishing

### 4.1 Direct post, video

`POST /v2/post/publish/video/init/`, scope `video.publish`, limit 6 requests per minute per token.

Body:

```json
{
  "post_info": {
    "title": "string, max 2200 UTF-16 runes, hashtags and mentions parsed",
    "privacy_level": "from creator_info options only",
    "disable_duet": false,
    "disable_comment": false,
    "disable_stitch": false,
    "video_cover_timestamp_ms": 1000,
    "brand_content_toggle": false,
    "brand_organic_toggle": false,
    "is_aigc": false
  },
  "source_info": {
    "source": "FILE_UPLOAD | PULL_FROM_URL",
    "video_size": 50000123,
    "chunk_size": 10000000,
    "total_chunk_count": 5,
    "video_url": "https://... (PULL_FROM_URL only)"
  }
}
```

Response `data`: `publish_id` (max 64 chars), and `upload_url` for `FILE_UPLOAD` only (valid 1 hour).

- `FILE_UPLOAD`: `video_size`, `chunk_size`, `total_chunk_count` are required. Upload follows section 4.4.
- `PULL_FROM_URL`: `video_url` must be on a domain or URL prefix verified for the app. TikTok pulls the file itself, so app-serve does not transfer it.

`is_aigc: true` adds the "Creator labeled as AI-generated" tag. This product generates content, so the flag must be set truthfully on every generated video.

### 4.2 Direct post, photo

`POST /v2/post/publish/content/init/`, scope `video.publish` or `video.upload`.

Required body fields: `media_type: "PHOTO"`, `post_mode` (`DIRECT_POST` or `MEDIA_UPLOAD`), `post_info`, `source_info`. `source` is `PULL_FROM_URL` only. `photo_images` is the list of verified URLs.

Limits: title 90 UTF-16 runes, description 4000 UTF-16 runes. `privacy_level` required for `DIRECT_POST`. `auto_add_music` applies to `DIRECT_POST` only.

### 4.3 Draft upload (inbox share)

Draft upload does not publish. TikTok places the content in the creator's inbox, and the creator finishes the post in TikTok.

- Video: `POST /v2/post/publish/inbox/video/init/`, scope `video.upload`, limit 6 requests per minute per token. Body has `source_info` only (`source`, `video_size`, `chunk_size`, `total_chunk_count` or `video_url`). No `post_info`.
- Photo: `POST /v2/post/publish/content/init/` with `post_mode: "MEDIA_UPLOAD"`.

The app must tell the user to open the TikTok inbox notification to finish the post. The API does not post on the creator's behalf in this mode.

### 4.4 Chunk upload (FILE_UPLOAD only)

`PUT <upload_url>` with headers:

| Header | Value |
|---|---|
| `Content-Type` | `video/mp4`, `video/quicktime`, or `video/webm` |
| `Content-Length` | byte length of this chunk |
| `Content-Range` | `bytes {FIRST}-{LAST}/{TOTAL}` |

Body is the raw chunk bytes. The `upload_url` query string is used as returned.

Chunk plan rules (`media` module):

- Each chunk is between 5 MB and 64 MB, except the last chunk, which may be larger than `chunk_size` up to 64 MB. A trailing remainder under 5 MB merges into the previous chunk.
- At least 1 and at most 1000 chunks.
- Chunks upload sequentially, in order.
- A failed chunk is retried with the same `Content-Range`. Gateway and `5xx` errors are retryable.
- The whole upload must finish within one hour of `upload_url` issuance. If the window closes, start a new `video/init` call.

Supported formats: MP4 (H.264), QuickTime, WebM. Check size and format before init so bad files never reach TikTok.

### 4.5 Status

`POST /v2/post/publish/status/fetch/`, body `{"publish_id": "..."}`, scopes `video.publish` or `video.upload`, limit 30 requests per minute per token.

Response `data.status` values:

| Status | Meaning | Applies to |
|---|---|---|
| `PROCESSING_UPLOAD` | File upload in progress | FILE_UPLOAD |
| `PROCESSING_DOWNLOAD` | TikTok is pulling the URL | PULL_FROM_URL |
| `SEND_TO_USER_INBOX` | Draft delivered to the creator inbox | Draft |
| `PUBLISH_COMPLETE` | Post is live, or the creator posted the draft | All |
| `FAILED` | Terminal failure, see `fail_reason` | All |

`publicaly_available_post_id` is returned only after moderation. `uploaded_bytes` and `downloaded_bytes` are progress counters.

This endpoint is the polling fallback. Primary state comes from webhooks (see 5).

## 5. Webhooks (primary publish state)

### 5.1 Registration

The callback URL is set in the Developer Portal for the app: `https://app-post.kentangtech.com/webhooks/tiktok`. It must use HTTPS, return `200` immediately, and TikTok retries for up to 72 hours with exponential backoff. Delivery is at least once, so duplicates are normal.

### 5.2 Signature verification

Header: `TikTok-Signature: t=<unix_seconds>,s=<hex_signature>`.

Algorithm:

1. Split the header on `,`, then each element on `=`. Read `t` and `s`. Reject if either is missing or malformed.
2. Compute `signed_payload = t + "." + raw_body`, using the exact raw bytes, not a re-serialized JSON.
3. `expected = HMAC-SHA256(key = TIKTOK_CLIENT_SECRET, message = signed_payload)`, hex-encoded.
4. Compare with `hmac.Equal` (constant time) against `s`.
5. Reject if `|now - t|` is greater than the tolerance (suggested 5 minutes).

The raw body is read with a size limit (suggested 64 KB). Verification runs before JSON decoding. Any failure returns `401` with a generic problem response, and the failure is logged with the source IP.

### 5.3 Events

Envelope (verified on the Webhooks Events page): `client_key`, `event`, `create_time` (UTC seconds), `user_openid`, `content`. `content` is a JSON object serialized as a string, so it is decoded in two steps: first the envelope, then `content` into a typed struct per event. Never read it as a free map.

Source note: the `post.publish.*` events below come from the Get Post Status page. The Webhooks Events page does not list them, and it lists `video.upload.failed` and `video.publish.completed` under the Video Kit name instead. Confirm the exact event names and payloads in the Developer Portal before implementing.

| Event | Fields | Effect in app-serve |
|---|---|---|
| `post.publish.inbox_delivered` | `publish_id`, `publish_type` | Status to `SEND_TO_USER_INBOX`, notify user |
| `post.publish.complete` | `publish_id`, `publish_type` | Status to `PUBLISH_COMPLETE` |
| `post.publish.publicly_available` | `publish_id`, `post_id`, `publish_type` | Store `post_id`, mark public |
| `post.publish.no_longer_publicaly_available` | `publish_id`, `post_id`, `publish_type` | Clear public flag, keep history |
| `post.publish.failed` | `publish_id`, `reason`, `publish_type` | Status `FAILED`, store reason |
| `authorization.removed` | `reason` (int: 0 unknown, 1 user disconnected in TikTok, 2 account deleted, 3 age changed, 4 account banned, 5 developer revoked) | Mark account `revoked_at`, delete local token material, stop scheduled publishes for this `user_openid`. TikTok has already revoked the token when this arrives |
| `video.upload.failed`, `video.publish.completed` | `share_id` | Video Kit events. Not used by the Content Posting flow. Ignore with a logged acknowledgement until a feature needs them |

Note the spelling `publicaly` in two event names and in `publicaly_available_post_id`. This is TikTok's spelling, so the typed struct uses the exact wire name, while Go identifiers stay spelled correctly.

### 5.4 Processing

1. Verify signature (5.2).
2. Decode into a typed struct. Reject unknown `event` values with `200` after logging (acknowledge, do not retry forever).
3. Compute `dedupe_key = event + ":" + publish_id + ":" + (post_id or create_time)`.
4. Insert into `webhook_events` with the unique `dedupe_key`. A conflict means duplicate: return `200`, do nothing.
5. Acknowledge `200`. Processing happens after the response is sent, through a bounded worker pool with a panic recovery wrapper.
6. The worker updates `publish_attempts` in one transaction with the outbox row, then sets `processed_at`.

Acknowledge-first means a crash after step 4 but before step 6 leaves `processed_at` null. A reconciler picks these up (section 6), so no event is lost.

## 6. Polling Fallback

Polling is a reconciler, not a request path. It runs as a background worker in app-serve.

| Trigger | Action |
|---|---|
| `publish_attempts` open longer than the threshold (suggested 10 minutes) with no webhook | Call status fetch |
| `webhook_events` with `processed_at IS NULL` older than 5 minutes | Reprocess |
| Manual reconcile command for a single `publish_id` | Status fetch |

Backoff is exponential with jitter, capped. Status fetch respects the 30 requests per minute per token limit through the shared Redis limiter. A status returned as `FAILED` with an unknown `fail_reason` is stored as-is and logged, not dropped.

## 7. Statistics and Video Reads

| Endpoint | Scope | Limit | Notes |
|---|---|---|---|
| `POST /v2/video/list/` | `video.list` | Not documented per token. Treat as shared and back off on `429` | Public videos only, sorted by `create_time` desc. `max_count` default 10, max 20. `cursor` is a UTC millisecond timestamp. Use `has_more` and `cursor` for paging |
| `POST /v2/video/query/?fields=...` | `video.list` | Same | Up to 20 `video_ids` per call. Returns the requested `fields` and refreshes cover URL TTL |
| `GET /v2/user/info/?fields=...` | `user.info.basic` (+ `.profile`, `.stats`) | Same | `fields` picks the set: `open_id`, `union_id`, `avatar_url`, `display_name`, `bio_description`, `profile_deep_link`, `follower_count`, `following_count`, `likes_count`, `video_count` |

Video fields available: `id`, `create_time`, `cover_image_url`, `share_url`, `video_description`, `duration`, `height`, `width`, `title`, `embed_html`, `embed_link`, `like_count`, `comment_count`, `share_count`, `view_count`, `is_aigc`.

Statistics are stored as a `video_stats` series for trends. A background worker sweeps every connected account on `STATS_REFRESH_INTERVAL` (default 3 hours) and prunes points older than `STATS_RETENTION` (default 168 hours), so the series covers a 7 day window. The UI shows the stored series with each point's `fetched_at`, never a live number presented as current. Cover URLs expire, so the app re-queries before display when the URL is older than its TTL.

Stats are shown only when the TikTok response contains them. Empty means empty, not zero (`AGENTS.md` and antislop R-17).

## 8. Error Mapping

Every TikTok response has `error.code`, `error.message`, `error.log_id`. Any code other than `ok` is a failure, regardless of HTTP status.

| TikTok code (HTTP) | App `code` | Retry | Client message (English) |
|---|---|---|---|
| `invalid_param` (400) | `TIKTOK_INVALID_PARAM` | No | The request to TikTok was invalid. Check the input. |
| `access_token_invalid` (401) | `TIKTOK_REAUTH_REQUIRED` | Refresh once, then no | The TikTok connection needs to be renewed. |
| `scope_not_authorized` (401) | `TIKTOK_SCOPE_MISSING` | No | This action needs permission you have not granted. |
| `spam_risk_too_many_posts` (403) | `TIKTOK_DAILY_POST_LIMIT` | No, try later | You have reached the posting limit. Try again later. |
| `spam_risk_user_banned_from_posting` (403) | `TIKTOK_POSTING_BLOCKED` | No | This TikTok account cannot post right now. |
| `reached_active_user_cap` (403) | `TIKTOK_APP_QUOTA_REACHED` | No, try later | Posting is temporarily unavailable. Try again later. |
| `unaudited_client_can_only_post_to_private_accounts` (403) | `TIKTOK_UNAUDITED_PRIVATE_ONLY` | No | Posts are limited to private until the app passes TikTok's audit. |
| `url_ownership_unverified` (403) | `TIKTOK_URL_NOT_VERIFIED` | No | The video URL domain is not verified for posting. |
| `privacy_level_option_mismatch` (403) | `PUBLISH_PRIVACY_LEVEL_MISMATCH` | No | The selected privacy level is not available for this account. |
| `rate_limit_exceeded` (429) | `TIKTOK_RATE_LIMITED` | Yes, with backoff | Too many requests. Try again shortly. |
| `internal_error` / 5xx | `TIKTOK_UNAVAILABLE` | Yes, bounded | TikTok is temporarily unavailable. Try again shortly. |

Unknown codes map to `TIKTOK_UNKNOWN_ERROR` with the `log_id` kept for support. The raw TikTok message is logged, never sent to the client in production.

Post-failure reasons (`fail_reason`, webhook `reason`) are stored verbatim and surfaced on the attempt as `fail_reason`, not mapped to an error code: `file_format_check_failed`, `duration_check_failed`, `frame_rate_check_failed`, `picture_size_check_failed`, `video_pull_failed`, `photo_pull_failed`, `internal`, and `RECONCILE_EXHAUSTED` when the polling fallback reaches its retry cap. The console maps the reason to copy that tells the user which input to change.

## 9. Constraints Checklist for Implementation

- Unaudited clients post private content only, capped at 5 users per 24 hours. The UI must show this state, not hide it.
- `PULL_FROM_URL` requires a verified domain or URL prefix. `MEDIA_PUBLIC_BASE_URL` is the verified prefix, and it is not configured until the domain is verified in the portal.
- Every posting user must set the privacy level and interactions manually.
- Creator name must be visible on the publish page.
- The consent declaration must appear before the publish button.
- Generated content must set `is_aigc` truthfully.

## 10. Open Items

1. Exact consent and policy wording, copied from the Music Usage Confirmation and content sharing guidelines during implementation, verified against the live page.
2. `video/list` has no documented per-token limit, so the app caps it at 30 per minute per account (`FamilyListVideos`) until TikTok confirms otherwise.
3. The `user.info.stats` fields are implemented as `follower_count`, `following_count`, `likes_count`, and `video_count`; re-verify against the live page before the console copy is finalized.
