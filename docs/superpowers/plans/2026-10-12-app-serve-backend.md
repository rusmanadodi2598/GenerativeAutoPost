# GenerativeAutoPost Backend (app-serve) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the app-serve backend: a Go modular monolith behind `/api/v1` that owns OAuth, the TikTok token vault, publishing, webhook-first status, stats, and a git-clean gate set.

**Architecture:** One binary (`cmd/app-serve`), one PostgreSQL 17 database, one Redis 7. Module boundaries are package boundaries: `config`, `platform/*`, `apierror`, `tiktokclient`, `auth`, `account`, `publish`, `webhook`, `insight`, `media`. Strict layering `schema -> domain -> repository -> service -> handler -> router`. Chunk uploads and upstream URLs stay server-side. Webhook is the primary publish-state signal; the status endpoint is a bounded reconciler only.

**Tech Stack:** Go 1.26 (toolchain go1.26.5), PostgreSQL 17, Redis 7, `go-playground/validator/v10`, Gin (data plane only), `net/http` for upstream, AES-256-GCM token encryption, golang-migrate, Testcontainers for integration tests, `staticcheck` + `golangci-lint` + `gitleaks` in gates.

**Spec:** `docs/SPEC/001-BACKEND-ARCHITECTURE.md`, `docs/SPEC/002-TIKTOK-INTEGRATION.md`, `docs/SPEC/003-API-CONTRACT.md`. Decision: `docs/DECISIONS/001-SCOPE-V1.md`. Rules: `AGENTS.md`, `docs/RULLES/TDD.md`, `docs/RULLES/OWASP.md`.

## Global Constraints

- Every hand-written `.go` file under `app-serve/internal/**` and `app-serve/cmd/**` stays under 250 lines; generated code (`*_gen.go`, `*.pb.go`) exempt.
- Every hand-written `.go` file carries the eight-tag header (`@file @for @uses @reason @author @layer @stability @since`), `@author` is exactly `Dodi Rusmana <rusmanadodi@kentangtech.com>`, `@layer` one of `schema domain repository service handler router worker job util config`.
- All client-facing error messages in English; internal logs may use Indonesian.
- `any`/`interface{}` forbidden in application code except narrow I/O boundaries, decode to concrete structs and validate immediately.
- Layer flow `schema -> domain -> repository -> service -> handler -> router`; `service` never imports `net/http`; `handler` holds no SQL and no Redis.
- `go vet` and `staticcheck` clean; every outbound call has an explicit `context` timeout; every goroutine recovers from panic and has a termination condition.
- All external input decodes into a typed `schema` struct and validates with `go-playground/validator/v10` before use.
- Every outbound call retries only on network errors, 429, 5xx with exponential backoff plus jitter and bounded attempts; 4xx never retries.
- Module path `github.com/kentangtech/GenerativeAutoPost/app-serve`.
- PostgreSQL 17 and Redis 7 are the target versions; local Docker Compose must pin them.
- Public domain `app-tiktok.kentangtech.com`; verified media prefix via `MEDIA_PUBLIC_BASE_URL`.
- Tests: table-driven, 3-5 variations per function (typical, boundary, empty, negative/extreme where applicable), plus one benign control for security tests; no `t.Skip()`; `go test -race ./...` required.
- Strict TDD per `docs/RULLES/TDD.md`: analysis first, implementation second, compliance self-check third, in every plan task that writes code.

## Review Focus

- `state` reuse race on the OAuth callback: two parallel callbacks with the same `state` must result in exactly one success and one rejection.
- Webhook signature with a subtly wrong body (trailing newline, re-ordered keys): must fail verification even though the JSON is semantically identical.
- `privacy_level` value that is valid JSON but not in the creator's current options: must be rejected with `PUBLISH_PRIVACY_LEVEL_MISMATCH`, not passed through to TikTok.
- Idempotency key reused with a different body on a publish POST: must return `409`, not the stored response and not a second publish.
- Chunk out of order or with a wrong range on `PUT /publish/{id}/chunks`: must be rejected before anything is forwarded upstream.

---

### Task 1: Repository root, git, ignore files

**Files:**
- Create: `.gitignore`, `.gitleaks.toml`, `README.md`
- Execute: `git init` (or verify existing), set `core.hooksPath` later in Task 4

**Interfaces:**
- Consumes: nothing
- Produces: a git repository whose root `scrypts/` helper can resolve via `git rev-parse --show-toplevel`

- [ ] **Step 1: Initialize git if absent**

Run:
```bash
cd /home/rusmanadodi/GenerativeAutoPost && git status >/dev/null 2>&1 || git init
git status --short
```
Expected: a clean status line set (possibly with untracked docs).

- [ ] **Step 2: Write `.gitignore`**

