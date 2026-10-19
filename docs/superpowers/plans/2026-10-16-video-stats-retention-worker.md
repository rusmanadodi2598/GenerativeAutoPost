# Video Stats Retention and Refresh Worker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** Completed 2026-10-16. Every task landed on `main`, the checked steps were confirmed against the commit history on 2026-10-18, and `scripts/gates/all.sh` passes, including the tagged suite against the local PostgreSQL 14 host cluster. The compose stack still pins PostgreSQL 17.

**Goal:** Keep the `video_stats` series warm with a background worker that sweeps every connected account, and bound the table with a 7 day retention.

**Architecture:** A new `insight.StatsWorker` follows the existing `webhook.Reconciler` worker shape: a `Run(ctx)` ticker loop that the composition root starts with panic recovery and joins on shutdown. Each tick refreshes every account token, pages the account's newest videos through the shared Redis rate limiter, appends one snapshot per video, then prunes rows older than the cutoff. The stats read path stops refetching the newest point on every open and serves stored points.

**Tech Stack:** Go 1.26, PostgreSQL 17, Redis 7, pgx/v5, gin, go-playground/validator, log/slog.

**Spec:** `docs/superpowers/specs/2026-10-16-video-stats-retention-worker-design.md`

## Global Constraints

- Module `github.com/kentangtech/GenerativeAutoPost/app-serve`, Go 1.26.
- Every new or modified `.go` file stays under 250 lines (warning at 220) and carries the `AGENTS.md` 1.2 header with `@author Dodi Rusmana <rusmanadodi@kentangtech.com>`.
- Client-facing error `detail` strings are English. No `any` outside an I/O boundary.
- Layer flow for `internal/insight` stays `schema -> domain -> repository -> service -> handler -> router`; the new worker file carries `@layer worker`.
- Every outbound call takes a `context` with a timeout. Every goroutine recovers from a panic.
- `video/query` takes at most 20 ids per call. `video/list` spends at most 30 calls per account per minute.
- The stats `range` value is exactly `7d` and defaults to `7d`.
- `STATS_REFRESH_INTERVAL` defaults to `3h`. `STATS_RETENTION` defaults to `168h`. Both are parsed at boot and must be positive.
- Gates: `bash scripts/gates/all.sh` must pass, including `go test -race`.

## Review Focus

Input classes and failure modes the spec implies; each line is pinned by a test in the named task.

1. A series read for a video with no stored point and a failing upstream returns the upstream error, not an empty 200. Task 4.
2. A sweep where one account's token refresh fails still sweeps the remaining accounts. Task 5.
3. A sweep where the list budget runs out mid catalog appends the ids already listed and keeps the other accounts. Task 5.
4. Retention prunes by age across every video, not just one. Task 2.
5. A video published after the last tick is appened once on first view, and the next read does not refetch. Task 4.

## File Structure

| File | Responsibility |
|---|---|
| `internal/config/config.go` | Two new duration fields, defaults, validation |
| `internal/config/wiring_test.go` | Boot tests for the two fields |
| `internal/insight/repository.go` | `DeleteStatsBefore` prune query |
| `internal/insight/stats.go` | `SnapshotStore` port, read-only `StatsSeries` |
| `internal/insight/schema.go` | `7d`-only range enum and parser |
| `internal/insight/service.go` | `StatsTTL` removal |
| `internal/insight/handler.go` | Range message and stats handler comment |
| `internal/insight/worker.go` | `StatsWorker`, its ports, and the sweep |
| `internal/compose/stats_worker.go` | Account id and refreshing adapters |
| `internal/compose/build.go` | Build and expose `StatsWorker` |
| `cmd/app-serve/main.go` | Start and join the worker |
| `docs/DECISIONS/003-VIDEO-STATS-RETENTION-WORKER.md` | The decision record |
| `SYSTEM_MAP.md`, `docs/SPEC/001`, `docs/SPEC/003`, `docs/DECISIONS/002`, `docs/CHANGELOG/001-V1-BACKEND.md`, `.env.example` | Governance updates |

---

### Task 1: Config bounds for cadence and retention

**Files:**
- Modify: `app-serve/internal/config/config.go`
- Modify: `app-serve/internal/config/wiring_test.go`
- Modify: `app-serve/.env.example`
- Modify: `docs/SPEC/001-BACKEND-ARCHITECTURE.md`

**Interfaces:**
- Consumes: `durationOr` from `internal/config/env.go`.
- Produces: `Config.StatsRefreshInterval time.Duration`, `Config.StatsRetention time.Duration`.

- [x] **Step 1: Write the failing tests**

