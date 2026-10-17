# Operator Google Login Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the dashboard a real operator sign in with Google OIDC, allowlisted to one email, and demote TikTok OAuth to a connect-account flow.

**Architecture:** A new `internal/platform/googleoidc` client builds the authorize URL, exchanges the code, and verifies the `id_token` against Google's JWKS with stdlib crypto. `internal/auth` gains a Google service that consumes the state, verifies the token, checks the allowlist, ensures the operator row, and mints the existing Redis session. `internal/operator` owns the `users` row. The TikTok callback stops minting sessions.

**Tech Stack:** Go 1.26, Gin, pgx/PostgreSQL, Redis, `crypto/rsa`, `crypto/sha256`, `encoding/json`, `net/http`.

**Spec:** `docs/superpowers/specs/2026-10-17-operator-google-login-design.md`

## Global Constraints

- Go standard library first. No OAuth, OIDC, or JWT library.
- Every hand-written `.go` file under 250 lines with the `AGENTS.md` 1.2 header; split at ~220.
- Every outbound call takes a `context` with a timeout. No `context.Background()` into an HTTP client.
- No `any` outside an I/O boundary.
- Client-facing strings are English; no em or en dashes anywhere.
- Config is typed and validated at boot; fail fast.
- Layer flow `schema -> domain -> repository -> service -> handler -> router`.
- Tests before or with implementation; `go test -race` clean; no `t.Skip()`.
- Client secret, tokens, and upstream error text never reach a response or a log at info level.
- Commit after each task.

## Review Focus

- A validly signed `id_token` for the wrong `aud` must be rejected, not accepted because the signature checked out (Task 2).
- An unknown `kid` from key rotation must refetch the JWKS, not lock the operator out (Task 2).
- The allowlist match must survive trailing space and mixed case (Task 5).
- A replayed or mismatched `nonce` must reject the login (Task 5).
- An unauthenticated browser hit on the TikTok connect routes must redirect, not write a JSON 401 (Task 6).

---

### Task 1: Config keys

**Files:**
- Modify: `app-serve/internal/config/config.go`
- Test: `app-serve/internal/config/wiring_test.go`
- Modify: `app-serve/.env.example`

**Interfaces:**
- Produces: `Config.GoogleClientID`, `Config.GoogleClientSecret`, `Config.GoogleRedirectURI`, `Config.OperatorEmail`, `Config.AppUIBaseURL` (all `string`).

- [ ] **Step 1: Write the failing tests**

Add to `config/wiring_test.go`:
- `TestLoad_GoogleLoginKeysRequired` sets a full valid env, then clears each of `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URI`, `OPERATOR_EMAIL` in turn and asserts `Load` returns an error naming that key.
- `TestLoad_OperatorEmailMustBeAnEmail` sets `OPERATOR_EMAIL=not-an-email` and asserts an error.
- `TestLoad_AppUIBaseURLIsOptional` leaves `APP_UI_BASE_URL` empty and asserts `Load` succeeds with an empty value, then sets it and asserts it round-trips.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd app-serve && go test ./internal/config/ -run Google -v`
Expected: FAIL, the fields and env keys do not exist.

- [ ] **Step 3: Add the fields and loading**

In `config.go`, add to `Config`:
```go
GoogleClientID     string `validate:"required"`
GoogleClientSecret string `validate:"required"`
GoogleRedirectURI  string `validate:"required"`
OperatorEmail      string `validate:"required,email"`
AppUIBaseURL       string
```
Populate them in `Load` with `getenv("GOOGLE_CLIENT_ID")`, `getenv("GOOGLE_CLIENT_SECRET")`, `getenv("GOOGLE_REDIRECT_URI")`, `getenv("OPERATOR_EMAIL")`, `getenv("APP_UI_BASE_URL")`.

- [ ] **Step 4: Add the env keys to `.env.example`**

Under the TikTok credentials block, add commented-empty `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URI`, `OPERATOR_EMAIL`, and `APP_UI_BASE_URL`, each with a one-line comment. Keep the existing "Every key maps to docs/SPEC/001 section 11" note true.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd app-serve && go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add app-serve/internal/config/config.go app-serve/internal/config/wiring_test.go app-serve/.env.example
git commit -m "feat(config): add google login and app ui base settings"
```

---

### Task 2: googleoidc id_token verification

**Files:**
- Create: `app-serve/internal/platform/googleoidc/oidc.go`
- Create: `app-serve/internal/platform/googleoidc/jwks.go`
- Test: `app-serve/internal/platform/googleoidc/oidc_test.go`