```gitignore
# binaries and build output
*.exe
*.out
/bin/
/dist/

# env and secrets (per subfolder, never committed)
app-serve/.env
app-ui/.env
*.env
!.env.example

# local tooling
/tmp/
*.log
.DS_Store

# Go
/vendor/

# Bun / Node
node_modules/
app-ui/dist/
app-ui/.bun/
```

- [ ] **Step 3: Write `.gitleaks.toml`**

```toml
title = "GenerativeAutoPost gitleaks config"

[extend]
useDefault = true

[allowlist]
description = "example files and docs must not trip the scanner"
paths = [
  '''\.example$''',
  '''docs/''',
  '''README\.md$''',
]
```

- [ ] **Step 4: Write root `README.md`**

Content: project name, one paragraph purpose, layout (`scripts/`, `migrations/`, `docs/`, `deployment/`, `app-serve/`, `app-ui/`), links to `docs/SPEC/`, `docs/DECISIONS/`, gates usage (`scripts/gates/all.sh`), webhook host placeholder.

- [ ] **Step 5: Commit**

```bash
git add .gitignore .gitleaks.toml README.md
git commit -m "chore: repository root, ignore rules, gitleaks config"
```

---

### Task 2: Gate scripts and git hooks

**Files:**
- Create: `scripts/lib/common.sh`, `scripts/gates/go-lint.sh`, `scripts/gates/go-test.sh`, `scripts/gates/go-headers.sh`, `scripts/gates/secrets.sh`, `scripts/gates/antislop.sh`, `scripts/gates/all.sh`, `scripts/hooks/pre-commit`, `scripts/hooks/pre-push`, `scripts/hooks/install.sh`
- Test: `scripts/gates/self-test.sh` (a runner that injects a bad file into a temp worktree and asserts each gate fails)

**Interfaces:**
- Consumes: repo root resolvable via git
- Produces: `scripts/gates/all.sh` exit 0 on clean tree, non-zero on violation; `core.hooksPath=scripts/hooks`

Notes: adapt from the `pannelAI/scrypts` pattern, renamed to `scripts/` per the user brief. Env prefix for this repo is `GENAUTO_` (not `PANNELAI_`). No Svelte/panel gates yet: `all.sh` runs go gates + headers + antislop + secrets; frontend gates are added when app-ui exists. The Review Focus line about `state` reuse will get its test in Task 9.

- [ ] **Step 1: Write `scripts/lib/common.sh`**

Content: `repo_root()` via `git rev-parse --show-toplevel`; colour helpers (`gate_start`, `gate_pass`, `gate_fail`, `gate_skip`); `have`, `require_tool`, `tool_path`; `go_service_dirs()` finding `app-*/go.mod` at maxdepth 2; `changed_files()` and `change_set()` with the `GATES_BASE_REF` guard that fails when the base does not resolve (a gate that inspects nothing is not a pass).

- [ ] **Step 2: Write `scripts/gates/go-lint.sh`**

Content: for each dir from `go_service_dirs()`: `go vet ./...`, `go vet -tags=integration ./...`, `gofmt -l`, `staticcheck ./...` (required, fail if missing), `golangci-lint run ./...` (skip with warning if missing). Line-limit check on changed files matching `app-*/internal/*.go`, `app-*/internal/*/*.go`, `app-*/cmd/*.go`, `app-*/cmd/*/*.go`, excluding `*_gen.go`, `*.pb.go`, vendor: fail over 250 lines, warn over 220.

- [ ] **Step 3: Write `scripts/gates/go-test.sh`**

Content: for each service dir: `go test -race -count=1 ./...`. If `GENAUTO_TEST_POSTGRES_DSN` is set, also run `go test -race -p 1 -tags=integration -count=1 ./...`; else skip with a message naming the variable. `GENAUTO_TEST_REDIS_ADDR` required by tagged tests.

- [ ] **Step 4: Write `scripts/gates/go-headers.sh`**

Content: check the eight tags exactly once each in every `app-*/internal/**` and `app-*/cmd/**` `.go` file; `@file` must equal the path relative to the module root containing `go.mod`; `@author` exactly `Dodi Rusmana <rusmanadodi@kentangtech.com>`; `@layer` and `@stability` from the enumerated sets; generated files exempt. Mode default: changed files; `--all` checks the whole tree.

- [ ] **Step 5: Write `scripts/gates/secrets.sh`**

Content: mode `--staged` runs `gitleaks git --staged`; mode `--all` runs `gitleaks git` plus `gitleaks dir` on tracked and untracked-but-not-ignored files. Both read the repo `.gitleaks.toml`. Fail when `gitleaks` is missing.

- [ ] **Step 6: Write `scripts/gates/antislop.sh`**

Content: fail on em dash (`—`) in hand-written Go comments; fail on a header region (`@file`..`@since`) with a wrapped continuation line; fail on comment citations matching `draft [0-9]+` or `audit [0-9]+` below the `package` line (header region exempt, string literals stripped first). Scope: changed Go files by default, `--all` for the tree.