Add to the `cases` table in `TestLoadWiringFields`:

```go
{
	name:   "stats worker bounds fall back to the documented defaults",
	mutate: func(m map[string]string) {
		delete(m, "STATS_REFRESH_INTERVAL")
		delete(m, "STATS_RETENTION")
	},
	check: func(t *testing.T, cfg config.Config) {
		if cfg.StatsRefreshInterval != 3*time.Hour {
			t.Errorf("StatsRefreshInterval = %v, want 3h", cfg.StatsRefreshInterval)
		}
		if cfg.StatsRetention != 168*time.Hour {
			t.Errorf("StatsRetention = %v, want 168h", cfg.StatsRetention)
		}
	},
},
{
	name:    "malformed STATS_REFRESH_INTERVAL fails fast",
	mutate:  func(m map[string]string) { m["STATS_REFRESH_INTERVAL"] = "soon" },
	wantErr: true,
},
{
	name:    "a zero STATS_RETENTION fails fast",
	mutate:  func(m map[string]string) { m["STATS_RETENTION"] = "0" },
	wantErr: true,
},
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `cd app-serve && go test -count=1 ./internal/config/ -run TestLoadWiringFields -v`
Expected: FAIL, `cfg.StatsRefreshInterval` is undefined.

- [x] **Step 3: Implement the fields**

In `internal/config/config.go`, add two defaults next to the other default constants:

```go
// defaultStatsRefreshInterval and defaultStatsRetention bound the stats
// worker: how often it sweeps, and how old a point may be before it is pruned.
defaultStatsRefreshInterval = 3 * time.Hour
defaultStatsRetention        = 168 * time.Hour
```

Add to the `Config` struct next to the webhook bounds:

```go
StatsRefreshInterval time.Duration `validate:"gt=0"`
StatsRetention       time.Duration `validate:"gt=0"`
```

Parse in `Load` next to `webhookThrottleWnd`:

```go
statsInterval, err := durationOr("STATS_REFRESH_INTERVAL", defaultStatsRefreshInterval)
if err != nil {
	return Config{}, err
}
statsRetention, err := durationOr("STATS_RETENTION", defaultStatsRetention)
if err != nil {
	return Config{}, err
}
```

Add `StatsRefreshInterval: statsInterval,` and `StatsRetention: statsRetention,` to the `Config{...}` literal.

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd app-serve && go test -count=1 ./internal/config/ -run TestLoadWiringFields -v`
Expected: PASS.

- [x] **Step 5: Update `.env.example` and SPEC 001 section 11**

In `.env.example`, after the `WORKER_SIZE` block, append:

```
# Stats worker: sweep cadence and the 7 day retention window
STATS_REFRESH_INTERVAL=
STATS_RETENTION=
```

In `docs/SPEC/001-BACKEND-ARCHITECTURE.md` section 11, add the two rows to the table:

```
| `STATS_REFRESH_INTERVAL` | Stats worker sweep cadence, default `3h` |
| `STATS_RETENTION` | Stats point age cutoff, default `168h` |
```

- [x] **Step 6: Commit**

```bash
git add app-serve/internal/config/config.go app-serve/internal/config/wiring_test.go app-serve/.env.example docs/SPEC/001-BACKEND-ARCHITECTURE.md
git commit -m "feat(app-serve): add stats worker cadence and retention config"
```

---

### Task 2: Repository pruning and the store port

**Files:**
- Modify: `app-serve/internal/insight/repository.go`
- Modify: `app-serve/internal/insight/stats.go` (the `SnapshotStore` interface)
- Modify: `app-serve/internal/insight/repository_test.go`
- Modify: `app-serve/internal/insight/fakes_test.go`

**Interfaces:**
- Consumes: `Repository.pool`, the `video_stats` table.
- Produces: `func (r *Repository) DeleteStatsBefore(ctx context.Context, cutoff time.Time) (int64, error)`, and the same method on the `SnapshotStore` interface.

- [x] **Step 1: Write the failing integration test**

Add to `internal/insight/repository_test.go` (file already has `//go:build integration`):