**Interfaces:**
- Produces:
```go
package googleoidc

type Config struct {
    ClientID     string
    ClientSecret string
    RedirectURI  string
    AuthURL      string // empty means the Google default
    TokenURL     string // empty means the Google default
    JWKSURL      string // empty means the Google default
    HTTPClient   *http.Client // nil means a client with no global timeout
}

type Claims struct {
    Subject       string
    Email         string
    EmailVerified bool
}

type Client struct { /* ... */ }
func New(cfg Config) *Client
func (c *Client) VerifyIDToken(ctx context.Context, raw string, nonce string) (Claims, error)
```
- `VerifyIDToken` order: parse JWT, require `alg=RS256`, read `kid`, select the JWKS key (refetch once on unknown `kid`), verify the signature over `header.payload`, decode the payload, then check `iss` in `{"https://accounts.google.com","accounts.google.com"}`, `aud == ClientID`, `exp` in the future, `iat` not in the future, `nonce` equal, `email_verified` true, `email` non-empty. Any failure is an error.

- [ ] **Step 1: Write the failing tests**

In `oidc_test.go`, build helpers that generate an RSA key, sign a JWT with `crypto/rsa` + `crypto/sha256`, and serve its JWKS over `httptest`. Tests:
- `TestVerifyIDToken_AcceptsAValidToken` returns `Email` and `Subject` from the claims.
- `TestVerifyIDToken_RejectsBadSignature` signs with a different key, expects an error.
- `TestVerifyIDToken_RejectsWrongAudience` sets `aud` to another client id, expects an error.
- `TestVerifyIDToken_RejectsWrongIssuer` sets a foreign `iss`, expects an error.
- `TestVerifyIDToken_RejectsExpired` sets `exp` in the past, expects an error.
- `TestVerifyIDToken_RejectsNonceMismatch` passes a different nonce, expects an error.
- `TestVerifyIDToken_RejectsUnverifiedEmail` sets `email_verified=false`, expects an error.
- `TestVerifyIDToken_RefetchesOnUnknownKid` serves two key ids across two responses and asserts the second key verifies.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd app-serve && go test ./internal/platform/googleoidc/ -v`
Expected: FAIL, package or symbols missing.

- [ ] **Step 3: Implement**

`oidc.go`: the `Client`, `New`, JWT split, header/payload structs, and the claim checks. `jwks.go`: the JWKS struct (`keys[].kid`, `kty="RSA"`, `n`, `e`), base64url big-int decode into `*rsa.PublicKey`, a small cached key map with a TTL, and a refetch path for an unknown `kid`. Use `encoding/base64` RawURLEncoding and `math/big`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd app-serve && go test -race ./internal/platform/googleoidc/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add app-serve/internal/platform/googleoidc/
git commit -m "feat(googleoidc): verify the id token against google jwks"
```

---

### Task 3: googleoidc authorize and token exchange

**Files:**
- Create: `app-serve/internal/platform/googleoidc/exchange.go`
- Test: `app-serve/internal/platform/googleoidc/exchange_test.go`

**Interfaces:**
- Consumes: `Client`, `Config`, `New` from Task 2.
- Produces:
```go
func (c *Client) AuthorizeURL(state, nonce string) string
func (c *Client) ExchangeCode(ctx context.Context, code string) (string, error) // returns the raw id_token
```
- `AuthorizeURL` sets `client_id`, `redirect_uri`, `response_type=code`, `scope=openid email`, `state`, `nonce` on the configured `AuthURL`.
- `ExchangeCode` posts `application/x-www-form-urlencoded` with `code`, `client_id`, `client_secret`, `redirect_uri`, `grant_type=authorization_code`, and returns the `id_token` field from the JSON response.

- [ ] **Step 1: Write the failing tests**

In `exchange_test.go` with `httptest`:
- `TestAuthorizeURL_CarriesStateAndNonce` asserts the parsed query has the client id, redirect, `scope=openid email`, the state, and the nonce.
- `TestExchangeCode_PostsGrantAndReturnsIDToken` stands up a fake token endpoint and asserts the form fields and the returned `id_token`.
- `TestExchangeCode_ErrorsOnProviderError` returns `{"error":"invalid_grant"}` and expects an error that does not echo the body verbatim.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd app-serve && go test ./internal/platform/googleoidc/ -run Exchange -v`
Expected: FAIL, methods missing.

- [ ] **Step 3: Implement** `exchange.go` with a `net/http` client and an explicit request timeout on the context.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd app-serve && go test -race ./internal/platform/googleoidc/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add app-serve/internal/platform/googleoidc/exchange.go app-serve/internal/platform/googleoidc/exchange_test.go
git commit -m "feat(googleoidc): add authorize url and token exchange"
```