- [ ] **Step 7: Write `scripts/gates/all.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/../lib/common.sh"
root="$(repo_root)"
failed=0
declare -a summary=()
run_gate() {
  local label="$1"; shift
  gate_start "$label"
  if "$@"; then gate_pass "$label"; summary+=("PASS  $label")
  else gate_fail "$label"; summary+=("FAIL  $label"); failed=1; fi
}
run_gate "go lint (vet, gofmt, staticcheck, line limits)" bash "$root/scripts/gates/go-lint.sh"
run_gate "go headers (AGENTS.md 1.2)" bash "$root/scripts/gates/go-headers.sh"
run_gate "antislop comment rules" bash "$root/scripts/gates/antislop.sh"
run_gate "go test (race)" bash "$root/scripts/gates/go-test.sh"
run_gate "secrets (gitleaks)" bash "$root/scripts/gates/secrets.sh" --all
printf '\n==> summary\n'; printf '%s\n' "${summary[@]}"
[ "$failed" -eq 0 ]
```

- [ ] **Step 8: Write hooks `pre-commit`, `pre-push`, `install.sh`**

`pre-commit`: gofmt on staged Go files, `go-headers.sh` on staged files, `antislop.sh`, `secrets.sh --staged`. `pre-push`: full `all.sh` with `GATES_BASE_REF` set to the push base. `install.sh`: sets `core.hooksPath=scripts/hooks`, verifies each hook exists and is executable.

- [ ] **Step 9: Write the failing self-test first**

Create `scripts/gates/self-test.sh`: builds a temp copy of the tree, injects (a) a Go file over 250 lines, (b) a header missing `@reason`, (c) a comment with an em dash, (d) a fake secret `TIKTOK_CLIENT_SECRET = "x" * 40` in a staged file, and asserts the matching gate fails. Run it: expect FAIL because the gates do not exist yet.

- [ ] **Step 10: Implement gates, then run**

```bash
chmod +x scripts/gates/*.sh scripts/hooks/* scripts/gates/self-test.sh
bash scripts/hooks/install.sh
bash scripts/gates/all.sh
```
Expected: PASS on the current tree (docs only, no Go module yet: go gates SKIP with "no Go module found").

- [ ] **Step 11: Commit**

```bash
git add scripts/ .gitignore
git commit -m "chore: gates and git hooks per AGENTS.md"
```

---

### Task 3: app-serve scaffold, config, platform, errors

**Files:**
- Create: `app-serve/go.mod`, `app-serve/.env.example`, `app-serve/cmd/app-serve/main.go`, `app-serve/internal/config/config.go`, `app-serve/internal/platform/postgres/postgres.go`, `app-serve/internal/platform/redisx/redisx.go`, `app-serve/internal/platform/logging/logging.go`, `app-serve/internal/platform/crypto/crypto.go`, `app-serve/internal/platform/httpapi/httpapi.go`, `app-serve/internal/apierror/apierror.go`
- Test: `app-serve/internal/config/config_test.go`, `app-serve/internal/platform/crypto/crypto_test.go`, `app-serve/internal/apierror/apierror_test.go`

**Interfaces:**
- Consumes: `GENAUTO_*` env names (Task 2)
- Produces:
  - `config.Load() (Config, error)` with all Section 11 variables from 001, validated at boot, fail fast
  - `crypto.Keyring.Encrypt(ctx, plaintext []byte, keyID string) (ciphertext []byte, keyID string, error)` and matching `Decrypt`
  - `crypto.VerifyHMACSHA256(key, message []byte, expectedHex string) bool` (constant time)
  - `apierror.New(code Code, status int, detail string) *Error`, `Write(w, r, err)` producing RFC 9457 with `debug` only when `APP_ENV=development`, secret redaction pass
  - `httpapi.Server` with request-id middleware, structured slog, panic recovery at the HTTP boundary

Module path: `module github.com/kentangtech/GenerativeAutoPost/app-serve`, `go 1.26`, `toolchain go1.26.5`. Dependencies allowed in this task: `go-playground/validator/v10`, `github.com/jackc/pgx/v5`, `github.com/redis/go-redis/v9`, `github.com/gin-gonic/gin`. Document the Gin-for-data-plane choice in `app-serve/README.md` per AGENTS.md (stdlib default, Gin justified by route groups and middleware).

- [ ] **Step 1: Write failing config test**

`config_test.go` table: missing `DATABASE_URL` fails; malformed `APP_PORT` fails; `MEDIA_DRIVER=tape` fails; `TOKEN_ENC_KEY` not 32 bytes fails; valid minimal env passes. Run `go test ./internal/config/`: FAIL, package missing.

- [ ] **Step 2: Implement `config.go`**

Typed struct, `Load()` reading env once, `validate.Struct` tags, no `os.Getenv` anywhere else. Keep under 250 lines by splitting field docs into the `.env.example` comments instead of the struct.

- [ ] **Step 3: Write failing crypto tests**