```go
func TestDeleteStatsBeforeRemovesEveryVideoOlderThanCutoff(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	now := time.Now().UTC()
	oldID := "video-" + uuid.NewString()
	freshID := "video-" + uuid.NewString()
	if _, err := repo.InsertStats(ctx, StatSnapshot{VideoID: oldID, ViewCount: intPtr(7), FetchedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	if _, err := repo.InsertStats(ctx, StatSnapshot{VideoID: freshID, ViewCount: intPtr(8), FetchedAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("insert fresh: %v", err)
	}
	removed, err := repo.DeleteStatsBefore(ctx, now)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if removed < 1 {
		t.Fatalf("removed = %d, want at least the old row", removed)
	}
	oldPoints, _, err := repo.ListStats(ctx, oldID, time.Time{}, 10)
	if err != nil {
		t.Fatalf("list old: %v", err)
	}
	if len(oldPoints) != 0 {
		t.Fatalf("old points = %d, want 0", len(oldPoints))
	}
	freshPoints, _, err := repo.ListStats(ctx, freshID, time.Time{}, 10)
	if err != nil {
		t.Fatalf("list fresh: %v", err)
	}
	if len(freshPoints) != 1 {
		t.Fatalf("fresh points = %d, want 1", len(freshPoints))
	}
}
```

- [x] **Step 2: Run the test to verify it fails**

Run: `cd app-serve && GENAUTO_TEST_POSTGRES_DSN="postgres://rusmanadodi@/genauto_test?host=/var/run/postgresql" go test -count=1 -tags=integration ./internal/insight/ -run TestDeleteStatsBefore -v`
Expected: FAIL, `repo.DeleteStatsBefore` is undefined.

- [x] **Step 3: Implement the prune**

Add to `internal/insight/repository.go`, after `ListStats`:

```go
// DeleteStatsBefore prunes every point fetched before the cutoff and returns
// how many rows left. The age bound is global even though the series window is
// per video: retention is a property of the table, not of one chart.
func (r *Repository) DeleteStatsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM video_stats WHERE fetched_at < $1`
	tag, err := r.pool.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("insight: pruning video stats: %w", err)
	}
	return tag.RowsAffected(), nil
}
```

Add to the `SnapshotStore` interface in `internal/insight/stats.go`:

```go
DeleteStatsBefore(ctx context.Context, cutoff time.Time) (int64, error)
```

- [x] **Step 4: Add the fake and run the test to verify it passes**

Add to `internal/insight/fakes_test.go`:

```go
// DeleteStatsBefore drops every stored point older than the cutoff.
func (f *fakeStore) DeleteStatsBefore(_ context.Context, cutoff time.Time) (int64, error) {
	kept := f.stats[:0]
	var removed int64
	for _, snap := range f.stats {
		if snap.FetchedAt.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, snap)
	}
	f.stats = kept
	return removed, nil
}
```

Run: `cd app-serve && GENAUTO_TEST_POSTGRES_DSN="postgres://rusmanadodi@/genauto_test?host=/var/run/postgresql" go test -count=1 -tags=integration ./internal/insight/ -run TestDeleteStatsBefore -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add app-serve/internal/insight/repository.go app-serve/internal/insight/repository_test.go app-serve/internal/insight/stats.go app-serve/internal/insight/fakes_test.go
git commit -m "feat(app-serve): add video stats pruning to the insight store"
```

---

### Task 3: Range enum shrinks to 7d

**Files:**
- Modify: `app-serve/internal/insight/schema.go`
- Modify: `app-serve/internal/insight/handler.go`
- Modify: `app-serve/internal/insight/stats.go` (the validation detail)
- Modify: `app-serve/internal/insight/stats_series_test.go`
- Modify: `app-serve/internal/insight/stats_route_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `StatRange7d` as the only `StatRange`; `parseRange(raw string) (StatRange, error)` defaults to `StatRange7d` and rejects every other value.

- [x] **Step 1: Write the failing tests**

In `stats_series_test.go`, replace `TestRangeParsingAcceptsTheClosedSet` with:

```go
func TestRangeParsingAcceptsOnlySevenDays(t *testing.T) {
	for raw, want := range map[string]StatRange{"": StatRange7d, "7d": StatRange7d} {
		got, err := parseRange(raw)
		if err != nil || got != want {
			t.Errorf("parseRange(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"30d", "90d", "1y"} {
		if _, err := parseRange(raw); err == nil {
			t.Errorf("parseRange(%q) accepted, want rejection", raw)
		}
	}
}
```

In `stats_route_test.go`, change `TestStatsRouteAnswersTheSeries` to expect `data["range"] == "7d"`.

- [x] **Step 2: Run the tests to verify they fail**

Run: `cd app-serve && go test -count=1 ./internal/insight/ -run TestRangeParsing -v`
Expected: FAIL, `30d` is still accepted.

- [x] **Step 3: Implement the shrink**

In `internal/insight/schema.go`:

```go
// Supported stats windows. Retention is 7 days, so 7d is the only window that
// can hold a full series; a longer range is a contract change, not a value.
const (
	StatRange7d StatRange = "7d"
)

// statRangeDays widths one range to the number of days the series covers.
var statRangeDays = map[StatRange]int{
	StatRange7d: 7,
}
```