---

### Task 4: Operator row migration and repository

**Files:**
- Create: `migrations/000009_users_drop_password.up.sql`
- Create: `migrations/000009_users_drop_password.down.sql`
- Create: `app-serve/internal/operator/repository.go`
- Test: `app-serve/internal/operator/repository_test.go`

**Interfaces:**
- Produces:
```go
package operator

type Repository struct { /* ... */ }
func NewRepository(pool *pgxpool.Pool) *Repository
func (r *Repository) Ensure(ctx context.Context, id string, email string) error
```
- `Ensure` inserts `(id, email)` into `users` and, on re-login, updates `email` for the same `id`.

- [ ] **Step 1: Write the migration**

`up`: `ALTER TABLE users DROP COLUMN password_hash;`
`down`: `ALTER TABLE users ADD COLUMN password_hash text NOT NULL DEFAULT '';`
One-line comment each, naming `users` and the Google-only operator.

- [ ] **Step 2: Write the failing tests**

`repository_test.go` under the `integration` build tag using the shared test pool helper:
- `TestEnsure_InsertsTheOperatorRow` calls `Ensure` then queries `users` for the id and email.
- `TestEnsure_IsIdempotentAndUpdatesEmail` calls `Ensure` twice with the same id and a new email, asserts one row and the new email.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `GENAUTO_TEST_POSTGRES_DSN="..." go test -tags=integration ./internal/operator/ -v`
Expected: FAIL, package missing.

- [ ] **Step 4: Implement** `repository.go` with `INSERT ... ON CONFLICT (id) DO UPDATE SET email = EXCLUDED.email`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `GENAUTO_TEST_POSTGRES_DSN="..." go test -race -tags=integration ./internal/operator/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add migrations/000009_users_drop_password.up.sql migrations/000009_users_drop_password.down.sql app-serve/internal/operator/
git commit -m "feat(operator): ensure the operator row on login"
```

---

### Task 5: auth Google service

**Files:**
- Create: `app-serve/internal/auth/google.go`
- Test: `app-serve/internal/auth/google_test.go`
- Modify: `app-serve/internal/auth/session.go` (a shared `mintSession` helper)
- Modify: `app-serve/internal/auth/service.go` (drop session minting from the TikTok callback)
- Modify: `app-serve/internal/auth/state.go` (generalize the payload error wording)

**Interfaces:**
- Consumes: `googleoidc.Claims`, `StateStore`, `SessionStore`, the `Session` shape.
- Produces:
```go
type GoogleProvider interface {
    AuthorizeURL(state, nonce string) string
    ExchangeCode(ctx context.Context, code string) (string, error)
    VerifyIDToken(ctx context.Context, raw, nonce string) (googleoidc.Claims, error)
}
type OperatorEnsurer interface {
    Ensure(ctx context.Context, id, email string) error
}
type GoogleConfig struct {
    OperatorID    string
    OperatorEmail string
}
type GoogleService struct { /* ... */ }
func NewGoogleService(cfg GoogleConfig, provider GoogleProvider, operators OperatorEnsurer, states *StateStore, sessions *SessionStore, logger *slog.Logger) *GoogleService
func (s *GoogleService) AuthorizeURL(ctx context.Context) (string, error)
func (s *GoogleService) Complete(ctx context.Context, code, state string) (*Session, error)
```
- `AuthorizeURL` mints a nonce, stores it as the state payload, and returns the provider URL.
- `Complete` consumes the state into the nonce, exchanges the code, verifies the token with that nonce, compares the email to `OperatorEmail` trimmed and lowercased, returns a generic 403 on a mismatch, calls `operators.Ensure`, and mints the session with an empty scope list.
- `Service.CompleteCallback(ctx, operatorID, code, state) error` no longer mints; it upserts the account for `operatorID` only.

- [ ] **Step 1: Write the failing tests**