`crypto_test.go` table: encrypt/decrypt round trip across 3 plaintext sizes (empty must fail, 16 bytes, 64KB random); decrypt with wrong key fails; `VerifyHMACSHA256` true on the exact `t.body` vector `t=1633174587,s=<hmac>`, false on body with trailing newline, false on re-ordered JSON keys, false on wrong key (covers the Review Focus webhook-body line; the HTTP-level test lands in the webhook task).

- [ ] **Step 4: Implement `crypto.go`**

AES-256-GCM with random nonce per encryption, `key_id` prefix stored alongside ciphertext so rotation works; HMAC-SHA256 hex compare with `hmac.Equal`.

- [ ] **Step 5: Write failing apierror tests**

`apierror_test.go`: `Write` produces `application/problem+json` with the six members; `log_id` omitted when empty; `debug` present in development and absent in production; a body containing a fake `client_secret` value is redacted in both environments; golden file for the `VALIDATION_FAILED` shape.

- [ ] **Step 6: Implement `apierror.go` plus `code` catalog**

Codes from 002 section 8 and 003 sections 6/9, each mapped to HTTP status and a stable `type` URI. Redaction pass over the serialized body matching `client_secret`, `refresh_token`, `access_token`, `password`, `authorization` case-insensitively.

- [ ] **Step 7: Implement platform packages and `main.go`**

`postgres.go`: pgx pool with explicit max open/idle/lifetime from config. `redisx.go`: client plus key-namespace constants. `logging.go`: slog JSON in production, text in development. `httpapi.go`: Gin engine, request-id, logger, recovery; health route `GET /healthz`. `main.go`: load config, wire pool/client/logger, run server, graceful shutdown on SIGTERM.

- [ ] **Step 8: Write `.env.example` and run gates**

`.env.example` lists every variable from 001 section 11 with comments, no values. Then:
```bash
cd app-serve && go mod tidy && gofmt -l . && go vet ./... && go test -race ./...
cd /home/rusmanadodi/GenerativeAutoPost && bash scripts/gates/all.sh
```
Expected: all PASS.

- [ ] **Step 9: Commit**

```bash
git add app-serve/ scripts/
git commit -m "feat(app-serve): scaffold, typed config, crypto, RFC9457 errors"
```

---

### Task 4: Migrations for the ten tables

**Files:**
- Create: `migrations/000001_core.up.sql`, `migrations/000001_core.down.sql` (users, sessions, tiktok_accounts, publish_attempts), `migrations/000002_publish.up.sql`, `.down.sql` (publish_chunks, webhook_events, outbox), `migrations/000003_insight.up.sql`, `.down.sql` (video_stats, creator_info_snapshots), `app-serve/internal/platform/postgres/migrate.go`
- Test: `app-serve/internal/platform/postgres/migrate_test.go` (tagged `integration`)

**Interfaces:**
- Consumes: `postgres.Pool` from Task 3
- Produces: `migrate.Up(ctx, pool, dir) error`, `migrate.Down(ctx, pool, dir, steps int) error`, journal table `schema_migrations`

Column-for-column per 001 section 5, including the unique indexes on `tiktok_accounts(open_id)`, `publish_attempts(publish_id)`, `webhook_events(dedupe_key)`, and `video_stats(video_id, fetched_at DESC)`. Token columns: `*_enc BYTEA NOT NULL`, plus `token_key_id TEXT`.

- [ ] **Step 1: Write failing integration migration test**

`migrate_test.go` (`//go:build integration`): `Up` on an empty database creates all ten tables; applying `Up` twice is a no-op; `Down` rolls back one step. Run without `GENAUTO_TEST_POSTGRES_DSN`: expect SKIP message, not failure. Run with it: FAIL, no files.

- [ ] **Step 2: Write the three up/down pairs**

Keep each pair focused; destructive statements forbidden in `up` (drops only appear in `down` with a comment citing the sign-off rule).

- [ ] **Step 3: Implement the runner**

Journal-based, transactional per file, `migrate.Up` used by `main.go` behind a flag.

- [ ] **Step 4: Run against a real PostgreSQL 17**

```bash
docker run -d --name genauto-pg -e POSTGRES_PASSWORD=secret -p 5433:5432 postgres:17
GENAUTO_TEST_POSTGRES_DSN=postgres://postgres:secret@localhost:5433/postgres?sslmode=disable \
  go test -race -tags=integration ./internal/platform/postgres/
```
Expected: PASS. (Local PG 14 is not the target; the gate uses 17.)

- [ ] **Step 5: Commit**

```bash
git add migrations/ app-serve/internal/platform/postgres/
git commit -m "feat(app-serve): core, publish, insight migrations"
```

---

### Task 5: TikTok outbound client with fake server

**Files:**
- Create: `app-serve/internal/tiktokclient/client.go`, `schema.go` (typed req/resp for every endpoint in 002), `errors.go` (code map), `ratelimit.go`, `faketiktok_test.go` (fake server), `client_test.go`
- Test: same files; integration tag NOT needed (hermetic `httptest`)