In `parseRange`, return `StatRange7d` for `""`, and keep the map lookup as the rejection.

In `internal/insight/handler.go`, change the validation detail to `"Stats range must be 7d."` and the `Stats` doc comment to say the series is served from stored points.

In `internal/insight/stats.go`, change the `StatsSeries` validation detail to `"Stats range must be 7d."`.

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd app-serve && go test -race -count=1 ./internal/insight/`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add app-serve/internal/insight/schema.go app-serve/internal/insight/handler.go app-serve/internal/insight/stats.go app-serve/internal/insight/stats_series_test.go app-serve/internal/insight/stats_route_test.go
git commit -m "feat(app-serve): close the stats range to the 7 day window"
```

---

### Task 4: Read path is read-only with one first-sighting fetch

**Files:**
- Modify: `app-serve/internal/insight/stats.go`
- Modify: `app-serve/internal/insight/service.go`
- Modify: `app-serve/internal/insight/repository.go`
- Modify: `app-serve/internal/insight/service_test.go`
- Modify: `app-serve/internal/insight/stats_series_test.go`
- Modify: `app-serve/internal/insight/stats_route_test.go`
- Modify: `app-serve/internal/insight/fakes_test.go`
- Modify: `app-serve/internal/insight/repository_test.go`
- Modify: `app-serve/internal/insight/handler_test.go`

**Interfaces:**
- Consumes: `Store.ListStats`, `Service.fetchStats`, `Service.access`.
- Produces: `StatsSeries` performs no upstream call when the video has stored points; `Service.VideoStats`, `Service.Config.StatsTTL`, `SnapshotStore.LatestStats`, and `Repository.LatestStats` are removed.

- [x] **Step 1: Write the failing tests**

Replace the four `TestVideoStats*` tests in `service_test.go` with series tests, and replace `TestStatsSeriesGivenStaleLatestPoint_ThenExactlyOneRefetch` in `stats_series_test.go`:

```go
func TestStatsSeriesGivenStoredPoints_ThenNoRefetch(t *testing.T) {
	h := newServiceHarness(t)
	h.store.stats = []StatSnapshot{{
		VideoID: testVideo, ViewCount: count(int64(5)),
		FetchedAt: time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC),
	}}
	series, err := h.svc.StatsSeries(context.Background(), testOperator, testAccount, testVideo, StatRange7d)
	if err != nil {
		t.Fatalf("StatsSeries: %v", err)
	}
	if len(series.Points) != 1 || *series.Points[0].ViewCount != 5 {
		t.Fatalf("points = %+v", series.Points)
	}
	if h.store.writes != 0 {
		t.Fatalf("writes = %d, want no refetch", h.store.writes)
	}
	if len(h.tiktok.queryIDs) != 0 {
		t.Fatalf("upstream called with %v, want none", h.tiktok.queryIDs)
	}
}

func TestStatsSeriesGivenNoPoints_ThenOneFetchAndTheNextReadDoesNotRefetch(t *testing.T) {
	h := newServiceHarness(t)
	h.tiktok.videos = []tiktokclient.Video{{ID: testVideo, ViewCount: count(int64(9))}}
	first, err := h.svc.StatsSeries(context.Background(), testOperator, testAccount, testVideo, StatRange7d)
	if err != nil {
		t.Fatalf("first StatsSeries: %v", err)
	}
	if len(first.Points) != 1 || *first.Points[0].ViewCount != 9 || h.store.writes != 1 {
		t.Fatalf("first = %+v writes = %d", first.Points, h.store.writes)
	}
	second, err := h.svc.StatsSeries(context.Background(), testOperator, testAccount, testVideo, StatRange7d)
	if err != nil {
		t.Fatalf("second StatsSeries: %v", err)
	}
	if h.store.writes != 1 {
		t.Fatalf("writes = %d after the second read, want 1", h.store.writes)
	}
	if len(second.Points) != 1 {
		t.Fatalf("second = %+v", second.Points)
	}
}

func TestStatsSeriesGivenNoPointsAndUpstreamError_ThenErrorPropagates(t *testing.T) {
	h := newServiceHarness(t)
	h.tiktok.err = apierror.New(apierror.CodeTikTokUnavailable, 0, "TikTok is unavailable.")
	if _, err := h.svc.StatsSeries(context.Background(), testOperator, testAccount, testVideo, StatRange7d); err == nil {
		t.Fatal("expected an error")
	}
	if h.store.writes != 0 {
		t.Fatalf("writes = %d", h.store.writes)
	}
}
```