`google_test.go` with a fake `GoogleProvider`, a fake `OperatorEnsurer`, and the real `StateStore`/`SessionStore` over `miniredis`:
- `TestGoogleComplete_MintsASessionForTheAllowlistedEmail`.
- `TestGoogleComplete_RejectsANonAllowlistedEmail` expects `CodeForbidden` and no session.
- `TestGoogleComplete_MatchesTheAllowlistCaseInsensitively` uses `  Operator@Example.COM  ` against `operator@example.com`.
- `TestGoogleComplete_RejectsAReplayedState` calls `Complete` twice, the second expects unauthorized.
- `TestGoogleComplete_RejectsATokenWithTheWrongNonce`.
- `TestGoogleComplete_EnsuresTheOperatorRow` asserts `Ensure` was called with the operator id and email.
- Update `service_callback_test.go`: `TestCompleteCallback_UpsertsOnly` asserts no session is created and the account is upserted for the passed operator id.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd app-serve && go test ./internal/auth/ -run Google -v`
Expected: FAIL, symbols missing.

- [ ] **Step 3: Implement** `google.go`, add `mintSession(operatorID string, scopes []string)` to `session.go`, remove `Service.newSession`, and change `CompleteCallback` to the new signature. Log a refused email at warn with the address, and drop `ServiceConfig.OperatorID` if `CompleteCallback`'s new parameter leaves it unused.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd app-serve && go test -race ./internal/auth/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add app-serve/internal/auth/google.go app-serve/internal/auth/google_test.go app-serve/internal/auth/session.go app-serve/internal/auth/service.go app-serve/internal/auth/state.go app-serve/internal/auth/service_callback_test.go
git commit -m "feat(auth): add google login and demote the tiktok callback"
```

---

### Task 6: auth handlers, routes, and browser guard

**Files:**
- Create: `app-serve/internal/auth/handler_google.go`
- Modify: `app-serve/internal/auth/router.go`
- Modify: `app-serve/internal/auth/middleware.go`
- Modify: `app-serve/internal/auth/handler_callback.go`
- Test: `app-serve/internal/auth/google_handler_test.go`
- Test: `app-serve/internal/auth/handler_oauth_test.go`

**Interfaces:**
- Consumes: `GoogleService` from Task 5.
- Produces:
```go
type GoogleHandler struct { /* ... */ }
func NewGoogleHandler(svc *GoogleService, cfg HandlerConfig) *GoogleHandler
func (h *GoogleHandler) Authorize(c *gin.Context)
func (h *GoogleHandler) Callback(c *gin.Context)

// RequireSessionRedirect answers a missing session with a redirect to the
// result page instead of a JSON 401. For browser-navigated routes only.
func RequireSessionRedirect(store *SessionStore, cookieName string, baseURL string) gin.HandlerFunc

// IPLimiter is the per-IP window the two Google routes share.
type IPLimiter interface {
    Allow(ctx context.Context, key string) (bool, error)
}

// Throttle refuses over-budget sources with 429 before the handler runs.
func Throttle(limiter IPLimiter, logger *slog.Logger) gin.HandlerFunc

// RouterConfig gains Throttle gin.HandlerFunc (nil means no limit).
func RegisterRoutes(r gin.IRouter, svc *Service, google *GoogleService, sessions *SessionStore, cfg RouterConfig)
```
- `Authorize` redirects to the Google URL; `Callback` sets the cookie and
  redirects to the console root on success, or to `/auth/result?status=error`
  on any failure.
- `Throttle` keys the limiter on `genauto:ratelimit:auth:` plus `c.ClientIP()`,
  the `AGENTS.md` 1.6 control for the new public routes.
- The TikTok `authorize` and `callback` routes now use `RequireSessionRedirect`.
- `handler_callback.go` reads the session and calls
  `CompleteCallback(ctx, sess.OperatorID, code, state)`.

- [ ] **Step 1: Write the failing tests**

- `TestGoogleAuthorize_RedirectsToGoogle` asserts a 302 whose Location host is Google.
- `TestGoogleAuthorize_Throttled` uses a refusing `IPLimiter` and expects 429.
- `TestGoogleCallback_SetsTheCookieAndRedirects` asserts a 302 to the console root and a `Set-Cookie`.
- `TestGoogleCallback_RefusalRedirectsWithoutACookie` asserts a redirect to `status=error` and no cookie.
- `TestTikTokAuthorize_WithoutSessionRedirects` asserts a 302 to the result page, not a 401.
- Update `handler_oauth_test.go` so the TikTok callback tests carry a session.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd app-serve && go test ./internal/auth/ -run 'Google|TikTokAuthorize' -v`
Expected: FAIL.

- [ ] **Step 3: Implement** the handlers, the redirect guard, and the route changes.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd app-serve && go test -race ./internal/auth/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add app-serve/internal/auth/handler_google.go app-serve/internal/auth/router.go app-serve/internal/auth/middleware.go app-serve/internal/auth/handler_callback.go app-serve/internal/auth/google_handler_test.go app-serve/internal/auth/handler_oauth_test.go
git commit -m "feat(auth): mount the google routes and guard the tiktok connect routes"
```