**Interfaces:**
- Consumes: `crypto.Keyring` for nothing yet (tokens passed in), Redis limiter in `ratelimit.go` via `redisx`
- Produces:
  - `client.ExchangeCode(ctx, code string) (TokenSet, error)`
  - `client.RefreshToken(ctx, refresh string) (TokenSet, error)`
  - `client.Revoke(ctx, access string) error`
  - `client.QueryCreatorInfo(ctx, access string) (CreatorInfo, error)`
  - `client.InitVideoPost(ctx, access string, req VideoInitRequest) (VideoInitResponse, error)`
  - `client.InitPhotoPost(ctx, access string, req PhotoInitRequest) (PhotoInitResponse, error)`
  - `client.InitDraftUpload(ctx, access string, req DraftInitRequest) (DraftInitResponse, error)`
  - `client.UploadChunk(ctx, uploadURL string, chunk Chunk) error`
  - `client.FetchStatus(ctx, access string, publishID string) (PublishStatus, error)`
  - `client.ListVideos(ctx, access string, cursor string, maxCount int) (VideoPage, error)`
  - `client.QueryVideos(ctx, access string, ids []string, fields []string) ([]Video, error)`
  - `client.GetUserInfo(ctx, access string, fields []string) (UserInfo, error)`
  - Every method returns a typed `*apierror.Error` on failure with the mapped code from 002 section 8

The fake server replays: happy paths for all endpoints, each TikTok error code in the map table, `429` after N requests to exercise the limiter, and chunk `Content-Range` validation.

- [ ] **Step 1: Write the fake server and one failing test**

`client_test.go` table for `ExchangeCode`: valid code returns tokens; invalid code returns `TIKTOK_REAUTH_REQUIRED`. Run: FAIL, no client.

- [ ] **Step 2: Implement schema, client, error map**

One file per concern to respect the 250-line limit: `schema.go` (wire types with exact TikTok names, including the `publicaly` misspelling on the wire with correctly-spelled Go names and a comment citing 002 section 5.3), `client.go` (do with timeout, decode, map), `errors.go` (the 002 section 8 table as data, unknown codes to `TIKTOK_UNKNOWN_ERROR`).

- [ ] **Step 3: Add the remaining table tests**

Per endpoint: happy path, one domain error, `429` then success after backoff, `5xx` then success after bounded retry, malformed JSON body from upstream to `TIKTOK_BAD_UPSTREAM`. Chunk test: wrong range rejected client-side before any HTTP call. Run full file: `go test -race ./internal/tiktokclient/`, PASS.

- [ ] **Step 4: Implement `ratelimit.go`**

Redis token bucket per account per endpoint family (6/min video init, 20/min creator info, 30/min status). When Redis is unreachable, fail closed for publish paths (refuse) and fail open for read paths (log), documented in the file header `@reason`.

- [ ] **Step 5: Gates and commit**

```bash
cd app-serve && go test -race ./internal/tiktokclient/ && cd .. && bash scripts/gates/all.sh
git add app-serve/internal/tiktokclient/
git commit -m "feat(app-serve): TikTok client with fake server and error map"
```

---

### Task 6: Auth module (OAuth, session, accounts)

**Files:**
- Create: `app-serve/internal/auth/schema.go`, `domain.go`, `service.go`, `handler.go`, `router.go`, `auth_test.go` (unit + httptest)
- Depends on: `account` aggregate interface (define the minimal interface this task needs: `UpsertAccount(ctx, TokenSet, scopes) (Account, error)`, and implement the real one in Task 7)

**Interfaces:**
- Consumes: `tiktokclient.*`, `config`, Redis session store
- Produces:
  - `GET /api/v1/auth/tiktok/authorize` building the exact 002 section 2.1 URL, storing single-use `state` in Redis (10 min TTL)
  - `GET /api/v1/auth/tiktok/callback` validating and deleting `state` (covers the Review Focus race: second use of the same `state` fails), exchanging the code, upserting the account, setting the session cookie
  - `POST /api/v1/auth/tiktok/refresh`, `POST /api/v1/auth/tiktok/revoke`, `POST /api/v1/auth/logout`, `GET /api/v1/auth/session`
  - Session middleware `RequireSession` attaching operator ID, CSRF check middleware for state-changing routes

Cookie: opaque session ID, `HttpOnly`, `Secure`, `SameSite=Lax`, name/TTL from config. CSRF token per session compared against `X-CSRF-Token`.

- [ ] **Step 1: Write failing tests**

Table for callback: valid code+state creates session and sets cookie; reused `state` fails with `401`-family code (the race test: fire two callbacks with the same `state` concurrently, assert exactly one success); mismatched `state` fails; TikTok `error=access_denied` redirects to app-ui failure without echoing the description; missing scope in the granted list marks the account partial. Auth-failure test per route: no cookie to `200`.