In `stats_route_test.go`, keep `TestStatsRouteAnswersTheSeries` as the first-sighting path (no stored point, one fetch, `writes == 1`), and change `TestStatsRouteKeepsStoredHistoryBehindTheFreshestPoint` to `TestStatsRouteServesStoredHistoryWithoutRefetch`, asserting `h.store.writes == 0` and `len(points) == 2`.

- [x] **Step 2: Run the tests to verify they fail**

Run: `cd app-serve && go test -count=1 ./internal/insight/ -run TestStatsSeriesGivenStoredPoints -v`
Expected: FAIL, the read still refetches.

- [x] **Step 3: Implement the read-only series**

In `internal/insight/stats.go`, replace `VideoStats` and the body of `StatsSeries`:

```go
// StatsSeries reads the stored history for one window. The worker is the only
// steady writer, so a read spends no upstream quota. A video with no stored
// point at all gets one fetch and append, which is a first fetch rather than a
// refetch, so the chart is never blank while a brand-new video waits for the
// next tick. History is never refetched, only appended to.
func (s *Service) StatsSeries(ctx context.Context, operatorID, accountID, videoID string, window StatRange) (StatSeries, error) {
	access, err := s.access(ctx, operatorID, accountID)
	if err != nil {
		return StatSeries{}, err
	}
	days, ok := statRangeDays[window]
	if !ok {
		return StatSeries{}, apierror.New(apierror.CodeValidationFailed, 0,
			"Stats range must be 7d.")
	}
	since := s.cfg.Now().AddDate(0, 0, -days)
	points, truncated, err := s.cfg.Store.ListStats(ctx, videoID, since, maxSeriesPoints)
	if err != nil {
		return StatSeries{}, err
	}
	if len(points) == 0 {
		snap, err := s.fetchStats(ctx, access, videoID)
		if err != nil {
			return StatSeries{}, err
		}
		points = append(points, snap)
	}
	return StatSeries{VideoID: videoID, Range: window, Points: points, Truncated: truncated}, nil
}
```

Remove `LatestStats` from the `SnapshotStore` interface.

In `internal/insight/service.go`, remove `defaultStatsTTL`, the `StatsTTL` field, and its defaulting block in `NewService`.

In `internal/insight/repository.go`, remove `LatestStats`.

In `internal/insight/fakes_test.go`, remove `fakeStore.LatestStats`.

In `internal/insight/handler_test.go` and `service_test.go`, remove every `StatsTTL: time.Minute,` line.

In `internal/insight/repository_test.go`, delete `TestStatsNilStaysNil`, `TestStatsNewerFetchedAtWins`, `TestStatsZeroReadsBackNonNil`, and `TestStatsLatestUnknownVideoIsNotFound`, and add one `ListStats` test that pins window, ascending order, and nil versus zero:

```go
func TestStatsListReturnsTheWindowAscendingWithNilAndZeroKept(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool(t))
	videoID := "video-" + uuid.NewString()
	now := time.Now().UTC()
	zero := int64(0)
	if _, err := repo.InsertStats(ctx, StatSnapshot{VideoID: videoID, LikeCount: &zero, FetchedAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatalf("insert zero: %v", err)
	}
	if _, err := repo.InsertStats(ctx, StatSnapshot{VideoID: videoID, FetchedAt: now}); err != nil {
		t.Fatalf("insert nil: %v", err)
	}
	points, truncated, err := repo.ListStats(ctx, videoID, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if truncated || len(points) != 1 {
		t.Fatalf("points = %d truncated = %v", len(points), truncated)
	}
	if points[0].LikeCount != nil {
		t.Fatalf("like count = %v, want absent", points[0].LikeCount)
	}
	all, _, err := repo.ListStats(ctx, videoID, now.Add(-3*time.Hour), 10)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 || all[0].LikeCount == nil || *all[0].LikeCount != 0 {
		t.Fatalf("all = %+v, want the zero point first and non-nil", all)
	}
}
```

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd app-serve && go test -race -count=1 ./internal/insight/`
Run: `cd app-serve && GENAUTO_TEST_POSTGRES_DSN="postgres://rusmanadodi@/genauto_test?host=/var/run/postgresql" go test -count=1 -tags=integration ./internal/insight/ -run TestStats`
Expected: PASS on both.

- [x] **Step 5: Commit**

```bash
git add app-serve/internal/insight
git commit -m "feat(app-serve): serve stats from stored points with a first-sighting fetch"
```

---

### Task 5: The stats sweep worker

**Files:**
- Create: `app-serve/internal/insight/worker.go`
- Create: `app-serve/internal/insight/worker_test.go`

**Interfaces:**
- Consumes: `VideoClient`, `AccountResolver`, `SnapshotStore` (with `DeleteStatsBefore`), `videoQueryFields`, `snapshotFromVideo`, `maxQueryIDs`.
- Produces:

```go
type TokenRefresher interface {
	Refresh(ctx context.Context, operatorID, accountID string) error
}
type AccountLister interface {
	List(ctx context.Context, operatorID string) ([]string, error)
}
type RateGate interface {
	Allow(ctx context.Context, accountID string) error
}
type StatsWorkerConfig struct {
	OperatorID string
	Videos     VideoClient
	Accounts   AccountResolver
	Refresher  TokenRefresher
	Lister     AccountLister
	Gate       RateGate
	Store      SnapshotStore
	Interval   time.Duration
	Retention  time.Duration
	Batch      int
	Now        func() time.Time
	Logger     *slog.Logger
}
func NewStatsWorker(cfg StatsWorkerConfig) *StatsWorker
func (w *StatsWorker) Run(ctx context.Context)
func (w *StatsWorker) SweepOnce(ctx context.Context) error
```

- [x] **Step 1: Write the failing tests**

Create `internal/insight/worker_test.go` with the fakes and these tests:

```go
func TestSweepOnceGivenTwoAccounts_ThenEachIsRefreshedAndAppended(t *testing.T) {
	// lister returns {"a1","a2"}; each account lists one video; store gets one
	// InsertStats per video and one DeleteStatsBefore call.
	// assert refresher.calls == {"a1","a2"}, store.writes == 2, pruneCutoff == now-168h
}

