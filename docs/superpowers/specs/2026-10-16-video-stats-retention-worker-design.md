# Video Stats Retention and Refresh Worker

- **Status:** Accepted 2026-10-16
- **Date:** 2026-10-16
- **Owner:** Dodi Rusmana
- **Depends on:** `docs/DECISIONS/002-UPLOAD-IDEMPOTENCY-R2-STATS-SERIES.md` (decision 3)
- **Governing rules:** `AGENTS.md`, `docs/RULLES/TDD.md`

## Intent

The console reads one stats series per video. Today that series is kept fresh
by a fetch on read: opening the chart may append one point. Freshness then
depends on someone looking, and the upstream quota is spent at read time.
Meanwhile `video_stats` is append-only and unbounded.

This change moves freshness to a background worker. The worker sweeps every
connected TikTok account on a fixed interval, appends one point per live video,
and prunes rows older than 7 days. A read then serves stored points and does
not need an upstream call to be current.

## Goals

- A series read is never much staler than one worker tick.
- Rows older than 7 days leave the table on a schedule.
- The series response stays bounded.

## Non-goals

- Downsampling or rollup tables.
- Cursor pagination for the series.
- Any change to the manual refresh endpoint.
- Multi-operator account partitioning, which v1 does not have.

## Anti-goals

- Freshness that depends on a viewer opening the chart.
- A read that silently falls back to another store.
- An unbounded table or an unbounded series response.

## Decisions

1. **The worker sweeps every connected account.** It is not limited to videos
   the operator has already opened, so a newly published video gains a point
   on the next tick without a manual open.
2. **The tick is paced in hours.** Default 3 hours, set by
   `STATS_REFRESH_INTERVAL`. A tighter tick would push 7 days of points past
   the 500 point response cap; a per minute point is 500 points in 8.3 hours.
3. **Retention is 7 days.** `STATS_RETENTION` defaults to `168h`. Rows older
   than the cutoff are deleted after each sweep.
4. **The `range` enum shrinks to `7d`.** A 7 day retention leaves a 30 day or
   90 day window empty past its seventh day, so those two values are removed
   and `7d` becomes the default. `range` stays a parameter so a future window
   is a contract change, not a new field.
5. **The 500 point cap stays.** `maxSeriesPoints` already bounds the response,
   so there is no cursor pagination.
6. **A read is read-only, with one first-sighting exception.** `StatsSeries`
   no longer refreshes the newest point on every open. When a video has no
   point inside the scope window (a video published after the last tick and
   opened before the next), the service performs one fetch and append. That is
   a first fetch, not a refetch, so it does not conflict with "append, never
   refetch," and it closes the cold-start gap the worker alone would leave.
   `StatsTTL` and the stale-latest refetch leave with it, so the only
   read-time upstream call is that first fetch. `docs/DECISIONS/002`,
   `docs/SPEC/003` section 7.5, and `SYSTEM_MAP.md` move in the same change.
7. **One failure does not stop the sweep.** A failed account or video is
   logged with its account id and video id, and the sweep continues to the
   next one.
8. **The worker refreshes tokens itself.** A stored access token is good for
   about a day, and this worker runs for months with no operator present, so
   each account is refreshed through a `TokenRefresher` port wired in the
   composition root to `auth.Service.Refresh`, before its sweep starts. A
   rejected refresh token marks the account needs-reauth through the existing
   `auth.Refresh` path, which is a re-login prompt for the operator, not a
   silent skip. Confirmed by the operator 2026-10-16.
9. **The sweep walks the newest catalog and goes through the shared limiter.**
   `video/list` is paged until `has_more` is false, newest first, with no page
   cap. Every list call passes the existing Redis token bucket through the
   `FamilyListVideos` family, so a catalog larger than the per-minute budget
   stops for this tick and resumes on the next one instead of overrunning
   TikTok. Old videos past that point simply keep the points they already
   have. `video/query` is batched 20 ids per call, so bulk is bounded by the
   list budget that gates it.
10. **A failed first-sighting fetch propagates.** The read returns the
    upstream error, as `VideoStats` does today, rather than an empty series
    that hides the failure. The cost is one call per open for a video the
    sweep also cannot fetch, which is rare and bounded by chart opens.

## Worker flow, one tick

1. List every connected account for the operator (`cfg.OperatorID`).
2. For each account, refresh the token through the `TokenRefresher` port,
   then resolve an access token. A revoked account or one that needs reauth is
   logged and skipped.
3. Page through `video/list` until `has_more` is false, gated by the shared
   limiter under `FamilyListVideos`, collecting video ids. A spent budget ends
   this account's sweep until the next tick.
4. Query stats in batches of 20 ids, the upstream limit, and append one
   `video_stats` row per returned video.
5. Delete `video_stats` rows older than the retention cutoff.

The worker lives in `internal/insight`, follows the `webhook.Reconciler`
shape (`Run(ctx)` loop), is wired in `compose.Build`, and is started in
`main.go` through `supervised` with panic recovery and the shutdown wait
group.

## Read path

- `GET /videos/{id}/stats` serves `ListStats` results only, except the
  first-sighting fetch in decision 6.
- `POST /videos/{id}/stats/refresh` is unchanged: it always fetches and
  appends.

## Data

- No schema change and no new column.
- The series read keeps using `ix_video_stats_video_fetched`.
- Retention is a `DELETE ... WHERE fetched_at < $1`. That is a periodic bulk
  prune, not a per-request lookup, and the table is bounded by retention, so
  no new index is added. This is the builder's call, recorded here.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `STATS_REFRESH_INTERVAL` | `3h` | Worker tick |
| `STATS_RETENTION` | `168h` | Row age cutoff |

Both are parsed at boot through the existing `durationOr` helper and must be
positive.

## Constraints

- Files stay under 250 lines and carry the `AGENTS.md` 1.2 header.
- Upstream limits: 20 ids per `QueryVideos`, 30 `status/fetch` per account per
  minute, and 30 `video/list` per account per minute. The sweep gates its list
  calls through the shared limiter and is paced by the interval.
- Every outbound call takes a context with a timeout.
- Client-facing errors stay English.

## What is left to the builder

- Package layout and exact method signatures for the account lister and the
  retention delete.
- The port names and where the new interfaces sit.
- The test list.

## Builder calls recorded here

- Decision 6 removes `StatsTTL`; the stale-latest refetch test inverts, and
  002, 003, and SYSTEM_MAP move in the same change.
- Decision 9 keeps no page cap and adds limiter wiring for `video/list`.
- Decision 10 propagates a failed first-sighting fetch.
