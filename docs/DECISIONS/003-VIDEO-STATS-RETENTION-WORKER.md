# 003: Video Stats Retention and Refresh Worker

- **Status:** Accepted
- **Date:** 2026-10-16
- **Owner:** Dodi Rusmana
- **Deciders:** Dodi Rusmana
- **Supersedes:** `docs/DECISIONS/002-UPLOAD-IDEMPOTENCY-R2-STATS-SERIES.md` decision 3

## Context

The console reads one stats series per video. Decision 3 of ADR 002 kept that
series fresh by fetching on read, so opening the chart could append one point.
Freshness then depended on someone looking, the upstream quota was spent at
read time, and `video_stats` stayed append-only and unbounded.

The design at
`docs/superpowers/specs/2026-10-16-video-stats-retention-worker-design.md`
moves freshness to a background worker. The worker sweeps every connected
account on a fixed interval, appends one point per live video, and prunes rows
older than 7 days. A read then serves stored points and does not need an
upstream call to be current.

## Decision 1: the worker sweeps every connected account

The sweep covers every account the operator has connected, not only videos the
operator has already opened. A newly published video gains a point on the next
tick without a manual open.

## Decision 2: the tick is paced in hours

`STATS_REFRESH_INTERVAL` defaults to 3 hours. A tighter tick pushes 7 days of
points past the 500 point response cap: a per minute point is 500 points in
8.3 hours.

## Decision 3: retention is 7 days

`STATS_RETENTION` defaults to `168h`. Rows older than the cutoff are deleted
after each sweep, so the table stays bounded without a per-request lookup.

## Decision 4: the `range` enum shrinks to `7d`

A 7 day retention leaves a 30 day or 90 day window empty past its seventh day,
so those two values are removed and `7d` becomes the default. `range` stays a
parameter so a future window is a contract change, not a new field.

## Decision 5: the 500 point cap stays

`maxSeriesPoints` already bounds the response, so there is no cursor
pagination.

## Decision 6: a read is read-only, with one first-sighting exception

`StatsSeries` no longer refreshes the newest point on every open. When a video
has no point inside the scope window, the service performs one fetch and
append. That is a first fetch, not a refetch, so it does not conflict with
"append, never refetch," and it closes the cold-start gap the worker alone
would leave for a video published after the last tick and opened before the
next. `StatsTTL` and the stale-latest refetch leave with it, so the only
read-time upstream call is that first fetch. `docs/DECISIONS/002`,
`docs/SPEC/003` section 7.5, and `SYSTEM_MAP.md` moved in the same change.

## Decision 7: one failure does not stop the sweep

A failed account or video is logged with its account id and video id, and the
sweep continues to the next one.

## Decision 8: the worker refreshes tokens itself

A stored access token is good for about a day, and this worker runs for months
with no operator present, so each account is refreshed through a
`TokenRefresher` port before its sweep starts. The port is wired in the
composition root to `auth.Service.Refresh`. A rejected refresh token marks the
account needs-reauth through the existing `auth.Refresh` path, which is a
re-login prompt for the operator, not a silent skip. Confirmed by the operator
2026-10-16.

## Decision 9: the sweep walks the newest catalog and goes through the shared limiter

`video/list` is paged until `has_more` is false, newest first, with no page
cap. Every list call passes the existing Redis token bucket through the
`FamilyListVideos` family, so a catalog larger than the per-minute budget stops
for this tick and resumes on the next one instead of overrunning TikTok. Old
videos past that point keep the points they already have. `video/query` is
batched 20 ids per call, so bulk is bounded by the list budget that gates it.

## Decision 10: a failed first-sighting fetch propagates

The read returns the upstream error, as `VideoStats` did before, rather than an
empty series that hides the failure. The cost is one call per open for a video
the sweep also cannot fetch, which is rare and bounded by chart opens.

## Consequences

**Positive**

- Freshness no longer depends on a reader opening the chart, so a series is at
  most one tick stale.
- `video_stats` is bounded at 7 days, so the table and the series response stay
  bounded together.
- The read path spends no upstream quota except on a first sighting, which
  leaves the read budget to the rate-limited worker and the manual refresh.
- A failed account, video, or token does not stall the rest of the sweep.

**Negative and constraints**

- A window longer than 7 days is now a 30 day or 90 day gap: the enum is `7d`
  only until a longer retention or a rollup ships.
- The first-sighting fetch keeps one read-time upstream call. A video the sweep
  cannot fetch pays one call per open, which decision 10 accepts as rare.
- The sweep needs `video.list` scope and a working refresh token per account,
  so a revoked or needs-reauth account stays stale until the operator re-logs
  in.
- No new index is added. Retention is a periodic bulk delete on `fetched_at`,
  and the series read keeps using `ix_video_stats_video_fetched`.

## Open Questions

1. Downsampling or rollup tables for a window longer than 7 days. Retention is
   settled here; a longer range still needs a storage decision.