func TestSweepOnceGivenOneAccountRefreshFails_ThenOtherAccountsStillSweep(t *testing.T) {
	// refresher errors for "a1" only; assert "a2" still appended (store.writes == 1)
}

func TestSweepOnceGivenListBudgetSpent_ThenPartialIDsAreAppended(t *testing.T) {
	// gate denies on the second Allow; the first page's ids are still queried
	// and appended, and the sweep returns nil
}

func TestSweepOnceGivenMoreThanTwentyIDs_ThenQueriedInBatches(t *testing.T) {
	// 25 ids asserted as calls of sizes 20 and 5
}

func TestSweepOnceGivenPagedCatalog_ThenEveryPageIsWalked(t *testing.T) {
	// two pages, has_more true then false; assert 2 list calls and both ids
}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `cd app-serve && go test -count=1 ./internal/insight/ -run TestSweepOnce -v`
Expected: FAIL, `StatsWorker` is undefined.

- [x] **Step 3: Implement `StatsWorker`**

Create `internal/insight/worker.go` with the `AGENTS.md` 1.2 header at `@layer worker`, `@file internal/insight/worker.go`. The body:

```go
// defaults bound one sweep when a field is zero.
const (
	defaultStatsWorkerInterval  = 3 * time.Hour
	defaultStatsWorkerRetention = 168 * time.Hour
)

type StatsWorker struct {
	cfg StatsWorkerConfig
}

func NewStatsWorker(cfg StatsWorkerConfig) *StatsWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultStatsWorkerInterval
	}
	if cfg.Retention <= 0 {
		cfg.Retention = defaultStatsWorkerRetention
	}
	if cfg.Batch <= 0 || cfg.Batch > maxQueryIDs {
		cfg.Batch = maxQueryIDs
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &StatsWorker{cfg: cfg}
}

// Run sweeps on the configured interval until ctx is done.
func (w *StatsWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.SweepOnce(ctx); err != nil {
				w.cfg.Logger.WarnContext(ctx, "stats sweep failed", "error", err.Error())
			}
		}
	}
}

// SweepOnce walks every connected account and prunes old points. A per-account
// failure is logged and skipped so one bad token cannot stall the rest.
func (w *StatsWorker) SweepOnce(ctx context.Context) error {
	accounts, err := w.cfg.Lister.List(ctx, w.cfg.OperatorID)
	if err != nil {
		return fmt.Errorf("insight: listing accounts for the stats sweep: %w", err)
	}
	for _, accountID := range accounts {
		if err := ctx.Err(); err != nil {
			return err
		}
		w.sweepAccount(ctx, accountID)
	}
	cutoff := w.cfg.Now().Add(-w.cfg.Retention)
	if _, err := w.cfg.Store.DeleteStatsBefore(ctx, cutoff); err != nil {
		return fmt.Errorf("insight: pruning video stats: %w", err)
	}
	return nil
}

// sweepAccount refreshes one token, lists its newest videos, and appends a
// point per video. A spent list budget ends this account early and resumes on
// the next tick.
func (w *StatsWorker) sweepAccount(ctx context.Context, accountID string) {
	log := w.cfg.Logger.With("account_id", accountID)
	if err := w.cfg.Refresher.Refresh(ctx, w.cfg.OperatorID, accountID); err != nil {
		log.WarnContext(ctx, "refreshing the account token, skipping account", "error", err.Error())
		return
	}
	access, err := w.cfg.Accounts.Owned(ctx, w.cfg.OperatorID, accountID)
	if err != nil {
		log.WarnContext(ctx, "resolving the access token, skipping account", "error", err.Error())
		return
	}
	ids := w.listVideoIDs(ctx, accountID, access, log)
	if len(ids) > 0 {
		w.appendStats(ctx, access, ids, log)
	}
}

// listVideoIDs pages the newest videos until the catalog ends or the shared
// list budget is spent. A spent budget returns what it already listed.
func (w *StatsWorker) listVideoIDs(ctx context.Context, accountID, access string, log *slog.Logger) []string {
	var ids []string
	cursor := ""
	for {
		if err := w.cfg.Gate.Allow(ctx, accountID); err != nil {
			log.WarnContext(ctx, "list budget spent, resuming on the next tick", "error", err.Error())
			return ids
		}
		page, err := w.cfg.Videos.ListVideos(ctx, access, cursor, 0)
		if err != nil {
			log.WarnContext(ctx, "listing videos", "error", err.Error())
			return ids
		}
		for _, video := range page.Videos {
			ids = append(ids, video.ID)
		}
		if !page.HasMore || page.Cursor == 0 {
			return ids
		}
		cursor = strconv.FormatInt(page.Cursor, 10)
	}
}

// appendStats queries the collected ids in batches and appends one point per
// video. A failed batch is logged and skipped, so one bad id cannot drop the rest.
func (w *StatsWorker) appendStats(ctx context.Context, access string, ids []string, log *slog.Logger) {
	for start := 0; start < len(ids); start += w.cfg.Batch {
		end := min(start+w.cfg.Batch, len(ids))
		videos, err := w.cfg.Videos.QueryVideos(ctx, access, ids[start:end], videoQueryFields)
		if err != nil {
			log.WarnContext(ctx, "querying video stats", "error", err.Error())
			continue
		}
		for _, video := range videos {
			if _, err := w.cfg.Store.InsertStats(ctx, snapshotFromVideo(video, w.cfg.Now())); err != nil {
				log.WarnContext(ctx, "appending video stats", "video_id", video.ID, "error", err.Error())
			}
		}
	}
}
```

