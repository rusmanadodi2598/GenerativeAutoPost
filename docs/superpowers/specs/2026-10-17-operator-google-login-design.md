# Operator Google Login

- **Status:** Draft for review
- **Date:** 2026-10-17
- **Owner:** Dodi Rusmana
- **Depends on:** `docs/SPEC/001-BACKEND-ARCHITECTURE.md`, `docs/SPEC/003-API-CONTRACT.md`, `SYSTEM_MAP.md`
- **Governing rules:** `AGENTS.md`, `docs/RULLES/TDD.md`, `docs/RULLES/OWASP.md`

## Intent

The dashboard has no sign in. `auth` mints a session only as a side effect of
the TikTok OAuth callback, so the only way in today is to connect a TikTok
account, and any TikTok account that finishes the flow receives a session for
the configured operator. The `users` row the account foreign key needs is
never created, so the first account upsert would fail.

This change gives the operator a real sign in: Google OIDC, one allowlisted
email. The operator signs in with Google, then connects TikTok accounts from
inside the dashboard. TikTok OAuth stops being a login path.

## Scope

Backend only, `app-serve`. app-ui is not built yet and is a later task, so this
change ends at the HTTP contract the console will call. The sign-in screen, the
result page, and the dashboard landing page are app-ui work, deferred until the
console exists.

## Goals

- One operator signs in to the dashboard with Google.
- Only the allowlisted email gets a session.
- TikTok OAuth is demoted to "connect an account", reachable only with a session.
- The operator `users` row exists, so `tiktok_accounts.user_id` holds.

## Non-goals

- Self-service signup, password login, teams, roles, billing, subscription plans.
- Multi-tenant isolation beyond the existing `OPERATOR_ID` scoping.
- A second identity provider.

## Anti-goals

- Any path that mints a session without the allowlist check.
- Storing a password we do not use.
- Re-keying `tiktok_accounts.user_id`.

## Constraints

- Go standard library first; no OAuth or OIDC library, following the `tiktokclient` pattern.
- Every outbound call takes a `context` with a timeout.
- No `any` outside an I/O boundary.
- Client facing strings are English.

## Decisions already taken

1. Provider is Google OIDC, not a password in env.
2. One account, no self-service.
3. Forward compatible: this is the same flow SaaS will use later, with the allowlist opened and `users` keyed per email.

## Design

### Identity

Google proves who the operator is; the internal operator id stays
`OPERATOR_ID` from env. That keeps every existing account, token, and session
row valid. `Session.OperatorID` remains `OPERATOR_ID`.

A Google session carries an empty `Scopes` list. TikTok scopes live on the
account rows and reach the console through `/accounts`; the session scopes are
display only and gate nothing, so an empty list is safe.

### Flow

1. app-ui sends the browser to `GET /auth/google/authorize`.
2. app-serve issues a single-use `state` and a `nonce` in Redis and redirects
   to Google's authorize endpoint with `scope=openid email` and that nonce.
3. Google redirects back to `GET /auth/google/callback?code=...&state=...`.
4. app-serve consumes the `state` (replay proof) and exchanges the `code` at
   Google's token endpoint. The response carries an `id_token`.
5. It verifies the `id_token` against Google's published keys (see below). A
   token that fails any check mints no session; the callback redirects to the
   error result. This route always redirects and never writes a JSON body, so
   the browser never sees an upstream detail.
6. It requires `email_verified` true and the email on the allowlist, then
   ensures the operator `users` row exists, mints the existing Redis session
   and CSRF token, sets the session cookie, and redirects to the dashboard.

### ID token verification

The callback trusts nothing in the token response until the `id_token` passes
every check, all against Google's JWKS:

- Signature is RS256, verified with the key named by the token's `kid`. The
  key set is cached and refetched when a `kid` is unknown, so key rotation does
  not lock the operator out.
- `iss` is `https://accounts.google.com` or `accounts.google.com`.
- `aud` equals `GOOGLE_CLIENT_ID`.
- `exp` is in the future and `iat` is not in the future.
- `nonce` equals the value stored with the `state`.
- `email_verified` is true, and `email` is the claim the allowlist reads.

Verification uses `crypto/rsa`, `crypto/sha256`, and `encoding/json` over the
JWKS, so no JWT library is added. The JWKS URL is a constant with an injectable
override for tests.

### Routes

