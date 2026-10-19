# app-serve

Go HTTP service behind the GenerativeAutoPost gateway. Typed config validated
at boot, AES-256-GCM token encryption with key rotation, RFC 9457 errors, and
a Gin data plane with request ids, structured slog, and panic recovery.

## Why Gin

The Go standard library is the default per AGENTS.md. app-serve uses Gin for
the HTTP data plane only, because route groups keep `/api/v1`, `/auth`, and
the version-independent `/webhooks/tiktok` separated, and its middleware chain
carries request id, access logging, and recovery in one place. Nothing else in
the service imports `net/http` server helpers; business logic stays in
`service` packages that never see an HTTP type.

## Layout

- `cmd/app-serve/main.go`: composition root, wiring only
- `internal/config`: typed env, validated at boot
- `internal/apierror`: RFC 9457 writer and code catalog
- `internal/operator`: users row upsert for the signed in operator
- `internal/platform/crypto`: AES-256-GCM keyring and HMAC checks
- `internal/platform/postgres`, `internal/platform/redisx`: pool and client
- `internal/platform/r2`: AWS SigV4 signer and the Cloudflare R2 object client
- `internal/platform/googleoidc`: Google OIDC authorize, code exchange, and id token verification
- `internal/platform/logging`: slog JSON in production, text in development
- `internal/platform/httpapi`: Gin engine and `GET /healthz`

## Run

The process reads the environment directly; it does not parse `.env` itself.
For a local run, export the file first:

```bash
cp .env.example .env
set -a; . ./.env; set +a
go run ./cmd/app-serve
```

Docker Compose loads the same file through `env_file`, so `docker compose up`
needs no manual export.

## Testing

Tests use `github.com/alicebob/miniredis/v2` as a test-only dependency for the
Redis rate limiter: it runs the real Lua script and exercises window expiry,
which a hand-rolled fake would not.

The standard library cannot generate UUIDv7, so `github.com/google/uuid`
provides the account row identifiers.

The R2 client signs with AWS SigV4 from `crypto/hmac`, `crypto/sha256`, and
`net/http`. It is around 420 lines across a signer and a client, and it is
verified against the AWS test-suite vector, so no AWS SDK is pulled in for
three object operations.
