# 001: v1 backend

## Scope

The Go service in `app-serve/` behind the gateway, from first commit through
the deployment files. No admin UI ships in this version.

## What landed

- `internal/config`: typed env, validated at boot, fail fast on bad values.
- `internal/apierror`: RFC 9457 problem responses and the error code catalog.
- `internal/platform/crypto`: AES-256-GCM token keyring with rotation, HMAC checks.
- `internal/auth`: Google OIDC operator sign in, TikTok connect, server-side session cookie.
- `internal/operator`: users row upsert for the signed in operator.
- `internal/platform/googleoidc`: Google OIDC authorize, code exchange, and id token verification.
- `internal/account`: account aggregate, pgx repository, token vault.
- `internal/tiktokclient`: outbound client, rate limiter, and error map.
- `internal/media`: disk and S3 stores, chunk planner, upload service.
- `internal/publish`: init for video, photo, and draft, chunk proxy, status reconcile.
- `internal/webhook`: signature verification, idempotent intake, dispatcher, worker pool, reconciler.
- `internal/insight`: video list and query, stats series, creator profile, background stats refresh worker.
- `deployment/`: local compose stack and the nginx TLS edge config.
- `DESIGN.md`: direction brief template, awaiting owner input.

## Data

Nine tables, migrated up and down through `migrations/`. Stats and creator
snapshots are append-only: every write is an insert, so the history is never
rewritten. The `video_stats` table keeps a 7 day window; the refresh worker
prunes rows older than the retention cutoff after each sweep.

## Behavior worth knowing

- Counts TikTok omits stay omitted on the wire. The console renders "not
  available" instead of zero.
- Snapshot rows carry `fetched_at`, which is how the console judges freshness.
- A background worker appends one `video_stats` point per live video on its
  interval, so freshness no longer waits on someone opening the chart. The
  stats read fetches only on a first sighting, when a video has no point
  inside the window.
- The TikTok upload URL never reaches a client; chunks are proxied server-side.
- Webhook intake is idempotent per event id, and a duplicate apply is a no-op.
- Sign in is Google only, and only the email in `OPERATOR_EMAIL` is allowed.
  TikTok OAuth is a connect step for an operator who already has a session.
- `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URI`, and
  `OPERATOR_EMAIL` are required at boot; an existing `.env` must add them
  before the next deploy. Behind a reverse proxy set `TRUSTED_PROXIES`, or
  every client shares one auth throttle budget.
- The operator row in `users` is upserted at sign in, so
  `tiktok_accounts.user_id` has a parent.
- The public host is `app-post.kentangtech.com`, recorded in
  `docs/DECISIONS/004`. It is the RFC 9457 `type` URI base and the Nginx
  `server_name`, so the TikTok webhook callback and the Google redirect URI
  are registered against it.

## Not in this version

The admin UI, its design direction, and the OAuth console login screen.