---

### Task 7: compose wiring

**Files:**
- Modify: `app-serve/internal/compose/build.go`
- Modify: `app-serve/internal/compose/routes.go` (only if the auth mount signature changes)
- Test: `app-serve/internal/compose/build_test.go`

**Interfaces:**
- Consumes: `googleoidc.New`, `operator.NewRepository`, `auth.NewGoogleService`, `auth.RegisterRoutes`, `auth.Throttle`, `ratelimit.NewWindow`.
- Produces: a wired `App` whose auth routes include Google, whose auth redirect base is `cfg.AppUIBaseURL`, and whose Google routes carry the IP throttle.

- [ ] **Step 1: Write the failing test**

Add to `build_test.go`: `TestBuild_WiresGoogleLogin` asserts `Build` succeeds with the Google config set and does not panic, and that the auth `ServiceConfig.AppUIBaseURL` is `cfg.AppUIBaseURL` (assert through the built engine or a small accessor).

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd app-serve && go test ./internal/compose/ -run Google -v`
Expected: FAIL.

- [ ] **Step 3: Implement** the wiring: build one `auth.NewStateStore(client)`, pass it to both services, construct `googleoidc.New`, `operator.NewRepository(pool)`, `auth.NewGoogleService`, set `auth.ServiceConfig.AppUIBaseURL = cfg.AppUIBaseURL`, build the throttle as `auth.Throttle(ratelimit.NewWindow(client, googleThrottleMax, googleThrottleWindow, logger), logger)` with `googleThrottleMax = 30` and `googleThrottleWindow = time.Minute`, set it on `auth.RouterConfig.Throttle`, and pass the Google service to `auth.RegisterRoutes`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd app-serve && go test -race ./internal/compose/ -v`
Expected: PASS.

- [ ] **Step 5: Build and run the full suite**

Run: `cd app-serve && go build ./... && go test -race ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add app-serve/internal/compose/
git commit -m "feat(compose): wire the google login client and operator store"
```

---

### Task 8: docs sync

**Files:**
- Modify: `docs/SPEC/001-BACKEND-ARCHITECTURE.md`
- Modify: `docs/SPEC/003-API-CONTRACT.md`
- Modify: `SYSTEM_MAP.md`
- Modify: `docs/CHANGELOG/001-V1-BACKEND.md`
- Modify: `app-serve/README.md`

**Interfaces:** none; documentation only.

- [ ] **Step 1: Update SPEC 001**

Section 4.1: auth now covers Google sign in and TikTok connect. Section 5: `users` loses `password_hash`, gained at login. Section 11: add `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URI`, `OPERATOR_EMAIL`, `APP_UI_BASE_URL`.

- [ ] **Step 2: Update SPEC 003**

Section 7.1: add `GET /auth/google/authorize` and `GET /auth/google/callback`; mark the TikTok authorize and callback routes session-guarded. Section 8: the Google callback is the login entry, the allowlist gates it.

- [ ] **Step 3: Update SYSTEM_MAP**

Section 3 route table: add the two Google routes, note the TikTok connect guard. Section 5 integrations: add Google OIDC. Section 9 known gaps: drop "No operator login" and "No `sessions` mirror" stays only if still true.

- [ ] **Step 4: Update the CHANGELOG and README**

Add the sign-in line under "What landed"; note the `internal/operator` and `internal/platform/googleoidc` packages in the README layout if it lists packages.

- [ ] **Step 5: Run the doc gate**

Run: `bash scripts/gates/antislop.sh`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add docs/SPEC/001-BACKEND-ARCHITECTURE.md docs/SPEC/003-API-CONTRACT.md SYSTEM_MAP.md docs/CHANGELOG/001-V1-BACKEND.md app-serve/README.md
git commit -m "docs: record the operator google login"
```

---

## Final Verification

- [ ] `bash scripts/gates/all.sh` passes at the repo root.
- [ ] `GENAUTO_TEST_POSTGRES_DSN="..." go test -race -p 1 -tags=integration ./...` passes in `app-serve`.
- [ ] A whole-branch review runs before merge.