- [ ] **Step 2: Implement schema, domain, service**

`state` generation with `crypto/rand` 32 bytes base64url. Refresh path takes a per-account Redis lock (`SET NX PX`) before calling `RefreshToken`. Refresh failure with invalid token marks needs-reauth; no retry loop.

- [ ] **Step 3: Implement handler and router, run tests**

`go test -race ./internal/auth/`: PASS. Then gates.

- [ ] **Step 4: Commit**

```bash
git add app-serve/internal/auth/
git commit -m "feat(app-serve): OAuth authorize, callback, session, CSRF"
```

---

### Task 7: Account aggregate and token vault

**Files:**
- Create: `app-serve/internal/account/schema.go`, `domain.go` (aggregate with `RotateTokens`, `Revoke`, `HasScope`, `MarkNeedsReauth`), `repository.go` (pgx), `service.go`, `account_test.go`, `repository_test.go` (integration)
- Test: unit for the aggregate (table: rotate updates expiry; revoke clears material; `HasScope` false on partial; re-auth flag), integration for the repository (upsert, unique `open_id`, encrypted columns round-trip)

**Interfaces:**
- Consumes: `crypto.Keyring`, `postgres.Pool`
- Produces: the real `UpsertAccount` implementation consumed by Task 6; `account.Service.GetOwned(ctx, operatorID, accountID) (Account, error)` enforcing ownership (deny by default, `404` on foreign IDs)

- [ ] **Step 1: Write failing aggregate unit tests**

Run: FAIL, no package.

- [ ] **Step 2: Implement domain with unexported fields**

Token material never leaves the package decrypted except through `AccessToken(ctx)` which returns plaintext only to the calling service, never to a handler.

- [ ] **Step 3: Write failing repository integration tests, implement**

`repository_test.go` (`//go:build integration`): upsert then read returns the same `open_id`; second upsert with new tokens rotates; raw DB read shows ciphertext, not the `act.` prefix (assert the stored bytes do not contain the test token).

- [ ] **Step 4: Wire the real upsert into auth, run everything**

```bash
GENAUTO_TEST_POSTGRES_DSN=... GENAUTO_TEST_REDIS_ADDR=... go test -race -p 1 -tags=integration ./internal/account/ ./internal/auth/
```

- [ ] **Step 5: Commit**

```bash
git add app-serve/internal/account/
git commit -m "feat(app-serve): TikTokAccount aggregate and encrypted token vault"
```

---

### Task 8: Media module (disk + R2, chunk planner)

**Files:**
- Create: `app-serve/internal/media/schema.go`, `domain.go` (`PlanChunks(totalBytes, chunkSize) ([]ChunkPlan, error)`), `store.go` (`Store` interface: `Put`, `Get`, `Delete`, `SignPublicURL`), `disk.go`, `s3.go`, `service.go`, `handler.go`, `router.go`, `media_test.go`
- Test: unit + httptest; S3 tests against a fake S3 (local `httptest` stub of the S3 API surface used, or `minio` via Testcontainers under the integration tag; choose the stub to keep hermetic)

**Interfaces:**
- Consumes: `config` (`MEDIA_DRIVER`, R2 settings, size caps), `account.Service` for ownership
- Produces:
  - `POST /api/v1/media/uploads` validating content type, byte cap, and duration before storage
  - `PlanChunks` enforcing 5MB-64MB per chunk, at most 1000, sequential, trailing remainder under 5MB merged into the previous chunk (table test with at least: exact multiple, remainder 123 bytes, single chunk, 1001-chunk input rejected, zero bytes rejected)
  - `SignPublicURL` producing URLs under `MEDIA_PUBLIC_BASE_URL` only

- [ ] **Step 1: Write failing `PlanChunks` table test**

Run: FAIL.

- [ ] **Step 2: Implement domain and stores**

Disk store under a configured root; S3 store via `s3.PutObject` against `S3_ENDPOINT` (R2). Both return the same `MediaObject` shape from 003 section 7.3.

- [ ] **Step 3: Implement service, handler, router; run tests and gates**

Validation failure test: `.exe` upload rejected; oversize rejected; `PULL_FROM_URL` request for a non-verified host rejected.

- [ ] **Step 4: Commit**

```bash
git add app-serve/internal/media/
git commit -m "feat(app-serve): media store, chunk planner, upload routes"
```

---

### Task 9: Publish module (creator info, direct post, drafts, chunks, status)

**Files:**
- Create: `app-serve/internal/publish/schema.go`, `domain.go` (`PublishAttempt` aggregate with transitions `CREATED -> PROCESSING_* -> PUBLISH_COMPLETE | FAILED`, `SEND_TO_USER_INBOX` for drafts), `repository.go`, `service.go`, `handler.go`, `router.go`, `publish_test.go`, `repository_test.go` (integration)
- Test: unit with fake `tiktokclient` and fake `media`; integration for the repository and the chunk bookkeeping