If the file passes 220 lines, move `listVideoIDs` and `appendStats` into `internal/insight/worker_sweep.go`.

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd app-serve && go test -race -count=1 ./internal/insight/ -run TestSweepOnce -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add app-serve/internal/insight/worker.go app-serve/internal/insight/worker_sweep.go app-serve/internal/insight/worker_test.go
git commit -m "feat(app-serve): add the video stats sweep worker"
```

---

### Task 6: Wire the worker into the composition root

**Files:**
- Create: `app-serve/internal/compose/stats_worker.go`
- Create: `app-serve/internal/compose/stats_worker_test.go`
- Modify: `app-serve/internal/compose/build.go`
- Modify: `app-serve/internal/compose/build_test.go`
- Modify: `cmd/app-serve/main.go`

**Interfaces:**
- Consumes: `insight.NewStatsWorker`, `insight.StatsWorkerConfig`, `auth.Service.Refresh`, `account.Service.List`, `account.Service.Owned`, `tiktokclient.Limiter`, `newGateAdapter`.
- Produces: `App.StatsWorker *insight.StatsWorker`, and `accountIDs` as the `insight.AccountLister` adapter.

- [x] **Step 1: Write the failing test**

In `internal/compose/build_test.go`, extend `TestBuildMountsAQueryableHealthProbe`:

```go
if app.StatsWorker == nil {
	t.Fatal("App.StatsWorker is nil, the stats sweep is not wired")
}
```

Create `internal/compose/stats_worker_test.go`:

```go
type fakeAccountLister struct{ views []account.View }

func (f fakeAccountLister) List(context.Context, string) ([]account.View, error) {
	return f.views, nil
}

func TestAccountIDsMapsTheOwnedAccountsInOrder(t *testing.T) {
	ids, err := accountIDs{svc: fakeAccountLister{views: []account.View{{ID: "a1"}, {ID: "a2"}}}}.List(context.Background(), "op")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 2 || ids[0] != "a1" || ids[1] != "a2" {
		t.Fatalf("ids = %v", ids)
	}
}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `cd app-serve && go test -count=1 ./internal/compose/ -run TestBuildMountsAQueryableHealthProbe -v`
Expected: FAIL, `App.StatsWorker` is undefined.