| Method | Path | Session | Purpose |
|---|---|---|---|
| GET | `/auth/google/authorize` | no | Start Google sign in |
| GET | `/auth/google/callback` | no | Finish it, mint the session |
| GET | `/auth/session` | yes | Current operator |
| POST | `/auth/logout` | yes + CSRF | Clear the session |
| GET | `/auth/tiktok/authorize` | yes | Connect a TikTok account |
| GET | `/auth/tiktok/callback` | yes | Finish connecting |
| POST | `/auth/tiktok/refresh` | yes + CSRF | unchanged |
| POST | `/auth/tiktok/revoke` | yes + CSRF | unchanged |

### TikTok connect flow

After this change the TikTok routes connect an account, they do not log anyone
in.

- The account upsert uses the session's operator id, so a signed-in operator
  connects to their own row.
- The callback no longer mints a session. On success it redirects to the result
  page with `status=ok`; on failure, `status=error`, as today.
- The connect routes are browser navigations, so an unauthenticated hit
  redirects to the error result instead of writing a JSON 401. This needs a
  browser-aware guard next to the existing `RequireSession`.

### Config

| Variable | Purpose |
|---|---|
| `GOOGLE_CLIENT_ID` | OAuth client id |
| `GOOGLE_CLIENT_SECRET` | OAuth client secret |
| `GOOGLE_REDIRECT_URI` | Must match the Google console entry exactly |
| `OPERATOR_EMAIL` | The one email allowed to sign in, validated as an email at boot |
| `APP_UI_BASE_URL` | Base for browser redirects back to the console; empty means same-origin relative |

The Google and operator keys are required at boot and fail fast.

`APP_UI_BASE_URL` is new. Today `compose` sets the auth redirect base to
`MEDIA_PUBLIC_BASE_URL`, which is the media host, not the console, so a
redirect would strand the browser on the wrong origin. This change stops that
reuse. The console is same-origin behind nginx in production, so an empty value
keeps a relative redirect that works there.

### Data model

Migration `000009` drops `users.password_hash`. A Google-only operator has no
password and no code path writes the column, so dropping it is honest and keeps
the down migration clean: the down re-adds `password_hash text NOT NULL DEFAULT
''`, which is reversible without touching row data.

The login flow inserts the operator row on first success (`id = OPERATOR_ID`,
`email` from Google). No new table.

### Package shape

- New `internal/googleoidc`: the outbound client (authorize URL, token
  exchange, id_token verification) with injectable endpoints and `*http.Client`
  for tests, mirroring `tiktokclient`.
- `internal/auth` gains a Google service and handler, declares the id_token
  verifier port, and loses session minting from the TikTok callback.
- `internal/compose` wires the new client and routes.

### Security notes

- The `id_token` is verified before its claims are read, and the allowlist is
  checked before any session is minted. The comparison trims surrounding space
  and lowercases both sides, so case never locks the operator out.
- `state` is single use and short lived, reused from the TikTok flow.
- The cookie keeps `HttpOnly`, `Secure`, `SameSite=Lax`; POST routes keep CSRF.
- `/auth/google/authorize` and `/auth/google/callback` are rate limited per IP
  through the shared Redis window, so a flood cannot fill Redis with states or
  spend the Google token quota. This is the `AGENTS.md` 1.6 control for a new
  public endpoint.
- A refused email is logged with the address and answered with a generic
  result, never an "account exists" hint.

## My calls

Marked as mine, so they can be checked.

- The `id_token` is verified locally, not read from the userinfo endpoint.
  Signature, issuer, audience, expiry, and nonce are all checked, so a forged
  or replayed token cannot mint a session. This is the zero-trust rule the
  owner chose.
- `APP_UI_BASE_URL` is added and the `MEDIA_PUBLIC_BASE_URL` reuse for console
  redirects is dropped. Success lands on the console root; failure lands on
  `/auth/result?status=error`.
- One email, not a list. A list is the multi-user extension, not this change.
- The migration drops `password_hash` rather than making it nullable, so the
  down path is clean.
- Browser-facing auth routes redirect on failure. API routes keep their RFC
  9457 body.

## Left to the builder

- The `users` upsert conflict target.
- Whether to reuse `StateStore` as-is with the state and nonce as its payload,
  or generalize it.
- The JWKS cache TTL and rotation handling.
- The rate limit window and budget, chosen consistently with the webhook throttle.
- Google test doubles through `httptest`.