**Interfaces:**
- Consumes: `tiktokclient.*`, `media.Service`, `account.Service`, outbox writer (define `outbox.Publisher` interface here; real Redis Streams implementation in Task 10)
- Produces:
  - `POST /api/v1/accounts/{id}/creator-info` (cache-first, 5 min TTL)
  - `POST /api/v1/publish/videos`, `POST /api/v1/publish/photos`, `POST /api/v1/publish/drafts` returning `202` with the 003 section 7.4 body (no upstream `upload_url` exposed)
  - `PUT /api/v1/publish/{id}/chunks` validating index order and `Content-Range` against the stored plan (Review Focus: out-of-order chunk rejected locally), proxying bytes upstream
  - `GET /api/v1/publish/{id}`, `GET /api/v1/publish`, `POST /api/v1/publish/{id}/reconcile`
  - `Idempotency-Key` handling per 003 section 10 (Review Focus: same key different body to `409`)

Server-side rules from 003 section 7.4 enforced in the service in this order: privacy option check (Review Focus: foreign value to `PUBLISH_PRIVACY_LEVEL_MISMATCH`), UTF-16 rune length (write a `utf16len` helper with its own table test), commercial-toggle coherence, verified-URL check, server-set `is_aigc`.

- [ ] **Step 1: Write failing service tests**

Tables: init happy path returns `202` shape; `privacy_level` not in options rejected; title 2201 UTF-16 runes rejected; branded flag with `SELF_ONLY` rejected; `PULL_FROM_URL` with unverified host rejected; duplicate idempotency key with changed body to `409`.

- [ ] **Step 2: Implement domain transitions and service**

Aggregate owns the status machine; illegal transitions (e.g. `FAILED -> PUBLISH_COMPLETE` without a new attempt) are compile-time impossible by keeping the transition method private to the package with explicit methods (`MarkProcessing`, `MarkComplete`, `MarkFailed`).

- [ ] **Step 3: Implement chunk proxy and repository, run suites**

`PUT` handler streams the request body to the stored `upload_url` with computed `Content-Range`; records `publish_chunks` rows; a 1-hour-expired `upload_url` starts a fresh init transparently and returns the new plan.

- [ ] **Step 4: Gates and commit**

```bash
cd app-serve && go test -race ./internal/publish/ && cd .. && bash scripts/gates/all.sh
git add app-serve/internal/publish/
git commit -m "feat(app-serve): publish orchestration, chunk proxy, idempotency"
```

---

### Task 10: Webhook intake, verification, dispatcher, reconciler

**Files:**
- Create: `app-serve/internal/webhook/schema.go` (envelope + per-event `content` structs with exact wire names), `verify.go`, `repository.go` (event store), `service.go` (dispatcher), `handler.go`, `router.go` (mounted at `/webhooks/tiktok`, outside `/api/v1`), `worker.go` (bounded pool + reconciler), `webhook_test.go`
- Test: httptest for the handler (valid signature processes, invalid to `401`, stale timestamp beyond 5 min to `401`, duplicate delivery to `200` with no state change, unknown event to `200` with log), unit for `verify.go` vectors, integration for the event store unique `dedupe_key`

**Interfaces:**
- Consumes: `crypto.VerifyHMACSHA256`, `publish` status updater interface (`MarkPublishComplete(ctx, publishID, postID)`, `MarkPublishFailed(ctx, publishID, reason)`, `MarkInboxDelivered`, `MarkUnpublished`), Redis Streams outbox
- Produces: `POST /webhooks/tiktok` acknowledging `200` before processing; worker pool (size from config, default 4) consuming the stream; reconciler for `processed_at IS NULL` rows and open attempts past 10 minutes calling `FetchStatus` through the shared limiter

`authorization.removed` handling per 002: mark revoked, delete token material, cancel scheduled publishes for the `user_openid`.

- [ ] **Step 1: Write failing verification and handler tests**

Vectors from 002 section 5.2, including the Review Focus body-mutation cases. Run: FAIL.

- [ ] **Step 2: Implement verify, schema, handler**

Body read with a 64KB limit before verification. Constant-time compare. Any failure to `401` with a generic problem body and a structured log including the source IP but never the body.

- [ ] **Step 3: Implement store, dispatcher, worker, reconciler**

`dedupe_key = event + ":" + publish_id + ":" + (post_id or create_time)`. Insert-then-ack ordering per 002 section 5.4. Worker wraps each job in panic recovery. Reconciler uses exponential backoff with jitter and stops after a bounded number of attempts, marking the attempt `RECONCILE_EXHAUSTED` for operator attention.

- [ ] **Step 4: Run suites, gates, commit**

```bash
cd app-serve && go test -race ./internal/webhook/ && cd .. && bash scripts/gates/all.sh
git add app-serve/internal/webhook/
git commit -m "feat(app-serve): TikTok webhook intake, verify, dispatch, reconcile"
```

