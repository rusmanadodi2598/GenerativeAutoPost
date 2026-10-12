# 001: V1 Scope for GenerativeAutoPost

- **Status:** Accepted
- **Date:** 2026-10-12
- **Owner:** Dodi Rusmana
- **Deciders:** Dodi Rusmana
- **Supersedes:** none

## Context

GenerativeAutoPost posts generated content to TikTok. Before any spec or scaffold is written, the surface for v1 has to be fixed, because it decides:

- which TikTok products we register for and which scopes we request,
- how many tokens and permission states we store per TikTok account,
- whether we need a verified public domain (for `PULL_FROM_URL`), a webhook endpoint, or both,
- the size of the first implementation plan.

Three scopes were on the table. The user selected the widest one and added a delivery constraint: publish outcomes arrive by webhook first, with polling only as a fallback.

## Options Considered

| # | Option | Surface | Why it was on the table | Why it was not chosen |
|---|---|---|---|---|
| 1 | Posting only | OAuth, creator info, video and photo direct post, status | Smallest shippable loop | Leaves out statistics, which was part of the original brief |
| 2 | Posting plus stats | Option 1, plus `video/list`, `video/query`, `user/info`, profile stats | Covers post and measure | Leaves out the draft flow (`video.upload` / inbox share) |
| 3 | **Full** | Option 2, plus draft upload to creator inbox, webhooks, and token lifecycle | One integration cycle for the whole TikTok surface | Larger v1, more scopes to get audited |

## Decision

**Option 3: Full.** The v1 backend implements the complete TikTok content surface in a single integration cycle:

| Area | TikTok endpoint | Method | Scope |
|---|---|---|---|
| Authorization | `www.tiktok.com/v2/auth/authorize/` | GET (redirect) | requested scopes |
| Token exchange | `/v2/oauth/token/` | POST | none (client creds) |
| Token refresh | `/v2/oauth/token/` | POST | none (refresh token) |
| Token revoke | `/v2/oauth/revoke/` | POST | none (client creds) |
| Creator info | `/v2/post/publish/creator_info/query/` | POST | `video.publish` |
| Direct post, video | `/v2/post/publish/video/init/` | POST | `video.publish` |
| Direct post, photo | `/v2/post/publish/content/init/` | POST | `video.publish` |
| Draft upload | `/v2/post/publish/inbox/video/init/` | POST | `video.upload` |
| Post status | `/v2/post/publish/status/fetch/` | POST | `video.publish`/`video.upload` |
| List videos | `/v2/video/list/` | POST | `video.list` |
| Query videos | `/v2/video/query/` | POST | `video.list` |
| User info | `/v2/user/info/` | GET | `user.info.basic`, `user.info.profile`, `user.info.stats` |

**Publish outcomes are webhook-first.** Polling is a reconciliation fallback, never the primary signal.

### Webhook-first

TikTok Content Posting webhooks are delivered as HTTPS POST to the callback registered in the Developer Portal. The signature header is `TikTok-Signature` with the shape `t=<unix_seconds>,s=<hex_hmac_sha256>`; the signed payload is `<timestamp>.<raw_json_body>`, keyed with the app `client_secret`. Verification is HMAC-SHA256 over the raw body, with a timestamp tolerance, and it runs before the event is parsed or acted on.

Events consumed in v1:

| Event | Key fields | Meaning |
|---|---|---|
| `post.publish.inbox_delivered` | `publish_id`, `publish_type` | Draft reached the creator inbox |
| `post.publish.complete` | `publish_id`, `publish_type` | Creator published from the content we sent |
| `post.publish.publicly_available` | `publish_id`, `post_id`, `publish_type` | Post passed moderation and is public |
| `post.publish.no_longer_publicaly_available` | `publish_id`, `post_id`, `publish_type` | Post stopped being publicly viewable |
| `post.publish.failed` | `publish_id`, `reason`, `publish_type` | Publish failed, reason is an enum |

Delivery is at-least-once, so a duplicate `publish_id` with the same event and `post_id` is a no-op. TikTok retries an unacknowledged event for up to 72 hours with exponential backoff, so the endpoint acknowledges with `200` immediately and processes the event after the acknowledgement, not before it.

Reasons to prefer the webhook:

- `post.publish.publicly_available` carries `post_id`, which the status endpoint cannot return until moderation finishes. Polling alone cannot close the loop on a public post.
- A stuck `PUBLISH_COMPLETE` polling loop keeps hitting a per-account limit of 30 requests per minute. Webhook delivery has no such cost.
- The `reason` enum on `post.publish.failed` lets us map a user-facing error without a second call.

### Polling as fallback

Polling exists to cover the gaps the webhook cannot:

- a local dev or test environment with no public HTTPS callback,
- a lost or late delivery during the 72 hour retry window,
- manual reconciliation after an outage or a consumer restart.

The fallback is a bounded background reconciler over publish attempts that are still open past a threshold, with backoff, not an inline loop on the request path.

## Consequences

**Positive**

- One authorization and audit pass covers posting, drafts, and statistics.
- `post_id` becomes available through `post.publish.publicly_available`, enabling stats reads without racing moderation.
- Stored token state is complete from day one: `access_token` (24h), `refresh_token` (365d), `open_id`, granted scopes, revoke state.

**Negative and constraints**

- More scopes to get approved: `user.info.basic`, `user.info.profile`, `user.info.stats`, `video.list`, `video.publish`, `video.upload`.
- An unaudited client can only post private content and is limited to 5 posting users per 24 hours. Public posting needs the audit.
- `PULL_FROM_URL` needs a verified domain or URL prefix, which becomes a deployment requirement. `FILE_UPLOAD` needs chunked upload (5 MB to 64 MB per chunk, at most 1000 chunks, sequential).
- The webhook endpoint is a new public attack surface, so it carries the OWASP A01/A08 controls: signature verification, timestamp tolerance, replay guard, and idempotent storage.
- Unverified webhook callers must not be able to influence stored state, so verification precedes parsing.

## Out of Scope for V1

- Research API and Commercial Content API.
- Comment management and direct messaging.
- Multi-account scheduling beyond a queued or on-demand publish.
- Analytics beyond the fields TikTok exposes (`view_count`, `like_count`, `comment_count`, `share_count`).

## Open Questions

1. Hosting target for the public webhook callback, and whether the verified domain for `PULL_FROM_URL` shares that host.
2. Which granted scopes we request at first login versus on demand, given the audit requirement.
3. Whether draft upload (`video.upload`) ships in the first milestone or the second, since the webhook events for it are already modeled.