- [x] **Step 3: Implement the adapters and the wiring**

Create `internal/compose/stats_worker.go` with the `AGENTS.md` 1.2 header at `@layer service`:

```go
// accountLister is the account service slice the stats sweep reads.
type accountLister interface {
	List(ctx context.Context, operatorID string) ([]account.View, error)
}

// accountIDs adapts the account service to the worker's id lister.
type accountIDs struct {
	svc accountLister
}

// List returns the connected account ids in creation order.
func (a accountIDs) List(ctx context.Context, operatorID string) ([]string, error) {
	views, err := a.svc.List(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.ID)
	}
	return ids, nil
}
```

In `internal/compose/build.go`:

- Add `StatsWorker *insight.StatsWorker` to the `App` struct.
- Reuse one repository: `insightRepo := insight.NewRepository(pool)`, pass it to `insight.NewService`, and reuse it for the worker.
- After the reconciler wiring, build the worker:

```go
statsWorker := insight.NewStatsWorker(insight.StatsWorkerConfig{
	OperatorID: cfg.OperatorID,
	Videos:     tiktok,
	Accounts:   accounts,
	Refresher:  authSvc,
	Lister:     accountIDs{svc: accounts},
	Gate:       newGateAdapter(limiter, tiktokclient.FamilyListVideos),
	Store:      insightRepo,
	Interval:   cfg.StatsRefreshInterval,
	Retention:  cfg.StatsRetention,
	Logger:     logger,
})
```

- Add `StatsWorker: statsWorker,` to the returned `&App{...}`.

In `cmd/app-serve/main.go`, add the fourth worker:

```go
workers.Add(4)
go supervised(&workers, logger, "stats sweep", func() {
	defer workers.Done()
	app.StatsWorker.Run(work)
})
```

- [x] **Step 4: Run the tests to verify they pass**

Run: `cd app-serve && go test -race -count=1 ./internal/compose/`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add app-serve/internal/compose/stats_worker.go app-serve/internal/compose/stats_worker_test.go app-serve/internal/compose/build.go app-serve/internal/compose/build_test.go cmd/app-serve/main.go
git commit -m "feat(app-serve): start the stats sweep in the composition root"
```

---

### Task 7: Governance documents

**Files:**
- Create: `docs/DECISIONS/003-VIDEO-STATS-RETENTION-WORKER.md`
- Modify: `docs/DECISIONS/002-UPLOAD-IDEMPOTENCY-R2-STATS-SERIES.md`
- Modify: `docs/SPEC/003-API-CONTRACT.md` (section 7.5)
- Modify: `SYSTEM_MAP.md`
- Modify: `docs/CHANGELOG/001-V1-BACKEND.md`

**Interfaces:**
- Consumes: the accepted design at `docs/superpowers/specs/2026-10-16-video-stats-retention-worker-design.md`.
- Produces: no code.

- [x] **Step 1: Write ADR 003**

Create `docs/DECISIONS/003-VIDEO-STATS-RETENTION-WORKER.md` recording decisions 1 to 10 from the design, with the positive and negative consequences and the confirmed token refresh.

- [x] **Step 2: Update the affected docs**

- `docs/DECISIONS/002`: mark decision 3's range as `7d` only, point open question 1 at ADR 003, and note the read path is now read-only.
- `docs/SPEC/003` section 7.5: change the enum to `7d`, the default to `7d`, and replace the refresh-on-read rule with first-sighting plus the worker.
- `SYSTEM_MAP.md`: add the stats sweep to the worker flow and the background jobs, and update the stats read description.
- `docs/CHANGELOG/001-V1-BACKEND.md`: add the retention and worker entry.

- [x] **Step 3: Verify the antislop comment and text gate**

Run: `bash scripts/gates/antislop.sh`
Expected: PASS.

- [x] **Step 4: Commit**

```bash
git add docs/DECISIONS docs/SPEC/003-API-CONTRACT.md SYSTEM_MAP.md docs/CHANGELOG/001-V1-BACKEND.md
git commit -m "docs: record the video stats retention and worker decision"
```

---

## Final verification

- [x] **Whole gate suite**

Run: `bash scripts/gates/all.sh`
Expected: all PASS.

- [x] **Tagged integration suite**

Run: `cd app-serve && GENAUTO_TEST_POSTGRES_DSN="postgres://rusmanadodi@/genauto_test?host=/var/run/postgresql" go test -race -p 1 -count=1 -tags=integration ./...`
Expected: all `ok`.