---

### Task 11: Insight module (videos, profile, stats)

**Files:**
- Create: `app-serve/internal/insight/schema.go`, `repository.go` (snapshots), `service.go`, `handler.go`, `router.go`, `insight_test.go`, `repository_test.go` (integration)

**Interfaces:**
- Consumes: `tiktokclient.ListVideos/QueryVideos/GetUserInfo`, `account.Service`
- Produces:
  - `GET /api/v1/videos`, `POST /api/v1/videos/query` (max 20 ids enforced), `GET /api/v1/videos/{video_id}`, `GET /api/v1/videos/{video_id}/stats`, `POST /api/v1/videos/{video_id}/stats/refresh`, `GET /api/v1/accounts/{id}/profile`
  - Missing count fields omitted, never zeroed; every stats body carries `fetched_at`; stale cover URLs refreshed via query before display

- [ ] **Step 1: Write failing tests**

Tables: list maps TikTok page to our cursor envelope; query with 21 ids rejected; stats body omits a missing `like_count` (assert key absence, not zero); profile maps `follower_count` etc. from `user.info.stats`.

- [ ] **Step 2: Implement repository, service, handler, router**

Snapshot writes are inserts, never updates, so history is append-only.

- [ ] **Step 3: Run suites, gates, commit**

```bash
git add app-serve/internal/insight/
git commit -m "feat(app-serve): video reads, profile, stats snapshots"
```

---

### Task 12: Deployment, docs close-out, DESIGN.md placeholder

**Files:**
- Create: `deployment/docker-compose.yml` (app-serve, app-ui placeholder, `postgres:17`, `redis:7`), `deployment/nginx/app-tiktok.conf`, `deployment/README.md`, `app-ui/.env.example`, `DESIGN.md` (direction brief template, empty sections for the owner to fill), `docs/CHANGELOG/001-V1-BACKEND.md`
- Modify: root `README.md` (gates, compose, webhook host)

**Interfaces:**
- Consumes: all service ports and env names from Tasks 3-11
- Produces: `docker compose up` runs the full stack locally; Nginx terminates TLS for `app-tiktok.kentangtech.com`, serves app-ui, proxies `/api/v1` to app-serve and `/webhooks/tiktok` to the webhook handler

- [ ] **Step 1: Write compose and nginx config**

Postgres 17 with a named volume; Redis 7 with a named volume and `appendonly`; app-serve built from `app-serve/` with its `.env`; healthchecks on `/healthz`.

- [ ] **Step 2: Write DESIGN.md skeleton**

Sections: product, audience, mood, palette, typography, dials (`ENERGY/RHYTHM/MOTION`), identity motif. Explicitly marked as awaiting owner direction; no invented brand content (antislop R-37).

- [ ] **Step 3: Full gate run and commit**

```bash
bash scripts/gates/all.sh
git add deployment/ app-ui/.env.example DESIGN.md docs/ README.md
git commit -m "chore: deployment, DESIGN.md brief, v1 changelog"
```

---

## Self-Review

**1. Spec coverage.** 001 architecture: Tasks 3, 4, 6-11 (layout, layers, ten tables, Redis keys, timeouts, testing). 001 section 11 config: Task 3. 002 auth/token/scopes: Tasks 5, 6, 7 (progressive scope request recorded as the service behavior in Task 6). 002 creator info + UX rules: Task 9. 002 video/photo/draft/chunk/status: Tasks 5, 8, 9. 002 webhooks incl. `authorization.removed` and spelled-wire names: Task 10. 002 polling fallback: Task 10 reconciler + Task 9 manual endpoint. 002 stats/reads: Task 11. 002 error map: Tasks 3 (writer) + 5 (map). 003 envelopes, RFC 9457, dev/prod split, status codes, endpoints, session/CSRF, rate limits, idempotency, chunk proxy secrecy, webhook outside versioning: Tasks 3, 6, 8-11. Deployment + `.env.example`: Task 12. Gates/hooks: Task 2. DESIGN.md: Task 12 skeleton only, awaiting owner direction.

**2. Placeholder scan.** No TBD/TODO; every step names files, signatures, commands, and expected outcomes. Env prefix fixed to `GENAUTO_`. Module path fixed.

**3. Type consistency.** `tiktokclient` methods named once in Task 5 and reused verbatim in Tasks 6, 9, 10, 11. `account.Service.GetOwned` and `UpsertAccount` named in Task 7, consumed in Tasks 6, 8, 9, 11. `outbox.Publisher` interface declared in Task 9, implemented in Task 10. Webhook-to-publish updater methods named in Task 10 only.

**4. Review Focus.** All five lines carry an owning test: state-reuse race (Task 6), signature body mutation (Tasks 3 + 10), foreign privacy level (Task 9), idempotency-key reuse with changed body (Task 9), out-of-order chunk (Task 9).
