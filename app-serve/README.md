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
- `internal/platform/crypto`: AES-256-GCM keyring and HMAC checks
- `internal/platform/postgres`, `internal/platform/redisx`: pool and client
- `internal/platform/logging`: slog JSON in production, text in development
- `internal/platform/httpapi`: Gin engine and `GET /healthz`

## Run

```bash
cp .env.example .env
go run ./cmd/app-serve
```
